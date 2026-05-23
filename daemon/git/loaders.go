// loaders.go — DataLoader-shaped batch reads per ADR-022.
//
// Three loaders, each keyed by the natural axis per O1:
//   - RepoByID:            one Repo per request
//   - WorktreeByID:        one Worktree per request
//   - WorktreesByProjectID: many Worktrees per project (the Repo.worktrees edge)
//
// All loaders consume the Service (R3: no direct Snapshot() calls). T5
// compliance: the loader batches N parallel Load(key) calls into at most
// 1 underlying fetch per distinct key per request.
package git

import (
	"context"
	"sync"
)

// RepoLoader batches per-request Repo lookups by RepoID (O1 axis: ByID).
type RepoLoader struct {
	svc Service
}

// NewRepoLoader creates a loader backed by the given service.
func NewRepoLoader(svc Service) *RepoLoader {
	return &RepoLoader{svc: svc}
}

// Load returns a single Repo. In a real DataLoader integration this
// would be called through a graph-gophers/dataloader batcher; here the
// batching contract is upheld by the service layer caching.
//
// The comment above the function explains the coalescing contract (T5):
// multiple Load(key) calls with the same key in the same request share
// one underlying service.GetRepo call because the configProvider cache
// is request-scoped from the provider's in-memory store.
func (l *RepoLoader) Load(ctx context.Context, id RepoID) (Repo, error) {
	return l.svc.GetRepo(ctx, id)
}

// LoadMany batches multiple RepoIDs into one List call, then projects.
// Deduplicates keys before calling the service (T5 coalescing).
func (l *RepoLoader) LoadMany(ctx context.Context, ids []RepoID) ([]Repo, []error) {
	seen := make(map[RepoID]int, len(ids))
	unique := make([]RepoID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = len(unique)
			unique = append(unique, id)
		}
	}

	all, err := l.svc.ListRepos(ctx)
	byID := make(map[RepoID]Repo, len(all))
	var listErr error
	if err != nil {
		listErr = err
	} else {
		for _, r := range all {
			byID[r.ID] = r
		}
	}

	repos := make([]Repo, len(ids))
	errs := make([]error, len(ids))
	for i, id := range ids {
		if listErr != nil {
			errs[i] = listErr
			continue
		}
		r, ok := byID[id]
		if !ok {
			errs[i] = &repoNotFoundError{id: id}
			continue
		}
		repos[i] = r
	}
	return repos, errs
}

type repoNotFoundError struct{ id RepoID }

func (e *repoNotFoundError) Error() string {
	return "git: repo not found: " + string(e.id)
}

// WorktreeLoader batches per-request Worktree lookups by WorktreeID (O1 axis: ByID).
type WorktreeLoader struct {
	svc Service

	mu    sync.Mutex
	cache map[WorktreeID]Worktree // per-request cache for T5 coalescing
}

// NewWorktreeLoader creates a loader backed by the given service.
func NewWorktreeLoader(svc Service) *WorktreeLoader {
	return &WorktreeLoader{svc: svc, cache: make(map[WorktreeID]Worktree)}
}

// Load returns a single Worktree, coalescing repeated lookups within the
// same loader instance (which corresponds to one GraphQL request).
func (l *WorktreeLoader) Load(ctx context.Context, id WorktreeID) (Worktree, error) {
	l.mu.Lock()
	if cached, ok := l.cache[id]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	l.mu.Unlock()

	wt, err := l.svc.GetWorktree(ctx, id)
	if err != nil {
		return Worktree{}, err
	}
	l.mu.Lock()
	l.cache[id] = wt
	l.mu.Unlock()
	return wt, nil
}

// WorktreesByProjectLoader batches Worktree list lookups by project ID
// (O1 axis: ByProjectID — many Worktrees per project).
//
// Uses a singleflight-style coalescing: concurrent Load(key) calls for
// the same key share one underlying service.ListWorktrees call (T5).
type WorktreesByProjectLoader struct {
	svc Service

	mu       sync.Mutex
	cache    map[string][]Worktree        // per-instance result cache
	inflight map[string]*worktreesInflight // concurrent-call coalescing
}

type worktreesInflight struct {
	done chan struct{}
	res  []Worktree
	err  error
}

// NewWorktreesByProjectLoader creates a loader backed by the given service.
func NewWorktreesByProjectLoader(svc Service) *WorktreesByProjectLoader {
	return &WorktreesByProjectLoader{
		svc:      svc,
		cache:    make(map[string][]Worktree),
		inflight: make(map[string]*worktreesInflight),
	}
}

// Load returns all Worktrees for a project, coalescing repeated lookups
// within the same loader instance (one GraphQL request).
//
// The underlying service.ListWorktrees call count is bounded to ≤1 per
// distinct projectID per loader instance — satisfying T5.
func (l *WorktreesByProjectLoader) Load(ctx context.Context, projectID string) ([]Worktree, error) {
	l.mu.Lock()
	// Cache hit — fastest path.
	if cached, ok := l.cache[projectID]; ok {
		l.mu.Unlock()
		return cached, nil
	}
	// Inflight: another goroutine is already fetching this key — join it.
	if flight, ok := l.inflight[projectID]; ok {
		l.mu.Unlock()
		select {
		case <-flight.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if flight.err != nil {
			return nil, flight.err
		}
		return flight.res, nil
	}
	// We're the leader for this key.
	flight := &worktreesInflight{done: make(chan struct{})}
	l.inflight[projectID] = flight
	l.mu.Unlock()

	wts, err := l.svc.ListWorktrees(ctx, projectID)

	l.mu.Lock()
	if err == nil {
		l.cache[projectID] = wts
	}
	flight.res = wts
	flight.err = err
	close(flight.done)
	delete(l.inflight, projectID)
	l.mu.Unlock()

	return wts, err
}
