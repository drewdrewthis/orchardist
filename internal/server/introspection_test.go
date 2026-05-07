// Tests for introspection gating — issues #401 (original gate) and
// #469 F4 (default flipped to ON now that federation rides SSH tunnels
// per issue #474).
//
// We test the env-var helper directly (cheap, no daemon boot needed) and
// confirm via httptest that a daemon with introspection disabled returns
// the canonical "introspection disabled" error from gqlgen, while one
// with it enabled returns the schema.

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"
)

// --- env-var gate ---

func TestIntrospectionEnabled_DefaultsOn(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "")
	if !introspectionEnabled() {
		t.Errorf("default (unset) must be on; got off")
	}
}

func TestIntrospectionEnabled_OffWhenZero(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "0")
	if introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=0 must be off; got on")
	}
}

func TestIntrospectionEnabled_OffWhenFalse(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "false")
	if introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=false must be off; got on")
	}
}

func TestIntrospectionEnabled_OffWhenNo(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "no")
	if introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=no must be off; got on")
	}
}

func TestIntrospectionEnabled_OffWhenOff(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "off")
	if introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=off must be off; got on")
	}
}

func TestIntrospectionEnabled_OnWhenOne(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "1")
	if !introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=1 must be on; got off")
	}
}

func TestIntrospectionEnabled_OnWhenTrue(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "true")
	if !introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=true must be on; got off")
	}
}

func TestIntrospectionEnabled_OnOnGarbage(t *testing.T) {
	// Anything not in the explicit OFF allow-list keeps introspection on.
	t.Setenv("ORCHARD_INTROSPECTION", "maybe")
	if !introspectionEnabled() {
		t.Errorf("ORCHARD_INTROSPECTION=maybe must keep default on; got off")
	}
}

// --- end-to-end: daemon serves __schema by default ---

func TestIntrospection_HTTPEnabledByDefault(t *testing.T) {
	// Default empty env — server should serve __schema.
	t.Setenv("ORCHARD_INTROSPECTION", "")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	body := postQuery(t, srv.URL, `{ __schema { queryType { name } } }`)
	if strings.Contains(body, "introspection disabled") {
		t.Errorf("expected schema response by default; got error: %s", body)
	}
	if !strings.Contains(body, "Query") {
		t.Errorf("expected 'Query' in schema response; got: %s", body)
	}
}

func TestIntrospection_HTTPDisabledWhenEnvOff(t *testing.T) {
	t.Setenv("ORCHARD_INTROSPECTION", "0")

	res := &resolvers.Resolver{}
	srv := httptest.NewServer(graphqlHandlerFor(res))
	t.Cleanup(srv.Close)

	body := postQuery(t, srv.URL, `{ __schema { types { name } } }`)
	if !strings.Contains(body, "introspection disabled") {
		t.Errorf("expected 'introspection disabled' in response; got: %s", body)
	}
}

// postQuery posts a single GraphQL document and returns the raw response body.
func postQuery(t *testing.T, url, query string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(raw)
}
