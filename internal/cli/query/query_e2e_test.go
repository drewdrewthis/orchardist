package query_test

// CLI E2E for `orchard query projects`. The test compiles the binary
// from cmd/orchard-daemon, spins the daemon's HTTP handler in an
// httptest.Server (so we don't compete for the hard-coded localhost
// port), points the binary at that URL via ORCHARD_DAEMON_URL, and
// asserts stdout JSON. No mocks — real binary, real GraphQL
// roundtrip, real fsnotify-driven config provider.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

const cliE2EDeadline = 10 * time.Second

// repoRoot returns the absolute path to the git repo root (the working
// directory of `make daemon`). The test file is at
// internal/cli/query/, so two parent traversals get there.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	// .../internal/cli/query/query_e2e_test.go -> repo root
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

// orchardOnce caches the compiled binary for the test process; building
// once across N tests in this file is much faster than per-test rebuild.
var (
	orchardOnce sync.Once
	orchardPath string
	orchardErr  error
)

func buildOrchard(t *testing.T) string {
	t.Helper()
	orchardOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orchard-e2e-bin-*")
		if err != nil {
			orchardErr = err
			return
		}
		// Note: dir is intentionally not removed — the binary stays
		// for the duration of the test process and is small enough
		// not to matter.
		out := filepath.Join(dir, "orchard-daemon")
		root := repoRoot(t)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/orchard-daemon")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			orchardErr = err
			t.Logf("go build stderr: %s", stderr.String())
			return
		}
		orchardPath = out
	})
	if orchardErr != nil {
		t.Fatalf("build orchard: %v", orchardErr)
	}
	return orchardPath
}

func TestCLI_QueryProjects_E2E(t *testing.T) {
	binary := buildOrchard(t)

	// Real config + provider + httptest.Server.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := configprovider.File{
		Version: 1,
		Projects: []configprovider.ProjectRow{
			{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
			{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	adapter := configprovider.NewJSONFileAdapter(cfgPath, nil)
	provider := configprovider.NewProvider(adapter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider start: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", nil, server.WithProjects(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	// Run `bin/orchard query projects` against the test daemon URL.
	stdout, stderr, err := runOrchard(t, binary, ts.URL, "query", "projects")
	if err != nil {
		t.Fatalf("orchard query projects failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	// Parse the JSON array off stdout and assert shape.
	var got []map[string]any
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("decode stdout JSON: %v\nstdout: %s", err, stdout)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 projects, got %d (%+v)", len(got), got)
	}
	want := []map[string]string{
		{"id": "alpha", "directory": "/abs/alpha", "name": "Alpha"},
		{"id": "beta", "directory": "/abs/beta", "name": "Beta"},
	}
	for i, exp := range want {
		for k, v := range exp {
			if got[i][k] != v {
				t.Errorf("row %d field %q: got %v, want %s", i, k, got[i][k], v)
			}
		}
	}
}

func TestCLI_QueryProjects_Empty(t *testing.T) {
	binary := buildOrchard(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":1,"projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	adapter := configprovider.NewJSONFileAdapter(cfgPath, nil)
	provider := configprovider.NewProvider(adapter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", nil, server.WithProjects(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	stdout, stderr, err := runOrchard(t, binary, ts.URL, "query", "projects")
	if err != nil {
		t.Fatalf("err: %v\nstderr: %s", err, stderr)
	}
	got := strings.TrimSpace(string(stdout))
	if got != "[]" {
		t.Errorf("want '[]', got %q", got)
	}
}

func TestCLI_QueryProjects_DaemonDown(t *testing.T) {
	binary := buildOrchard(t)
	// Point at a localhost port that is not bound. The CLI must error
	// with the actionable hint, not a silent success.
	stdout, stderr, err := runOrchard(t, binary, "http://127.0.0.1:1", "query", "projects")
	if err == nil {
		t.Fatalf("expected non-zero exit, got nil; stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "daemon not running") {
		t.Errorf("expected actionable error, got stderr: %s", stderr)
	}
}

// runOrchard executes the compiled binary with ORCHARD_DAEMON_URL set so
// the CLI hits the test httptest.Server. Returns stdout, stderr, error.
func runOrchard(t *testing.T, binary, daemonURL string, args ...string) ([]byte, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cliE2EDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "ORCHARD_DAEMON_URL="+daemonURL)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.String(), err
}
