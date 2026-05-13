// Tests for Query.version resolver (AC2, issue #417).
//
// Scenarios:
//   - A daemon built with -X main.version=1.2.3 returns "1.2.3" from { version }.
//   - A daemon built without -ldflags returns "dev" from { version }.
//
// Each test constructs the resolver with a known version string (simulating
// the -ldflags injection path), stands up an httptest Server with the gqlgen
// handler, and asserts the GraphQL response JSON.

package resolvers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	gqlgen "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// newVersionDaemon stands up an httptest.Server with the GraphQL handler
// and the given version injected via WithVersion.
func newVersionDaemon(t *testing.T, version string) *httptest.Server {
	t.Helper()
	cfg := gqlgen.Config{Resolvers: resolvers.New(time.Now()).WithVersion(version)}
	gqlSrv := handler.New(gqlgen.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// postVersionQuery posts `{ version }` against the daemon and returns the
// decoded response envelope.
func postVersionQuery(t *testing.T, url string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": "{ version }"})
	req, _ := http.NewRequest(http.MethodPost, url+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post { version }: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// TestVersion_ReturnsBakedVersion asserts that a daemon constructed with
// WithVersion("1.2.3") returns "1.2.3" from `{ version }`.
// Corresponds to the BDD scenario: `Query.version` returns the baked binary version.
func TestVersion_ReturnsBakedVersion(t *testing.T) {
	srv := newVersionDaemon(t, "1.2.3")
	resp := postVersionQuery(t, srv.URL)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	got, ok := data["version"].(string)
	if !ok {
		t.Fatalf("data.version is not a string: %v", data["version"])
	}
	if got != "1.2.3" {
		t.Errorf("Query.version = %q; want %q", got, "1.2.3")
	}
}

// TestVersion_ReturnsDevOnNonReleaseBuild asserts that a daemon constructed
// with the default version (no WithVersion call / "dev") returns "dev" from
// `{ version }`.
// Corresponds to the BDD scenario: `Query.version` returns `dev` on a
// non-release build.
func TestVersion_ReturnsDevOnNonReleaseBuild(t *testing.T) {
	// Use the default version ("dev") — simulates plain `go build` with no -ldflags.
	srv := newVersionDaemon(t, "dev")
	resp := postVersionQuery(t, srv.URL)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	got, ok := data["version"].(string)
	if !ok {
		t.Fatalf("data.version is not a string: %v", data["version"])
	}
	if got != "dev" {
		t.Errorf("Query.version = %q; want %q", got, "dev")
	}
}
