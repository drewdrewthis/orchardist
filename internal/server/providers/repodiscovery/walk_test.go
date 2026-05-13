package repodiscovery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// mustGitDir creates dir, writes a `.git` directory inside it, and
// returns the symlink-resolved path so tests can compare against
// walkToRepoRoot output directly.
func mustGitDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	return resolved
}

func TestWalkToRepoRoot_FindsGitDir(t *testing.T) {
	tmp := t.TempDir()
	repo := mustGitDir(t, filepath.Join(tmp, "repo"))

	// Walk from a nested subdir inside the repo.
	deep := filepath.Join(repo, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}

	got, err := walkToRepoRoot(deep)
	if err != nil {
		t.Fatalf("walkToRepoRoot: %v", err)
	}
	if got != repo {
		t.Errorf("got %q, want %q", got, repo)
	}
}

func TestWalkToRepoRoot_FollowsWorktreePointer(t *testing.T) {
	// A `.git` file containing `gitdir: <main>/.git/worktrees/<name>`
	// is the worktree pointer git worktree creates. The walker must
	// resolve it back to the main repo so every worktree of one repo
	// dedupes to the same key. This is the fix for issue #527 — the
	// previous walker stopped at the worktree dir and surfaced every
	// worktree as its own repo with zero children.
	tmp := t.TempDir()
	main := mustGitDir(t, filepath.Join(tmp, "repo"))
	// Create the real worktrees dir tree.
	if err := os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature-x"), 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	gitdirLine := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feature-x") + "\n"
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(gitdirLine), 0o644); err != nil {
		t.Fatalf("write .git: %v", err)
	}

	// Walking from any path inside the worktree must return the *main*
	// repo path, not the worktree dir.
	got, err := walkToRepoRoot(wt)
	if err != nil {
		t.Fatalf("walkToRepoRoot: %v", err)
	}
	if got != main {
		t.Errorf("worktree did not resolve to main: got %q, want %q", got, main)
	}
}

func TestWalkToRepoRoot_GitFileWithSubmoduleWorktreeWalksToSuperproject(t *testing.T) {
	// A submodule worktree has `gitdir: <super>/.git/modules/<sub>/worktrees/<name>`
	// — the path doesn't directly identify the submodule's working
	// tree. The walker refuses to point at the worktree itself (which
	// would surface every submodule worktree as its own ghost repo —
	// issue #527's primary failure mode) and keeps climbing until it
	// finds a directory-shaped `.git`. That's the superproject.
	tmp := t.TempDir()
	super := mustGitDir(t, filepath.Join(tmp, "super"))
	subRoot := filepath.Join(super, "sub")
	wt := filepath.Join(subRoot, "wt-x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir wt: %v", err)
	}
	// Submodule-style gitdir line: the walker can't decode this shape,
	// so it falls back to climbing.
	gitdirLine := "gitdir: " + filepath.Join(super, ".git", "modules", "sub", "worktrees", "x") + "\n"
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(gitdirLine), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := walkToRepoRoot(wt)
	if err != nil {
		t.Fatalf("walkToRepoRoot: %v", err)
	}
	if got != super {
		t.Errorf("submodule worktree should resolve to superproject: got %q, want %q", got, super)
	}
}

func TestWalkToRepoRoot_NoGitAncestor(t *testing.T) {
	tmp := t.TempDir()
	got, err := walkToRepoRoot(tmp)
	if !errors.Is(err, errNoRepoRoot) {
		t.Fatalf("got err %v, want errNoRepoRoot; got %q", err, got)
	}
}

func TestWalkToRepoRoot_EmptyPath(t *testing.T) {
	if _, err := walkToRepoRoot(""); !errors.Is(err, errNoRepoRoot) {
		t.Errorf("empty path: got err %v, want errNoRepoRoot", err)
	}
}

func TestWalkToRepoRoot_ResolvesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	repo := mustGitDir(t, filepath.Join(tmp, "repo"))
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	got, err := walkToRepoRoot(link)
	if err != nil {
		t.Fatalf("walkToRepoRoot: %v", err)
	}
	if got != repo {
		t.Errorf("symlink not resolved: got %q, want %q", got, repo)
	}
}

func TestPathExists(t *testing.T) {
	tmp := t.TempDir()
	if !pathExists(tmp) {
		t.Errorf("tmp dir should exist: %s", tmp)
	}
	if pathExists("") {
		t.Errorf("empty path should not exist")
	}
	if pathExists(filepath.Join(tmp, "does-not-exist")) {
		t.Errorf("missing path should not exist")
	}
	// File (not dir) should NOT count — repo roots are directories.
	f := filepath.Join(tmp, "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if pathExists(f) {
		t.Errorf("file should not satisfy directory check")
	}
}
