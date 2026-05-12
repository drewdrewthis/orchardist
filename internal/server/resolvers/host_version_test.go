// Tests for Host.version resolver (AC3, issue #417).
//
// Scenarios:
//   - A daemon built with -X main.version=1.2.3 returns "1.2.3" from
//     { host { version } } (local host, non-nullable in practice).
//   - A local daemon configured with peer "boxd-vm" at version "1.2.4"
//     returns peers[0].version = "1.2.4" from { host { peers { version } } }.
//     The value is fetched via peerproxy.Provider on probe/reconnect —
//     NOT on every GraphQL request.
//   - peers[0].version is null when the peer probe has failed (unreachable).
//     No error is raised — null is the legitimate "unknown" value.
//
// Each test boots an httptest.Server with the gqlgen handler wired to
// an orchard server.Server. The peer scenarios also boot a second
// httptest.Server acting as the remote daemon.

package resolvers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"log/slog"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// hostVersionPost posts query against the graphqlURL and returns the decoded envelope.
func hostVersionPost(t *testing.T, graphqlURL, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest(http.MethodPost, graphqlURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post query: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// newLocalDaemon boots an httptest.Server with the orchard daemon at
// the given version and with an optional peerProxy provider.
func newLocalDaemon(t *testing.T, version string, peerProv *peerproxy.Provider) *httptest.Server {
	t.Helper()
	opts := []server.Option{server.WithVersion(version)}
	if peerProv != nil {
		opts = append(opts, server.WithPeerProxy(peerProv))
	}
	srv := server.New("", slog.Default(), opts...)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if peerProv != nil {
		if err := peerProv.Start(ctx); err != nil {
			t.Fatalf("peerProxy.Start: %v", err)
		}
		t.Cleanup(func() { _ = peerProv.Stop() })
	}

	if err := srv.StartHostProvider(ctx); err != nil {
		t.Fatalf("StartHostProvider: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/graphql", srv.GraphQLHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// fakePeerDaemon is a minimal HTTP handler simulating a remote orchard
// daemon. It responds to:
//   - `{ health { status } }` — the probe ping
//   - `{ version }` — the version ferry
//
// The handler routes by inspecting the `query` field of the POST body.
type fakePeerDaemon struct {
	version   string
	pingCount atomic.Int64
}

func (f *fakePeerDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	q := strings.TrimSpace(body.Query)
	switch {
	case strings.Contains(q, "health"):
		f.pingCount.Add(1)
		fmt.Fprintf(w, `{"data":{"health":{"status":"ok"}}}`)
	case strings.Contains(q, "version"):
		fmt.Fprintf(w, `{"data":{"version":%q}}`, f.version)
	default:
		// Unknown query — return empty data rather than an error so the
		// probe health-check still passes even if the test sends something
		// unexpected.
		fmt.Fprintf(w, `{"data":{}}`)
	}
}

// newFakePeerServer boots an httptest.Server for the given peer version.
// Returns the server and the address (host:port) for PeerConfig.Address.
func newFakePeerServer(t *testing.T, version string) (*httptest.Server, string) {
	t.Helper()
	handler := &fakePeerDaemon{version: version}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	return ts, u.Host
}

// waitForPeerProbe polls until PeerVersion returns a non-nil value or the
// timeout elapses. It returns true on success so tests can skip assertion
// if the probe never fired (a rare but possible CI flake guard).
func waitForPeerProbe(p *peerproxy.Provider, peerName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.PeerVersion(peerName) != nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestHostVersion_LocalReturnsVersion asserts that { host { version } }
// returns the baked version on the local host.
//
// BDD: "Host.version on the local host returns the local daemon's baked version".
func TestHostVersion_LocalReturnsVersion(t *testing.T) {
	ts := newLocalDaemon(t, "1.2.3", nil)
	resp := hostVersionPost(t, ts.URL+"/graphql", `{ host { version } }`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	got, ok := host["version"].(string)
	if !ok {
		t.Fatalf("host.version is not a string: %v", host["version"])
	}
	if got != "1.2.3" {
		t.Errorf("host.version = %q; want %q", got, "1.2.3")
	}
}

// TestHostVersion_PeerVersionFerried asserts that peers[0].version equals
// the value fetched via peerproxy on probe/reconnect.
//
// BDD: "Host.peers[].version is populated by ferrying `query { version }`
// over peerproxy".
func TestHostVersion_PeerVersionFerried(t *testing.T) {
	const peerName = "boxd-vm"
	const peerVersion = "1.2.4"

	_, peerAddr := newFakePeerServer(t, peerVersion)

	cfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: peerName, Address: peerAddr, TLS: false},
		},
	}
	peerProv := peerproxy.NewProvider(cfg, slog.Default())
	ts := newLocalDaemon(t, "1.2.3", peerProv)

	// Wait for the probe goroutine to fire and cache the version.
	if !waitForPeerProbe(peerProv, peerName, 2*time.Second) {
		t.Fatal("peerproxy probe did not fire within 2s — version never cached")
	}

	resp := hostVersionPost(t, ts.URL+"/graphql", `{ host { peers { version } } }`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) == 0 {
		t.Fatal("host.peers is empty")
	}
	peer, _ := peers[0].(map[string]any)
	got, ok := peer["version"].(string)
	if !ok {
		t.Fatalf("peers[0].version is not a string: %v", peer["version"])
	}
	if got != peerVersion {
		t.Errorf("peers[0].version = %q; want %q", got, peerVersion)
	}
}

// TestHostVersion_PeerNullWhenUnreachable asserts that peers[0].version
// is null (Go nil) when the peer probe has failed, and that no error is
// raised — null is the legitimate "unknown" signal.
//
// BDD: "Host.peers[].version is null when the peer is unreachable".
func TestHostVersion_PeerNullWhenUnreachable(t *testing.T) {
	const peerName = "dead-vm"

	// Point at a port that is not listening. The probe will fail quickly.
	cfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: peerName, Address: "127.0.0.1:1", TLS: false},
		},
	}
	peerProv := peerproxy.NewProvider(cfg, slog.Default())
	ts := newLocalDaemon(t, "1.2.3", peerProv)

	// Give the probe goroutine a moment to attempt (and fail) the probe.
	// We do not use waitForPeerProbe here — we expect nil to remain nil.
	time.Sleep(150 * time.Millisecond)

	resp := hostVersionPost(t, ts.URL+"/graphql", `{ host { peers { version } } }`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) == 0 {
		t.Fatal("host.peers is empty — expected the dead peer to still be listed")
	}
	peer, _ := peers[0].(map[string]any)
	// version must be explicitly null (the map key should map to nil or be absent).
	if v, exists := peer["version"]; exists && v != nil {
		t.Errorf("peers[0].version = %v; want null", v)
	}
}
