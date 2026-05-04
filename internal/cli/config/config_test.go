package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

func setHomeForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	return dir
}

// readConfig is a tiny test helper — round-trip the on-disk JSON back
// into the canonical configprovider.File so assertions can read fields.
func readConfig(t *testing.T, dir string) configprovider.File {
	t.Helper()
	cfgPath := filepath.Join(dir, "config", "orchard", "config.json")
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
	err := runAddRepo(&buf, "/nonexistent/abs/path", "", "", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAddRepo_RejectsNonGitDir(t *testing.T) {
	setHomeForTest(t)
	repo := t.TempDir() // empty dir, no .git
	var buf bytes.Buffer
	err := runAddRepo(&buf, repo, "", "", false)
	if err == nil {
		t.Fatal("expected error for non-git dir, got nil")
	}
}

func TestAddRepo_AcceptsNonGitWithFlag(t *testing.T) {
	dir := setHomeForTest(t)
	repo := t.TempDir()
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "Repo Name", "", true); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readConfig(t, dir)
	if len(cfg.Projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(cfg.Projects))
	}
	if cfg.Projects[0].ID != "repo-name" {
		t.Errorf("want slug 'repo-name', got %q", cfg.Projects[0].ID)
	}
	if cfg.Projects[0].Directory != repo {
		t.Errorf("want abs %q, got %q", repo, cfg.Projects[0].Directory)
	}
}

func TestAddRepo_AcceptsGitRepo(t *testing.T) {
	dir := setHomeForTest(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkgit: %v", err)
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "", "", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readConfig(t, dir)
	if len(cfg.Projects) != 1 {
		t.Fatalf("want 1, got %d", len(cfg.Projects))
	}
}

func TestAddRepo_DeduplicatesByDirectory(t *testing.T) {
	dir := setHomeForTest(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, repo, "First", "", false); err != nil {
		t.Fatal(err)
	}
	if err := runAddRepo(&buf, repo, "Second", "", false); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t, dir)
	if len(cfg.Projects) != 1 {
		t.Fatalf("expected dedupe, got %d entries", len(cfg.Projects))
	}
	if cfg.Projects[0].Name != "Second" {
		t.Errorf("expected updated name 'Second', got %q", cfg.Projects[0].Name)
	}
}

func TestAddRepo_MultipleProjects(t *testing.T) {
	dir := setHomeForTest(t)
	a := t.TempDir()
	b := t.TempDir()
	for _, p := range []string{a, b} {
		if err := os.Mkdir(filepath.Join(p, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := runAddRepo(&buf, a, "Alpha", "", false); err != nil {
		t.Fatal(err)
	}
	if err := runAddRepo(&buf, b, "Beta", "", false); err != nil {
		t.Fatal(err)
	}
	cfg := readConfig(t, dir)
	if len(cfg.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(cfg.Projects))
	}
}
