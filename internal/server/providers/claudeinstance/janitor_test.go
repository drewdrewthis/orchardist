package claudeinstance

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// makeTestDir creates a temp dir and writes the given filenames (empty
// content is fine — the janitor only removes by name, never reads
// content).
func makeTestDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatalf("makeTestDir: %v", err)
		}
	}
	return dir
}

// staticSessions returns a liveSessions func that always succeeds with
// the provided set.
func staticSessions(names ...string) func(context.Context) (map[string]bool, error) {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return func(context.Context) (map[string]bool, error) { return m, nil }
}

// bufLogger returns a logger that writes to a buffer and the buffer.
func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestJanitor_RemovesOrphanFiles(t *testing.T) {
	dir := makeTestDir(t,
		"orchard-claude-alpha.json",
		"orchard-claude-alpha.inflight.json",
		"orchard-claude-bravo.json",
		"orchard-claude-bravo.inflight.json",
		"orchard-claude-charlie.json",
		"orchard-claude-charlie.inflight.json",
	)

	logger, _ := bufLogger()
	j := NewSidecarJanitor(dir, staticSessions("alpha"), logger)
	count := j.Sweep(context.Background())

	if count != 4 {
		t.Errorf("Sweep returned %d, want 4", count)
	}

	// alpha files must still exist.
	for _, name := range []string{"orchard-claude-alpha.json", "orchard-claude-alpha.inflight.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to still exist: %v", name, err)
		}
	}

	// bravo and charlie files must be gone.
	for _, name := range []string{
		"orchard-claude-bravo.json",
		"orchard-claude-bravo.inflight.json",
		"orchard-claude-charlie.json",
		"orchard-claude-charlie.inflight.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, got err: %v", name, err)
		}
	}
}

func TestJanitor_KeepsLiveSessionFiles(t *testing.T) {
	dir := makeTestDir(t,
		"orchard-claude-main.json",
		"orchard-claude-main.inflight.json",
	)

	logger, _ := bufLogger()
	j := NewSidecarJanitor(dir, staticSessions("main"), logger)
	count := j.Sweep(context.Background())

	if count != 0 {
		t.Errorf("Sweep returned %d, want 0", count)
	}

	for _, name := range []string{"orchard-claude-main.json", "orchard-claude-main.inflight.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to still exist: %v", name, err)
		}
	}
}

func TestJanitor_EmptyDirIsFine(t *testing.T) {
	// Non-existent directory — use a subpath that doesn't exist in the temp tree.
	logger, _ := bufLogger()
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	j := NewSidecarJanitor(nonexistent, staticSessions(), logger)
	count := j.Sweep(context.Background())
	if count != 0 {
		t.Errorf("Sweep in non-existent dir returned %d, want 0", count)
	}

	// Empty directory.
	dir := t.TempDir()
	j2 := NewSidecarJanitor(dir, staticSessions(), logger)
	count2 := j2.Sweep(context.Background())
	if count2 != 0 {
		t.Errorf("Sweep in empty dir returned %d, want 0", count2)
	}
}

func TestJanitor_HandlesInflightOnly(t *testing.T) {
	// A stray inflight file with no matching .json (crashed hook).
	dir := makeTestDir(t, "orchard-claude-stray.inflight.json")

	logger, _ := bufLogger()
	j := NewSidecarJanitor(dir, staticSessions(), logger)
	count := j.Sweep(context.Background())

	if count != 1 {
		t.Errorf("Sweep returned %d, want 1", count)
	}
	if _, err := os.Stat(filepath.Join(dir, "orchard-claude-stray.inflight.json")); !os.IsNotExist(err) {
		t.Errorf("expected inflight file to be removed")
	}
}

func TestJanitor_LiveSessionFuncError(t *testing.T) {
	dir := makeTestDir(t,
		"orchard-claude-alpha.json",
		"orchard-claude-alpha.inflight.json",
	)

	errSessions := func(context.Context) (map[string]bool, error) {
		return nil, errors.New("tmux not available")
	}

	logger, buf := bufLogger()
	j := NewSidecarJanitor(dir, errSessions, logger)
	count := j.Sweep(context.Background())

	if count != 0 {
		t.Errorf("Sweep with session error returned %d, want 0", count)
	}

	// Files must still exist.
	for _, name := range []string{"orchard-claude-alpha.json", "orchard-claude-alpha.inflight.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to still exist: %v", name, err)
		}
	}

	// Error must be logged.
	if !bytes.Contains(buf.Bytes(), []byte("tmux not available")) {
		t.Errorf("expected error to be logged; log output: %s", buf.String())
	}
}

func TestJanitor_LogsRemovals(t *testing.T) {
	dir := makeTestDir(t, "orchard-claude-dead.json")

	logger, buf := bufLogger()
	j := NewSidecarJanitor(dir, staticSessions(), logger)
	count := j.Sweep(context.Background())

	if count != 1 {
		t.Errorf("Sweep returned %d, want 1", count)
	}

	// The log must mention the removed file.
	if !bytes.Contains(buf.Bytes(), []byte("dead")) {
		t.Errorf("expected log to mention session 'dead'; log output: %s", buf.String())
	}
	// The summary sweep line must be present.
	if !bytes.Contains(buf.Bytes(), []byte("janitor")) {
		t.Errorf("expected janitor log line; log output: %s", buf.String())
	}
}
