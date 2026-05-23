package git

import (
	"context"
	"testing"
)

// TestRepoResolverQueryRepos verifies that QueryRepos returns all repos (T1).
func TestRepoResolverQueryRepos(t *testing.T) {
	svc := newStubService()
	svc.repos = []Repo{
		{ID: "a", Slug: "owner/a", Path: "/tmp/a"},
		{ID: "b", Slug: "owner/b", Path: "/tmp/b"},
	}

	loader := NewWorktreesByProjectLoader(svc)
	r := NewRepoResolver(svc, loader)

	repos, err := r.QueryRepos(context.Background())
	if err != nil {
		t.Fatalf("QueryRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
}

// TestRepoResolverID verifies Repo.id projection (T1).
func TestRepoResolverID(t *testing.T) {
	r := NewRepoResolver(nil, nil)
	repo := Repo{ID: "owner-foo", Slug: "owner/foo", Path: "/tmp/foo"}

	id, err := r.RepoID(context.Background(), repo)
	if err != nil {
		t.Fatalf("RepoID: %v", err)
	}
	if id != "owner-foo" {
		t.Errorf("expected 'owner-foo', got %q", id)
	}
}

// TestRepoResolverSlug verifies Repo.slug projection (T1).
func TestRepoResolverSlug(t *testing.T) {
	r := NewRepoResolver(nil, nil)
	repo := Repo{ID: "foo", Slug: "owner/foo", Path: "/tmp/foo"}

	slug, err := r.RepoSlug(context.Background(), repo)
	if err != nil {
		t.Fatalf("RepoSlug: %v", err)
	}
	if slug != "owner/foo" {
		t.Errorf("expected 'owner/foo', got %q", slug)
	}
}

// TestRepoResolverPath verifies Repo.path projection (T1).
func TestRepoResolverPath(t *testing.T) {
	r := NewRepoResolver(nil, nil)
	repo := Repo{ID: "foo", Slug: "owner/foo", Path: "/tmp/foo"}

	path, err := r.RepoPath(context.Background(), repo)
	if err != nil {
		t.Fatalf("RepoPath: %v", err)
	}
	if path != "/tmp/foo" {
		t.Errorf("expected '/tmp/foo', got %q", path)
	}
}

// TestRepoResolverWorktrees verifies that Repo.worktrees goes through the loader (T1, R3).
func TestRepoResolverWorktrees(t *testing.T) {
	svc := newStubService()
	svc.worktrees["proj-x"] = []Worktree{
		{ID: "proj-x:main", ProjectID: "proj-x", Name: "main", Path: "/tmp/x", Branch: "main"},
	}

	loader := NewWorktreesByProjectLoader(svc)
	r := NewRepoResolver(svc, loader)

	repo := Repo{ID: "proj-x", Slug: "x", Path: "/tmp/x"}
	wts, err := r.RepoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("RepoWorktrees: %v", err)
	}
	if len(wts) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(wts))
	}
	if wts[0].Name != "main" {
		t.Errorf("expected 'main', got %q", wts[0].Name)
	}
}
