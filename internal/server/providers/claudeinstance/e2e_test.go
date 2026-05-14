package claudeinstance_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// stubSnapshotReader implements claudeinstance.SnapshotReader for E2E tests.
// Keys are "cwd|sessionID".
type stubSnapshotReader struct {
	byKey map[string][]claudeinstance.Record
}

func (s *stubSnapshotReader) ReadSnapshot(_ context.Context, cwd, sessionID string) ([]claudeinstance.Record, bool) {
	if s.byKey == nil {
		return nil, false
	}
	recs, ok := s.byKey[cwd+"|"+sessionID]
	return recs, ok
}

// fakePaneFinder is the in-package fake the e2e test injects through
// claudeinstance.PaneFinder. Returns a stubbed pane for matching pids.
type fakePaneFinder struct {
	byPid     map[int]*gql.TmuxPane
	bySession map[string]*gql.TmuxPane
}

func (f *fakePaneFinder) FindByPid(_ context.Context, _ string, pid int) (*gql.TmuxPane, bool) {
	p, ok := f.byPid[pid]
	return p, ok
}

func (f *fakePaneFinder) FindBySession(_ context.Context, _, session string) (*gql.TmuxPane, bool) {
	p, ok := f.bySession[session]
	return p, ok
}

type fakeProcessFinder struct{ byPid map[int]*gql.Process }

func (f *fakeProcessFinder) FindByPid(_ context.Context, _ string, pid int) (*gql.Process, bool) {
	p, ok := f.byPid[pid]
	return p, ok
}

type fakeAccountFinder struct{ acct *gql.ClaudeAccount }

func (f *fakeAccountFinder) Active(_ context.Context, _ string) (*gql.ClaudeAccount, bool) {
	if f.acct == nil {
		return nil, false
	}
	return f.acct, true
}

// fakeLiveness wires the LivenessChecker for tests so we can keep both
// stubbed pids "alive" deterministically.
type fakeLiveness struct{ alive map[int]bool }

func (f fakeLiveness) IsAlive(pid int) bool { return f.alive[pid] }

// TestClaudeInstance_E2E_TwoFreshInstances writes two heartbeat files
// into a tmpdir, boots a httptest GraphQL server with stubbed sibling
// providers, queries `claudeInstances`, and asserts both instances
// appear with the right `state`. Then it touches one heartbeat
// backwards and asserts that instance's state collapses to no_claude.
//
// This is the briefing's headline E2E test. NO PII: tmux session names
// are fixture values ("alpha"/"bravo"), pids are obviously fake (10000+),
// and every path is `t.TempDir()`-rooted.
func TestClaudeInstance_E2E_TwoFreshInstances(t *testing.T) {
	heartbeatDir := t.TempDir()

	// Use a deterministic clock so the staleness window is precisely
	// reproducible across CI environments.
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	freshTimestamp := now.Add(-2 * time.Second).Format(time.RFC3339)
	clock := func() time.Time { return now }

	writeHeartbeat(t, heartbeatDir, "alpha", map[string]any{
		"tmux_session":    "alpha",
		"session_id":      "uuid-alpha",
		"state":           "working",
		"timestamp":       freshTimestamp,
		"claudePid":       10042,
		"cwd":             "/workspace/alpha",
		"rcUrl":           "https://claude.ai/code/session_alpha",
		"rcEnabled":       true,
		"lastHeartbeatAt": freshTimestamp,
	})
	writeHeartbeat(t, heartbeatDir, "bravo", map[string]any{
		"tmux_session":    "bravo",
		"session_id":      "uuid-bravo",
		"state":           "idle",
		"timestamp":       freshTimestamp,
		"claudePid":       10099,
		"cwd":             "/workspace/bravo",
		"rcEnabled":       false,
		"lastHeartbeatAt": freshTimestamp,
	})

	// Stubbed sibling providers — the dep-injection checkpoint.
	panes := &fakePaneFinder{
		byPid: map[int]*gql.TmuxPane{
			10042: {ID: "TmuxPane:local:%26"},
			10099: {ID: "TmuxPane:local:%27"},
		},
	}
	procs := &fakeProcessFinder{
		byPid: map[int]*gql.Process{
			10042: {ID: "Process:local:10042"},
			10099: {ID: "Process:local:10099"},
		},
	}
	accts := &fakeAccountFinder{
		acct: &gql.ClaudeAccount{ID: "ClaudeAccount:local:dev@example.com"},
	}
	liveness := fakeLiveness{alive: map[int]bool{10042: true, 10099: true}}

	// Snapshot reader: alpha has open tool_use (working), bravo is ended (idle).
	snaps := &stubSnapshotReader{byKey: map[string][]claudeinstance.Record{
		"/workspace/alpha|uuid-alpha": {
			{
				Timestamp: now.Add(-3 * time.Second),
				Type:      "user",
				Message:   &claudeinstance.Message{},
			},
			{
				Timestamp: now.Add(-2 * time.Second),
				Type:      "assistant",
				Message: &claudeinstance.Message{
					StopReason: "tool_use",
					Content: []claudeinstance.ContentItem{
						{Type: "tool_use", ID: "bash_1", Name: "Bash"},
					},
				},
			},
		},
		"/workspace/bravo|uuid-bravo": {
			{
				Timestamp: now.Add(-3 * time.Second),
				Type:      "user",
				Message:   &claudeinstance.Message{},
			},
			{
				Timestamp: now.Add(-2 * time.Second),
				Type:      "assistant",
				Message: &claudeinstance.Message{StopReason: "end_turn"},
			},
		},
	}}

	composer := claudeinstance.NewComposerWith("local", panes, procs, accts, liveness, nil, clock, claudeinstance.HeartbeatStaleAfter).WithSnapshot(snaps)
	reader := claudeinstance.NewFileReader(heartbeatDir)
	provider := claudeinstance.NewWith("local", reader, composer, clock)

	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	srv := newTestDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query {
		claudeInstances {
			id
			state
			rcUrl
			rcEnabled
			sessionUuid
			pane { id }
			process { id }
			account { id }
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.ClaudeInstances); got != 2 {
		t.Fatalf("got %d instances, want 2: %+v", got, resp.Data.ClaudeInstances)
	}

	// Find by id since order is by id-sort.
	instById := map[string]instanceNode{}
	for _, inst := range resp.Data.ClaudeInstances {
		instById[inst.ID] = inst
	}

	alpha, ok := instById["ClaudeInstance:local:10042"]
	if !ok {
		t.Fatalf("no alpha instance: %+v", instById)
	}
	if alpha.State != "working" {
		t.Errorf("alpha state = %s, want working", alpha.State)
	}
	if alpha.RcURL == nil || !strings.Contains(*alpha.RcURL, "claude.ai") {
		t.Errorf("alpha rcUrl = %v, want claude.ai URL", alpha.RcURL)
	}
	if !alpha.RcEnabled {
		t.Error("alpha rcEnabled = false, want true")
	}
	if alpha.Pane == nil || alpha.Pane.ID == "" {
		t.Errorf("alpha pane = %v, want stubbed pane", alpha.Pane)
	}
	if alpha.Process == nil || alpha.Process.ID == "" {
		t.Errorf("alpha process = %v, want stubbed process", alpha.Process)
	}
	if alpha.Account == nil || alpha.Account.ID == "" {
		t.Errorf("alpha account = %v, want stubbed account", alpha.Account)
	}

	bravo, ok := instById["ClaudeInstance:local:10099"]
	if !ok {
		t.Fatalf("no bravo instance: %+v", instById)
	}
	if bravo.State != "idle" {
		t.Errorf("bravo state = %s, want idle", bravo.State)
	}
	if bravo.RcEnabled {
		t.Error("bravo rcEnabled = true, want false")
	}

	// -------- Phase 2: touch alpha heartbeat backward but keep pid alive.
	// Hook is event-driven so an idle session legitimately stops
	// heartbeating; as long as the tracked pid is alive and the jsonl shows
	// working (open tool_use), the dashboard must keep showing working —
	// NOT collapse to no_claude. Cf. #421, #501.
	staleTimestamp := now.Add(-2 * time.Minute).Format(time.RFC3339)
	writeHeartbeat(t, heartbeatDir, "alpha", map[string]any{
		"tmux_session":    "alpha",
		"session_id":      "uuid-alpha",
		"state":           "working",
		"timestamp":       staleTimestamp,
		"claudePid":       10042,
		"cwd":             "/workspace/alpha",
		"rcUrl":           "https://claude.ai/code/session_alpha",
		"rcEnabled":       true,
		"lastHeartbeatAt": staleTimestamp,
	})
	if err := provider.Refresh(context.Background(), "test-stale"); err != nil {
		t.Fatalf("refresh after stale write: %v", err)
	}

	resp2 := postQuery(t, srv.URL, `query { claudeInstances { id state } }`)
	if len(resp2.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp2.Errors)
	}
	staleByID := map[string]string{}
	for _, inst := range resp2.Data.ClaudeInstances {
		staleByID[inst.ID] = inst.State
	}
	if got := staleByID["ClaudeInstance:local:10042"]; got != "working" {
		t.Errorf("alpha state after staling (pid still alive) = %q, want working", got)
	}
	if got := staleByID["ClaudeInstance:local:10099"]; got != "idle" {
		t.Errorf("bravo state should still be idle, got %q", got)
	}

	// -------- Phase 3: alpha pid dies. Now the session is genuinely gone.
	// Stale heartbeat + dead pid + no other live pid in the pane = no_claude.
	liveness.alive[10042] = false
	if err := provider.Refresh(context.Background(), "test-dead"); err != nil {
		t.Fatalf("refresh after dead pid: %v", err)
	}
	resp3 := postQuery(t, srv.URL, `query { claudeInstances { id state } }`)
	if len(resp3.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp3.Errors)
	}
	deadByID := map[string]string{}
	for _, inst := range resp3.Data.ClaudeInstances {
		deadByID[inst.ID] = inst.State
	}
	if got := deadByID["ClaudeInstance:local:10042"]; got != "no_claude" {
		t.Errorf("alpha state after dead pid = %q, want no_claude", got)
	}
}

// writeHeartbeat marshals payload to JSON and writes it into dir as
// orchard-claude-<name>.json. NO PII: name and payload come from the
// caller's literal fixture values, not from any environment-derived
// data.
func writeHeartbeat(t *testing.T, dir, name string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	path := filepath.Join(dir, "orchard-claude-"+name+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newTestDaemon mirrors internal/server/server.go's GraphQL wiring with
// a pre-started Provider so the e2e test can drive it without launching
// the full HTTP listener. All real plumbing — schema, resolvers,
// transports — except the network listener is httptest.
func newTestDaemon(t *testing.T, provider *claudeinstance.Provider) *httptest.Server {
	t.Helper()
	cfg := gql.Config{Resolvers: resolvers.New(time.Now()).WithClaudeInstance(provider)}
	gqlSrv := handler.New(gql.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})

	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// graphqlResponse mirrors only the bits the e2e test asserts on, so a
// schema addition elsewhere does not break this unmarshal.
type graphqlResponse struct {
	Data struct {
		ClaudeInstances []instanceNode `json:"claudeInstances"`
	} `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

type instanceNode struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	RcURL       *string `json:"rcUrl"`
	RcEnabled   bool    `json:"rcEnabled"`
	SessionUUID *string `json:"sessionUuid"`
	Pane        *idOnly `json:"pane"`
	Process     *idOnly `json:"process"`
	Account     *idOnly `json:"account"`
}

type idOnly struct {
	ID string `json:"id"`
}

// postQuery issues a GraphQL POST and decodes into graphqlResponse.
// Failures are fatal — a broken transport invalidates the whole test.
func postQuery(t *testing.T, url, query string) graphqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(string(body)))
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
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, string(raw))
	}
	var out graphqlResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out
}
