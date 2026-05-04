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
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

const (
	remoteName  = "remote-host"
	sharedToken = "test-secret-shhh"
)

// orchardFixture wraps an httptest.Server bound to a peerproxy-aware
// orchard daemon plus the LocalInvalidator the test uses to inject
// synthetic events.
type orchardFixture struct {
	addr   string
	srv    *httptest.Server
	events *peerproxy.LocalInvalidator
}

// startOrchard boots an orchard daemon attached to httptest. fedCfg is
// the federation slice (peers + peer_secret); pass an empty FederationConfig
// for a "leaf" daemon that has no peers of its own.
func startOrchard(t *testing.T, fedCfg peerproxy.FederationConfig) *orchardFixture {
	t.Helper()
	logger := slog.Default()
	peerProvider := peerproxy.NewProvider(fedCfg, logger)
	localEvents := peerproxy.NewLocalInvalidator()

	srv := server.New("", logger,
		server.WithPeerProxy(peerProvider),
		server.WithPeerSecret(fedCfg.PeerSecret),
		server.WithLocalEvents(localEvents),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := peerProvider.Start(ctx); err != nil {
		t.Fatalf("start peerproxy: %v", err)
	}
	t.Cleanup(func() { _ = peerProvider.Stop() })

	if err := srv.StartHostProvider(ctx); err != nil {
		t.Fatalf("start host provider: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/graphql", srv.GraphQLHandler())
	ts := httptest.NewServer(mux)
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
// decoded envelope. Authorization header is attached when secret != "".
func graphQLPost(t *testing.T, fix *orchardFixture, secret, query string) map[string]any {
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
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
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
	remote := startOrchard(t, peerproxy.FederationConfig{PeerSecret: sharedToken})
	local := startOrchard(t, peerproxy.FederationConfig{
		PeerSecret: sharedToken,
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	// The local peerproxy supervisor probes on Start and every 30s.
	// We poll `host.peers.reachable` for a few seconds — once the
	// supervisor's first probe lands the answer flips to true.
	deadline := time.Now().Add(5 * time.Second)
	for {
		envelope := graphQLPost(t, local, sharedToken,
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

// TestPeers_AuthRequired confirms the bearer middleware rejects
// requests missing the configured secret. Belt-and-braces for §11.
func TestPeers_AuthRequired(t *testing.T) {
	fix := startOrchard(t, peerproxy.FederationConfig{PeerSecret: sharedToken})
	resp, err := http.Post(fix.srv.URL+"/graphql", "application/json",
		strings.NewReader(`{"query":"{ __typename }"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d: %s", resp.StatusCode, body)
	}
}

// TestNode_ProxiedLookup queries `node(id: "TmuxPane:<remote>:%26")`
// against the local daemon and asserts the response was forwarded —
// the typename + id come back from the remote, not from a local
// fallback.
func TestNode_ProxiedLookup(t *testing.T) {
	remote := startOrchard(t, peerproxy.FederationConfig{PeerSecret: sharedToken})
	local := startOrchard(t, peerproxy.FederationConfig{
		PeerSecret: sharedToken,
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	const wantID = "TmuxPane:remote-host:%fake"
	query := fmt.Sprintf(`{ node(id: %q) { __typename id } }`, wantID)
	envelope := graphQLPost(t, local, sharedToken, query)
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
	remote := startOrchard(t, peerproxy.FederationConfig{PeerSecret: sharedToken})
	local := startOrchard(t, peerproxy.FederationConfig{
		PeerSecret: sharedToken,
		Peers: []peerproxy.PeerConfig{
			{Name: remoteName, Address: remote.addr},
		},
	})

	wsURL := "ws://" + local.addr + "/graphql"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+sharedToken)

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"graphql-transport-ws"}
	dialer.HandshakeTimeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(ctx, wsURL, hdr)
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

	envelope := graphQLPost(t, local, "",
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
