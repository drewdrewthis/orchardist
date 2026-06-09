package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
)

// Provider implements the git worktree cache. One instance per orchard
// daemon; multiple projects are registered into it.
//
// Concurrency model:
//   - projects is guarded by mu.
//   - store mirrors the freshest known state; reads + writes go through mu.
//   - Each project owns one watcher goroutine + one consumer goroutine,
//     both bound to a per-project context derived from rootCtx.
//   - Watcher invalidation channels are fanned out to provider subscribers.
//
// The provider owns its goroutines: Stop() returns once every watcher
// goroutine has exited (R10).
type Provider struct {
	mu             sync.RWMutex
	projects       []Project
	store          map[WorktreeID]storeEntry
	watchers       map[string]*watcher           // keyed by project id
	projectCancels map[string]context.CancelFunc // keyed by project id
	projectDoneChs map[string]chan struct{}      // closes when per-project goroutines exit
	subs           map[chan adapter.InvalidationEvent[WorktreeID]]struct{}
	subsMu         sync.Mutex
	adapterImp     *GitWorktreeAdapter
	logger         *slog.Logger
	wg             sync.WaitGroup
	rootCtx        context.Context
	rootCancel     context.CancelFunc
	spawnCounts    map[string]int // test seam: incremented each time AddProject starts goroutines
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

// Adapter exposes the underlying adapter. Used by tests.
func (p *Provider) Adapter() *GitWorktreeAdapter { return p.adapterImp }

// AddProject registers a project for scanning and starts a watcher for
// it. Returns nil for duplicate IDs (idempotent re-add).
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
		// R17: panic-recover + structured logging
		defer func() {
			if r := recover(); r != nil {
				p.logger.Error("git provider watcher goroutine panicked",
					"project", proj.ID, "panic", r)
			}
		}()
		w.run(projCtx)
	}()
	go func() {
		defer p.wg.Done()
		defer inner.Done()
		// R17: panic-recover + structured logging
		defer func() {
			if r := recover(); r != nil {
				p.logger.Error("git provider consumer goroutine panicked",
					"project", proj.ID, "panic", r)
			}
		}()
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
// drains the consumer goroutine, and unregisters it. Idempotent.
func (p *Provider) RemoveProject(id string) error {
	if id == "" {
		return fmt.Errorf("git: project id cannot be empty")
	}

	p.mu.Lock()
	cancel, hasCancel := p.projectCancels[id]
	done, hasDone := p.projectDoneChs[id]
	_, hasWatcher := p.watchers[id]
	if !hasCancel && !hasWatcher {
		p.mu.Unlock()
		return nil
	}
	delete(p.watchers, id)
	delete(p.projectCancels, id)
	delete(p.projectDoneChs, id)
	p.projects = removeProjectFromSlice(p.projects, id)
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
	if hasDone {
		<-done
	}
	return nil
}

func removeProjectFromSlice(projects []Project, id string) []Project {
	out := projects[:0]
	for _, proj := range projects {
		if proj.ID != id {
			out = append(out, proj)
		}
	}
	return out
}

func projectsEqual(a, b Project) bool {
	return a.ID == b.ID && a.Dir == b.Dir
}

// ApplyProjects diffs the current project set against projs and issues
// the minimum AddProject / RemoveProject calls to converge.
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

// SpawnCount returns the number of times AddProject has launched goroutines
// for id. Intended for tests that verify ApplyProjects idempotency.
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

// Stop cancels watchers and waits for them to exit.
func (p *Provider) Stop() {
	p.rootCancel()
	p.wg.Wait()
}

// consumeInvalidations forwards a watcher's events to provider subscribers
// and triggers a per-key refresh from disk.
func (p *Provider) consumeInvalidations(w *watcher) {
	for id := range w.invalidations {
		ev := adapter.InvalidationEvent[WorktreeID]{
			Key:    id,
			Reason: "fs-modify",
			At:     time.Now(),
		}
		// Refresh the cache FIRST so a Get racing the broadcast sees the
		// new value (R16: emit after write).
		p.refreshKey(p.rootCtx, id)
		p.broadcast(ev)
	}
}

// refreshKey re-fetches a single key.
func (p *Provider) refreshKey(ctx context.Context, id WorktreeID) {
	v, err := p.adapterImp.Fetch(ctx, id)
	if err != nil {
		p.mu.Lock()
		delete(p.store, id)
		p.mu.Unlock()
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

// refreshProject runs FetchAll and replaces the project's slice of the store.
func (p *Provider) refreshProject(ctx context.Context, proj Project) error {
	all, err := p.adapterImp.FetchAll(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
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

// ListByProject returns the cached worktrees for a project (R3: hot read
// path, never Snapshot() in resolver). Falls back to synchronous FetchAll
// on cold cache.
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

// Subscribe implements adapter.Provider. R12: returns receive-only channel.
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

// broadcast pushes an event to every subscriber, dropping under back-pressure.
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
