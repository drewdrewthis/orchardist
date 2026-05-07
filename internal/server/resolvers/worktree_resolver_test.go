package resolvers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// writeGitConfig writes a minimal .git/config under dir with the given
// origin URL. Passing an empty originURL omits the [remote "origin"] block
// entirely — simulating a repo that has no configured origin.
func writeGitConfig(t *testing.T, dir string, originURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n"
	if originURL != "" {
		cfg += "\n[remote \"origin\"]\n\turl = " + originURL + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	cfgPath := filepath.Join(gitDir, "config")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}
}

// newWorktreeResolver returns a worktreeResolver backed by a minimal
// Resolver with no providers wired — enough for Host and Repo tests.
func newWorktreeResolver() *worktreeResolver {
	return &worktreeResolver{&Resolver{}}
}

// --- Host resolver tests ---

// TestWorktreeHost_ReturnsLocal verifies the v1 sentinel: any worktree
// resolves to the literal string "local", regardless of path or branch.
func TestWorktreeHost_ReturnsLocal(t *testing.T) {
	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "myproject:main", Path: "/some/path", Branch: "main"}

	got, err := r.Host(context.Background(), obj)
	if err != nil {
		t.Fatalf("Host() returned error: %v", err)
	}
	if got != "local" {
		t.Errorf("Host() = %q, want %q", got, "local")
	}
}

// --- Repo resolver tests ---

// TestWorktreeRepo_GitHubSSHURL verifies that a git@ GitHub SSH remote
// produces the owner/repo slug.
func TestWorktreeRepo_GitHubSSHURL(t *testing.T) {
	dir := t.TempDir()
	writeGitConfig(t, dir, "git@github.com:drewdrewthis/git-orchard-rs.git")

	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:main", Path: dir}

	got, err := r.Repo(context.Background(), obj)
	if err != nil {
		t.Fatalf("Repo() returned unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("Repo() returned nil, want non-nil")
	}
	want := "drewdrewthis/git-orchard-rs"
	if *got != want {
		t.Errorf("Repo() = %q, want %q", *got, want)
	}
}

// TestWorktreeRepo_NonGitHubURL verifies that a non-GitHub origin
// (e.g. GitLab) causes the resolver to return nil.
func TestWorktreeRepo_NonGitHubURL(t *testing.T) {
	dir := t.TempDir()
	writeGitConfig(t, dir, "git@gitlab.com:other/repo.git")

	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:feat", Path: dir}

	got, err := r.Repo(context.Background(), obj)
	if err != nil {
		t.Fatalf("Repo() returned unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Repo() = %q, want nil for non-GitHub origin", *got)
	}
}

// TestWorktreeRepo_NoOriginRemote verifies that a repo with no origin
// configured causes the resolver to return nil.
func TestWorktreeRepo_NoOriginRemote(t *testing.T) {
	dir := t.TempDir()
	writeGitConfig(t, dir, "") // no origin

	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:feat", Path: dir}

	got, err := r.Repo(context.Background(), obj)
	if err != nil {
		t.Fatalf("Repo() returned unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("Repo() = %q, want nil when no origin remote", *got)
	}
}
