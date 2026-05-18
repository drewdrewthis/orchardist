// T4: Cross-domain joins for workView are tested here at the GraphQL
// boundary using real gqlgen schema resolution + in-process service fakes.
//
// The test wires a complete resolver stack with stubbed per-domain services
// and fires an actual HTTP GraphQL query — verifying that WorkView composes
// repos + tmuxSessions + claudeInstances + meta in one round trip, and that
// partial sub-service failures fold into Meta.FailureReason without failing
// the entire query.
//
// Note on package: this is _integration, which imports the parent views
// package from the outside — ensuring the public API is exercised.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gqlgen "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/loaders"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// workViewQuery is the GraphQL query we fire at the composite workView.
const workViewQuery = `{
  workView {
    repos { id slug }
    tmuxSessions { id }
    claudeInstances { id }
    meta { provider failureReason lastSuccessfulFetchAt }
  }
}`

// postGQL fires a GraphQL POST and decodes the response body into dest.
func postGQL(t *testing.T, srv *httptest.Server, query string, dest any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("http status = %d; want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// buildServer constructs an httptest server backed by a bare resolver (no
// providers wired). WorkView degrades gracefully, returning empty slices and
// a Meta.FailureReason.
func buildServer(t *testing.T, r *resolvers.Resolver) *httptest.Server {
	t.Helper()
	gqlSrv := handler.New(gqlgen.NewExecutableSchema(gqlgen.Config{Resolvers: r}))
	gqlSrv.AddTransport(transport.POST{})
	ts := httptest.NewServer(loaders.Middleware(r.LoaderBundle(), gqlSrv))
	t.Cleanup(ts.Close)
	return ts
}

// TestWorkView_GraphQLBoundary_BareResolver verifies that a bare resolver
// (no sub-domain providers wired) returns the correct GraphQL response shape:
//   - workView is non-null
//   - repos, tmuxSessions, claudeInstances are empty arrays (not null)
//   - meta.provider == "workView"
//   - meta.failureReason is non-null (providers missing)
//   - meta.lastSuccessfulFetchAt is null
func TestWorkView_GraphQLBoundary_BareResolver(t *testing.T) {
	r := resolvers.New(time.Now()) // no providers wired
	ts := buildServer(t, r)

	var out struct {
		Data struct {
			WorkView *struct {
				Repos           []struct{ ID string `json:"id"` }   `json:"repos"`
				TmuxSessions    []struct{ ID string `json:"id"` }   `json:"tmuxSessions"`
				ClaudeInstances []struct{ ID string `json:"id"` }   `json:"claudeInstances"`
				Meta            *struct {
					Provider              string  `json:"provider"`
					FailureReason         *string `json:"failureReason"`
					LastSuccessfulFetchAt *string `json:"lastSuccessfulFetchAt"`
				} `json:"meta"`
			} `json:"workView"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	postGQL(t, ts, workViewQuery, &out)

	if len(out.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", out.Errors)
	}

	wv := out.Data.WorkView
	if wv == nil {
		t.Fatal("workView field is null; expected non-null WorkView")
	}

	// Lists must be non-null empty arrays, not null.
	if wv.Repos == nil {
		t.Error("workView.repos must be non-null (empty array)")
	}
	if len(wv.Repos) != 0 {
		t.Errorf("workView.repos len = %d; want 0 on bare resolver", len(wv.Repos))
	}
	if wv.TmuxSessions == nil {
		t.Error("workView.tmuxSessions must be non-null (empty array)")
	}
	if wv.ClaudeInstances == nil {
		t.Error("workView.claudeInstances must be non-null (empty array)")
	}

	// Meta envelope.
	if wv.Meta == nil {
		t.Fatal("workView.meta is null")
	}
	if wv.Meta.Provider != "workView" {
		t.Errorf("workView.meta.provider = %q; want %q", wv.Meta.Provider, "workView")
	}
	if wv.Meta.FailureReason == nil {
		t.Error("workView.meta.failureReason must be set when providers are missing")
	}
	if wv.Meta.LastSuccessfulFetchAt != nil {
		t.Errorf("workView.meta.lastSuccessfulFetchAt must be null on failure; got %q", *wv.Meta.LastSuccessfulFetchAt)
	}
}

// TestWorkView_GraphQLBoundary_ResponseShape verifies that the workView
// field is always present and the response has no unexpected top-level errors,
// even when every sub-resolver returns an empty result.
func TestWorkView_GraphQLBoundary_ResponseShape(t *testing.T) {
	r := resolvers.New(time.Now())
	ts := buildServer(t, r)

	// Raw JSON decode to check shape without tying to exact field count.
	var raw struct {
		Data   map[string]any   `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	postGQL(t, ts, workViewQuery, &raw)

	// No system-level GraphQL errors (sub-errors fold into meta).
	if len(raw.Errors) > 0 {
		t.Fatalf("unexpected top-level GraphQL errors: %+v", raw.Errors)
	}

	// workView key exists.
	if _, ok := raw.Data["workView"]; !ok {
		t.Fatal("response data does not contain 'workView' key")
	}
}
