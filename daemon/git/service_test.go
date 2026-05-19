package git

import (
	"context"
	"sync"
	"testing"
)

// --- stub service for unit tests (T1, T5) ---

type stubService struct {
	repos     []Repo
	worktrees map[string][]Worktree // keyed by projectID

	mu          sync.Mutex
	invocations map[string]int // count calls per method+key for T5
}

func newStubService() *stubService {
	return &stubService{
		worktrees:   make(map[string][]Worktree),
		invocations: make(map[string]int),
	}
}

func (s *stubService) incr(key string) {
	s.mu.Lock()
	s.invocations[key]++
	s.mu.Unlock()
}

func (s *stubService) ListRepos(_ context.Context) ([]Repo, error) {
	s.incr("ListRepos")
	return s.repos, nil
}

func (s *stubService) GetRepo(_ context.Context, id RepoID) (Repo, error) {
	s.incr("GetRepo:" + string(id))
	for _, r := range s.repos {
		if r.ID == id {
			return r, nil
		}
	}
	return Repo{}, &stubRepoNotFoundError{id: id}
}

func (s *stubService) ListWorktrees(_ context.Context, projectID string) ([]Worktree, error) {
	s.incr("ListWorktrees:" + projectID)
	return s.worktrees[projectID], nil
}

func (s *stubService) GetWorktree(_ context.Context, id WorktreeID) (Worktree, error) {
	s.incr("GetWorktree:" + string(id))
	projectID, name, ok := splitID(id)
	if !ok {
		return Worktree{}, &stubWorktreeNotFoundError{id: id}
	}
	for _, wt := range s.worktrees[projectID] {
		if wt.Name == name {
			return wt, nil
		}
	}
	return Worktree{}, &stubWorktreeNotFoundError{id: id}
}

func (s *stubService) Subscribe(_ context.Context) <-chan WorktreeInvalidation {
	ch := make(chan WorktreeInvalidation)
	close(ch)
	return ch
}

type stubRepoNotFoundError struct{ id RepoID }
func (e *stubRepoNotFoundError) Error() string { return "repo not found: " + string(e.id) }

type stubWorktreeNotFoundError struct{ id WorktreeID }
func (e *stubWorktreeNotFoundError) Error() string { return "worktree not found: " + string(e.id) }

// --- Service compile-time check ---

var _ Service = (*stubService)(nil)

// TestServiceListRepos verifies that ListRepos returns the repos (T1).
func TestServiceListRepos(t *testing.T) {
	svc := newStubService()
	svc.repos = []Repo{
		{ID: "my-repo", Slug: "owner/my-repo", Path: "/tmp/my-repo"},
	}

	repos, err := svc.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: unexpected error: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Slug != "owner/my-repo" {
		t.Errorf("expected slug owner/my-repo, got %q", repos[0].Slug)
	}
}

// TestServiceGetRepo verifies GetRepo returns the correct repo (T1).
func TestServiceGetRepo(t *testing.T) {
	svc := newStubService()
	svc.repos = []Repo{
		{ID: "alpha", Slug: "alpha", Path: "/tmp/alpha"},
		{ID: "beta", Slug: "beta", Path: "/tmp/beta"},
	}

	repo, err := svc.GetRepo(context.Background(), "beta")
	if err != nil {
		t.Fatalf("GetRepo: unexpected error: %v", err)
	}
	if repo.Path != "/tmp/beta" {
		t.Errorf("expected /tmp/beta, got %q", repo.Path)
	}
}

// TestServiceGetRepoMissing verifies GetRepo returns error for unknown ID (T1).
func TestServiceGetRepoMissing(t *testing.T) {
	svc := newStubService()
	_, err := svc.GetRepo(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing repo, got nil")
	}
}

// TestServiceListWorktrees verifies ListWorktrees returns worktrees (T1).
func TestServiceListWorktrees(t *testing.T) {
	svc := newStubService()
	wts := []Worktree{
		{ID: "proj:main", ProjectID: "proj", Name: "main", Path: "/tmp/proj", Branch: "main"},
		{ID: "proj:feature", ProjectID: "proj", Name: "feature", Path: "/tmp/proj-worktrees/feature", Branch: "feature"},
	}
	svc.worktrees["proj"] = wts

	result, err := svc.ListWorktrees(context.Background(), "proj")
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(result))
	}
}
