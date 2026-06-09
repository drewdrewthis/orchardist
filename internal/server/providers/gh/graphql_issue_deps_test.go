package gh_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// newDepsProvider mirrors newEnrichProvider but serves a single
// dependency-edge body. The atomic counts /graphql requests so cache
// tests can assert second/third calls don't hit the wire.
func newDepsProvider(t *testing.T, body []byte, clock func() time.Time) (*gh.Provider, *atomic.Int32, *http.Header) {
	t.Helper()
	var count atomic.Int32
	var lastHeader http.Header
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		lastHeader = r.Header.Clone()
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
	return p, &count, &lastHeader
}

// TestEnrichIssueDependencies_ParsesAllFourEdges confirms the
// envelope is parsed into the four edges (#563). AC1–AC4.
func TestEnrichIssueDependencies_ParsesAllFourEdges(t *testing.T) {
	body := []byte(`{
		"data": {
			"repository": {
				"issue": {
					"blockedByIssues": { "nodes": [
						{"number": 558, "title": "blocked-by-558", "repository": {"owner": {"login":"alice"}, "name":"repo"}}
					]},
					"blockingIssues": { "nodes": [
						{"number": 600, "title": "blocking-600", "repository": {"owner": {"login":"alice"}, "name":"repo"}}
					]},
					"subIssues": { "nodes": [
						{"number": 700, "title": "sub-700", "repository": {"owner": {"login":"alice"}, "name":"repo"}},
						{"number": 701, "title": "sub-701", "repository": {"owner": {"login":"alice"}, "name":"repo"}}
					]},
					"parent": {"number": 800, "title": "parent-800", "repository": {"owner": {"login":"alice"}, "name":"repo"}}
				}
			}
		}
	}`)
	p, count, lastHeader := newDepsProvider(t, body, time.Now)

	deps, err := p.EnrichIssueDependencies(context.Background(), gh.IssueKey{Owner: "alice", Name: "repo", Number: 544})
	if err != nil {
		t.Fatalf("EnrichIssueDependencies: %v", err)
	}
	if got := count.Load(); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
	if got := lastHeader.Get("GraphQL-Features"); !strings.Contains(got, "sub_issues") {
		t.Errorf("GraphQL-Features = %q, want it to contain sub_issues", got)
	}
	if got := lastHeader.Get("X-Github-Next-Global-ID"); got != "1" {
		t.Errorf("X-Github-Next-Global-ID = %q, want 1", got)
	}
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].Number != 558 {
		t.Errorf("BlockedBy = %+v, want [#558]", deps.BlockedBy)
	}
	if len(deps.Blocking) != 1 || deps.Blocking[0].Number != 600 {
		t.Errorf("Blocking = %+v, want [#600]", deps.Blocking)
	}
	if len(deps.SubIssues) != 2 {
		t.Errorf("SubIssues = %+v, want 2 entries", deps.SubIssues)
	}
	if deps.Parent == nil || deps.Parent.Number != 800 {
		t.Errorf("Parent = %+v, want #800", deps.Parent)
	}
	if deps.BlockedBy[0].Title != "blocked-by-558" {
		t.Errorf("BlockedBy[0].Title = %q, want %q", deps.BlockedBy[0].Title, "blocked-by-558")
	}
	if deps.Parent.Title != "parent-800" {
		t.Errorf("Parent.Title = %q, want %q", deps.Parent.Title, "parent-800")
	}
}

// TestEnrichIssueDependencies_EmptyEdges asserts the resolver returns
// empty (non-nil) slices and a nil parent when GitHub reports no
// dependencies. Guards the GraphQL non-null-list contract.
func TestEnrichIssueDependencies_EmptyEdges(t *testing.T) {
	body := []byte(`{"data":{"repository":{"issue":{
		"blockedByIssues":{"nodes":[]},
		"blockingIssues":{"nodes":[]},
		"subIssues":{"nodes":[]},
		"parent":null
	}}}}`)
	p, _, _ := newDepsProvider(t, body, time.Now)

	deps, err := p.EnrichIssueDependencies(context.Background(), gh.IssueKey{Owner: "alice", Name: "repo", Number: 1})
	if err != nil {
		t.Fatalf("EnrichIssueDependencies: %v", err)
	}
	if deps.BlockedBy == nil || len(deps.BlockedBy) != 0 {
		t.Errorf("BlockedBy = %+v, want empty non-nil slice", deps.BlockedBy)
	}
	if deps.Blocking == nil || len(deps.Blocking) != 0 {
		t.Errorf("Blocking = %+v, want empty non-nil slice", deps.Blocking)
	}
	if deps.SubIssues == nil || len(deps.SubIssues) != 0 {
		t.Errorf("SubIssues = %+v, want empty non-nil slice", deps.SubIssues)
	}
	if deps.Parent != nil {
		t.Errorf("Parent = %+v, want nil", deps.Parent)
	}
}

// TestEnrichIssueDependencies_CachesWithinTTL asserts repeat calls
// within the 60s TTL hit the cache and never re-fetch.
func TestEnrichIssueDependencies_CachesWithinTTL(t *testing.T) {
	body := []byte(`{"data":{"repository":{"issue":{
		"blockedByIssues":{"nodes":[]},
		"blockingIssues":{"nodes":[]},
		"subIssues":{"nodes":[]},
		"parent":null
	}}}}`)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}
	p, count, _ := newDepsProvider(t, body, clock.Now)

	for i := 0; i < 3; i++ {
		if _, err := p.EnrichIssueDependencies(context.Background(), gh.IssueKey{Owner: "alice", Name: "repo", Number: 1}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("count = %d, want 1 (cache served calls 2 and 3)", got)
	}
}

// TestEnrichIssueDependencies_GraphqlErrorsSurface confirms a
// GraphQL-level error envelope propagates as a Go error so the
// resolver layer can translate to a per-field GraphQL error.
func TestEnrichIssueDependencies_GraphqlErrorsSurface(t *testing.T) {
	body := []byte(`{"errors":[{"message":"Field 'issueDependencies' doesn't exist on type 'Issue'"}]}`)
	p, _, _ := newDepsProvider(t, body, time.Now)

	_, err := p.EnrichIssueDependencies(context.Background(), gh.IssueKey{Owner: "alice", Name: "repo", Number: 1})
	if err == nil {
		t.Fatalf("err = nil, want a graphql errors wrapper")
	}
	if !strings.Contains(err.Error(), "issueDependencies") {
		t.Errorf("err = %v, want it to mention the GraphQL error message", err)
	}
}

// TestIssueRefID asserts the GraphQL-stable id format matches the
// Issue.ID() format so resolver projections produce identical ids
// whether the value comes from a full Issue or a thin IssueRef.
func TestIssueRefID(t *testing.T) {
	ref := gh.IssueRef{Owner: "alice", Name: "repo", Number: 42}
	if got, want := ref.ID(), "Issue:alice/repo#42"; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
}
