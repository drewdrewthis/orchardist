package git

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
)

// Provider implements adapter.Provider[WorktreeID, Worktree] for the
// git backend. One instance per orchard daemon; multiple projects are
// registered into it.
//
// Concurrency model:
//   - projects is guarded by mu.
//   - store mirrors the freshest known state; reads + writes go through mu.
//   - Each project owns one watcher goroutine; its invalidation channel
//     is fanned out to provider subscribers.
//
// The provider owns its goroutines: Stop() returns once every watcher
// goroutine has exited.
type Provider struct {
	mu         sync.RWMutex
	projects   []Project
	store      map[WorktreeID]storeEntry
	watchers   map[string]*watcher // keyed by project id
	subs       map[chan adapter.InvalidationEvent[WorktreeID]]struct{}
	subsMu     sync.Mutex
	adapterImp *GitWorktreeAdapter
	logger     *slog.Logger
	wg         sync.WaitGroup
	rootCtx    context.Context
	rootCancel context.CancelFunc
}

type storeEntry struct {
	value     Worktree
	freshness adapter.Freshness
}

// NewProvider builds a provider that scans nothing until projects are
// registered. The caller is expected to call AddProject() at boot or
// in response to config changes.
func NewProvider(logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Provider{
		store:      map[WorktreeID]storeEntry{},
		watchers:   map[string]*watcher{},
		subs:       map[chan adapter.InvalidationEvent[WorktreeID]]struct{}{},
		logger:     logger,
		rootCtx:    ctx,
		rootCancel: cancel,
	}
	p.adapterImp = NewGitWorktreeAdapter(p.snapshotProjects)
	return p
}

// snapshotProjects is the func passed to the adapter; safe to call
// concurrently with AddProject / RemoveProject.
func (p *Provider) snapshotProjects() []Project {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Project, len(p.projects))
	copy(out, p.projects)
	return out
}

// Adapter exposes the underlying adapter. Used by tests; resolvers must
// depend on the Provider interface, not the concrete adapter.
func (p *Provider) Adapter() *GitWorktreeAdapter { return p.adapterImp }

// AddProject registers a project for scanning and starts a watcher for
// it. Returns nil for duplicate IDs (idempotent re-add). The watcher
// runs until Stop() is called.
func (p *Provider) AddProject(proj Project) error {
	if proj.ID == "" {
		return fmt.Errorf("git: project id cannot be empty")
	}
	if proj.Dir == "" {
		return fmt.Errorf("git: project %q dir cannot be empty", proj.ID)
	}

	p.mu.Lock()
	for _, existing := range p.projects {
		if existing.ID == proj.ID {
			p.mu.Unlock()
			return nil
		}
	}
	p.projects = append(p.projects, proj)
	p.mu.Unlock()

	w, err := newWatcher(proj, p.logger)
	if err != nil {
		return fmt.Errorf("watcher for %q: %w", proj.ID, err)
	}

	p.mu.Lock()
	p.watchers[proj.ID] = w
	p.mu.Unlock()

	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		w.run(p.rootCtx)
	}()
	go func() {
		defer p.wg.Done()
		p.consumeInvalidations(w)
	}()

	// Cold-load the project's worktrees so subscribers querying right
	// after registration see something.
	if err := p.refreshProject(p.rootCtx, proj); err != nil {
		p.logger.Warn("git initial refresh failed", "project", proj.ID, "err", err)
	}
	return nil
}

// Stop cancels watchers and waits for them to exit. Safe to call at
// most once. Subsequent operations error with context.Canceled.
func (p *Provider) Stop() {
	p.rootCancel()
	p.wg.Wait()
}

// consumeInvalidations forwards a watcher's events to provider
// subscribers and triggers a per-key refresh from disk so the cached
// value matches the next read.
func (p *Provider) consumeInvalidations(w *watcher) {
	for id := range w.invalidations {
		ev := adapter.InvalidationEvent[WorktreeID]{
			Key:    id,
			Reason: "fs-modify",
			At:     time.Now(),
		}
		// Refresh the cache first so a Get racing the broadcast sees
		// the new value.
		p.refreshKey(p.rootCtx, id)
		p.broadcast(ev)
	}
}

// refreshKey re-fetches a single key. A Fetch that returns ErrNotExist
// removes the entry from the store; other errors leave the store as-is
// and surface via slog.
func (p *Provider) refreshKey(ctx context.Context, id WorktreeID) {
	v, err := p.adapterImp.Fetch(ctx, id)
	if err != nil {
		p.mu.Lock()
		delete(p.store, id)
		p.mu.Unlock()
		// fs.ErrNotExist is the routine "worktree was removed" path —
		// not worth a warning.
		p.logger.Debug("git refreshKey: removing", "id", id, "err", err)
		return
	}
	p.mu.Lock()
	p.store[id] = storeEntry{
		value: v,
		freshness: adapter.Freshness{
			LastFetchedAt: time.Now(),
			Source:        adapter.SourceWatcherPush,
		},
	}
	p.mu.Unlock()
}

// refreshProject runs FetchAll and replaces the project's slice of the
// store wholesale.
func (p *Provider) refreshProject(ctx context.Context, proj Project) error {
	all, err := p.adapterImp.FetchAll(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	// Strip prior entries for this project.
	for id := range p.store {
		pid, _, ok := splitID(id)
		if ok && pid == proj.ID {
			delete(p.store, id)
		}
	}
	for id, v := range all {
		if v.ProjectID != proj.ID {
			continue
		}
		p.store[id] = storeEntry{
			value:     v,
			freshness: adapter.Freshness{LastFetchedAt: now, Source: adapter.SourcePoll},
		}
	}
	return nil
}

// Get implements adapter.Provider.
func (p *Provider) Get(ctx context.Context, key WorktreeID) (Worktree, adapter.Freshness, error) {
	p.mu.RLock()
	if e, ok := p.store[key]; ok {
		p.mu.RUnlock()
		return e.value, e.freshness, nil
	}
	p.mu.RUnlock()

	v, err := p.adapterImp.Fetch(ctx, key)
	if err != nil {
		return Worktree{}, adapter.Freshness{}, err
	}
	fr := adapter.Freshness{LastFetchedAt: time.Now(), Source: adapter.SourcePoll}
	p.mu.Lock()
	p.store[key] = storeEntry{value: v, freshness: fr}
	p.mu.Unlock()
	return v, fr, nil
}

// GetMany implements adapter.Provider.
func (p *Provider) GetMany(ctx context.Context, keys []WorktreeID) (map[WorktreeID]Worktree, map[WorktreeID]adapter.Freshness, error) {
	values := make(map[WorktreeID]Worktree, len(keys))
	freshness := make(map[WorktreeID]adapter.Freshness, len(keys))
	for _, k := range keys {
		v, fr, err := p.Get(ctx, k)
		if err != nil {
			continue
		}
		values[k] = v
		freshness[k] = fr
	}
	return values, freshness, nil
}

// Keys implements adapter.Provider.
func (p *Provider) Keys(ctx context.Context) ([]WorktreeID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]WorktreeID, 0, len(p.store))
	for k := range p.store {
		out = append(out, k)
	}
	return out, nil
}

// ListByProject returns the cached worktrees for a project. Resolvers
// hit this on the `Project.worktrees` edge — it is the hot path, so
// it returns from the in-memory store without touching disk.
//
// If the cache is empty for a project (e.g. a Get missed before the
// initial refresh completed), it falls back to a synchronous FetchAll
// to populate; subsequent calls hit cache.
func (p *Provider) ListByProject(ctx context.Context, projectID string) ([]Worktree, error) {
	p.mu.RLock()
	var out []Worktree
	hasAny := false
	for id, e := range p.store {
		pid, _, ok := splitID(id)
		if !ok || pid != projectID {
			continue
		}
		hasAny = true
		out = append(out, e.value)
	}
	p.mu.RUnlock()

	if hasAny {
		return out, nil
	}

	// Cold path — refresh the project synchronously.
	var proj Project
	p.mu.RLock()
	for _, prj := range p.projects {
		if prj.ID == projectID {
			proj = prj
			break
		}
	}
	p.mu.RUnlock()
	if proj.ID == "" {
		return nil, nil
	}
	if err := p.refreshProject(ctx, proj); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, e := range p.store {
		pid, _, ok := splitID(id)
		if !ok || pid != projectID {
			continue
		}
		out = append(out, e.value)
	}
	return out, nil
}

// Subscribe implements adapter.Provider. The returned channel is
// buffered; if a subscriber is slow, it loses events (the next fetch
// will catch them up). Channel is closed when ctx is cancelled.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[WorktreeID] {
	ch := make(chan adapter.InvalidationEvent[WorktreeID], 32)
	p.subsMu.Lock()
	p.subs[ch] = struct{}{}
	p.subsMu.Unlock()

	go func() {
		<-ctx.Done()
		p.subsMu.Lock()
		delete(p.subs, ch)
		p.subsMu.Unlock()
		close(ch)
	}()
	return ch
}

// broadcast pushes an event to every subscriber, dropping under
// back-pressure.
func (p *Provider) broadcast(ev adapter.InvalidationEvent[WorktreeID]) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- ev:
		default:
			p.logger.Warn("git provider subscriber dropped event", "key", ev.Key)
		}
	}
}

// compile-time check that Provider satisfies adapter.Provider.
var _ adapter.Provider[WorktreeID, Worktree] = (*Provider)(nil)
