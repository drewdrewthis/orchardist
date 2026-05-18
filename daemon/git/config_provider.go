// config_provider.go — Repo config provider, migrated from
// internal/server/providers/config/. Exposes Repo nodes to the git domain.
//
// Repo declarations in ~/.orchard/config.json are the source of truth;
// this provider holds an in-memory cache and a single fsnotify watcher
// (via configAdapter). All writes to the config file happen via the CLI
// (orchard config add-repo / rm-repo); the daemon only reads.
package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// configFreshnessSource enumerates how the cached Repo value was last populated.
type configFreshnessSource string

const (
	configSourceWatcherPush configFreshnessSource = "watcher-push"
	configSourceCold        configFreshnessSource = "cold-load"
)

// configFreshness annotates each cache entry with the time and origin.
type configFreshness struct {
	LastFetchedAt time.Time
	Source        configFreshnessSource
}

// configInvalidationEvent is emitted on the provider's subscribe channel.
type configInvalidationEvent struct {
	Key    RepoID
	Reason string
	At     time.Time
}

// configProvider surfaces Repo nodes from ~/.orchard/config.json.
// It satisfies the RepoStore interface used by Service.
type configProvider struct {
	adp    *configJSONAdapter
	logger *slog.Logger

	mu     sync.RWMutex
	cache  map[RepoID]Repo
	fresh  map[RepoID]configFreshness
	loaded bool

	subMu sync.Mutex
	subs  map[chan configInvalidationEvent]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// newConfigProvider wires a configJSONAdapter into a provider.
func newConfigProvider(adp *configJSONAdapter, logger *slog.Logger) *configProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &configProvider{
		adp:    adp,
		logger: logger,
		cache:  map[RepoID]Repo{},
		fresh:  map[RepoID]configFreshness{},
		subs:   map[chan configInvalidationEvent]struct{}{},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start hydrates the cache and launches the watcher goroutine.
func (p *configProvider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		if err := p.reload(ctx, "boot", configSourceCold); err != nil {
			startErr = fmt.Errorf("hydrate config cache: %w", err)
			return
		}
		ch, err := p.adp.Watch(ctx)
		if err != nil {
			startErr = fmt.Errorf("start config watcher: %w", err)
			return
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error("git config provider watcher panicked", "panic", r)
				}
			}()
			p.run(ctx, ch)
		}()
	})
	return startErr
}

// Stop tears down the watcher and any subscribers. Idempotent.
func (p *configProvider) Stop() error {
	var err error
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
		err = p.adp.Close()
		p.subMu.Lock()
		for ch := range p.subs {
			close(ch)
			delete(p.subs, ch)
		}
		p.subMu.Unlock()
	})
	return err
}

// List implements RepoStore.
func (p *configProvider) List(_ context.Context) ([]Repo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Repo, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	return out, nil
}

// Get implements RepoStore.
func (p *configProvider) Get(ctx context.Context, key RepoID) (Repo, error) {
	p.mu.RLock()
	v, ok := p.cache[key]
	p.mu.RUnlock()
	if ok {
		return v, nil
	}
	all, err := p.adp.FetchAll(ctx)
	if err != nil {
		return Repo{}, err
	}
	repo, ok := all[key]
	if !ok {
		return Repo{}, fmt.Errorf("git: repo %q not found", key)
	}
	p.mu.Lock()
	p.cache[key] = repo
	p.fresh[key] = configFreshness{LastFetchedAt: time.Now(), Source: configSourceCold}
	p.mu.Unlock()
	return repo, nil
}

// Excluded returns the on-disk `excluded` list (issue #527).
func (p *configProvider) Excluded(ctx context.Context) ([]string, error) {
	return p.adp.FetchExcluded(ctx)
}

// Subscribe returns a buffered channel of invalidation events.
func (p *configProvider) Subscribe(ctx context.Context) <-chan configInvalidationEvent {
	ch := make(chan configInvalidationEvent, 8)
	p.subMu.Lock()
	p.subs[ch] = struct{}{}
	p.subMu.Unlock()

	go func() {
		<-ctx.Done()
		p.subMu.Lock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
		p.subMu.Unlock()
	}()
	return ch
}

func (p *configProvider) run(ctx context.Context, ch <-chan RepoID) {
	defer close(p.doneCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := p.reload(ctx, "fs-modify", configSourceWatcherPush); err != nil {
				p.logger.Error("git config: reload failed", "err", err)
			}
		}
	}
}

func (p *configProvider) reload(ctx context.Context, reason string, source configFreshnessSource) error {
	all, err := p.adp.FetchAll(ctx)
	if err != nil {
		return err
	}
	now := time.Now()

	p.mu.Lock()
	old := p.cache
	p.cache = all
	p.fresh = make(map[RepoID]configFreshness, len(all))
	for k := range all {
		p.fresh[k] = configFreshness{LastFetchedAt: now, Source: source}
	}
	p.loaded = true
	p.mu.Unlock()

	changed := make([]RepoID, 0)
	for k, v := range all {
		ov, had := old[k]
		if !had || ov != v {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, still := all[k]; !still {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 && reason == "boot" {
		return nil
	}
	p.broadcastRepoEvents(changed, reason, now)
	return nil
}

func (p *configProvider) broadcastRepoEvents(keys []RepoID, reason string, at time.Time) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, k := range keys {
		ev := configInvalidationEvent{Key: k, Reason: reason, At: at}
		for ch := range p.subs {
			select {
			case ch <- ev:
			default:
				p.logger.Warn("git config: subscriber lagging, dropping event", "key", string(k))
			}
		}
	}
}

// ---- configJSONAdapter ----

// configJSONAdapter reads ~/.orchard/config.json and watches it for changes.
type configJSONAdapter struct {
	path     string
	logger   *slog.Logger
	watcher  *fsnotify.Watcher
	closedCh chan struct{}
}

// configAllKey is the sentinel emitted on Watch — config.json is a single document.
const configAllKey RepoID = "*"

func newConfigJSONAdapter(path string, logger *slog.Logger) *configJSONAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &configJSONAdapter{
		path:     path,
		logger:   logger,
		closedCh: make(chan struct{}),
	}
}

// FetchAll loads and normalises every repo in the config file.
func (a *configJSONAdapter) FetchAll(ctx context.Context) (map[RepoID]Repo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(a.path) //nolint:gosec
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[RepoID]Repo{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", a.path, err)
	}
	if len(data) == 0 {
		return map[RepoID]Repo{}, nil
	}
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.path, err)
	}
	out := make(map[RepoID]Repo, len(f.Repos))
	for _, row := range f.Repos {
		r := row.toRepo()
		if r.Path == "" {
			a.logger.Warn("git config: skipping repo with empty path", "id", string(r.ID))
			continue
		}
		out[r.ID] = r
	}
	return out, nil
}

// FetchExcluded returns the `excluded` top-level array from the config file.
func (a *configJSONAdapter) FetchExcluded(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(a.path) //nolint:gosec
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", a.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.path, err)
	}
	return f.Excluded, nil
}

// Watch starts an fsnotify watcher on the parent directory.
func (a *configJSONAdapter) Watch(ctx context.Context) (<-chan RepoID, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify new: %w", err)
	}
	a.watcher = w

	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("ensure config dir %s: %w", dir, err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}

	out := make(chan RepoID, 8)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.logger.Error("git config adapter watcher panicked", "panic", r)
			}
		}()
		a.runWatch(ctx, out)
	}()
	return out, nil
}

func (a *configJSONAdapter) runWatch(ctx context.Context, out chan<- RepoID) {
	defer close(out)
	defer close(a.closedCh)

	base := filepath.Base(a.path)
	for {
		select {
		case <-ctx.Done():
			_ = a.watcher.Close()
			return
		case ev, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			a.logger.Debug("git config: fsnotify event", "op", ev.Op.String(), "name", ev.Name)
			select {
			case out <- configAllKey:
			case <-ctx.Done():
				_ = a.watcher.Close()
				return
			}
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			a.logger.Warn("git config: fsnotify error", "err", err)
		}
	}
}

// Close shuts down the watcher. Idempotent.
func (a *configJSONAdapter) Close() error {
	if a.watcher == nil {
		return nil
	}
	err := a.watcher.Close()
	<-a.closedCh
	a.watcher = nil
	if err != nil {
		return fmt.Errorf("close config watcher: %w", err)
	}
	return nil
}

// ---- config file types ----

// configFile is the on-disk shape of ~/.orchard/config.json.
type configFile struct {
	Version  int           `json:"version"`
	Repos    []configRepoRow `json:"repos"`
	Peers    []interface{} `json:"peers,omitempty"`
	Excluded []string      `json:"excluded,omitempty"`
}

// configRepoRow is one entry in `repos`.
type configRepoRow struct {
	Slug string `json:"slug"`
	Path string `json:"path"`
}

func (r configRepoRow) toRepo() Repo {
	slug := r.Slug
	if slug == "" {
		slug = configSlugFromPath(r.Path)
	}
	return Repo{
		ID:   RepoID(slug),
		Slug: slug,
		Path: r.Path,
	}
}

// configSlugFromPath derives a slug from a filesystem path basename.
func configSlugFromPath(path string) string {
	name := filepath.Base(path)
	var b strings.Builder
	prevDash := true
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "repo"
	}
	return out
}
