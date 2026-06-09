// E2E coverage for `Query.gh(query, variables)` — the GitHub GraphQL
// pass-through introduced in issue #418.
//
// The shape is the same as gh_e2e_test.go: a stubbed GitHub GraphQL
// endpoint (httptest.NewTLSServer), a fake `gh auth token` shellout,
// and a daemon httptest.Server with the gh provider wired in. The
// query under test rides through the orchard daemon and lands on our
// stub; assertions cover envelope round-trip + variable forwarding.
package gh_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/providers/gh"
)

// stubGraphQLAPI mounts /graphql on a TLS server. The handler captures
// the inbound request so the test can assert on what the daemon sent
// to GitHub. Other paths fail loudly.
func stubGraphQLAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestGH_E2E_Passthrough_QueryRoundTrip drives a GraphQL query through
// the orchard daemon → gh provider → stub GitHub. Asserts the daemon
// forwards the query verbatim and returns GitHub's envelope as opaque
// JSON.
func TestGH_E2E_Passthrough_QueryRoundTrip(t *testing.T) {
	installFakeGH(t)

	const inboundQuery = `{ repository(owner:"alice", name:"repo") { pullRequest(number:42) { mergeStateStatus reviewDecision } } }`
	const upstreamBody = `{"data":{"repository":{"pullRequest":{"mergeStateStatus":"CLEAN","reviewDecision":"APPROVED"}}}}`

	api := stubGraphQLAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("upstream method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-fixture" {
			t.Errorf("upstream auth header = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if body.Query != inboundQuery {
			t.Errorf("upstream query = %q, want %q", body.Query, inboundQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, upstreamBody)
	})
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)

	srv := newDaemon(t, provider)
	defer srv.Close()

	// Caller-side query: daemon's `gh` field with the GitHub query as
	// the `query` argument. JSON-escape it for embedding in the outer
	// query string.
	escaped, _ := json.Marshal(inboundQuery)
	outer := `query { gh(query: ` + string(escaped) + `) }`

	raw := postQueryRaw(t, srv.URL, outer)
	var resp struct {
		Data struct {
			Gh map[string]any `json:"gh"`
		} `json:"data"`
		Errors []map[string]any `json:"errors,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v -- %s", err, raw)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}

	// Walk the returned envelope; the daemon must have forwarded
	// GitHub's `data` payload verbatim, untouched.
	data, ok := resp.Data.Gh["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing data: %+v", resp.Data.Gh)
	}
	repo, _ := data["repository"].(map[string]any)
	pr, _ := repo["pullRequest"].(map[string]any)
	if pr["mergeStateStatus"] != "CLEAN" {
		t.Errorf("mergeStateStatus = %v, want CLEAN", pr["mergeStateStatus"])
	}
	if pr["reviewDecision"] != "APPROVED" {
		t.Errorf("reviewDecision = %v, want APPROVED", pr["reviewDecision"])
	}
}

// TestGH_E2E_Passthrough_VariablesForwarded confirms variables flow
// from the outer query, through the resolver, into the upstream POST
// body. Without this the JSON scalar wiring is silently broken.
func TestGH_E2E_Passthrough_VariablesForwarded(t *testing.T) {
	installFakeGH(t)

	api := stubGraphQLAPI(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		if body.Variables["owner"] != "alice" {
			t.Errorf("variables[owner] = %v, want alice", body.Variables["owner"])
		}
		if body.Variables["number"] != float64(42) {
			t.Errorf("variables[number] = %v, want 42", body.Variables["number"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"ok":true}}`)
	})
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)
	srv := newDaemon(t, provider)
	defer srv.Close()

	const outer = `query GhWithVars($q: String!, $v: JSON) { gh(query: $q, variables: $v) }`
	const inner = `query($owner:String!,$number:Int!){ repository(owner:$owner) { pullRequest(number:$number) { id } } }`
	body, _ := json.Marshal(map[string]any{
		"query": outer,
		"variables": map[string]any{
			"q": inner,
			"v": map[string]any{"owner": "alice", "number": 42},
		},
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("response missing data: %s", raw)
	}
}

// TestGH_E2E_Passthrough_ErrorsRideThrough confirms GitHub-level
// GraphQL errors come back in the envelope's `errors[]` array — they
// MUST NOT be promoted to a per-field error on the orchard side, or
// callers can't distinguish 'GitHub said no' from 'orchard couldn't
// reach GitHub'.
func TestGH_E2E_Passthrough_ErrorsRideThrough(t *testing.T) {
	installFakeGH(t)

	api := stubGraphQLAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":null,"errors":[{"message":"Field 'nope' doesn't exist on type 'Query'"}]}`)
	})
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)
	srv := newDaemon(t, provider)
	defer srv.Close()

	escaped, _ := json.Marshal(`{ nope }`)
	outer := `query { gh(query: ` + string(escaped) + `) }`

	raw := postQueryRaw(t, srv.URL, outer)
	var resp struct {
		Data struct {
			Gh map[string]any `json:"gh"`
		} `json:"data"`
		Errors []map[string]any `json:"errors,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("orchard reported a per-field error for a github-level graphql error: %+v", resp.Errors)
	}
	errs, ok := resp.Data.Gh["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("envelope missing github errors: %+v", resp.Data.Gh)
	}
	first, _ := errs[0].(map[string]any)
	if !strings.Contains(first["message"].(string), "Field 'nope'") {
		t.Errorf("error message lost: %v", first)
	}
}
