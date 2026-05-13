package server_test

// End-to-end coverage for the fleet-manifest ingestion path (issue #584).
// Boots a server.Server with a real GraphQL handler, points the manifest
// provider at a tempdir YAML file, then exercises the public surface:
//
//   - Query.health.manifest reflects ingestion status (loaded, hostCount,
//     error, lastLoadedAt).
//   - Query.hosts returns the merged live-fleet + manifest view, with
//     drift and offline-host shapes per the issue ACs.
//   - A broken manifest does not crash the daemon — the health query
//     surfaces the parse error and Query.hosts still serves live data.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/manifest"
)

const fleetYAML = `hosts:
  - name: drudrukungfu
    role: local_orchardist
    address: local
    purpose: "Drew's Mac."
    owner_orchardist: local_orchardist
    decommission_signal: never
    last_verified: "2026-05-13"
  - name: orchard.boxd.sh
    role: boxd_orchardist
    address: "boxd@orchard.boxd.sh"
    purpose: "Always-on Hetzner VM."
    owner_orchardist: boxd_orchardist
    decommission_signal: never
    last_verified: "2026-05-13"
  - name: issue3201
    role: fork_per_issue
    address: "boxd@issue3201.boxd.sh"
    purpose: "Dedicated VM for lw#3201."
    owner_orchardist: boxd_orchardist
    decommission_signal: "lw#3201 closed AND PR merged"
    last_verified: unknown
`

// writeFleetYAML writes the canonical fixture to a tempfile under
// t.TempDir() and returns the absolute path.
func writeFleetYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fleet manifest: %v", err)
	}
	return path
}

// startServerWithManifest boots a server with the manifest provider
// (and a real host provider) wired in. Returns the running httptest
// server.
func startServerWithManifest(t *testing.T, path string) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	provider := manifest.New(
		path,
		manifest.WithInterval(20*time.Millisecond),
		manifest.WithLogger(slog.Default()),
	)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("manifest.Start: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", slog.Default(), server.WithManifest(provider))
	if err := srv.StartHostProvider(ctx); err != nil {
		t.Fatalf("host start: %v", err)
	}

	ts := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(ts.Close)
	return ts
}

func TestManifestE2E_HealthExposesLoadedStatus(t *testing.T) {
	path := writeFleetYAML(t, fleetYAML)
	ts := startServerWithManifest(t, path)

	resp := postQuery(t, ts.URL, `query {
		health { status manifest { path loaded hostCount error lastLoadedAt } }
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	health, _ := resp.Data["health"].(map[string]any)
	manifestStatus, _ := health["manifest"].(map[string]any)
	if got := manifestStatus["loaded"]; got != true {
		t.Fatalf("manifest.loaded = %v, want true", got)
	}
	if got := manifestStatus["path"]; got != path {
		t.Fatalf("manifest.path = %v, want %s", got, path)
	}
	if got := manifestStatus["hostCount"]; got != float64(3) {
		t.Fatalf("manifest.hostCount = %v, want 3", got)
	}
	if got := manifestStatus["error"]; got != nil {
		t.Fatalf("manifest.error = %v, want nil", got)
	}
	if manifestStatus["lastLoadedAt"] == nil {
		t.Fatalf("manifest.lastLoadedAt should be populated when loaded=true")
	}
}

func TestManifestE2E_HostsReturnsMergedView(t *testing.T) {
	path := writeFleetYAML(t, fleetYAML)
	ts := startServerWithManifest(t, path)

	resp := postQuery(t, ts.URL, `query {
		hosts { hostname purpose role ownerOrchardist decommissionSignal lastVerified inManifest reachable lastSeenAt }
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	rawHosts, _ := resp.Data["hosts"].([]any)
	if len(rawHosts) < 3 {
		t.Fatalf("expected at least 3 hosts (1 live + 2 offline manifest entries), got %d", len(rawHosts))
	}

	// Index by hostname for assertions.
	byName := make(map[string]map[string]any, len(rawHosts))
	for _, h := range rawHosts {
		m := h.(map[string]any)
		byName[m["hostname"].(string)] = m
	}

	// The local host should always exist and may or may not match a
	// manifest entry depending on the machine's actual hostname. Check
	// that at least one manifest-only host comes through with the
	// expected shape.
	offline, ok := byName["issue3201"]
	if !ok {
		t.Fatalf("expected manifest-only host issue3201 in merged output, got %v", byName)
	}
	if offline["reachable"] != false {
		t.Fatalf("offline host should report reachable=false, got %v", offline["reachable"])
	}
	if offline["lastSeenAt"] != "" {
		t.Fatalf("offline host should report empty lastSeenAt, got %v", offline["lastSeenAt"])
	}
	if offline["inManifest"] != true {
		t.Fatalf("offline host should report inManifest=true, got %v", offline["inManifest"])
	}
	if offline["role"] != "fork_per_issue" {
		t.Fatalf("role mismatch on offline host, got %v", offline["role"])
	}
	if offline["lastVerified"] != "unknown" {
		t.Fatalf("lastVerified should preserve `unknown` bareword, got %v", offline["lastVerified"])
	}
}

func TestManifestE2E_DriftSignalForLiveHostNotInManifest(t *testing.T) {
	// Manifest does NOT include the local hostname, so the live host
	// must come back with inManifest=false (drift signal per the ACs).
	path := writeFleetYAML(t, `hosts:
  - name: ghost-host
    role: external
`)
	ts := startServerWithManifest(t, path)

	resp := postQuery(t, ts.URL, `query { hosts { hostname inManifest reachable } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	rawHosts, _ := resp.Data["hosts"].([]any)
	foundLive := false
	for _, h := range rawHosts {
		m := h.(map[string]any)
		if m["reachable"] == true {
			foundLive = true
			if m["inManifest"] != false {
				t.Fatalf("live host %s should have inManifest=false (drift), got %v", m["hostname"], m["inManifest"])
			}
		}
	}
	if !foundLive {
		t.Fatal("expected at least one reachable host in the merged output")
	}
}

func TestManifestE2E_ParseErrorDoesNotCrashDaemon(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-manifest.yaml")
	// Unterminated flow map — yaml.v3 surfaces this as a parse error.
	if err := os.WriteFile(path, []byte("{not: closed"), 0o644); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}
	ts := startServerWithManifest(t, path)

	// health is still served — the daemon did not crash.
	resp := postQuery(t, ts.URL, `query { health { status manifest { loaded error hostCount } } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	health, _ := resp.Data["health"].(map[string]any)
	if got := health["status"]; got != "ok" {
		t.Fatalf("daemon.status = %v, want ok", got)
	}
	manifestStatus, _ := health["manifest"].(map[string]any)
	if manifestStatus["loaded"] != false {
		t.Fatalf("loaded should be false on parse error, got %v", manifestStatus["loaded"])
	}
	if manifestStatus["error"] == nil {
		t.Fatal("error should be non-null when parse fails")
	}
	if got := manifestStatus["hostCount"]; got != float64(0) {
		t.Fatalf("hostCount should be 0 when never loaded, got %v", got)
	}

	// And hosts still serves the live entry — manifest failure is
	// non-fatal.
	resp = postQuery(t, ts.URL, `query { hosts { hostname reachable } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	rawHosts, _ := resp.Data["hosts"].([]any)
	if len(rawHosts) == 0 {
		t.Fatal("hosts should still return live data on manifest parse failure")
	}
}

func TestManifestE2E_PeriodicRefreshPicksUpEdits(t *testing.T) {
	path := writeFleetYAML(t, `hosts:
  - name: orig
    role: external
`)
	ts := startServerWithManifest(t, path)

	// Initial state: hostCount=1.
	resp := postQuery(t, ts.URL, `query { health { manifest { hostCount } } }`)
	health, _ := resp.Data["health"].(map[string]any)
	manifestStatus, _ := health["manifest"].(map[string]any)
	if got := manifestStatus["hostCount"]; got != float64(1) {
		t.Fatalf("initial hostCount = %v, want 1", got)
	}

	// Overwrite the file with a fresh 3-host manifest.
	if err := os.WriteFile(path, []byte(fleetYAML), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var got float64
	for time.Now().Before(deadline) {
		r := postQuery(t, ts.URL, `query { health { manifest { hostCount } } }`)
		h, _ := r.Data["health"].(map[string]any)
		m, _ := h["manifest"].(map[string]any)
		if v, ok := m["hostCount"].(float64); ok {
			got = v
			if v == 3 {
				return
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("manifest refresh did not pick up the edit; hostCount stuck at %v after 2s", got)
}

// Quick sanity check: ensure GraphQL handler accepts unauthenticated
// JSON POSTs (no auth wrapping has snuck in unintentionally).
func TestManifestE2E_HandlerAcceptsPOST(t *testing.T) {
	path := writeFleetYAML(t, fleetYAML)
	ts := startServerWithManifest(t, path)
	req, _ := http.NewRequest("POST", ts.URL, nil)
	req.Header.Set("Content-Type", "application/json")
	// Empty body is invalid but the server should not 500.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		t.Fatalf("empty-body POST should not 5xx, got %d", resp.StatusCode)
	}
	_ = json.NewDecoder(resp.Body).Decode(&struct{}{})
}
