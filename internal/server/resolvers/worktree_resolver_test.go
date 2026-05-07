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

// --- Issue resolver tests ---

// TestWorktreeIssue_EmptyBranch verifies that a detached-HEAD worktree
// (empty branch) causes the resolver to return nil, nil without calling
// the gh provider.
func TestWorktreeIssue_EmptyBranch(t *testing.T) {
	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:", Path: "/any/path", Branch: ""}

	got, err := r.Issue(context.Background(), obj)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error for empty branch: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil for empty branch (detached HEAD)", got)
	}
}

// TestWorktreeIssue_UnparseableBranch verifies that a branch with no
// parseable issue number causes the resolver to return nil without error.
func TestWorktreeIssue_UnparseableBranch(t *testing.T) {
	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:main", Path: "/any/path", Branch: "main"}

	got, err := r.Issue(context.Background(), obj)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error for unparseable branch: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil for branch with no parseable issue number", got)
	}
}

// TestWorktreeIssue_BelowFloor verifies that a bare leading-numeric branch
// whose number is below the 100 floor causes the resolver to return nil.
func TestWorktreeIssue_BelowFloor(t *testing.T) {
	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:feat", Path: "/any/path", Branch: "12-some-thing"}

	got, err := r.Issue(context.Background(), obj)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error for below-100 branch: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil for branch below 100 floor", got)
	}
}

// TestWorktreeIssue_NilObject verifies that a nil worktree object returns
// nil, nil gracefully.
func TestWorktreeIssue_NilObject(t *testing.T) {
	r := newWorktreeResolver()

	got, err := r.Issue(context.Background(), nil)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error for nil object: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil for nil worktree object", got)
	}
}

// TestWorktreeIssue_NoGitDir verifies that a parseable branch but missing
// .git directory causes the resolver to return nil without error (origin
// unreadable → graceful nil).
func TestWorktreeIssue_NoGitDir(t *testing.T) {
	dir := t.TempDir() // no .git config written

	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:issue441", Path: dir, Branch: "issue441/something"}

	got, err := r.Issue(context.Background(), obj)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error when .git is absent: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil when .git directory is absent", got)
	}
}

// TestWorktreeIssue_NonGitHubOrigin verifies that a parseable branch but
// a non-GitHub origin causes the resolver to return nil without error.
func TestWorktreeIssue_NonGitHubOrigin(t *testing.T) {
	dir := t.TempDir()
	writeGitConfig(t, dir, "git@gitlab.com:other/repo.git")

	r := newWorktreeResolver()
	obj := &graphql1.Worktree{ID: "proj:issue441", Path: dir, Branch: "issue441/something"}

	got, err := r.Issue(context.Background(), obj)
	if err != nil {
		t.Fatalf("Issue() returned unexpected error for non-GitHub origin: %v", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil for non-GitHub origin", got)
	}
}

// TestWorktreeIssue_GHNotConfigured verifies that when the gh provider is
// nil and a valid issue branch + GitHub origin exist, the resolver returns
// errGHNotConfigured.
func TestWorktreeIssue_GHNotConfigured(t *testing.T) {
	dir := t.TempDir()
	writeGitConfig(t, dir, "git@github.com:drewdrewthis/git-orchard-rs.git")

	r := newWorktreeResolver() // GH is nil by default

	obj := &graphql1.Worktree{ID: "proj:issue441", Path: dir, Branch: "issue441/something"}

	got, err := r.Issue(context.Background(), obj)
	if err == nil {
		t.Fatal("Issue() expected errGHNotConfigured but got nil error")
	}
	if err != errGHNotConfigured {
		t.Errorf("Issue() error = %v, want errGHNotConfigured", err)
	}
	if got != nil {
		t.Errorf("Issue() = %v, want nil when gh provider is not configured", got)
	}
}
