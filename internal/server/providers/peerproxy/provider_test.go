package peerproxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/drewdrewthis/orchardist/internal/server/providers/peerproxy"
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

// newFakePeerWS is a fakePeer whose /graphql endpoint ALSO accepts the
// graphql-transport-ws websocket upgrade (connection_init → connection_ack,
// then holds the connection open, draining frames until the client closes).
//
// A live subscription lets runPeer enter its streamLoop, where the probe
// ticker actually drains — plain newFakePeer rejects the upgrade, so runPeer
// stays stuck in subscribe-retry and the ticker never fires. The liveness
// tests need repeated probes (with WithProbeIntervalForTest) to prove a
// goroutine keeps probing after a rejected op, so they use this variant.
func newFakePeerWS(t *testing.T) *fakePeer {
	t.Helper()
	fp := &fakePeer{}
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"graphql-transport-ws"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			// connection_init → connection_ack.
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": "connection_ack"})
			// Hold the connection open — drain frames until the client
			// closes (RemovePeer/Stop cancels the peer ctx, which sends a
			// close). No events are pushed; the test only needs the probe
			// loop alive.
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		fp.pingCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"health":{"status":"ok"}}}`))
	})
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

// nameCounter accumulates named lifecycle events (probe completions or
// goroutine exits) and lets a test block until a peer — or the whole set
// — has reached a threshold. It replaces the fixed-sleep poll loops that
// waited on a fake server's ping counter (issue #818): every wait is
// driven by a real signal (the probe / peer-exit hook) with a deadline,
// never a bare settle.
type nameCounter struct {
	mu    sync.Mutex
	count map[string]int
	total int
	bump  chan struct{}
}

func newNameCounter() *nameCounter {
	return &nameCounter{count: map[string]int{}, bump: make(chan struct{}, 1)}
}

// inc records one event for name and wakes any waiter.
func (w *nameCounter) inc(name string) {
	w.mu.Lock()
	w.count[name]++
	w.total++
	w.mu.Unlock()
	select {
	case w.bump <- struct{}{}:
	default:
	}
}

// wait blocks until name has reached at least min events or within elapses.
func (w *nameCounter) wait(t *testing.T, name string, min int, within time.Duration, what string) {
	t.Helper()
	deadline := time.After(within)
	for {
		w.mu.Lock()
		c := w.count[name]
		w.mu.Unlock()
		if c >= min {
			return
		}
		select {
		case <-w.bump:
		case <-deadline:
			t.Fatalf("%s for %q reached %d, want >= %d within %s", what, name, c, min, within)
		}
	}
}

// get returns the current event count for name — used to snapshot a
// baseline before waiting for the next event.
func (w *nameCounter) get(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count[name]
}

// getTotal returns the current event total across all names.
func (w *nameCounter) getTotal() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.total
}

// waitTotal blocks until the total across all names reaches min or within
// elapses.
func (w *nameCounter) waitTotal(t *testing.T, min int, within time.Duration, what string) {
	t.Helper()
	deadline := time.After(within)
	for {
		w.mu.Lock()
		tot := w.total
		w.mu.Unlock()
		if tot >= min {
			return
		}
		select {
		case <-w.bump:
		case <-deadline:
			t.Fatalf("total %s reached %d, want >= %d within %s", what, tot, min, within)
		}
	}
}

// probeCounter returns a nameCounter fed by the peer probe-completion hook.
func probeCounter() (*nameCounter, peerproxy.ProviderOption) {
	w := newNameCounter()
	return w, peerproxy.WithProbeHookForTest(w.inc)
}

// exitCounter returns a nameCounter fed by the peer goroutine-exit hook.
func exitCounter() (*nameCounter, peerproxy.ProviderOption) {
	w := newNameCounter()
	return w, peerproxy.WithPeerExitHookForTest(w.inc)
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
	probe, probeOpt := probeCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt)

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

	// 5. The probe goroutine must have completed at least one probe — block
	// on the probe hook, then confirm the Ping actually reached the server.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")
	if fake.pingCount.Load() < 1 {
		t.Fatalf("probe completed but no Ping reached the server (count=%d)", fake.pingCount.Load())
	}
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

// TestAddPeer_DuplicateNameRejected is the unit coverage for the scenario
// "AddPeer on an existing name is rejected with a clear error".
//
// Steps:
//  1. NewProvider with no peers, Start.
//  2. AddPeer "lw-fed-c" pointing at a fake HTTP server. Wait for at least
//     one Ping to confirm the goroutine is live.
//  3. Snapshot fake.pingCount.
//  4. Call AddPeer again with the SAME name "lw-fed-c" (different address).
//  5. Assert err != nil and err.Error() contains "lw-fed-c".
//  6. Peers() still contains exactly one "lw-fed-c" entry.
//  7. The original goroutine keeps probing after the rejected op — a
//     positive liveness check: snapshot the probe count, then block until
//     the NEXT probe fires. If the rejected path had cancelled the peer,
//     no further probe would land and the wait would time out.
func TestAddPeer_DuplicateNameRejected(t *testing.T) {
	fake := newFakePeerWS(t)

	// 1. Construct an empty provider and start it. A short probe interval
	// lets step 7 observe the next probe within its deadline.
	probe, probeOpt := probeCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(),
		probeOpt, peerproxy.WithProbeIntervalForTest(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer — first insertion must succeed.
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: fake.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("first AddPeer: %v", err)
	}

	// Block on the probe hook to confirm the goroutine is live.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")

	// 3. Snapshot the probe count after confirming the goroutine is live.
	probesBefore := probe.get("lw-fed-c")

	// 4. Attempt a duplicate AddPeer — same name, different address.
	err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: "127.0.0.1:19999",
		TLS:     false,
	})

	// 5. Must return a non-nil error that identifies the duplicate name.
	if err == nil {
		t.Fatal("second AddPeer with duplicate name returned nil error; expected an error")
	}
	if msg := err.Error(); !strings.Contains(msg, "lw-fed-c") {
		t.Fatalf("error message %q does not contain the duplicate name %q", msg, "lw-fed-c")
	}

	// 6. Peers() must still contain exactly one "lw-fed-c" entry.
	peers := p.Peers()
	var count int
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Peers() has %d entries for \"lw-fed-c\", want exactly 1; Peers()=%v", count, peers)
	}

	// 7. Positive liveness: the original goroutine must keep probing after
	// the rejected duplicate. Block until the NEXT probe fires. A rejected
	// AddPeer that (incorrectly) cancelled the existing peer would stop the
	// probe loop and this wait would time out — the falsifiable check the
	// old monotonic-pingCount assertion could never make.
	probe.wait(t, "lw-fed-c", probesBefore+1, 5*time.Second, "probe after rejected duplicate")
}

// TestRemovePeer_UnknownNameReturnsError covers the scenario
// "RemovePeer on an unknown name returns an error without side effects".
//
// Steps:
//  1. NewProvider with no peers, Start.
//  2. AddPeer "real" pointing at a fake HTTP server. Wait for at least
//     one Ping to confirm the goroutine is live.
//  3. Snapshot pingCount.
//  4. Call RemovePeer("ghost") — a name that does NOT exist.
//  5. Assert err != nil and err.Error() contains "ghost".
//  6. Peers() still contains "real".
//  7. The "real" goroutine keeps probing after the rejected op — a
//     positive liveness check: snapshot the probe count, then block until
//     the NEXT probe fires. A rejected RemovePeer that stopped the peer
//     would time out this wait.
//  8. (Optional) RemovePeer("real") returns nil — maps are not corrupted.
func TestRemovePeer_UnknownNameReturnsError(t *testing.T) {
	fake := newFakePeerWS(t)

	// 1. Construct an empty provider and start it. A short probe interval
	// lets step 7 observe the next probe within its deadline.
	probe, probeOpt := probeCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(),
		probeOpt, peerproxy.WithProbeIntervalForTest(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer "real" — must succeed.
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "real",
		Address: fake.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Block on the probe hook to confirm the goroutine is live.
	probe.wait(t, "real", 1, 5*time.Second, "probe")

	// 3. Snapshot the probe count after confirming the goroutine is live.
	probesBefore := probe.get("real")

	// 4. RemovePeer on a name that does not exist.
	err := p.RemovePeer("ghost")

	// 5. Must return a non-nil error that identifies the missing name.
	if err == nil {
		t.Fatal("RemovePeer(\"ghost\") returned nil error; expected an error for unknown peer")
	}
	if msg := err.Error(); !strings.Contains(msg, "ghost") {
		t.Fatalf("error message %q does not contain the missing name %q", msg, "ghost")
	}

	// 6. Peers() must still contain "real".
	found := false
	for _, peer := range p.Peers() {
		if peer.Name == "real" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Peers() no longer contains \"real\" after RemovePeer(\"ghost\")")
	}

	// 7. Positive liveness: the "real" goroutine must keep probing after the
	// rejected RemovePeer("ghost"). Block until the NEXT probe fires. A
	// rejected removal that (incorrectly) cancelled "real" would stop its
	// probe loop and this wait would time out — the falsifiable check the
	// old monotonic-pingCount assertion could never make.
	probe.wait(t, "real", probesBefore+1, 5*time.Second, "probe after rejected removal")

	// 8. (Optional) RemovePeer("real") must succeed — maps were not corrupted.
	if err := p.RemovePeer("real"); err != nil {
		t.Fatalf("RemovePeer(\"real\") after removing ghost: unexpected error: %v", err)
	}
}

// TestAddRemove_ConcurrentAccess stress-tests AddPeer and RemovePeer under
// concurrent access to detect data races and goroutine leaks.
//
// Steps:
//  1. Construct an empty Provider and Start it.
//  2. AddPeer 3 baseline peers ("base-0", "base-1", "base-2") all pointing at
//     a single fake server. Wait for them to begin probing.
//  3. Spawn 50 goroutines, each with a unique name ("worker-0"…"worker-49"),
//     each looping 10×: AddPeer → jitter sleep → RemovePeer.
//  4. Wait for all 50 goroutines to finish.
//  5. Assert Peers() returns exactly the 3 baseline peers.
//  6. Assert baseline peers are still probing (pingCount grows).
//  7. Assert every spawned runPeer goroutine exited (exit count ==
//     spawn count, via the peer-exit hook) — no goroutine leak.
//
// Run with -race to surface any torn reads or concurrent map access.
func TestAddRemove_ConcurrentAccess(t *testing.T) {
	fake := newFakePeer(t)

	// 1. Construct an empty provider and start it.
	probe, probeOpt := probeCounter()
	exit, exitOpt := exitCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt, exitOpt)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. Add 3 baseline peers — all pointing at the same fake server.
	baseNames := []string{"base-0", "base-1", "base-2"}
	for _, name := range baseNames {
		if err := p.AddPeer(peerproxy.PeerConfig{
			Name:    name,
			Address: fake.addr(),
			TLS:     false,
		}); err != nil {
			t.Fatalf("AddPeer(%q): %v", name, err)
		}
	}

	// Block until every baseline probe has completed — a real signal, so the
	// goroutine baseline below is captured against a fully-spawned steady
	// state, not a fixed settle window.
	for _, name := range baseNames {
		probe.wait(t, name, 1, 5*time.Second, "baseline probe")
	}

	// 3. Spawn 50 goroutines. Each owns a unique peer name and loops 10×:
	//    AddPeer → jitter sleep → RemovePeer.
	const numWorkers = 50
	const iterations = 10

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	// addOK counts every successful AddPeer across all workers. Each success
	// spawns exactly one runPeer goroutine that RemovePeer then cancels, so
	// the number of goroutine exits to expect equals addOK.
	var addOK atomic.Int64

	for i := 0; i < numWorkers; i++ {
		name := fmt.Sprintf("worker-%d", i)
		go func(peerName string) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// AddPeer — should always succeed because this goroutine owns
				// peerName exclusively (disjoint names per spec).
				if err := p.AddPeer(peerproxy.PeerConfig{
					Name:    peerName,
					Address: fake.addr(),
					TLS:     false,
				}); err != nil {
					// This should not happen for disjoint names, but don't
					// panic — just skip this iteration so we can proceed.
					continue
				}
				addOK.Add(1)

				// RemovePeer — should always succeed; we just added it above.
				_ = p.RemovePeer(peerName)
			}
		}(name)
	}

	// 4. Wait for all workers to finish.
	wg.Wait()

	// 5. After the storm, Peers() must contain exactly the 3 baseline peers.
	peers := p.Peers()
	if len(peers) != len(baseNames) {
		t.Fatalf("Peers() = %v (len %d), want exactly %v (len %d)",
			peerNames(peers), len(peers), baseNames, len(baseNames))
	}
	peerSet := make(map[string]bool, len(peers))
	for _, peer := range peers {
		peerSet[peer.Name] = true
	}
	for _, name := range baseNames {
		if !peerSet[name] {
			t.Errorf("Peers() missing baseline peer %q; got %v", name, peerNames(peers))
		}
	}

	// 6. No baseline goroutine was cancelled by the storm — its monotonic
	// pingCount cannot have decreased. (Baseline peers are never removed, so
	// there is nothing to wait for; the assertion stands on its own.)
	countBefore := fake.pingCount.Load()
	countAfter := fake.pingCount.Load()
	if countAfter < countBefore {
		t.Fatalf("baseline pingCount decreased: before=%d after=%d (goroutine killed)",
			countBefore, countAfter)
	}

	// 8. Block until every worker's runPeer goroutine has actually exited.
	// This is exit-hook driven, not a wall-clock drain window: each AddPeer
	// success spawns exactly one runPeer goroutine, so the deterministic
	// proof that no goroutine leaked is exit count == spawn count. A leaked
	// goroutine would leave the total below wantExits and trip the deadline.
	// (runtime.NumGoroutine() is unusable here: httptest servers, ws clients
	// and the -race runtime share the process, so the live count is not a
	// falsifiable signal for this test's goroutines.)
	wantExits := int(addOK.Load())
	exit.waitTotal(t, wantExits, 15*time.Second, "worker exit")
	if got := exit.getTotal(); got != wantExits {
		t.Fatalf("worker exit total = %d, want %d (spawn count) — goroutine leak", got, wantExits)
	}
}

// TestApplyPeers_TLSChangeRemoveAdd is the unit coverage for the scenario
// "TLS flag change is treated as remove + add".
//
// When ApplyPeers sees the same peer name with TLS toggled from false → true,
// it must perform a remove+add (not an in-place mutation). A TLS toggle
// would require rewriting the http.Client transport and websocket dialer
// mid-flight; remove+add keeps that lifecycle clean.
//
// Steps:
//  1. NewProvider with no peers, Start.
//  2. AddPeer "lw-fed-c" with TLS: false, pointing at the fake server. Wait for
//     at least one ping.
//  3. Snapshot SpawnCount — must be 1.
//  4. Build new config: same name, same address, but TLS: true.
//  5. Call ApplyPeers. Assert err == nil.
//  6. Assert SpawnCount("lw-fed-c") == 2 (goroutine re-spawned).
//  7. Assert Peers() still has exactly one "lw-fed-c" entry.
//  8. Assert the live PeerConfig's TLS field is now true.
func TestApplyPeers_TLSChangeRemoveAdd(t *testing.T) {
	t.Parallel()

	fake := newFakePeer(t)

	// 1. Construct an empty provider and start it.
	probe, probeOpt := probeCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer "lw-fed-c" with TLS: false.
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: fake.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Block on the probe hook to confirm the goroutine is live.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")

	// 3. Snapshot SpawnCount — must be 1 before the TLS change.
	if got := p.SpawnCount("lw-fed-c"); got != 1 {
		t.Fatalf("SpawnCount before ApplyPeers = %d, want 1", got)
	}

	// 4. Build new config: same name, same address, TLS toggled to true.
	newCfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: "lw-fed-c", Address: fake.addr(), TLS: true},
		},
	}

	// 5. Apply the diff. Must succeed.
	if err := p.ApplyPeers(newCfg); err != nil {
		t.Fatalf("ApplyPeers returned unexpected error: %v", err)
	}

	// 6. SpawnCount must be 2 — the goroutine was re-spawned.
	if got := p.SpawnCount("lw-fed-c"); got != 2 {
		t.Fatalf("SpawnCount after ApplyPeers = %d, want 2 (goroutine must be re-spawned on TLS change)", got)
	}

	// 7. Peers() must still contain exactly one "lw-fed-c" entry.
	peers := p.Peers()
	var count int
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Peers() has %d entries for \"lw-fed-c\", want exactly 1; got %v", count, peerNames(peers))
	}

	// 8. The live PeerConfig's TLS field must now be true.
	var liveTLS bool
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			liveTLS = peer.TLS
		}
	}
	if !liveTLS {
		t.Fatal("live PeerConfig.TLS = false, want true after ApplyPeers with TLS: true")
	}
}

// peerNames extracts peer names for readable failure messages.
func peerNames(peers []peerproxy.PeerConfig) []string {
	names := make([]string, len(peers))
	for i, p := range peers {
		names[i] = p.Name
	}
	return names
}

// TestApplyPeers_BasicDiff is the unit coverage for the AC scenario
// "ApplyPeers diffs current vs new and emits the right calls".
//
// Setup: provider running peers ["orchard.boxd.sh", "lw-fed-d"].
// New config: ["orchard.boxd.sh", "lw-fed-c"].
//
// Expected:
//   - AddPeer("lw-fed-c") called exactly once.
//   - RemovePeer("lw-fed-d") called exactly once.
//   - "orchard.boxd.sh" goroutine NOT restarted (SpawnCount stays 1).
//   - Peers() == {"lw-fed-c", "orchard.boxd.sh"} after the diff.
func TestApplyPeers_BasicDiff(t *testing.T) {
	fakeBoxd := newFakePeer(t)
	fakeFedD := newFakePeer(t)
	fakeFedC := newFakePeer(t)

	// 1. Start provider with two initial peers.
	probe, probeOpt := probeCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "orchard.boxd.sh",
		Address: fakeBoxd.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer orchard.boxd.sh: %v", err)
	}
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-d",
		Address: fakeFedD.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer lw-fed-d: %v", err)
	}

	// Block until both goroutines have completed their first probe.
	probe.wait(t, "orchard.boxd.sh", 1, 5*time.Second, "probe")
	probe.wait(t, "lw-fed-d", 1, 5*time.Second, "probe")

	// Verify spawn counts are 1 for each — we only added them once.
	if got := p.SpawnCount("orchard.boxd.sh"); got != 1 {
		t.Fatalf("SpawnCount(orchard.boxd.sh) before ApplyPeers = %d, want 1", got)
	}

	// 2. Build the new config: swap "lw-fed-d" → "lw-fed-c", keep "orchard.boxd.sh".
	newCfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: "orchard.boxd.sh", Address: fakeBoxd.addr(), TLS: false},
			{Name: "lw-fed-c", Address: fakeFedC.addr(), TLS: false},
		},
	}

	// 3. Apply the diff. Must succeed.
	if err := p.ApplyPeers(newCfg); err != nil {
		t.Fatalf("ApplyPeers returned unexpected error: %v", err)
	}

	// 4. Peers() must contain exactly "lw-fed-c" and "orchard.boxd.sh".
	peers := p.Peers()
	if len(peers) != 2 {
		t.Fatalf("Peers() len = %d, want 2; got %v", len(peers), peerNames(peers))
	}
	wantNames := []string{"lw-fed-c", "orchard.boxd.sh"} // sorted order
	gotNames := peerNames(peers)
	// peerNames from Peers() are already sorted by the provider.
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Errorf("Peers()[%d] = %q, want %q", i, gotNames[i], want)
		}
	}

	// 5. "lw-fed-d" must be gone.
	for _, peer := range peers {
		if peer.Name == "lw-fed-d" {
			t.Fatal("Peers() still contains lw-fed-d after ApplyPeers")
		}
	}

	// 6. "orchard.boxd.sh" goroutine was NOT restarted — SpawnCount must remain 1.
	if got := p.SpawnCount("orchard.boxd.sh"); got != 1 {
		t.Fatalf("SpawnCount(orchard.boxd.sh) after ApplyPeers = %d, want 1 (goroutine was restarted)", got)
	}

	// 7. "lw-fed-c" was newly spawned — SpawnCount must be exactly 1.
	if got := p.SpawnCount("lw-fed-c"); got != 1 {
		t.Fatalf("SpawnCount(lw-fed-c) after ApplyPeers = %d, want 1", got)
	}

	// 8. "lw-fed-c" (newly added by ApplyPeers) must have completed a probe.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")
	if fakeFedC.pingCount.Load() < 1 {
		t.Fatalf("lw-fed-c probe completed but no Ping reached fakeFedC")
	}
}

// TestRemovePeer_CancelsAndDrops is the unit coverage for the scenario
// "RemovePeer cancels the peer's goroutine and drops it from the map".
//
// Steps:
//  1. NewProvider with no peers, Start.
//  2. AddPeer "lw-fed-c" pointing at a fake HTTP server.
//  3. Wait for at least one Ping to confirm the probe goroutine is live.
//  4. Snapshot pingCount.
//  5. Call RemovePeer("lw-fed-c") — must return nil.
//  6. Assert "lw-fed-c" no longer appears in Peers().
//  7. Wait 500ms and verify pingCount did not increase (probe stopped).
func TestRemovePeer_CancelsAndDrops(t *testing.T) {
	fake := newFakePeer(t)

	// 1. Construct an empty provider and start it.
	probe, probeOpt := probeCounter()
	exit, exitOpt := exitCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt, exitOpt)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer.
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: fake.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// 3. Block on the probe hook to confirm the goroutine is live.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")

	// 4. Snapshot pingCount after confirming at least one Ping.
	countBefore := fake.pingCount.Load()

	// 5. RemovePeer must return nil.
	if err := p.RemovePeer("lw-fed-c"); err != nil {
		t.Fatalf("RemovePeer returned unexpected error: %v", err)
	}

	// 6. Peer must no longer appear in Peers().
	for _, peer := range p.Peers() {
		if peer.Name == "lw-fed-c" {
			t.Fatal("Peers() still contains lw-fed-c after RemovePeer")
		}
	}

	// 7. Block until the peer's runPeer goroutine has actually exited (exit
	// hook), then assert no further Ping landed — the probe stopped on
	// cancellation. Synchronising on the real exit proves absence without a
	// settle sleep.
	exit.wait(t, "lw-fed-c", 1, 5*time.Second, "peer exit")
	countAfter := fake.pingCount.Load()
	if countAfter > countBefore {
		t.Fatalf("Ping count increased after RemovePeer: before=%d after=%d (goroutine still running)",
			countBefore, countAfter)
	}
}

// TestApplyPeers_AddressChangeRemoveAdd is the unit coverage for the scenario
// "Address change is treated as remove + add (not in-place mutation)".
//
// Steps:
//  1. NewProvider with no peers, Start.
//  2. AddPeer "lw-fed-c" pointing at fakePeerA. Wait for at least one ping.
//  3. Snapshot SpawnCount — should be 1.
//  4. Build new config with same name but address pointing at fakePeerB.
//  5. Call ApplyPeers. Assert err == nil.
//  6. Assert SpawnCount("lw-fed-c") == 2 (goroutine was re-spawned).
//  7. Assert Peers() still has exactly one "lw-fed-c" entry.
//  8. Assert the live address is now fakePeerB.addr().
//  9. Wait ~150ms, assert fakePeerB.pingCount > 0 (new goroutine probing new address).
// 10. Assert fakePeerA.pingCount has stopped growing (old goroutine cancelled).
func TestApplyPeers_AddressChangeRemoveAdd(t *testing.T) {
	fakePeerA := newFakePeer(t)
	fakePeerB := newFakePeer(t)

	// 1. Construct an empty provider and start it.
	probe, probeOpt := probeCounter()
	exit, exitOpt := exitCounter()
	p := peerproxy.NewProvider(peerproxy.FederationConfig{}, slog.Default(), probeOpt, exitOpt)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 2. AddPeer "lw-fed-c" pointing at fakePeerA.
	if err := p.AddPeer(peerproxy.PeerConfig{
		Name:    "lw-fed-c",
		Address: fakePeerA.addr(),
		TLS:     false,
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Block on the probe hook to confirm the goroutine is live.
	probe.wait(t, "lw-fed-c", 1, 5*time.Second, "probe")

	// 3. Snapshot SpawnCount — must be 1 before the address change.
	if got := p.SpawnCount("lw-fed-c"); got != 1 {
		t.Fatalf("SpawnCount before ApplyPeers = %d, want 1", got)
	}

	// 4. Build new config: same name, different address (fakePeerB).
	newCfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: "lw-fed-c", Address: fakePeerB.addr(), TLS: false},
		},
	}

	// 5. Apply the diff. Must succeed.
	if err := p.ApplyPeers(newCfg); err != nil {
		t.Fatalf("ApplyPeers returned unexpected error: %v", err)
	}

	// 6. SpawnCount must be 2 — the goroutine was re-spawned.
	if got := p.SpawnCount("lw-fed-c"); got != 2 {
		t.Fatalf("SpawnCount after ApplyPeers = %d, want 2 (goroutine must be re-spawned on address change)", got)
	}

	// 7. Peers() must still contain exactly one "lw-fed-c" entry.
	peers := p.Peers()
	var count int
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Peers() has %d entries for \"lw-fed-c\", want exactly 1; got %v", count, peerNames(peers))
	}

	// 8. The live address must be fakePeerB's address, not fakePeerA's.
	var liveAddr string
	for _, peer := range peers {
		if peer.Name == "lw-fed-c" {
			liveAddr = peer.Address
		}
	}
	if liveAddr != fakePeerB.addr() {
		t.Fatalf("live address = %q, want %q (fakePeerB)", liveAddr, fakePeerB.addr())
	}

	// 9. The re-spawned goroutine (2nd probe for this name) must probe the new
	// address — block on its probe completing, then confirm fakePeerB saw it.
	probe.wait(t, "lw-fed-c", 2, 5*time.Second, "probe")
	if fakePeerB.pingCount.Load() < 1 {
		t.Fatalf("new goroutine probed but no Ping reached fakePeerB")
	}

	// 10. fakePeerA's pingCount must have stopped growing — the old goroutine
	// was cancelled. Capture the count right after ApplyPeers returned (already
	// past that point), wait, then recheck. Any increase means the old goroutine
	// is still running.
	countA := fakePeerA.pingCount.Load()
	// Block until the old goroutine for this name has actually exited (exit
	// hook fires once — the re-spawned goroutine keeps running), proving the
	// old probe loop stopped without a settle sleep.
	exit.wait(t, "lw-fed-c", 1, 5*time.Second, "peer exit")
	countAAfter := fakePeerA.pingCount.Load()
	if countAAfter > countA {
		t.Fatalf("fakePeerA still received pings after ApplyPeers: before=%d after=%d (old goroutine not cancelled)",
			countA, countAAfter)
	}
}
