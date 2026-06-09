package repodiscovery

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/providers/config"
)

// DefaultTTL is the discovery cache lifetime. 30 seconds is short
// enough that a new tmux pane or Claude conversation surfaces in the
// dashboard within one poll cycle, and long enough that a burst of
// `workView` queries from the TUI doesn't refire the union every tick.
const DefaultTTL = 30 * time.Second

// ExcludedFetcher is the optional capability the [Provider] uses to
// load the `excluded[]` list from the config file. The [config.Provider]
// implements it; tests pass an in-memory fake.
type ExcludedFetcher interface {
	Excluded(ctx context.Context) ([]string, error)
}

// Provider is the discoverer that the daemon wires into the
// `Query.repos` (and therefore `workView.projects`) resolver. Its
// [List] method returns the union of configured + tmux + claudeprojects
// repos, with phantom configured rows dropped and excluded paths
// filtered out.
//
// The provider is goroutine-safe and self-contained — no Start/Stop
// lifecycle, no background goroutine. The first List call after a
// cache expiry single-flights a refresh; concurrent callers wait on
// the same in-flight result.
type Provider struct {
	configured ConfiguredLister
	excluded   ExcludedFetcher
	sources    []namedSource
	ttl        time.Duration
	clock      func() time.Time
	logger     *slog.Logger

	mu          sync.Mutex
	cached      []config.Repo
	cachedAt    time.Time
	loggedGhost map[string]struct{}
	inflight    *inflight
}

type namedSource struct {
	name   string
	source Source
}

// inflight gates concurrent refresh calls so a thundering herd of
// `workView` queries only triggers one filesystem walk. The leader
// writes either result or err before closing done; followers read
// both so they observe the same outcome (success-with-stale,
// success-with-fresh, or error-with-no-cache) the leader did.
type inflight struct {
	done   chan struct{}
	result []config.Repo
	err    error
}

// NewProvider builds a discoverer.
//
// configured supplies the authoritative slug/id mapping; the [Provider]
// reads it once per refresh and treats its entries as winning on
// collision. The excluded fetcher is consulted on every refresh; pass
// nil to skip exclusion.
//
// Additional discovery sources are attached via [AddSource]; the
// constructor returns a discoverer with only the configured source
// attached, ready to compose tmux and claudeprojects on top.
func NewProvider(configured ConfiguredLister, excluded ExcludedFetcher, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		configured:  configured,
		excluded:    excluded,
		ttl:         DefaultTTL,
		clock:       time.Now,
		logger:      logger,
		loggedGhost: map[string]struct{}{},
	}
}

// AddSource attaches a discovery source by name. Names are used in
// debug logs to identify which source contributed a given path.
// Returns the receiver for chaining.
func (p *Provider) AddSource(name string, src Source) *Provider {
	if src == nil {
		return p
	}
	p.sources = append(p.sources, namedSource{name: name, source: src})
	return p
}

// WithTTL overrides [DefaultTTL]. Use a small TTL in tests to drive
// the refresh path deterministically.
func (p *Provider) WithTTL(d time.Duration) *Provider {
	if d <= 0 {
		return p
	}
	p.ttl = d
	return p
}

// WithClock swaps the wall-clock for tests.
func (p *Provider) WithClock(c func() time.Time) *Provider {
	if c == nil {
		return p
	}
	p.clock = c
	return p
}

// List returns the union [config.Repo] set. The result is sorted by
// slug for stable output across calls.
//
// The cache is consulted first; on miss (or stale entry), one caller
// performs the refresh while concurrent callers block on the in-flight
// result. The TTL is measured against the [Provider]'s clock so tests
// can advance time deterministically.
func (p *Provider) List(ctx context.Context) ([]config.Repo, error) {
	p.mu.Lock()
	if p.cached != nil && p.clock().Sub(p.cachedAt) < p.ttl {
		out := append([]config.Repo(nil), p.cached...)
		p.mu.Unlock()
		return out, nil
	}
	if p.inflight != nil {
		flight := p.inflight
		p.mu.Unlock()
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Mirror the leader's outcome: if it errored with no cache,
		// surface the same error; otherwise return its result slice.
		if flight.err != nil {
			return nil, flight.err
		}
		return append([]config.Repo(nil), flight.result...), nil
	}
	flight := &inflight{done: make(chan struct{})}
	p.inflight = flight
	p.mu.Unlock()

	out, err := p.refresh(ctx)

	p.mu.Lock()
	if err == nil {
		p.cached = out
		p.cachedAt = p.clock()
	}
	// Decide what followers (and this caller) get back. A transient
	// refresh failure that still has a previous cache to fall back on
	// serves stale rather than blanking the dashboard; a failure with
	// no cache propagates so callers know discovery is dead.
	served := append([]config.Repo(nil), p.cached...)
	if err != nil && len(served) == 0 {
		flight.err = err
	} else {
		flight.result = served
	}
	close(flight.done)
	p.inflight = nil
	p.mu.Unlock()

	if err != nil {
		if len(served) > 0 {
			p.logger.Warn("repodiscovery: refresh failed, serving stale cache", "err", err)
			return served, nil
		}
		return nil, err
	}
	return append([]config.Repo(nil), out...), nil
}

// refresh performs one full union pass. Called under no lock; the
// caller holds inflight gating so siblings wait.
func (p *Provider) refresh(ctx context.Context) ([]config.Repo, error) {
	configuredRepos, cfgErr := p.loadConfigured(ctx)
	if cfgErr != nil {
		// The configured source is authoritative; refusing to serve
		// would mean returning no repos at all when the config file is
		// briefly unreadable. Log and proceed with an empty configured
		// set — discovered sources still contribute.
		p.logger.Error("repodiscovery: configured load failed", "err", cfgErr)
		configuredRepos = nil
	}
	excludedSet := p.loadExcluded(ctx)

	// Build the keyed map of configured repos, dropping phantoms.
	configuredByKey := make(map[string]config.Repo, len(configuredRepos))
	configuredSlugs := make(map[string]struct{}, len(configuredRepos))
	for _, r := range configuredRepos {
		key, err := canonicalKey(r.Path)
		if err != nil || !pathExists(key) {
			p.logGhost(r)
			continue
		}
		configuredByKey[key] = config.Repo{
			ID:   r.ID,
			Slug: r.Slug,
			Path: key,
		}
		configuredSlugs[r.Slug] = struct{}{}
	}

	// Collect discovered roots from every non-configured source.
	type discovered struct {
		key      string
		basename string
		parent   string
	}
	var discoveredList []discovered
	seenKey := make(map[string]struct{}, len(configuredByKey))
	for k := range configuredByKey {
		seenKey[k] = struct{}{}
	}

	for _, ns := range p.sources {
		if ns.source == nil {
			continue
		}
		roots, err := ns.source.Roots(ctx)
		if err != nil {
			p.logger.Warn("repodiscovery: source error", "source", ns.name, "err", err)
			continue
		}
		for _, raw := range roots {
			key, err := walkToRepoRoot(raw)
			if err != nil {
				continue
			}
			if !pathExists(key) {
				continue
			}
			if _, dup := seenKey[key]; dup {
				continue
			}
			if _, ex := excludedSet[key]; ex {
				continue
			}
			seenKey[key] = struct{}{}
			discoveredList = append(discoveredList, discovered{
				key:      key,
				basename: filepath.Base(key),
				parent:   filepath.Base(filepath.Dir(key)),
			})
		}
	}

	// Synthesise auto-discovered Repo records, disambiguating slugs
	// against configured + previously-seen-discovered names.
	taken := make(map[string]struct{}, len(configuredSlugs)+len(discoveredList))
	for s := range configuredSlugs {
		taken[s] = struct{}{}
	}

	for _, d := range discoveredList {
		slug := uniqueSlug(d.basename, d.parent, taken)
		taken[slug] = struct{}{}
		configuredByKey[d.key] = config.Repo{
			ID:   config.RepoID(slug),
			Slug: slug,
			Path: d.key,
		}
	}

	out := make([]config.Repo, 0, len(configuredByKey))
	for _, r := range configuredByKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (p *Provider) loadConfigured(ctx context.Context) ([]config.Repo, error) {
	if p.configured == nil {
		return nil, nil
	}
	return p.configured.List(ctx)
}

func (p *Provider) loadExcluded(ctx context.Context) map[string]struct{} {
	if p.excluded == nil {
		return nil
	}
	raw, err := p.excluded.Excluded(ctx)
	if err != nil {
		p.logger.Warn("repodiscovery: excluded fetch failed", "err", err)
		return nil
	}
	out := make(map[string]struct{}, len(raw))
	for _, e := range raw {
		key, err := canonicalKey(e)
		if err != nil {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func (p *Provider) logGhost(r config.Repo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.loggedGhost[r.Path]; seen {
		return
	}
	p.loggedGhost[r.Path] = struct{}{}
	p.logger.Warn("repodiscovery: skipping phantom configured repo (directory missing)",
		"slug", r.Slug, "path", r.Path)
}

// canonicalKey resolves a path to its absolute symlink-resolved form
// without requiring `.git`. Used for configured rows (which are already
// repo roots) and for excluded entries (which the operator typed in
// raw).
func canonicalKey(path string) (string, error) {
	if path == "" {
		return "", errors.New("repodiscovery: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// uniqueSlug derives a slug for an auto-discovered repo, suffixing the
// parent-dir basename when [basename] is already taken. Falls back to a
// "-N" counter when the suffix collides too.
func uniqueSlug(basename, parent string, taken map[string]struct{}) string {
	candidate := basename
	if _, dup := taken[candidate]; !dup {
		return candidate
	}
	if parent != "" && parent != "." && parent != "/" {
		candidate = basename + "-" + parent
		if _, dup := taken[candidate]; !dup {
			return candidate
		}
	}
	for n := 2; n < 1024; n++ {
		c := basename + "-" + strconv.Itoa(n)
		if _, dup := taken[c]; !dup {
			return c
		}
	}
	return basename + "-x"
}
