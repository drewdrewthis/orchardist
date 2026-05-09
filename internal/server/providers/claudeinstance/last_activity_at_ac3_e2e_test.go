package claudeinstance_test

// E2E tests for ClaudeInstance.lastActivityAt — AC 3 of issue #443.
//
// Two @e2e scenarios from specs/features/schema-claude-instance-last-activity-at.feature:
//
//  1. "Live daemon query returns lastActivityAt for tracked Claude instances"
//     — Query claudeInstances and assert that every instance with a heartbeat
//     last_activity returns a non-null, RFC3339-parseable lastActivityAt.
//
//  2. "Updates to a heartbeat are observable in the next nodeChanged event"
//     — Subscribe via WebSocket. Rewrite the heartbeat with a newer last_activity.
//     Provider.Refresh is called directly to simulate the watcher sweep. Expect
//     the subscriber to receive a nodeChanged event whose lastActivityAt matches
//     the new value.
//
// E2E infrastructure note: the live daemon at 127.0.0.1:7777 is not
// available in CI. These tests are implemented as httptest-backed integration
// tests that use the full GraphQL stack (schema, resolver, subscription relay,
// WebSocket transport) against a Provider driven by real heartbeat files in
// t.TempDir(). This is the same pattern used by the nodechanged_e2e_test.go
// file in internal/server/resolvers/ for WebSocket subscription tests.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/gorilla/websocket"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// newTestDaemonWS is like newTestDaemon but also registers the WebSocket
// transport so Subscription.nodeChanged is reachable over WS. Used for
// the AC 3 subscription e2e test.
func newTestDaemonWS(t *testing.T, provider *claudeinstance.Provider) *httptest.Server {
	t.Helper()
	cfg := gql.Config{Resolvers: resolvers.New(time.Now()).WithClaudeInstance(provider)}
	gqlSrv := handler.New(gql.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})
	gqlSrv.AddTransport(transport.Websocket{
		KeepAlivePingInterval: 10 * time.Second,
		Upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(*http.Request) bool { return true },
			Subprotocols:    []string{"graphql-transport-ws", "graphql-ws"},
		},
	})

	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// instanceNodeWithActivity extends instanceNode with the lastActivityAt
// field so it can be decoded from the query response.
type instanceNodeWithActivity struct {
	ID             string  `json:"id"`
	State          string  `json:"state"`
	LastActivityAt *string `json:"lastActivityAt"`
}

// graphqlResponseWithActivity decodes a claudeInstances query that
// selects lastActivityAt.
type graphqlResponseWithActivity struct {
	Data struct {
		ClaudeInstances []instanceNodeWithActivity `json:"claudeInstances"`
	} `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

// postQueryActivity issues a claudeInstances query that includes
// lastActivityAt and decodes the result.
func postQueryActivity(t *testing.T, serverURL string) graphqlResponseWithActivity {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": `query { claudeInstances { id state lastActivityAt } }`})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out graphqlResponseWithActivity
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode response %q: %v", raw, err)
	}
	return out
}

// TestClaudeInstance_E2E_QueryLastActivityAt covers the @e2e scenario:
//
//	"Live daemon query returns lastActivityAt for tracked Claude instances"
//
// Writes two heartbeats: one with last_activity set, one without.
// Queries `{ claudeInstances { id state lastActivityAt } }` and asserts:
//   - The instance whose heartbeat has last_activity returns a non-null,
//     RFC3339-parseable lastActivityAt.
//   - The instance whose heartbeat lacks last_activity AND whose pane has
//     no lastActivityAt returns null.
func TestClaudeInstance_E2E_QueryLastActivityAt(t *testing.T) {
	heartbeatDir := t.TempDir()
	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC)
	freshTS := now.Add(-2 * time.Second).Format(time.RFC3339)
	const activityTS = "2026-05-07T18:42:11Z"

	// alpha: has last_activity.
	writeHeartbeat(t, heartbeatDir, "alpha", map[string]any{
		"tmux_session":    "alpha",
		"session_id":      "uuid-alpha",
		"state":           "working",
		"timestamp":       freshTS,
		"claudePid":       20042,
		"lastHeartbeatAt": freshTS,
		"last_activity":   activityTS,
	})

	// bravo: no last_activity, no pane → lastActivityAt must be null.
	writeHeartbeat(t, heartbeatDir, "bravo", map[string]any{
		"tmux_session":    "bravo",
		"session_id":      "uuid-bravo",
		"state":           "idle",
		"timestamp":       freshTS,
		"claudePid":       20099,
		"lastHeartbeatAt": freshTS,
	})

	panes := &fakePaneFinder{
		byPid: map[int]*gql.TmuxPane{
			20042: {ID: "TmuxPane:local:%42"},
			// bravo (20099) intentionally absent so lastActivityAt is null.
		},
	}
	procs := &fakeProcessFinder{
		byPid: map[int]*gql.Process{
			20042: {ID: "Process:local:20042"},
			20099: {ID: "Process:local:20099"},
		},
	}
	liveness := fakeLiveness{alive: map[int]bool{20042: true, 20099: true}}

	composer := claudeinstance.NewComposerWith(
		"local", panes, procs, &fakeAccountFinder{}, liveness, nil,
		func() time.Time { return now }, claudeinstance.HeartbeatStaleAfter,
	)
	reader := claudeinstance.NewFileReader(heartbeatDir)
	provider := claudeinstance.NewWith("local", reader, composer, func() time.Time { return now })
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	resp := postQueryActivity(t, srv.URL)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.ClaudeInstances); got != 2 {
		t.Fatalf("got %d instances, want 2", got)
	}

	byID := map[string]instanceNodeWithActivity{}
	for _, inst := range resp.Data.ClaudeInstances {
		byID[inst.ID] = inst
	}

	// alpha: must have a parseable lastActivityAt.
	alpha, ok := byID["ClaudeInstance:local:20042"]
	if !ok {
		t.Fatalf("alpha instance missing from response; got: %v", byID)
	}
	if alpha.LastActivityAt == nil {
		t.Error("alpha.lastActivityAt is nil; expected RFC3339 string from heartbeat last_activity")
	} else {
		got := *alpha.LastActivityAt
		if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
			if _, err2 := time.Parse(time.RFC3339, got); err2 != nil {
				t.Errorf("alpha.lastActivityAt %q is not parseable as RFC3339: %v", got, err)
			}
		}
		wantParsed, _ := time.Parse(time.RFC3339, activityTS)
		gotParsed, _ := time.Parse(time.RFC3339Nano, got)
		if !gotParsed.Equal(wantParsed) {
			t.Errorf("alpha.lastActivityAt = %v, want %v", gotParsed, wantParsed)
		}
	}

	// bravo: no last_activity and no pane → lastActivityAt must be null.
	bravo, ok := byID["ClaudeInstance:local:20099"]
	if !ok {
		t.Fatalf("bravo instance missing from response; got: %v", byID)
	}
	if bravo.LastActivityAt != nil {
		t.Errorf("bravo.lastActivityAt = %v, want null (no heartbeat last_activity, no pane)", bravo.LastActivityAt)
	}
}

// TestClaudeInstance_E2E_SubscriptionOnLastActivityChange covers the
// @e2e scenario:
//
//	"Updates to a heartbeat are observable in the next nodeChanged event"
//
// Opens a WebSocket subscription on Subscription.nodeChanged for
// "ClaudeInstance:local:30042". Rewrites the heartbeat file with a newer
// last_activity. Calls provider.Refresh to simulate the watcher sweep.
// Asserts the subscriber receives a nodeChanged event whose lastActivityAt
// matches the new value.
func TestClaudeInstance_E2E_SubscriptionOnLastActivityChange(t *testing.T) {
	heartbeatDir := t.TempDir()
	now := time.Date(2026, 5, 7, 18, 42, 15, 0, time.UTC)
	freshTS := now.Add(-2 * time.Second).Format(time.RFC3339)
	const initialActivity = "2026-05-07T18:30:00Z"
	const updatedActivity = "2026-05-07T18:42:11Z"
	const pid = 30042

	writeHeartbeat(t, heartbeatDir, "gamma", map[string]any{
		"tmux_session":    "gamma",
		"session_id":      "uuid-gamma",
		"state":           "working",
		"timestamp":       freshTS,
		"claudePid":       pid,
		"lastHeartbeatAt": freshTS,
		"last_activity":   initialActivity,
	})

	liveness := fakeLiveness{alive: map[int]bool{pid: true}}
	composer := claudeinstance.NewComposerWith(
		"local",
		&fakePaneFinder{},
		&fakeProcessFinder{},
		&fakeAccountFinder{},
		liveness,
		nil,
		func() time.Time { return now },
		claudeinstance.HeartbeatStaleAfter,
	)
	reader := claudeinstance.NewFileReader(heartbeatDir)
	provider := claudeinstance.NewWith("local", reader, composer, func() time.Time { return now })
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	srv := newTestDaemonWS(t, provider)
	defer srv.Close()

	// Dial WebSocket and complete graphql-transport-ws handshake.
	wsURL := "ws://" + stripHost(t, srv.URL) + "/graphql"
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"graphql-transport-ws"}
	dialer.HandshakeTimeout = 5 * time.Second

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// connection_init → connection_ack.
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

	// Subscribe to nodeChanged for our instance id.
	const instanceID = "ClaudeInstance:local:30042"
	const subID = "ac3-sub-1"
	if err := conn.WriteJSON(map[string]any{
		"id":   subID,
		"type": "subscribe",
		"payload": map[string]any{
			"query": `subscription { nodeChanged(id: "ClaudeInstance:local:30042") { __typename ... on ClaudeInstance { id lastActivityAt } } }`,
		},
	}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}

	// Give the server a beat to register the subscription before we refresh.
	time.Sleep(100 * time.Millisecond)

	// Rewrite the heartbeat with the updated last_activity.
	writeHeartbeat(t, heartbeatDir, "gamma", map[string]any{
		"tmux_session":    "gamma",
		"session_id":      "uuid-gamma",
		"state":           "working",
		"timestamp":       freshTS,
		"claudePid":       pid,
		"lastHeartbeatAt": freshTS,
		"last_activity":   updatedActivity,
	})

	// Drive the refresh that the watcher would do.
	if err := provider.Refresh(context.Background(), "activity-update"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Read frames until we get a "next" for our subscription.
	type wsFrame struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	type nodeChangedPayload struct {
		Data struct {
			NodeChanged struct {
				Typename       string  `json:"__typename"`
				ID             string  `json:"id"`
				LastActivityAt *string `json:"lastActivityAt"`
			} `json:"nodeChanged"`
		} `json:"data"`
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var f wsFrame
		if err := conn.ReadJSON(&f); err != nil {
			t.Fatalf("read frame (timeout?): %v", err)
		}
		if f.ID != subID {
			continue
		}
		if f.Type == "error" {
			t.Fatalf("subscription error: %s", string(f.Payload))
		}
		if f.Type != "next" {
			continue
		}
		var p nodeChangedPayload
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v (%s)", err, string(f.Payload))
		}
		nc := p.Data.NodeChanged
		if nc.ID != instanceID {
			t.Fatalf("nodeChanged id = %q, want %q", nc.ID, instanceID)
		}
		if nc.Typename != "ClaudeInstance" {
			t.Fatalf("nodeChanged __typename = %q, want ClaudeInstance", nc.Typename)
		}
		if nc.LastActivityAt == nil {
			t.Fatal("nodeChanged.lastActivityAt is nil; expected the updated timestamp")
		}
		gotParsed, err := time.Parse(time.RFC3339Nano, *nc.LastActivityAt)
		if err != nil {
			gotParsed, err = time.Parse(time.RFC3339, *nc.LastActivityAt)
			if err != nil {
				t.Fatalf("nodeChanged.lastActivityAt %q not parseable: %v", *nc.LastActivityAt, err)
			}
		}
		wantParsed, _ := time.Parse(time.RFC3339, updatedActivity)
		if !gotParsed.Equal(wantParsed) {
			t.Errorf("nodeChanged.lastActivityAt = %v, want %v", gotParsed, wantParsed)
		}
		return // success
	}
}

// stripHost returns the host:port portion of a URL.
func stripHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u.Host
}
