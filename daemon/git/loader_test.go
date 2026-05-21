package git

import (
	"context"
	"sync"
	"testing"
)

// TestWorktreesByProjectLoaderCoalescing verifies that N parallel Load(key)
// calls with the same key produce at most 1 underlying service call (T5).
func TestWorktreesByProjectLoaderCoalescing(t *testing.T) {
	svc := newStubService()
	svc.worktrees["proj-A"] = []Worktree{
		{ID: "proj-A:main", ProjectID: "proj-A", Name: "main", Path: "/tmp/a", Branch: "main"},
	}

	loader := NewWorktreesByProjectLoader(svc)

	const n = 10
	var wg sync.WaitGroup
	results := make([][]Worktree, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = loader.Load(context.Background(), "proj-A")
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	for i, r := range results {
		if len(r) != 1 {
			t.Errorf("goroutine %d: expected 1 worktree, got %d", i, len(r))
		}
	}

	// The loader caches the result within its instance, so the underlying
	// service should have been called ≤1 times for "proj-A".
	svc.mu.Lock()
	callCount := svc.invocations["ListWorktrees:proj-A"]
	svc.mu.Unlock()
	if callCount > 1 {
		t.Errorf("T5 violation: expected ≤1 ListWorktrees call, got %d", callCount)
	}
}

// TestRepoLoaderLoadMany verifies that LoadMany deduplicates IDs (T5).
func TestRepoLoaderLoadMany(t *testing.T) {
	svc := newStubService()
	svc.repos = []Repo{
		{ID: "r1", Slug: "r1", Path: "/tmp/r1"},
		{ID: "r2", Slug: "r2", Path: "/tmp/r2"},
	}

	loader := NewRepoLoader(svc)

	// Ask for the same ID 3 times + one unique ID.
	ids := []RepoID{"r1", "r1", "r1", "r2"}
	repos, errs := loader.LoadMany(context.Background(), ids)

	for i, err := range errs {
		if err != nil {
			t.Errorf("index %d: unexpected error: %v", i, err)
		}
	}
	if len(repos) != len(ids) {
		t.Errorf("expected %d results, got %d", len(ids), len(repos))
	}

	// ListRepos should have been called exactly once (deduplication).
	svc.mu.Lock()
	listReposCount := svc.invocations["ListRepos"]
	svc.mu.Unlock()
	if listReposCount != 1 {
		t.Errorf("T5 violation: expected 1 ListRepos call, got %d", listReposCount)
	}
}

// TestWorktreeLoaderCoalescing verifies WorktreeLoader caches within instance (T5).
func TestWorktreeLoaderCoalescing(t *testing.T) {
	svc := newStubService()
	svc.worktrees["proj"] = []Worktree{
		{ID: "proj:main", ProjectID: "proj", Name: "main", Path: "/tmp/proj", Branch: "main"},
	}

	loader := NewWorktreeLoader(svc)

	// Call Load for the same key twice.
	wt1, err1 := loader.Load(context.Background(), "proj:main")
	wt2, err2 := loader.Load(context.Background(), "proj:main")

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if wt1.ID != wt2.ID {
		t.Errorf("expected same worktree on both calls, got %v vs %v", wt1.ID, wt2.ID)
	}

	// GetWorktree should be called ≤1 times due to loader cache.
	svc.mu.Lock()
	getWorktreeCount := svc.invocations["GetWorktree:proj:main"]
	svc.mu.Unlock()
	if getWorktreeCount > 1 {
		t.Errorf("T5 violation: expected ≤1 GetWorktree calls, got %d", getWorktreeCount)
	}
}
