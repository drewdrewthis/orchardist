package resolvers_test

// End-to-end test for the PullRequest enrichment fields introduced in issue #442.
//
// The five new fields (mergeable, mergeStateStatus, reviewDecision,
// statusCheckRollup, labels) are populated lazily via EnrichPullRequest,
// which calls GitHub's GraphQL API. This test stubs both the REST /pulls
// endpoint (for the base PR list) and the /graphql endpoint (for enrichment)
// so no real GitHub traffic flows.
//
// Scenario:
//   - Client queries pullRequests + all five enrichment fields for alice/repo.
//   - REST stub returns PR #42 (canonPullsBody shape).
//   - GraphQL stub returns MERGEABLE + CLEAN + APPROVED + SUCCESS + [P0, bug].
//   - Response asserts: all five fields have the expected values, and phase
//     labels are absent from labels[].

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	gqlgen "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// prEnrichFakeGHScript is the `gh auth token` shim for enrichment tests.
const prEnrichFakeGHScript = `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "token" ]; then
  echo "test-token-enrich-fixture"
  exit 0
fi
echo "unexpected gh invocation: $@" 1>&2
exit 2
`

// installPREnrichFakeGH installs the gh auth token shim for enrichment tests.
func installPREnrichFakeGH(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(prEnrichFakeGHScript), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

// prEnrichCanonPulls is the REST /pulls response — one open PR.
const prEnrichCanonPulls = `[
  {
    "number": 42,
    "title": "Add widget API",
    "body": "",
    "state": "open",
    "draft": false,
    "html_url": "https://github.com/alice/repo/pull/42",
    "created_at": "2026-04-01T10:00:00Z",
    "updated_at": "2026-04-02T11:00:00Z",
    "merged_at": null,
    "user": {"login": "bob"},
    "base": {"ref": "main"},
    "head": {"ref": "feature/widget"}
  }
]`

// prEnrichCanonGraphQL is the GraphQL enrichment response for PR #42.
const prEnrichCanonGraphQL = `{
  "data": {
    "repository": {
      "pullRequest": {
        "mergeable": "MERGEABLE",
        "mergeStateStatus": "CLEAN",
        "reviewDecision": "APPROVED",
        "labels": {
          "nodes": [
            {"name": "P0"},
            {"name": "bug"},
            {"name": "in-progress"}
          ]
        },
        "commits": {
          "nodes": [
            {
              "commit": {
                "statusCheckRollup": {"state": "SUCCESS"}
              }
            }
          ]
        }
      }
    }
  }
}`

// stubEnrichAPI builds the combined REST + GraphQL stub server for enrichment
// e2e tests.
func stubEnrichAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, prEnrichCanonPulls)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, prEnrichCanonGraphQL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newEnrichDaemon stands up an httptest.Server with the GraphQL handler and
// the gh provider wired in against the given stub API server.
func newEnrichDaemon(t *testing.T, p *gh.Provider) *httptest.Server {
	t.Helper()
	cfg := gqlgen.Config{Resolvers: resolvers.New(time.Now()).WithGH(p)}
	gqlSrv := handler.New(gqlgen.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})
	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// postEnrichQuery is a thin helper that posts a GraphQL query to the daemon
// and unmarshals the response.
func postEnrichQuery(t *testing.T, url, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest(http.MethodPost, url+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post query: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestPREnrich_E2E_AllFiveFields asserts the full enrichment pipeline:
// the five GraphQL-only fields are populated from the stub GraphQL endpoint
// and surfaced correctly through the resolver layer.
func TestPREnrich_E2E_AllFiveFields(t *testing.T) {
	installPREnrichFakeGH(t)
	api := stubEnrichAPI(t)
	tlsClient := api.Client()

	auth := gh.NewCommandAuthSource()
	provider := gh.NewWith(nil, api.URL, auth, time.Now)
	if err := provider.Start(context.Background()); err != nil {
		t.Logf("provider start (non-fatal): %v", err)
	}
	gh.SetHTTPClientForTest(provider, tlsClient)

	srv := newEnrichDaemon(t, provider)
	defer srv.Close()

	resp := postEnrichQuery(t, srv.URL, `query {
		pullRequests(repo: "alice/repo", state: OPEN) {
			id
			number
			mergeable
			mergeStateStatus
			reviewDecision
			statusCheckRollup
			labels { name color description }
		}
	}`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("graphql errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	prs, _ := data["pullRequests"].([]any)
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d: %v", len(prs), prs)
	}

	pr, _ := prs[0].(map[string]any)

	// mergeable
	if got := pr["mergeable"]; got != "MERGEABLE" {
		t.Errorf("mergeable = %v, want MERGEABLE", got)
	}

	// mergeStateStatus
	if got := pr["mergeStateStatus"]; got != "CLEAN" {
		t.Errorf("mergeStateStatus = %v, want CLEAN", got)
	}

	// reviewDecision
	if got := pr["reviewDecision"]; got != "APPROVED" {
		t.Errorf("reviewDecision = %v, want APPROVED", got)
	}

	// statusCheckRollup
	if got := pr["statusCheckRollup"]; got != "SUCCESS" {
		t.Errorf("statusCheckRollup = %v, want SUCCESS", got)
	}

	// labels — phase label "in-progress" must be filtered; P0 and bug must
	// survive with their full Label shape.
	rawLabels, _ := pr["labels"].([]any)
	names := make([]string, 0, len(rawLabels))
	for _, l := range rawLabels {
		obj, _ := l.(map[string]any)
		if obj == nil {
			continue
		}
		name, _ := obj["name"].(string)
		names = append(names, name)
	}
	namesStr := strings.Join(names, ",")
	if strings.Contains(namesStr, "in-progress") {
		t.Errorf("phase label 'in-progress' leaked into labels: %v", names)
	}
	if !strings.Contains(namesStr, "P0") {
		t.Errorf("user label 'P0' missing from labels: %v", names)
	}
	if !strings.Contains(namesStr, "bug") {
		t.Errorf("user label 'bug' missing from labels: %v", names)
	}
}

// TestPREnrich_E2E_NullReviewDecision asserts that a null reviewDecision
// on the wire surfaces as null in the GraphQL response (not an error).
func TestPREnrich_E2E_NullReviewDecision(t *testing.T) {
	installPREnrichFakeGH(t)

	// Serve a GraphQL response with reviewDecision: null.
	const nullReviewDecision = `{
		"data": {
			"repository": {
				"pullRequest": {
					"mergeable": "MERGEABLE",
					"mergeStateStatus": "CLEAN",
					"reviewDecision": null,
					"labels": {"nodes": []},
					"commits": {"nodes": [{"commit": {"statusCheckRollup": {"state": "PENDING"}}}]}
				}
			}
		}
	}`

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, prEnrichCanonPulls)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, nullReviewDecision)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	api := httptest.NewTLSServer(mux)
	t.Cleanup(api.Close)

	auth := gh.NewCommandAuthSource()
	provider := gh.NewWith(nil, api.URL, auth, time.Now)
	if err := provider.Start(context.Background()); err != nil {
		t.Logf("provider start (non-fatal): %v", err)
	}
	gh.SetHTTPClientForTest(provider, api.Client())

	srv := newEnrichDaemon(t, provider)
	defer srv.Close()

	resp := postEnrichQuery(t, srv.URL, `query {
		pullRequests(repo: "alice/repo", state: OPEN) {
			reviewDecision
			statusCheckRollup
		}
	}`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("graphql errors: %v", errs)
	}
	data, _ := resp["data"].(map[string]any)
	prs, _ := data["pullRequests"].([]any)
	if len(prs) == 0 {
		t.Fatal("expected at least 1 PR")
	}
	pr, _ := prs[0].(map[string]any)

	if got := pr["reviewDecision"]; got != nil {
		t.Errorf("reviewDecision = %v, want null", got)
	}
	if got := pr["statusCheckRollup"]; got != "PENDING" {
		t.Errorf("statusCheckRollup = %v, want PENDING", got)
	}
}
