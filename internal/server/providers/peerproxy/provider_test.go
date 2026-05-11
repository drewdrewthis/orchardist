package peerproxy_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// fakePeer boots a minimal HTTP server that handles GraphQL POSTs and
// counts how many times the health-ping query arrives. It is enough to
// satisfy Client.Ping, which POSTs `{ health { status } }`.
type fakePeer struct {
	srv       *httptest.Server
	pingCount atomic.Int64
}

func newFakePeer(t *testing.T) *fakePeer {
	t.Helper()
	fp := &fakePeer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Count every request — Ping and subscription health queries land here.
		fp.pingCount.Add(1)
		// Respond with a valid GraphQL health response.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"health":{"status":"ok"}}}`))
	})
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

// addr returns the bare host:port that PeerConfig.Address expects.
func (fp *fakePeer) addr() string {
	u, _ := stripScheme(fp.srv.URL)
	return u
}

// TestAddPeer_InsertsAndStartsProbe is the unit coverage for the AC2
// scenario "AddPeer inserts a new peer and starts its probe goroutine".
//
// Steps:
//  1. NewProvider with no peers, Start with a test-controlled context.
//  2. AddPeer for "lw-fed-c" pointing at a fake HTTP server.
//  3. err == nil.
//  4. Peers() now includes "lw-fed-c".
//  5. Within 100 ms the fake server has received at least one Ping.
func TestAddPeer_InsertsAndStartsProbe(t *testing.T) {
	fake := newFakePeer(t)

	// 1. Construct an empty provider (no peers at construction time).
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default())

	// Start with a test-controlled context.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer — TLS is false because the fake server is plain HTTP.
	err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: fake.addr(),
		TLS:     false,
	})

	// 3. Must return nil.
	if err != nil {
		t.Fatalf("AddPeer returned unexpected error: %v", err)
	}

	// 4. Peers() must include "lw-fed-c".
	peers := p.Peers()
	found := false
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Peers() = %v, want entry for %q", peers, "lw-fed-c")
	}

	// 5. Within 100 ms the probe goroutine must have issued at least one Ping.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fake.pingCount.Load() >= 1 {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no Ping observed within 100ms (count=%d)", fake.pingCount.Load())
}

// TestAddPeer_PreStartReturnsError asserts that AddPeer called before
// Start returns an error rather than panicking or silently succeeding.
func TestAddPeer_PreStartReturnsError(t *testing.T) {
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default())
	err := p.AddPeer(peerproxy.PeerConfig{Name: "x", Address: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected error from AddPeer before Start, got nil")
	}
}
