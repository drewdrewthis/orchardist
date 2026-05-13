package gh_test

// Tests for the PR enrichment layer (graphql_enrich.go).
//
// **No PII fixtures.** Repos are `alice/repo`, users are `bob`.
// **No real API calls.** The GitHub GraphQL endpoint is stubbed via the
// existing stubGraphQLServer + newClientForGraphQLTest pattern used in
// graphql_test.go. For provider-level tests we use the httptest.NewTLSServer
// + installFakeGH pattern from gh_e2e_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
)

// TestMapStatusCheckRollup confirms the aggregation rules from issue #442:
//   - SUCCESS → SUCCESS
//   - FAILURE or ERROR → FAILURE
//   - PENDING or EXPECTED → PENDING
//   - anything else → UNKNOWN
func TestMapStatusCheckRollup(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{"SUCCESS", "SUCCESS"},
		{"FAILURE", "FAILURE"},
		{"ERROR", "FAILURE"},
		{"PENDING", "PENDING"},
		{"EXPECTED", "PENDING"},
		{"", "UNKNOWN"},
		{"STALE", "UNKNOWN"},
	}
	for _, tc := range cases {
		got := gh.ExportMapStatusCheckRollup(tc.state)
		if string(got) != tc.want {
			t.Errorf("mapStatusCheckRollup(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// TestFilterPhaseLabels confirms that orchard lifecycle labels are stripped
// while user labels (P0, bug, autonomous) are preserved.
func TestFilterPhaseLabels(t *testing.T) {
	in := []string{
		"P0",
		"investigating",
		"bug",
		"in-progress",
		"autonomous",
		"planned",
		"needs-plan",
		"pr-ready",
		"blocked",
	}
	got := gh.ExportFilterPhaseLabels(in)

	// phase labels must be absent
	for _, l := range got {
		switch l {
		case "investigating", "needs-plan", "needs-repro", "planned",
			"in-progress", "in-ai-review", "pr-ready", "blocked":
			t.Errorf("phase label %q survived filterPhaseLabels", l)
		}
	}
	// user labels must be present
	want := map[string]bool{"P0": true, "bug": true, "autonomous": true}
	for _, l := range got {
		delete(want, l)
	}
	for l := range want {
		t.Errorf("user label %q was incorrectly removed by filterPhaseLabels", l)
	}
}

// enrichResponse builds a canned GitHub GraphQL enrichment payload.
func enrichResponse(mergeable, mergeStateStatus string, reviewDecision *string, statusState string, labels ...string) []byte {
	labelNodes := make([]map[string]string, 0, len(labels))
	for _, l := range labels {
		labelNodes = append(labelNodes, map[string]string{"name": l})
	}
	var statusRollup interface{} = nil
	if statusState != "" {
		statusRollup = map[string]string{"state": statusState}
	}
	body := map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"pullRequest": map[string]any{
					"mergeable":        mergeable,
					"mergeStateStatus": mergeStateStatus,
					"reviewDecision":   reviewDecision,
					"labels": map[string]any{
						"nodes": labelNodes,
					},
					"commits": map[string]any{
						"nodes": []any{
							map[string]any{
								"commit": map[string]any{
									"statusCheckRollup": statusRollup,
								},
							},
						},
					},
				},
			},
		},
	}
	out, _ := json.Marshal(body)
	return out
}

// newEnrichProvider builds a gh.Provider wired against a TLS test server
// that serves the given enrichment body for every /graphql request. The
// request count is returned via the atomic for cache tests.
func newEnrichProvider(t *testing.T, body []byte, clock func() time.Time) (*gh.Provider, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	auth := &gh.StaticAuthSource{TokenValue: "test-token-fixture"}
	p := gh.NewWith(nil, srv.URL, auth, clock)
	if err := p.Start(context.Background()); err != nil {
		t.Logf("provider start (non-fatal): %v", err)
	}
	gh.SetHTTPClientForTest(p, srv.Client())
	return p, &count
}

// TestEnrichPullRequest_Caches60s asserts that two EnrichPullRequest calls
// within the 60-second enrichment TTL share one HTTP round-trip.
func TestEnrichPullRequest_Caches60s(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}

	body := enrichResponse("MERGEABLE", "CLEAN", nil, "SUCCESS", "bug", "P0")
	p, count := newEnrichProvider(t, body, clock.Now)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 42}

	// First call — must hit the wire.
	pr1, err := p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("first enrich: %v", err)
	}
	if pr1.Mergeable != gh.MergeableStateMergeable {
		t.Errorf("mergeable = %q, want MERGEABLE", pr1.Mergeable)
	}
	if string(pr1.StatusCheckRollup) != "SUCCESS" {
		t.Errorf("ciStatus = %q, want SUCCESS", pr1.StatusCheckRollup)
	}
	if c := count.Load(); c != 1 {
		t.Fatalf("expected 1 HTTP call after first enrich, got %d", c)
	}

	// Advance clock by 30s — still within 60s TTL.
	clock.advance(30 * time.Second)

	// Second call — must use the cache (no new HTTP call).
	pr2, err := p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("second enrich: %v", err)
	}
	if c := count.Load(); c != 1 {
		t.Fatalf("expected still 1 HTTP call after second enrich (within TTL), got %d", c)
	}
	if pr2.Mergeable != pr1.Mergeable {
		t.Errorf("cached result differs: %+v vs %+v", pr1, pr2)
	}

	// Advance past TTL.
	clock.advance(35 * time.Second)

	// Third call — TTL expired, must re-fetch.
	_, err = p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("third enrich: %v", err)
	}
	if c := count.Load(); c != 2 {
		t.Fatalf("expected 2 HTTP calls after TTL expiry, got %d", c)
	}
}

// TestEnrichPullRequest_UnknownMergeableNotCachedAsDefinitive asserts that
// when GitHub returns UNKNOWN for mergeable, the result is NOT cached —
// the next call must re-fetch. This avoids the #367 flap pattern where a
// transient UNKNOWN hardens into a stale cached answer.
func TestEnrichPullRequest_UnknownMergeableNotCachedAsDefinitive(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}

	// Server always returns UNKNOWN.
	body := enrichResponse("UNKNOWN", "UNKNOWN", nil, "")
	p, count := newEnrichProvider(t, body, clock.Now)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 7}

	// Call 1.
	pr, err := p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("enrich 1: %v", err)
	}
	if pr.Mergeable != gh.MergeableStateUnknown {
		t.Errorf("mergeable = %q, want UNKNOWN", pr.Mergeable)
	}
	if c := count.Load(); c != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", c)
	}

	// Direct invariant check: enrichAt MUST be the zero time after an
	// UNKNOWN response. A future regression that wrote enrichAt[key]
	// while still treating Mergeable==UNKNOWN as a cache miss would
	// pass the HTTP-count assertion but break the contract — this
	// catches that drift directly.
	if ts := p.ExportEnrichTimestamp(key); !ts.IsZero() {
		t.Errorf("enrichAt[key] = %v after UNKNOWN, want zero time", ts)
	}

	// Do NOT advance clock — still within TTL window.
	// Call 2 — UNKNOWN means no cache; must still hit the wire.
	_, err = p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("enrich 2: %v", err)
	}
	if c := count.Load(); c != 2 {
		t.Fatalf("expected 2 HTTP calls (UNKNOWN not cached), got %d", c)
	}
}

// TestEnrichPullRequest_GraphQLError asserts that GraphQL-level envelope
// errors (200 OK, errors[] populated) surface as a Go error.
func TestEnrichPullRequest_GraphQLError(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"data": nil,
		"errors": []any{
			map[string]any{"message": "Field 'pullRequest' doesn't exist"},
		},
	})
	p, _ := newEnrichProvider(t, body, time.Now)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 1}
	_, err := p.EnrichPullRequest(context.Background(), key)
	if err == nil {
		t.Fatal("expected error on GraphQL envelope errors, got nil")
	}
}

// TestEnrichPullRequest_LabelFilter confirms phase labels are stripped and
// user labels preserved in the returned PullRequest.
func TestEnrichPullRequest_LabelFilter(t *testing.T) {
	rd := "APPROVED"
	body := enrichResponse("MERGEABLE", "CLEAN", &rd, "SUCCESS",
		"P0", "bug", "in-progress", "planned", "autonomous")
	p, _ := newEnrichProvider(t, body, time.Now)

	key := gh.PullRequestKey{Owner: "alice", Name: "repo", Number: 42}
	pr, err := p.EnrichPullRequest(context.Background(), key)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}

	phaseLabels := map[string]bool{
		"investigating": true, "needs-plan": true, "needs-repro": true,
		"planned": true, "in-progress": true, "in-ai-review": true,
		"pr-ready": true, "blocked": true,
	}
	for _, l := range pr.Labels {
		if phaseLabels[l.Name] {
			t.Errorf("phase label %q survived into PullRequest.Labels", l.Name)
		}
	}

	// user labels must survive
	want := map[string]bool{"P0": true, "bug": true, "autonomous": true}
	for _, l := range pr.Labels {
		delete(want, l.Name)
	}
	for l := range want {
		t.Errorf("user label %q missing from PullRequest.Labels", l)
	}

	// ReviewDecision should be populated
	if pr.ReviewDecision == nil {
		t.Error("ReviewDecision is nil, want APPROVED")
	} else if *pr.ReviewDecision != gh.ReviewDecisionApproved {
		t.Errorf("ReviewDecision = %q, want APPROVED", *pr.ReviewDecision)
	}
}

// fakeClock is a test-only monotonic clock backed by a mutable time value.
// advance is safe to call from the same goroutine as Now.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newEnrichGraphQLServer builds an httptest.NewTLSServer with a /graphql
// handler that responds with body, plus a /repos/.../pulls/* handler so
// the provider can resolve REST-level pulls for GetPullRequest. Returns the
// server + a trusting client.
func newEnrichGraphQLServer(t *testing.T, graphqlBody []byte, hitCount *atomic.Int32) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(graphqlBody)
	})
	mux.HandleFunc("/repos/alice/repo/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, canonOnePullBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"error":"unexpected path %s"}`, r.URL.Path)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}
