package gh_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
)

// TestReadOriginURL exercises the .git/config parser. We assemble a
// mini git config in a temp directory, then assert the expected URL
// pops out — for both the happy path and the missing-remote path.
func TestReadOriginURL(t *testing.T) {
	t.Run("happy path .git is a directory", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `[core]
  bare = false
[remote "origin"]
  url = git@github.com:alice/repo.git
  fetch = +refs/heads/*:refs/remotes/origin/*
[remote "upstream"]
  url = git@github.com:bob/repo.git
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := gh.ReadOriginURL(dir)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "git@github.com:alice/repo.git" {
			t.Errorf("got %q", got)
		}
	})

	t.Run(".git as a gitfile", func(t *testing.T) {
		// A linked-worktree-style .git: a regular file with `gitdir:`.
		dir := t.TempDir()
		realGitDir := filepath.Join(dir, ".real-git")
		if err := os.MkdirAll(realGitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitFile := filepath.Join(dir, ".git")
		if err := os.WriteFile(gitFile, []byte("gitdir: "+realGitDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := `[remote "origin"]
url = https://github.com/carol/something.git
`
		if err := os.WriteFile(filepath.Join(realGitDir, "config"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := gh.ReadOriginURL(dir)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "https://github.com/carol/something.git" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("no origin remote returns empty + error", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `[core]
  bare = false
`
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := gh.ReadOriginURL(dir)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})

	t.Run("non-git directory", func(t *testing.T) {
		dir := t.TempDir()
		_, err := gh.ReadOriginURL(dir)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})
}
