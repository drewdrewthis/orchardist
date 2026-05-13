package manifest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultRefreshInterval is how often the provider re-reads the manifest
// file in production. Picked to match the AC in issue #584 ("suggest 60s")
// — fast enough that human edits propagate within a minute, slow enough
// to keep file IO out of the request path.
const DefaultRefreshInterval = 60 * time.Second

// EnvVar is the environment variable that overrides the manifest path.
// Empty / unset falls back to the canonical orchard-codex location.
const EnvVar = "FLEET_MANIFEST"

// DefaultRelativePath is the manifest location relative to the user's
// home directory (`~/.claude/references/fleet-manifest.yaml`). Kept as a
// slash-joined string so DefaultPath can produce a platform-correct
// absolute path with filepath.Join.
var DefaultRelativePath = filepath.Join(".claude", "references", "fleet-manifest.yaml")

// DefaultPath resolves the manifest path the daemon should read at
// startup. Honours `$FLEET_MANIFEST` first; otherwise expands
// `~/.claude/references/fleet-manifest.yaml`. Returns an empty string
// when neither override nor a home directory is available — the
// provider treats that as "no manifest configured" and stays quiet.
func DefaultPath() string {
	if v := os.Getenv(EnvVar); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, DefaultRelativePath)
}

// Status mirrors the GraphQL `ManifestStatus` type — kept package-local
// so the resolver layer can project it without importing graphql models
// here. The provider does not depend on gqlgen.
type Status struct {
	Path         string
	Loaded       bool
	LastLoadedAt time.Time
	HostCount    int
	Error        string
}

// Provider owns the in-memory manifest snapshot. One instance per
// daemon — pass it into the resolver root.
//
// Concurrency: a single sync.RWMutex protects the entries slice and the
// status fields. Reads (`Snapshot`, `Status`) take the read lock; the
// poll loop takes the write lock for the duration of one parse, so a
// slow parse cannot block readers indefinitely (parses run on a
// goroutine and only the swap is under lock).
type Provider struct {
	path     string
	interval time.Duration
	logger   *slog.Logger
	clock    func() time.Time

	mu           sync.RWMutex
	entries      []Entry
	lastLoadedAt time.Time
	loaded       bool
	lastErr      error

	loopMu     sync.Mutex
	loopActive bool
	loopCancel context.CancelFunc
}

// Option mutates the Provider during construction. Production code
// constructs with New(); tests inject a clock and a custom interval via
// the options.
type Option func(*Provider)

// WithInterval overrides the poll cadence. Values <= 0 fall back to
// DefaultRefreshInterval. Tests pass a short interval so the loop fires
// inside the test deadline.
func WithInterval(d time.Duration) Option {
	return func(p *Provider) {
		if d > 0 {
			p.interval = d
		}
	}
}

// WithLogger plugs in a logger. nil leaves the default slog logger.
func WithLogger(l *slog.Logger) Option {
	return func(p *Provider) {
		if l != nil {
			p.logger = l
		}
	}
}

// WithClock injects a clock so tests can pin timestamps.
func WithClock(f func() time.Time) Option {
	return func(p *Provider) {
		if f != nil {
			p.clock = f
		}
	}
}

// New constructs a Provider rooted at the given path. An empty path is
// permitted — the provider initialises in a "no manifest configured"
// state and `Snapshot` returns an empty slice. Production callers use
// DefaultPath() to fill the path.
func New(path string, opts ...Option) *Provider {
	p := &Provider{
		path:     path,
		interval: DefaultRefreshInterval,
		logger:   slog.Default(),
		clock:    time.Now,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Path returns the manifest file path the provider was constructed with.
// Useful for the `health` resolver, which surfaces the path even when no
// load has succeeded yet.
func (p *Provider) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

// Start performs one synchronous load and then kicks off the refresh
// goroutine. Idempotent — a second call is a no-op while the existing
// loop is still active. The loop terminates when ctx is cancelled.
//
// Returns nil even on parse failure: the provider stores the error in
// its status so the `health` query can surface it. The daemon must boot
// in the face of a corrupt manifest, per the issue ACs.
func (p *Provider) Start(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.refresh(ctx)

	p.loopMu.Lock()
	if p.loopActive {
		p.loopMu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	p.loopActive = true
	p.loopCancel = cancel
	p.loopMu.Unlock()

	go p.loop(loopCtx)
	return nil
}

// Stop cancels the refresh loop. Safe to call repeatedly and on a never-
// started provider.
func (p *Provider) Stop() error {
	if p == nil {
		return nil
	}
	p.loopMu.Lock()
	defer p.loopMu.Unlock()
	if p.loopCancel != nil {
		p.loopCancel()
		p.loopCancel = nil
	}
	p.loopActive = false
	return nil
}

// Snapshot returns the most recent set of manifest entries. The slice
// is a copy — callers may mutate it without disturbing the provider's
// internal state. An empty slice means either "manifest never loaded"
// or "manifest loaded but had zero hosts"; differentiate via Status().
func (p *Provider) Snapshot() []Entry {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Entry, len(p.entries))
	copy(out, p.entries)
	return out
}

// LookupByName returns the manifest entry for a host name, falling back
// to (Entry{}, false) when no such entry exists. Used by the resolver
// to enrich live-fleet rows.
func (p *Provider) LookupByName(name string) (Entry, bool) {
	if p == nil || name == "" {
		return Entry{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, e := range p.entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Status returns the current ingestion status — used by the GraphQL
// `health` resolver to surface manifest parse errors.
func (p *Provider) Status() Status {
	if p == nil {
		return Status{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	st := Status{
		Path:         p.path,
		Loaded:       p.loaded,
		LastLoadedAt: p.lastLoadedAt,
		HostCount:    len(p.entries),
	}
	if p.lastErr != nil {
		st.Error = p.lastErr.Error()
	}
	return st
}

// loop refreshes the manifest on the configured interval. Errors are
// logged at debug because the status query is the authoritative
// surface — humans see them via `health`.
func (p *Provider) loop(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refresh(ctx)
		}
	}
}

// refresh reads the manifest file, parses it, and updates the snapshot.
// A successful read clears the stored error; a failed read keeps the
// previous snapshot (so stale data is preferred to no data) and records
// the failure for the `health` query.
//
// "File missing" is treated as a successful read with zero hosts — the
// daemon may boot on a machine that does not have the codex checkout
// (per ADR-008's principle that missing data is not failure data).
func (p *Provider) refresh(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if p.path == "" {
		p.recordFailure(fmt.Errorf("manifest path not configured"))
		return
	}
	data, err := os.ReadFile(p.path)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, fs.ErrNotExist):
		// Treat as empty manifest — don't crash, don't loop-log noisy
		// warnings. The `health` query reports loaded=true with zero
		// hosts so the dashboard can surface "no manifest configured".
		p.recordSuccess(nil)
		return
	default:
		p.recordFailure(fmt.Errorf("read %s: %w", p.path, err))
		return
	}
	entries, err := parseManifest(data)
	if err != nil {
		p.recordFailure(err)
		return
	}
	p.recordSuccess(entries)
}

// recordSuccess swaps in a fresh set of entries and clears the error.
// Holds the write lock for the swap only — the parse happens above
// without holding any locks.
func (p *Provider) recordSuccess(entries []Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = entries
	p.loaded = true
	p.lastLoadedAt = p.clock()
	p.lastErr = nil
}

// recordFailure stores the error without disturbing the cached entries.
// Callers see the prior snapshot via Snapshot() — staleness is preferred
// to wiping the daemon's knowledge of the fleet on a transient error.
func (p *Provider) recordFailure(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	if p.logger != nil {
		p.logger.Debug("manifest: refresh failed", "path", p.path, "err", err)
	}
}
