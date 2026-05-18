// discovery.go — Repo discovery startup routine.
//
// Walking the watched-projects list + tmux/claude CWDs to find git repos
// is a startup routine inside this domain — not its own domain (per
// docs/architecture.md: "Startup tasks / one-shot work are not domains").
// The data it populates (the set of Repo nodes) belongs to git.
//
// The discoverer is goroutine-safe and self-contained — no Start/Stop
// lifecycle, no background goroutine. The first List call after a cache
// expiry single-flights a refresh. Adapted from internal/server/providers/repodiscovery/.
package git

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DiscoveryTTL is the discovery cache lifetime.
const DiscoveryTTL = 30 * time.Second

// DiscoverySource returns absolute repo-root paths. Each adapter feeds
// raw candidate CWDs through walkToRepoRoot before returning.
type DiscoverySource interface {
	Roots(ctx context.Context) ([]string, error)
}

// ExcludedFetcher is the optional capability for loading the `excluded[]` list.
type ExcludedFetcher interface {
	Excluded(ctx context.Context) ([]string, error)
}

// ConfiguredLister is the read-side dependency on the existing config provider.
type ConfiguredLister interface {
	List(ctx context.Context) ([]Repo, error)
}

// Discoverer unifies configured repos with repos auto-discovered from
// tmux pane CWDs and Claude Code conversation CWDs. Returns []Repo sorted
// by slug.
type Discoverer struct {
	configured ConfiguredLister
	excluded   ExcludedFetcher
	sources    []namedDiscoverySource
	ttl        time.Duration
	clock      func() time.Time
	logger     *slog.Logger

	mu          sync.Mutex
	cached      []Repo
	cachedAt    time.Time
	loggedGhost map[string]struct{}
	inflight    *discoveryInflight
}

type namedDiscoverySource struct {
	name   string
	source DiscoverySource
}

type discoveryInflight struct {
	done   chan struct{}
	result []Repo
	err    error
}

// NewDiscoverer builds a discoverer. configured supplies the authoritative
// slug/id mapping; excluded may be nil to skip exclusion.
func NewDiscoverer(configured ConfiguredLister, excluded ExcludedFetcher, logger *slog.Logger) *Discoverer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Discoverer{
		configured:  configured,
		excluded:    excluded,
		ttl:         DiscoveryTTL,
		clock:       time.Now,
		logger:      logger,
		loggedGhost: map[string]struct{}{},
	}
}

// AddSource attaches a discovery source by name.
func (d *Discoverer) AddSource(name string, src DiscoverySource) *Discoverer {
	if src == nil {
		return d
	}
	d.sources = append(d.sources, namedDiscoverySource{name: name, source: src})
	return d
}

// WithTTL overrides DiscoveryTTL.
func (d *Discoverer) WithTTL(ttl time.Duration) *Discoverer {
	if ttl > 0 {
		d.ttl = ttl
	}
	return d
}

// WithClock swaps the wall-clock for tests.
func (d *Discoverer) WithClock(c func() time.Time) *Discoverer {
	if c != nil {
		d.clock = c
	}
	return d
}

// List returns the union Repo set, sorted by slug. Cache hit fast path;
// on miss a single-flight refresh runs.
func (d *Discoverer) List(ctx context.Context) ([]Repo, error) {
	d.mu.Lock()
	if d.cached != nil && d.clock().Sub(d.cachedAt) < d.ttl {
		out := append([]Repo(nil), d.cached...)
		d.mu.Unlock()
		return out, nil
	}
	if d.inflight != nil {
		flight := d.inflight
		d.mu.Unlock()
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if flight.err != nil {
			return nil, flight.err
		}
		return append([]Repo(nil), flight.result...), nil
	}
	flight := &discoveryInflight{done: make(chan struct{})}
	d.inflight = flight
	d.mu.Unlock()

	out, err := d.refresh(ctx)

	d.mu.Lock()
	if err == nil {
		d.cached = out
		d.cachedAt = d.clock()
	}
	served := append([]Repo(nil), d.cached...)
	if err != nil && len(served) == 0 {
		flight.err = err
	} else {
		flight.result = served
	}
	close(flight.done)
	d.inflight = nil
	d.mu.Unlock()

	if err != nil {
		if len(served) > 0 {
			d.logger.Warn("git discovery: refresh failed, serving stale cache", "err", err)
			return served, nil
		}
		return nil, err
	}
	return append([]Repo(nil), out...), nil
}

func (d *Discoverer) refresh(ctx context.Context) ([]Repo, error) {
	configuredRepos, cfgErr := d.loadConfigured(ctx)
	if cfgErr != nil {
		d.logger.Error("git discovery: configured load failed", "err", cfgErr)
		configuredRepos = nil
	}
	excludedSet := d.loadExcluded(ctx)

	configuredByKey := make(map[string]Repo, len(configuredRepos))
	configuredSlugs := make(map[string]struct{}, len(configuredRepos))
	for _, r := range configuredRepos {
		key, err := discoveryCanonicalKey(r.Path)
		if err != nil || !discoveryPathExists(key) {
			d.logGhost(r)
			continue
		}
		configuredByKey[key] = Repo{ID: r.ID, Slug: r.Slug, Path: key}
		configuredSlugs[r.Slug] = struct{}{}
	}

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

	for _, ns := range d.sources {
		roots, err := ns.source.Roots(ctx)
		if err != nil {
			d.logger.Warn("git discovery: source error", "source", ns.name, "err", err)
			continue
		}
		for _, raw := range roots {
			key, err := discoveryWalkToRoot(raw)
			if err != nil {
				continue
			}
			if !discoveryPathExists(key) {
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

	taken := make(map[string]struct{}, len(configuredSlugs)+len(discoveredList))
	for s := range configuredSlugs {
		taken[s] = struct{}{}
	}
	for _, disc := range discoveredList {
		slug := discoveryUniqueSlug(disc.basename, disc.parent, taken)
		taken[slug] = struct{}{}
		configuredByKey[disc.key] = Repo{
			ID:   RepoID(slug),
			Slug: slug,
			Path: disc.key,
		}
	}

	out := make([]Repo, 0, len(configuredByKey))
	for _, r := range configuredByKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (d *Discoverer) loadConfigured(ctx context.Context) ([]Repo, error) {
	if d.configured == nil {
		return nil, nil
	}
	return d.configured.List(ctx)
}

func (d *Discoverer) loadExcluded(ctx context.Context) map[string]struct{} {
	if d.excluded == nil {
		return nil
	}
	raw, err := d.excluded.Excluded(ctx)
	if err != nil {
		d.logger.Warn("git discovery: excluded fetch failed", "err", err)
		return nil
	}
	out := make(map[string]struct{}, len(raw))
	for _, e := range raw {
		key, err := discoveryCanonicalKey(e)
		if err != nil {
			continue
		}
		out[key] = struct{}{}
	}
	return out
}

func (d *Discoverer) logGhost(r Repo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, seen := d.loggedGhost[r.Path]; seen {
		return
	}
	d.loggedGhost[r.Path] = struct{}{}
	d.logger.Warn("git discovery: skipping phantom configured repo (directory missing)",
		"slug", r.Slug, "path", r.Path)
}

// discoveryCanonicalKey resolves a path to its absolute symlink-resolved form.
func discoveryCanonicalKey(path string) (string, error) {
	if path == "" {
		return "", errors.New("git discovery: empty path")
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

// discoveryWalkToRoot returns the nearest ancestor of path containing `.git`.
func discoveryWalkToRoot(path string) (string, error) {
	if path == "" {
		return "", errors.New("git discovery: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur := abs
	for {
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(gitPath)
		switch {
		case err == nil:
			if info.IsDir() {
				return discoveryCanonise(cur), nil
			}
			if main, ok := discoveryMainFromGitFile(gitPath); ok {
				return discoveryCanonise(main), nil
			}
		case errors.Is(err, fs.ErrNotExist):
			// keep climbing
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errors.New("no .git ancestor")
		}
		cur = parent
	}
}

func discoveryCanonise(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

func discoveryMainFromGitFile(gitFilePath string) (string, bool) {
	data, err := os.ReadFile(gitFilePath) //nolint:gosec
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitdir == "" {
		return "", false
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFilePath), gitdir)
	}
	gitdir = filepath.Clean(gitdir)
	const worktreesSeg = "/.git/worktrees/"
	if idx := strings.Index(gitdir, worktreesSeg); idx >= 0 {
		return gitdir[:idx], true
	}
	return "", false
}

func discoveryPathExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func discoveryUniqueSlug(basename, parent string, taken map[string]struct{}) string {
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
