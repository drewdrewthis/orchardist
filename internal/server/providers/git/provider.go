package git

import (
	"context"
	"errors"
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
//   - Each project owns one watcher goroutine + one consumer goroutine,
//     both bound to a per-project context derived from rootCtx. The
//     per-project cancel is stored in projectCancels so RemoveProject can
//     tear a single project down without affecting the others.
//   - Watcher invalidation channels are fanned out to provider subscribers.
//
// The provider owns its goroutines: Stop() returns once every watcher
// goroutine has exited.
type Provider struct {
	mu             sync.RWMutex
	projects       []Project
	store          map[WorktreeID]storeEntry
	watchers       map[string]*watcher           // keyed by project id
	projectCancels map[string]context.CancelFunc // keyed by project id
	projectDoneChs map[string]chan struct{}      // closes when both per-project goroutines exit
	subs           map[chan adapter.InvalidationEvent[WorktreeID]]struct{}
	subsMu         sync.Mutex
	adapterImp     *GitWorktreeAdapter
	logger         *slog.Logger
	wg             sync.WaitGroup
	rootCtx        context.Context
	rootCancel     context.CancelFunc
	spawnCounts    map[string]int // test seam: incremented each time AddProject starts watcher goroutines
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
		store:          map[WorktreeID]storeEntry{},
		watchers:       map[string]*watcher{},
		projectCancels: map[string]context.CancelFunc{},
		projectDoneChs: map[string]chan struct{}{},
		subs:           map[chan adapter.InvalidationEvent[WorktreeID]]struct{}{},
		logger:         logger,
		rootCtx:        ctx,
		rootCancel:     cancel,
		spawnCounts:    map[string]int{},
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
// runs until RemoveProject(id) or Stop() is called.
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
		// Undo the projects append so an AddProject failure doesn't leave a
		// half-registered project behind.
		p.mu.Lock()
		p.projects = removeProjectFromSlice(p.projects, proj.ID)
		p.mu.Unlock()
		return fmt.Errorf("watcher for %q: %w", proj.ID, err)
	}

	projCtx, projCancel := context.WithCancel(p.rootCtx)
	done := make(chan struct{})

	p.mu.Lock()
	p.watchers[proj.ID] = w
	p.projectCancels[proj.ID] = projCancel
	p.projectDoneChs[proj.ID] = done
	p.spawnCounts[proj.ID]++
	p.mu.Unlock()

	var inner sync.WaitGroup
	inner.Add(2)
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		defer inner.Done()
		w.run(projCtx)
	}()
	go func() {
		defer p.wg.Done()
		defer inner.Done()
		p.consumeInvalidations(w)
	}()
	go func() {
		inner.Wait()
		close(done)
	}()

	// Cold-load the project's worktrees so subscribers querying right
	// after registration see something.
	if err := p.refreshProject(projCtx, proj); err != nil {
		p.logger.Warn("git initial refresh failed", "project", proj.ID, "err", err)
	}
	return nil
}

// RemoveProject cancels the per-project context, closes the watcher,
// drains the consumer goroutine, drops the project's entries from the
// store, and unregisters it from the projects slice. Idempotent: a call
// for an unknown id is a no-op (returns nil), matching how callers
// expect "best-effort convergence" diffs (ApplyProjects) to behave.
//
// Safe to call concurrently with AddProject / RemoveProject.
func (p *Provider) RemoveProject(id string) error {
	if id == "" {
		return fmt.Errorf("git: project id cannot be empty")
	}

	p.mu.Lock()
	cancel, hasCancel := p.projectCancels[id]
	done, hasDone := p.projectDoneChs[id]
	_, hasWatcher := p.watchers[id]
	if !hasCancel && !hasWatcher {
		// Unknown id — no-op. The projects slice is the source of
		// truth for what's registered, but absence from both the cancel
		// map and the watchers map means there's nothing to tear down.
		p.mu.Unlock()
		return nil
	}
	delete(p.watchers, id)
	delete(p.projectCancels, id)
	delete(p.projectDoneChs, id)
	p.projects = removeProjectFromSlice(p.projects, id)
	// Drop cached worktrees for this project — once it's gone, callers
	// must not see stale entries.
	for k := range p.store {
		pid, _, ok := splitID(k)
		if ok && pid == id {
			delete(p.store, k)
		}
	}
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// Wait for the per-project watcher + consumer goroutines to exit so
	// callers can rely on no further events from this project after the
	// call returns. Watcher.run cancels the underlying fsnotify watcher
	// and closes its invalidation channel; consumeInvalidations exits as
	// soon as the channel drains.
	if hasDone {
		<-done
	}
	return nil
}

// removeProjectFromSlice returns projects with any entry whose ID
// matches id removed. Order-preserving.
func removeProjectFromSlice(projects []Project, id string) []Project {
	out := projects[:0]
	for _, proj := range projects {
		if proj.ID != id {
			out = append(out, proj)
		}
	}
	return out
}

// projectsEqual reports whether two Projects are equivalent for the
// purposes of the ApplyProjects diff. A change in Dir triggers a
// remove + add so the watcher re-seeds against the new directory.
func projectsEqual(a, b Project) bool {
	return a.ID == b.ID && a.Dir == b.Dir
}

// ApplyProjects diffs the current project set against projs and issues
// the minimum AddProject / RemoveProject calls needed to converge on
// the new set.
//
// Algorithm:
//   - Projects in projs but not in the current set → AddProject.
//   - Projects in the current set but not in projs → RemoveProject.
//   - Projects present in both with an unchanged Dir → left untouched
//     (watcher goroutine is NOT restarted).
//   - Projects present in both whose Dir changed → RemoveProject then
//     AddProject so the watcher rebinds.
//
// Error policy: best-effort. Errors from individual Add/Remove calls
// are collected; the rest of the diff is still applied. All errors are
// joined and returned at the end so a single misbehaving project does
// not block convergence for the others.
//
// Safe to call concurrently with AddProject / RemoveProject.
func (p *Provider) ApplyProjects(projs []Project) error {
	p.mu.RLock()
	current := make(map[string]Project, len(p.projects))
	for _, proj := range p.projects {
		current[proj.ID] = proj
	}
	p.mu.RUnlock()

	next := make(map[string]Project, len(projs))
	for _, proj := range projs {
		next[proj.ID] = proj
	}

	var errs []error
	for _, proj := range projs {
		cur, exists := current[proj.ID]
		if !exists {
			if err := p.AddProject(proj); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if !projectsEqual(cur, proj) {
			if err := p.RemoveProject(proj.ID); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := p.AddProject(proj); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for id := range current {
		if _, exists := next[id]; !exists {
			if err := p.RemoveProject(id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// SpawnCount returns the number of times AddProject has launched the
// watcher goroutines for id. Intended for tests that verify ApplyProjects
// did NOT restart a project's goroutines — a count of 1 means the
// project was added exactly once and left untouched.
func (p *Provider) SpawnCount(id string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.spawnCounts[id]
}

// HasProject returns true when id is currently registered.
func (p *Provider) HasProject(id string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.watchers[id]
	return ok
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
