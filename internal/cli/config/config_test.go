package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

// setHomeForTest overrides $HOME to a temp dir so config writes never
// leak into the developer's real ~/.orchard/. Per ADR-014 / ADR-015 the
// path resolver reads $HOME directly (XDG_CONFIG_HOME is intentionally
// ignored) so this is the only knob that prevents fixture leaks.
//
// #540 section A7: any test that exercises orchard config init or
// add-repo MUST call this helper, otherwise the test pollutes the real
// ~/.orchard/config.json.
func setHomeForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

// readConfig is a tiny test helper — round-trip the on-disk JSON back
// into the canonical configprovider.File so assertions can read fields.
func readConfig(t *testing.T, home string) configprovider.File {
	t.Helper()
	cfgPath := filepath.Join(home, ".orchard", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var f configprovider.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return f
}

func TestAddRepo_RejectsNonExistentPath(t *testing.T) {
	setHomeForTest(t)
	var buf bytes.Buffer
	err := runAddRepo(&buf, "/nonexistent/abs/path", "", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAddRepo_RejectsNonGitDir(t *testing.T) {
	setHomeForTest(t)
	repo := t.TempDir() // empty dir, no .git
	var buf bytes.Buffer
	err := runAddRepo(&buf, repo, "", false)
	if err == nil {
		t.Fatal("expected error for non-git dir, got nil")
	}
}

func TestAddRepo_AcceptsNonGitWithFlag(t *testing.T) {
	home := setHomeForTest(t)
	repo := t.TempDir()
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "team/example", true); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readConfig(t, home)
	if len(cfg.Repos) != 1 {
		t.Fatalf("want 1 repo, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0].Slug != "team/example" {
		t.Errorf("want slug 'team/example', got %q", cfg.Repos[0].Slug)
	}
	if cfg.Repos[0].Path != repo {
		t.Errorf("want path %q, got %q", repo, cfg.Repos[0].Path)
	}
}

func TestAddRepo_AcceptsGitRepo(t *testing.T) {
	home := setHomeForTest(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkgit: %v", err)
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "team/with-git", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readConfig(t, home)
	if len(cfg.Repos) != 1 {
		t.Fatalf("want 1, got %d", len(cfg.Repos))
	}
}

// TestAddRepo_DeduplicatesByPath asserts that re-adding the same path
// updates the existing entry (preserving slug if the second call omits
// it) rather than appending a duplicate row.
func TestAddRepo_DeduplicatesByPath(t *testing.T) {
	home := setHomeForTest(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "team/first", false); err != nil {
		t.Fatal(err)
	}
	if err := runAddRepo(&buf, repo, "team/second", false); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t, home)
	if len(cfg.Repos) != 1 {
		t.Fatalf("expected dedupe, got %d entries", len(cfg.Repos))
	}
	if cfg.Repos[0].Slug != "team/second" {
		t.Errorf("expected updated slug 'team/second', got %q", cfg.Repos[0].Slug)
	}
}

func TestAddRepo_MultipleRepos(t *testing.T) {
	home := setHomeForTest(t)
	a := t.TempDir()
	b := t.TempDir()
	for _, p := range []string{a, b} {
		if err := os.Mkdir(filepath.Join(p, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, a, "team/alpha", false); err != nil {
		t.Fatal(err)
	}
	if err := runAddRepo(&buf, b, "team/beta", false); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t, home)
	if len(cfg.Repos) != 2 {
		t.Fatalf("want 2 repos, got %d", len(cfg.Repos))
	}
}
