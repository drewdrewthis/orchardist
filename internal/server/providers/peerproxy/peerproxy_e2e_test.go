// E2E coverage for the peerproxy provider — boots a "remote" orchard
// daemon backed by httptest, configures a "local" orchard with the
// remote as a peer, then drives the federation surface end-to-end.
//
// Two daemons (local + remote) live in the same process, but talk over
// real HTTP and websockets via httptest. Tests exercise:
//
//   - `host { peers { reachable } }` returns the remote's reachability
//   - `node(id: "TmuxPane:remote-host:%26")` is proxied
//   - `subscription { peer(host: "remote-host") { ... } }` emits when
//     the remote pushes a synthetic invalidation
package peerproxy_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

const remoteName = "remote-host"

// orchardFixture wraps an httptest.Server bound to a peerproxy-aware
// orchard daemon plus the LocalInvalidator the test uses to inject
// synthetic events.
type orchardFixture struct {
	addr   string
	srv    *httptest.Server
	events *peerproxy.LocalInvalidator
}

// fixtureOpts tunes startOrchard for TLS variants and TLS-config
// injection. Plain federation tests use the zero value.
//
// psHostID, when non-empty, wires a real ps provider to the daemon
// using the given host id. Federation tests for `Host.processes` use
// this to give each daemon a distinct host namespace so id prefixes
// can be asserted on across the federation boundary (#465).
//
// psRunner, when non-nil, replaces the real shellout with a stub. The
// federation test uses this to give each daemon a deterministic and
// distinct process table — without it both daemons would shell out to
// the same OS `ps`, making "did the data come from the local or peer
// daemon?" unverifiable.
type fixtureOpts struct {
	tlsServer bool
	tlsConfig *tls.Config
	psHostID  string
	psRunner  ps.CommandRunner
}

// startOrchard boots an orchard daemon attached to httptest. fedCfg is
// the federation slice (peers); pass an empty FederationConfig for a
// "leaf" daemon that has no peers of its own.
func startOrchard(t *testing.T, fedCfg peerproxy.FederationConfig) *orchardFixture {
	return startOrchardOpts(t, fedCfg, fixtureOpts{})
}

func startOrchardOpts(t *testing.T, fedCfg peerproxy.FederationConfig, opts fixtureOpts) *orchardFixture {
	t.Helper()
	logger := slog.Default()
	provOpts := []peerproxy.ProviderOption{}
	if opts.tlsConfig != nil {
		provOpts = append(provOpts, peerproxy.WithTLSConfig(opts.tlsConfig))
	}
	peerProvider := peerproxy.NewProvider(fedCfg, logger, provOpts...)
	localEvents := peerproxy.NewLocalInvalidator()

	serverOpts := []server.Option{
		server.WithPeerProxy(peerProvider),
		server.WithLocalEvents(localEvents),
	}
	var psProv *ps.Provider
	if opts.psHostID != "" {
		adapter := ps.NewAdapter(opts.psHostID).WithPollInterval(time.Hour)
		if opts.psRunner != nil {
			adapter = adapter.WithRunner(opts.psRunner)
		}
		psProv = ps.New(adapter, logger)
		serverOpts = append(serverOpts, server.WithPS(psProv))
	}
	srv := server.New("", logger, serverOpts...)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := peerProvider.Start(ctx); err != nil {
		t.Fatalf("start peerproxy: %v", err)
	}
	t.Cleanup(func() { _ = peerProvider.Stop() })

	if psProv != nil {
		if err := psProv.Start(ctx); err != nil {
			t.Fatalf("start ps provider: %v", err)
		}
	}

	if err := srv.StartHostProvider(ctx); err != nil {
		t.Fatalf("start host provider: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/graphql", srv.GraphQLHandler())
	var ts *httptest.Server
	if opts.tlsServer {
		ts = httptest.NewTLSServer(mux)
	} else {
		ts = httptest.NewServer(mux)
	}
	t.Cleanup(ts.Close)

	addr, err := stripScheme(ts.URL)
	if err != nil {
		t.Fatalf("strip scheme: %v", err)
	}
	return &orchardFixture{
		addr:   addr,
		srv:    ts,
		events: localEvents,
	}
}

// stripScheme returns the host:port portion of a URL like
// "http://127.0.0.1:43217" — what peerproxy.PeerConfig.Address expects.
func stripScheme(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

// graphQLPost issues a single POST against the fixture and returns the
// decoded envelope. Uses the fixture's own http.Client so TLS-fronted
// fixtures (httptest.NewTLSServer) accept the self-signed cert.
func graphQLPost(t *testing.T, fix *orchardFixture, query string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, fix.srv.URL+"/graphql", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := fix.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v: %s", err, string(raw))
	}
	return out
}

// TestPeers_Reachable boots a remote, configures local with it as a
// peer, polls until the local probe succeeds, then asserts
// `host.peers[*].reachable == true`.
func TestPeers_Reachable(t *testing.T) {
	remote := startOrchard(t, peerproxy.FederationConfig{})
	local := startOrchard(t, peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	// The local peerproxy supervisor probes on Start and every 30s.
	// We poll `host.peers.reachable` for a few seconds — once the
	// supervisor's first probe lands the answer flips to true.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local,
			`{ host { peers { id reachable } } }`)
		errs, _ := envelope["errors"].([]any)
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		if len(peers) == 1 {
			peer := peers[0].(map[string]any)
			if peer["reachable"] == true {
				if peer["id"] != "Host:"+remoteName {
					t.Fatalf("unexpected peer id %v", peer["id"])
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer never marked reachable; envelope=%v", envelope)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestQueryPeers_TopLevel boots a local daemon with one configured
// peer and asserts the top-level `Query.peers` aggregate returns that
// peer without traversing `hosts`. This is the AC for #425: callers
// should not have to thread through `hosts[0].peers`.
func TestQueryPeers_TopLevel(t *testing.T) {
	remote := startOrchard(t, peerproxy.FederationConfig{})
	local := startOrchard(t, peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	// Poll the top-level `peers` field — same flat shape as
	// `tmuxSessions` and `claudeInstances`. Local has one peer, so the
	// flat aggregate must surface exactly that peer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local,
			`{ peers { id hostname address reachable } }`)
		if errs, ok := envelope["errors"].([]any); ok && len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		data, _ := envelope["data"].(map[string]any)
		peers, _ := data["peers"].([]any)
		if len(peers) == 1 {
			peer := peers[0].(map[string]any)
			if peer["id"] != "Host:"+remoteName {
				t.Fatalf("unexpected peer id %v", peer["id"])
			}
			if peer["hostname"] != remoteName {
				t.Fatalf("unexpected hostname %v", peer["hostname"])
			}
			if peer["reachable"] == true {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("top-level peers never surfaced reachable peer; envelope=%v", envelope)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestQueryHostServices_TopLevel asserts `Query.hostServices` returns
// the same shape as `host.hostServices` on a daemon with no
// hostservice provider wired (defaulting to an empty list). Even with
// no services the flat field must not error.
func TestQueryHostServices_TopLevel(t *testing.T) {
	local := startOrchard(t, peerproxy.FederationConfig{})

	envelope := graphQLPost(t, local,
		`{ hostServices { id name state } }`)
	if errs, ok := envelope["errors"].([]any); ok && len(errs) > 0 {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := envelope["data"].(map[string]any)
	if _, ok := data["hostServices"].([]any); !ok {
		t.Fatalf("expected hostServices array, got %v", data)
	}
}

// TestNode_ProxiedLookup queries `node(id: "TmuxPane:<remote>:%26")`
// against the local daemon and asserts the response was forwarded —
// the typename + id come back from the remote, not from a local
// fallback.
func TestNode_ProxiedLookup(t *testing.T) {
	remote := startOrchard(t, peerproxy.FederationConfig{})
	local := startOrchard(t, peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	const wantID = "TmuxPane:remote-host:%fake"
	query := fmt.Sprintf(`{ node(id: %q) { __typename id } }`, wantID)
	envelope := graphQLPost(t, local, query)
	if errs, ok := envelope["errors"].([]any); ok && len(errs) > 0 {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := envelope["data"].(map[string]any)
	node, _ := data["node"].(map[string]any)
	if node == nil {
		t.Fatalf("expected node, got %v", data)
	}
	if node["__typename"] != "TmuxPane" {
		t.Fatalf("expected TmuxPane typename, got %v", node["__typename"])
	}
	if node["id"] != wantID {
		t.Fatalf("expected echoed id %q, got %v", wantID, node["id"])
	}
}

// TestSubscription_PeerTunnel opens a websocket subscription against
// the local daemon's `Subscription.peer(host: "remote-host")` and
// fires a synthetic invalidation on the remote's LocalInvalidator.
// The subscriber must observe the touched node within the timeout.
func TestSubscription_PeerTunnel(t *testing.T) {
	remote := startOrchard(t, peerproxy.FederationConfig{})
	local := startOrchard(t, peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	wsURL := "ws://" + local.addr + "/graphql"

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"graphql-transport-ws"}
	dialer.HandshakeTimeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial local ws: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// connection_init → connection_ack
	if err := conn.WriteJSON(map[string]any{"type": "connection_init"}); err != nil {
		t.Fatalf("write init: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var ack map[string]any
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack["type"] != "connection_ack" {
		t.Fatalf("expected connection_ack, got %v", ack["type"])
	}

	const subQuery = `subscription { peer(host: "remote-host") { __typename id } }`
	if err := conn.WriteJSON(map[string]any{
		"id":      "1",
		"type":    "subscribe",
		"payload": map[string]any{"query": subQuery},
	}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	// Give the remote daemon time to register both the supervisor's
	// subscription (opened at provider Start) AND the resolver-driven
	// one we just sent. Until BOTH are registered, the LocalInvalidator
	// fan-out may miss the resolver-driven path and the test will flake.
	if !waitForCondition(3*time.Second, func() bool {
		return remote.events.SubscriberCount() >= 2
	}) {
		t.Fatalf("expected 2 upstream subscriptions on remote, saw %d",
			remote.events.SubscriberCount())
	}

	const wantID = "TmuxPane:remote-host:%99"
	remote.events.Push(peerproxy.InvalidationEvent{
		NodeID: peerproxy.NodeID(wantID),
		Reason: "test",
		At:     time.Now(),
	})

	// Pump frames off the websocket through a channel so we don't
	// run into gorilla's panic-on-second-read-after-error.
	type frame struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	frames := make(chan frame, 8)
	readErr := make(chan error, 1)
	go func() {
		for {
			var f frame
			if err := conn.ReadJSON(&f); err != nil {
				readErr <- err
				close(frames)
				return
			}
			frames <- f
		}
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("ws closed before invalidation arrived: %v", <-readErr)
			}
			if f.Type != "next" {
				continue
			}
			var payload struct {
				Data struct {
					Peer struct {
						Typename string `json:"__typename"`
						ID       string `json:"id"`
					} `json:"peer"`
				} `json:"data"`
			}
			if err := json.Unmarshal(f.Payload, &payload); err != nil {
				continue
			}
			if payload.Data.Peer.ID == wantID {
				if payload.Data.Peer.Typename != "TmuxPane" {
					t.Fatalf("expected TmuxPane typename, got %q", payload.Data.Peer.Typename)
				}
				return
			}
		case <-deadline:
			t.Fatalf("subscriber never received invalidation for %s", wantID)
		}
	}
}

// TestNode_UnknownPeer confirms the resolver routes only known-peer
// ids through the proxy. An id whose host is not configured falls
// back to the local resolver path (which surfaces a stub Node here
// because no real provider is wired in this fixture — the test
// asserts that no proxy attempt is made and the response is local).
func TestNode_UnknownPeer(t *testing.T) {
	local := startOrchard(t, peerproxy.FederationConfig{}) // no peers

	envelope := graphQLPost(t, local,
		`{ node(id: "TmuxPane:not-a-peer:%abc") { __typename id } }`)
	if errs, ok := envelope["errors"].([]any); ok && len(errs) > 0 {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := envelope["data"].(map[string]any)
	node, _ := data["node"].(map[string]any)
	if node == nil {
		t.Fatalf("expected stub local node, got nil")
	}
	if node["id"] != "TmuxPane:not-a-peer:%abc" {
		t.Fatalf("expected echoed id, got %v", node["id"])
	}
}

// TestPeers_TLS_ReachableAndProxiedLookup is the AC-5 coverage for
// HTTPS/WSS support: a TLS-fronted "remote" daemon, a "local" daemon
// configured with `tls=true` for that peer, and the same `host.peers`
// + `node(id)` round-trips that the plain-HTTP suite exercises.
//
// The remote uses `httptest.NewTLSServer` (self-signed cert). The local
// daemon's peerproxy is given the corresponding *tls.Config so its
// dialer accepts the test cert — this MUST stay test-scoped:
// production code never sets InsecureSkipVerify.
func TestPeers_TLS_ReachableAndProxiedLookup(t *testing.T) {
	remote := startOrchardOpts(t,
		peerproxy.FederationConfig{},
		fixtureOpts{tlsServer: true},
	)
	clientTLS := tlsConfigFromTestServer(remote.srv)
	local := startOrchardOpts(t,
		peerproxy.FederationConfig{
			Peers: []peerproxy.PeerConfig{
				{Name: remoteName, Address: remote.addr, TLS: true},
			},
		},
		fixtureOpts{tlsConfig: clientTLS},
	)

	// Wait until the local supervisor's HTTPS probe of the remote lands.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local,
			`{ host { peers { id reachable } } }`)
		if errs, _ := envelope["errors"].([]any); len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		if len(peers) == 1 {
			peer := peers[0].(map[string]any)
			if peer["reachable"] == true {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("TLS peer never reachable; envelope=%v", envelope)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Proxied node lookup over HTTPS.
	const wantID = "TmuxPane:remote-host:%fake"
	q := fmt.Sprintf(`{ node(id: %q) { __typename id } }`, wantID)
	envelope := graphQLPost(t, local, q)
	if errs, _ := envelope["errors"].([]any); len(errs) > 0 {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := envelope["data"].(map[string]any)
	node, _ := data["node"].(map[string]any)
	if node == nil {
		t.Fatalf("expected node, got %v", data)
	}
	if node["id"] != wantID {
		t.Fatalf("expected echoed id %q, got %v", wantID, node["id"])
	}
}

// TestPeers_TLS_WSSHandshake confirms the local supervisor's
// `peerproxy.Client.Subscribe` upgrades to WSS against the TLS-fronted
// remote. Once the supervisor's subscription is registered on the
// remote's LocalInvalidator, we know the WSS handshake + connection_init
// completed — covering AC-2's wsURL() change end-to-end.
func TestPeers_TLS_WSSHandshake(t *testing.T) {
	remote := startOrchardOpts(t,
		peerproxy.FederationConfig{},
		fixtureOpts{tlsServer: true},
	)
	clientTLS := tlsConfigFromTestServer(remote.srv)
	_ = startOrchardOpts(t,
		peerproxy.FederationConfig{
			Peers: []peerproxy.PeerConfig{
				{Name: remoteName, Address: remote.addr, TLS: true},
			},
		},
		fixtureOpts{tlsConfig: clientTLS},
	)

	if !waitForCondition(5*time.Second, func() bool {
		return remote.events.SubscriberCount() >= 1
	}) {
		t.Fatalf("expected ≥1 upstream WSS subscription on remote, saw %d",
			remote.events.SubscriberCount())
	}
}

// fixedPsRunner returns a CommandRunner whose `ps -ax -o pid,...`
// output is deterministic and tagged with `tag` in the COMMAND column.
// Lets the federation test prove that data on the wire actually came
// from the remote daemon's table, not the local one.
type fixedPsRunner struct {
	tag string
	pid int
}

func (r fixedPsRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ps" {
		return nil, fmt.Errorf("unexpected exec %q", name)
	}
	header := "  PID  PPID USER             TT  %CPU    RSS                 STARTED COMMAND\n"
	row := fmt.Sprintf(" %4d     1 testuser         ??   0.0    100 Mon Jan  1 00:00:00 2024 %s\n", r.pid, r.tag)
	return []byte(header + row), nil
}

// TestPeers_Processes_FederatedPerPeer is the load-bearing regression
// test for #465: querying `host { peers { processes } }` on the local
// daemon must return the *peer's* process table, not the local one.
//
// Setup: two daemons in the same process. Each runs a stub `ps` that
// emits exactly one process whose COMMAND column is the daemon's tag —
// "local-cmd" on the local, "remote-cmd" on the remote. Local is
// configured with the remote as a peer.
//
// Assertion: `peers[0].processes[0].command == "remote-cmd"` AND
// `peers[0].processes[0].id` carries the remote-host prefix. If the
// federation glue regresses, the test sees "local-cmd" and fails — the
// exact symptom the bug report describes.
func TestPeers_Processes_FederatedPerPeer(t *testing.T) {
	const localHostID = "local-mac"
	remote := startOrchardOpts(t,
		peerproxy.FederationConfig{},
		fixtureOpts{
			psHostID: remoteName,
			psRunner: fixedPsRunner{tag: "remote-cmd", pid: 7777},
		},
	)
	local := startOrchardOpts(t,
		peerproxy.FederationConfig{
			Peers: []peerproxy.PeerConfig{
				{Name: remoteName, Address: remote.addr},
			},
		},
		fixtureOpts{
			psHostID: localHostID,
			psRunner: fixedPsRunner{tag: "local-cmd", pid: 1111},
		},
	)

	// Wait for the local supervisor to mark the remote reachable, then
	// query peers[].processes. Without reachability the federate path
	// short-circuits with an error.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local,
			`{ host { peers { id reachable } } }`)
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		if len(peers) == 1 {
			peer := peers[0].(map[string]any)
			if peer["reachable"] == true {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer never reachable; envelope=%v", envelope)
		}
		time.Sleep(100 * time.Millisecond)
	}

	envelope := graphQLPost(t, local,
		`{ host { peers { id processes { id pid command } } } }`)
	if errs, ok := envelope["errors"].([]any); ok && len(errs) > 0 {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := envelope["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("expected exactly 1 peer, got %d (%v)", len(peers), peers)
	}
	peer := peers[0].(map[string]any)
	if peer["id"] != "Host:"+remoteName {
		t.Fatalf("unexpected peer id %v", peer["id"])
	}
	procs, _ := peer["processes"].([]any)
	if len(procs) == 0 {
		t.Fatalf("peer.processes empty; expected federated remote process table")
	}
	// Find the seeded remote-cmd row. Stub `ps` only emits one process,
	// so any COMMAND other than "remote-cmd" means we got the local
	// daemon's table — i.e. the bug regressed.
	var seenRemote bool
	for _, p := range procs {
		row := p.(map[string]any)
		cmd, _ := row["command"].(string)
		id, _ := row["id"].(string)
		if cmd == "local-cmd" {
			t.Fatalf("FEDERATION REGRESSION: peer.processes contains local daemon's row %+v (#465)", row)
		}
		if cmd == "remote-cmd" {
			seenRemote = true
			// id is built remote-side as `<remoteHostID>:<pid>`. The
			// federation glue MUST NOT rewrite it to the local prefix.
			if id != remoteName+":7777" {
				t.Fatalf("expected remote-prefixed id %q, got %q (federation rewrote the prefix)",
					remoteName+":7777", id)
			}
		}
	}
	if !seenRemote {
		t.Fatalf("peer.processes did not include the seeded remote-cmd row; got %v", procs)
	}
}

// TestPeers_Processes_UnreachablePeer asserts the second half of #465:
// when a peer is configured but not reachable, `peers[].processes` must
// surface a typed error rather than silently returning the local
// daemon's process table. The bug report explicitly calls out this
// confidently-wrong shape for unreachable peers.
func TestPeers_Processes_UnreachablePeer(t *testing.T) {
	const localHostID = "local-mac"
	const deadPeer = "fork-orchardist-punch"
	local := startOrchardOpts(t,
		peerproxy.FederationConfig{
			Peers: []peerproxy.PeerConfig{
				// Address points to a closed port so reachability stays false.
				{Name: deadPeer, Address: "127.0.0.1:1"},
			},
		},
		fixtureOpts{
			psHostID: localHostID,
			psRunner: fixedPsRunner{tag: "local-cmd", pid: 1111},
		},
	)

	// Wait until the supervisor's first probe fails — reachable=false.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local,
			`{ host { peers { reachable } } }`)
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		if len(peers) == 1 {
			peer := peers[0].(map[string]any)
			// reachable can be reported as false (probe completed) or
			// nil (probe pending). Either is fine — we just need it
			// not-true.
			if r, ok := peer["reachable"].(bool); ok && !r {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer reachability never resolved; envelope=%v", envelope)
		}
		time.Sleep(100 * time.Millisecond)
	}

	envelope := graphQLPost(t, local,
		`{ host { peers { id processes { id command } } } }`)
	// Either the field surfaces an error AND processes is null, OR the
	// processes list is empty. Returning the local daemon's table is
	// the regression — assert it never appears.
	data, _ := envelope["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) == 1 {
		peer := peers[0].(map[string]any)
		if procs, ok := peer["processes"].([]any); ok {
			for _, p := range procs {
				row := p.(map[string]any)
				if cmd, _ := row["command"].(string); cmd == "local-cmd" {
					t.Fatalf("FEDERATION REGRESSION: unreachable peer returned local daemon's row %+v (#465)", row)
				}
			}
		}
	}
	// Errors slice should mention the unreachable peer when present —
	// this is the typed-error half of the AC. Silent success with []
	// processes would mask the real failure mode.
	errs, _ := envelope["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("expected GraphQL errors for unreachable peer.processes; envelope=%v", envelope)
	}
}

// TestEndToEnd_AddingPeerSurfacesInGraphQL is the E2E proof for the feature
// scenario:
//
//	"Adding a peer entry surfaces it in the live Host.peers GraphQL query
//	 within 2s"
//
// The test drives the full stack end-to-end: a real daemon, a real config file
// watched by ConfigWatcher, and a real GraphQL query. No mocking of the
// Provider/ConfigWatcher internals.
//
// Steps:
//  1. Start two fake peer servers (orchard.boxd.sh, lw-fed-c) that count pings.
//  2. Write an initial config file with only orchard.boxd.sh.
//  3. Construct a Provider from the file-loaded config; start it.
//  4. Start a ConfigWatcher on the config file with a short debounce (100ms).
//  5. Wire server.New + mount GraphQL on an httptest.Server.
//  6. Wait for orchard.boxd.sh to appear in host.peers (initial round-trip).
//  7. Atomically rewrite the config to add lw-fed-c.
//  8. Poll host.peers every 100ms for up to 2 seconds.
//  9. Assert: lw-fed-c appears with machineId=="lw-fed-c".
// 10. Assert: fakeFedC.pingCount >= 1 (probe goroutine issued at least one Ping).
// 11. Assert: orchard.boxd.sh is still present (no regression).
func TestEndToEnd_AddingPeerSurfacesInGraphQL(t *testing.T) {
	// 1. Two fake peer servers.
	fakeBoxd := newFakePeer(t)
	fakeFedC := newFakePeer(t)

	// 2. Write the initial config — only orchard.boxd.sh.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	writeConfig(t, cfgPath, []peerproxy.PeerConfig{
		{Name: "orchard.boxd.sh", Address: fakeBoxd.addr(), TLS: false},
	})

	// 3. Construct provider from the on-disk config.
	initialCfg := loadConfig(t, cfgPath)
	logger := slog.Default()
	peerProvider := peerproxy.NewProvider(initialCfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := peerProvider.Start(ctx); err != nil {
		t.Fatalf("Provider.Start: %v", err)
	}
	t.Cleanup(func() { _ = peerProvider.Stop() })

	// 4. Start ConfigWatcher with a short debounce so the 2-second assertion
	// window comfortably covers debounce + probe latency.
	const debounce = 100 * time.Millisecond
	cw := peerproxy.NewConfigWatcher(cfgPath, peerProvider, logger,
		peerproxy.WithDebounce(debounce))
	if err := cw.Start(ctx); err != nil {
		t.Fatalf("ConfigWatcher.Start: %v", err)
	}
	t.Cleanup(func() { _ = cw.Close() })

	// 5. Wire the local daemon and mount GraphQL.
	localEvents := peerproxy.NewLocalInvalidator()
	srv := server.New("", logger,
		server.WithPeerProxy(peerProvider),
		server.WithLocalEvents(localEvents),
	)
	if err := srv.StartHostProvider(ctx); err != nil {
		t.Fatalf("StartHostProvider: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", srv.GraphQLHandler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	localFix := &orchardFixture{
		addr:   mustStripScheme(t, ts.URL),
		srv:    ts,
		events: localEvents,
	}

	// 6. Wait for orchard.boxd.sh to appear in host.peers. The local
	// peerproxy supervisor probes on Start; once it lands the reachable
	// bit flips to true and the peer appears in the GraphQL response.
	if !waitForCondition(5*time.Second, func() bool {
		envelope := graphQLPost(t, localFix,
			`{ host { peers { id machineId reachable } } }`)
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		for _, p := range peers {
			row, _ := p.(map[string]any)
			if mid, _ := row["machineId"].(string); mid == "orchard.boxd.sh" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("orchard.boxd.sh never appeared in host.peers within 5s (initial round-trip failed)")
	}

	// 7. Atomically rewrite the config to add lw-fed-c.
	// Use write-then-rename for atomicity — same as `orchard config add-peer`.
	newCfgData, err := json.Marshal(map[string]any{"peers": []peerproxy.PeerConfig{
		{Name: "orchard.boxd.sh", Address: fakeBoxd.addr(), TLS: false},
		{Name: "lw-fed-c", Address: fakeFedC.addr(), TLS: false},
	}})
	if err != nil {
		t.Fatalf("marshal new config: %v", err)
	}
	tmpCfg := cfgPath + ".tmp"
	if err := os.WriteFile(tmpCfg, newCfgData, 0o644); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	if err := os.Rename(tmpCfg, cfgPath); err != nil {
		t.Fatalf("rename config: %v", err)
	}

	// 8–9. Poll host.peers every 100ms for up to 2 seconds. Record the last
	// envelope for a meaningful failure message at deadline.
	const pollInterval = 100 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	var lastEnvelope map[string]any
	var foundFedC, foundBoxd bool
	for time.Now().Before(deadline) {
		envelope := graphQLPost(t, localFix,
			`{ host { peers { id machineId reachable } } }`)
		lastEnvelope = envelope
		data, _ := envelope["data"].(map[string]any)
		host, _ := data["host"].(map[string]any)
		peers, _ := host["peers"].([]any)
		foundFedC = false
		foundBoxd = false
		for _, p := range peers {
			row, _ := p.(map[string]any)
			mid, _ := row["machineId"].(string)
			switch mid {
			case "lw-fed-c":
				foundFedC = true
			case "orchard.boxd.sh":
				foundBoxd = true
			}
		}
		if foundFedC {
			break
		}
		time.Sleep(pollInterval)
	}

	// 9. lw-fed-c must appear within 2 seconds.
	if !foundFedC {
		t.Fatalf("lw-fed-c never appeared in host.peers within 2s; last response: %v", lastEnvelope)
	}

	// 10. Provider.Start spawned a runPeer goroutine for lw-fed-c which calls
	// Probe() immediately. The fake server counts all POST /graphql requests;
	// at least one Ping must have landed.
	if fakeFedC.pingCount.Load() < 1 {
		t.Fatalf("no Ping to lw-fed-c observed after it appeared in host.peers (pingCount=%d)", fakeFedC.pingCount.Load())
	}

	// 11. orchard.boxd.sh must still be present — adding a peer must not evict
	// an existing peer.
	if !foundBoxd {
		t.Fatalf("orchard.boxd.sh disappeared from host.peers after adding lw-fed-c; last response: %v", lastEnvelope)
	}
}

// mustStripScheme is the panic-on-error variant of stripScheme for inline use
// in test setup where a bad URL is a programming error.
func mustStripScheme(t *testing.T, rawURL string) string {
	t.Helper()
	h, err := stripScheme(rawURL)
	if err != nil {
		t.Fatalf("mustStripScheme(%q): %v", rawURL, err)
	}
	return h
}

// tlsConfigFromTestServer extracts a *tls.Config that trusts ts's
// self-signed cert. This is the standard httptest pattern — `ts.Client()`
// returns an http.Client whose Transport's TLSClientConfig has the
// test CA in its RootCAs pool. Cloning it gives peerproxy a config it
// can install on its own dialer.
func tlsConfigFromTestServer(ts *httptest.Server) *tls.Config {
	tr, ok := ts.Client().Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		// Fallback for older httptest internals — accept the test cert
		// only. Production code MUST NOT do this.
		return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test-only
	}
	return tr.TLSClientConfig.Clone()
}

// waitForCondition polls fn until it returns true or the timeout fires.
func waitForCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// dump is a debug helper kept around for the rare test failure that
// needs the raw envelope. Not called in normal runs.
func dump(t *testing.T, label string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	t.Logf("%s: %s", label, b)
}

// avoid unused warnings when dump is unused.
var _ = fmt.Stringer(nil)
var _ = dump
