package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSupergraph stands up a stub supergraph: /graphql answers each POST via
// graphqlFn (which may branch on the query body), /health returns healthBody.
// It points the package URLs at the stub for the test and restores them after —
// no real supergraph, no daemon.
func fakeSupergraph(t *testing.T, graphqlFn func(reqBody string) string, healthBody string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(graphqlFn(string(b))))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(healthBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	prevG, prevH := graphqlURL, healthURL
	graphqlURL = srv.URL + "/graphql"
	healthURL = srv.URL + "/health"
	t.Cleanup(func() { graphqlURL, healthURL = prevG, prevH })
}

const fastFixture = `{"data":{
  "claudeInstances":[
    {"pane":"%1","pid":100,"session":{"sessionId":"s1","state":"working","model":"claude-opus-4-6","lastEventAt":"2026-09-07T15:55:10Z","cwd":"/ws/a","gitBranch":"feat/a","issueNumber":844,"prNumber":null}}
  ],
  "tmuxSessions":[
    {"name":"work-a","worktree":"/ws/a","branch":"feat/a"},
    {"name":"work-b","worktree":"/ws/b","branch":"feat/b"}
  ],
  "tmuxPanes":[
    {"key":"%1","session":"work-a","window":0,"pane":0,"active":true}
  ]
}}`

const healthAllOK = `[{"plugin":"tmux","state":"ok"},{"plugin":"claude","state":"ok"}]`

// AC5: the fast lane fills the row model from the typed leaves. One row per
// tmux session; the session matching a claudeInstances entry carries its state
// and model; a session with no matching entry still yields a row, no panic.
func TestSupergraphFastLaneFillsRows(t *testing.T) {
	fakeSupergraph(t, func(string) string { return fastFixture }, healthAllOK)

	msg, ok := fetchFastSupergraph().(fastDataMsg)
	if !ok {
		t.Fatalf("want fastDataMsg, got %T", fetchFastSupergraph())
	}
	if msg.err != nil {
		t.Fatalf("healthy backend should carry no advisory: %v", msg.err)
	}
	byName := map[string]row{}
	for _, r := range msg.rows {
		byName[r.session] = r
	}
	if len(msg.rows) != 2 {
		t.Fatalf("want one row per tmux session (2), got %d: %+v", len(msg.rows), msg.rows)
	}
	a, ok := byName["work-a"]
	if !ok {
		t.Fatal("no row for work-a")
	}
	if a.state != "working" {
		t.Errorf("work-a state = %q, want working", a.state)
	}
	if a.model != "opus" {
		t.Errorf("work-a model = %q, want opus", a.model)
	}
	b, ok := byName["work-b"]
	if !ok {
		t.Fatal("no row for work-b (tmux session with no claude instance)")
	}
	if b.state != "shell" {
		t.Errorf("work-b state = %q, want shell", b.state)
	}
	// the pane->session map is served for the hook lane, keyed on the pane key
	if msg.paneToSess["%1"] != "work-a" {
		t.Errorf("paneToSess[%%1] = %q, want work-a", msg.paneToSess["%1"])
	}
}

// A claude instance whose pane maps to no known tmux session must not invent a
// row — the daemon drops it too.
func TestSupergraphFastLaneDropsOrphanInstance(t *testing.T) {
	orphan := `{"data":{
      "claudeInstances":[{"pane":"%9","pid":1,"session":{"sessionId":"x","state":"idle","model":null,"lastEventAt":"2026-09-07T15:55:10Z","cwd":"","gitBranch":null,"issueNumber":null,"prNumber":null}}],
      "tmuxSessions":[{"name":"only","worktree":null,"branch":null}],
      "tmuxPanes":[]
    }}`
	fakeSupergraph(t, func(string) string { return orphan }, healthAllOK)
	msg := fetchFastSupergraph().(fastDataMsg)
	if len(msg.rows) != 1 || msg.rows[0].session != "only" {
		t.Fatalf("orphan instance leaked a row: %+v", msg.rows)
	}
}

// AC8: a non-ok plugin in /health is surfaced as the failure reason naming that
// plugin, WITHOUT dropping the rows the data query resolved (the advisory rides
// alongside; applyFast keeps the rows and fastAt stays fresh).
func TestSupergraphHealthNamesDegradedPlugin(t *testing.T) {
	health := `[{"plugin":"github","state":"stale"},{"plugin":"tmux","state":"ok"}]`
	fakeSupergraph(t, func(string) string { return fastFixture }, health)

	msg := fetchFastSupergraph().(fastDataMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "github") {
		t.Fatalf("failure reason should name github, got %v", msg.err)
	}
	if len(msg.rows) != 2 {
		t.Fatalf("health advisory must not drop rows, got %d", len(msg.rows))
	}
	// the surface the sidebar actually reads: m.err after applyFast, with rows kept
	m := &model{}
	m.applyFast(msg)
	if m.err == nil || !strings.Contains(m.err.Error(), "github") {
		t.Errorf("m.err = %v, want it to name github", m.err)
	}
	if len(m.rows) != 2 {
		t.Errorf("rows dropped by an advisory: %d", len(m.rows))
	}
	if m.daemonDown() {
		t.Error("an advisory must not read as an offline backend")
	}
}

// AC4: the supergraph backend pointed at a dead port fails within the fast
// lane's timeout and degrades like a daemon-down failure — no panic, no rows,
// an error set. A fresh model applying it does not crash.
func TestSupergraphUnreachableDegrades(t *testing.T) {
	prevG, prevH := graphqlURL, healthURL
	graphqlURL = "http://127.0.0.1:1/graphql" // nothing listens on port 1
	healthURL = "http://127.0.0.1:1/health"
	t.Cleanup(func() { graphqlURL, healthURL = prevG, prevH })

	msg, ok := fetchFastSupergraph().(fastDataMsg)
	if !ok {
		t.Fatalf("want fastDataMsg, got %T", msg)
	}
	if msg.err == nil {
		t.Fatal("connection refused should surface as an error")
	}
	if msg.rows != nil {
		t.Fatalf("a hard failure carries no rows, got %+v", msg.rows)
	}
	m := &model{}
	m.applyFast(msg) // must not panic
	if m.err == nil {
		t.Error("model did not record the degraded state")
	}
}

// AC7: fields supergraph does not expose render as "—", distinguishable from a
// real value a daemon fixture sets. prStatus and branchLine are the composed-
// string sources for the PR verdict and worktree drift.
func TestSupergraphMissingFieldsRenderEmDash(t *testing.T) {
	// supergraph PR: verdict unknown -> "—"
	if got := prStatus(prInfo{Number: 7, State: "OPEN", unknown: true}); got != emDash {
		t.Errorf("unknown PR verdict = %q, want %q", got, emDash)
	}
	// a known MERGED/CLOSED state still resolves even when the verdict is unknown
	if got := prStatus(prInfo{Number: 7, State: "MERGED", unknown: true}); got != "merged" {
		t.Errorf("merged PR = %q, want merged", got)
	}
	// daemon PR with real verdict fields -> a real word, never "—"
	real := prInfo{Number: 7, State: "OPEN", ChecksRollup: "SUCCESS",
		MergeStateStatus: "CLEAN", ReviewDecision: strptr("APPROVED")}
	if got := prStatus(real); got != "green" || strings.Contains(got, emDash) {
		t.Errorf("daemon PR = %q, want green with no dash", got)
	}

	// worktree drift unknown -> "↑— ↓—" on the branch line
	sg := branchLine(row{branch: "feat/a", driftUnknown: true})
	if !strings.Contains(sg, "↑"+emDash) || !strings.Contains(sg, "↓"+emDash) {
		t.Errorf("supergraph branch line = %q, want ↑— ↓—", sg)
	}
	// daemon drift with real counts -> arrows with numbers, no dash
	two, one := 2, 1
	dm := branchLine(row{branch: "feat/a", ahead: &two, behind: &one})
	if strings.Contains(dm, emDash) {
		t.Errorf("daemon branch line %q must not contain a dash", dm)
	}
	if !strings.Contains(dm, "↑2") || !strings.Contains(dm, "↓1") {
		t.Errorf("daemon branch line = %q, want ↑2 ↓1", dm)
	}

	// the composed git box line for a supergraph PR carries the em dash
	items := gitBoxItems(row{branch: "feat/a", driftUnknown: true,
		pr: &prInfo{Number: 9, State: "OPEN", unknown: true}})
	var boxText string
	for _, it := range items {
		boxText += it.text + "\n"
	}
	if !strings.Contains(boxText, emDash) {
		t.Errorf("git box %q should render the PR verdict as —", boxText)
	}
}
