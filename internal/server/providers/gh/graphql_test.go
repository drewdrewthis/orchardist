package gh

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubGraphQLServer mounts an httptest.NewTLSServer that handles
// /graphql with a caller-controlled handler and rejects every other
// path so misrouted clients fail loudly. Returned alongside an
// http.Client whose RootCAs trust the self-signed cert.
func stubGraphQLServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	c := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	return srv, c
}

func newClientForGraphQLTest(t *testing.T, srv *httptest.Server, httpc *http.Client) *Client {
	t.Helper()
	c := NewClient(srv.URL, "test-token-fixture")
	c.HTTP = httpc
	return c
}

// TestGraphQL_Verbatim_Envelope confirms the client returns GitHub's
// envelope as-is. The handler returns a hand-crafted `{ data, errors }`
// response and we assert both halves survive round-trip without any
// orchard-side rewriting.
func TestGraphQL_Verbatim_Envelope(t *testing.T) {
	want := `{"data":{"repository":{"pullRequest":{"mergeStateStatus":"CLEAN","reviewDecision":"APPROVED"}}},"errors":[{"message":"deprecated field","path":["repository","pullRequest","oldField"]}]}`
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("got method %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token-fixture" {
			t.Errorf("Authorization = %q, want Bearer test-token-fixture", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, want)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	got, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil)
	if err != nil {
		t.Fatalf("GraphQL: %v", err)
	}
	if string(got) != want {
		t.Fatalf("envelope:\n got: %s\nwant: %s", got, want)
	}
}

// TestGraphQL_VariablesForwarded confirms the variables map is encoded
// into the request body alongside the query.
func TestGraphQL_VariablesForwarded(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Query != "query($n: Int!) { foo(n: $n) }" {
			t.Errorf("query = %q", body.Query)
		}
		if body.Variables["n"] != float64(7) {
			t.Errorf("variables[n] = %v, want 7", body.Variables["n"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"foo":42}}`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	if _, err := c.GraphQL(context.Background(),
		"query($n: Int!) { foo(n: $n) }",
		map[string]any{"n": 7},
	); err != nil {
		t.Fatalf("GraphQL: %v", err)
	}
}

// TestGraphQL_Unauthorized maps a 401 from GitHub onto the typed
// ErrNotAuthenticated, mirroring the REST path. This is what the
// resolver layer turns into a per-field GraphQL error.
func TestGraphQL_Unauthorized(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	_, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil)
	if !errors.Is(err, ErrNotAuthenticated) {
		t.Fatalf("err = %v, want ErrNotAuthenticated", err)
	}
}

// TestGraphQL_RateLimited converts a 403 + X-RateLimit-Remaining: 0
// into the typed ErrRateLimitedT, mirroring the REST path.
func TestGraphQL_RateLimited(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1234567890")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"API rate limit exceeded"}`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	_, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil)
	if !IsRateLimited(err) {
		t.Fatalf("err = %v, want ErrRateLimitedT", err)
	}
}

// TestGraphQL_GitHubErrorsRideThrough confirms that GitHub-level
// GraphQL errors (200 OK, errors[] populated) come back inside the
// envelope rather than as a Go error. This is the load-bearing
// invariant of pass-through.
func TestGraphQL_GitHubErrorsRideThrough(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":null,"errors":[{"message":"Field 'nope' doesn't exist on type 'Query'","locations":[{"line":1,"column":3}]}]}`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	raw, err := c.GraphQL(context.Background(), "{ nope }", nil)
	if err != nil {
		t.Fatalf("GraphQL returned go-error for graphql-error response: %v", err)
	}
	if !strings.Contains(string(raw), "Field 'nope' doesn't exist") {
		t.Fatalf("envelope did not preserve graphql error: %s", raw)
	}
}

// TestGraphQL_NonJSON_Response surfaces a Go error when GitHub returns
// 200 with a body that isn't valid JSON. This should never happen but
// the resolver should not crash if it does.
func TestGraphQL_NonJSON_Response(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html>oops</html>`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	_, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil)
	if err == nil {
		t.Fatalf("expected error on non-JSON response, got nil")
	}
}

// TestGraphQL_EmptyQuery rejects an empty query string before
// attempting the network round-trip. Cheap defensive guard.
func TestGraphQL_EmptyQuery(t *testing.T) {
	c := NewClient("https://example.invalid", "tok")
	_, err := c.GraphQL(context.Background(), "   ", nil)
	if err == nil || !strings.Contains(err.Error(), "empty query") {
		t.Fatalf("err = %v, want empty-query error", err)
	}
}

// TestGraphQL_Non2xxNonAuth surfaces other non-2xx statuses through
// the typed httpError. This catches GitHub returning 5xx / 502 from a
// transient outage — the resolver should see a clear error.
func TestGraphQL_Non2xxNonAuth(t *testing.T) {
	srv, httpc := stubGraphQLServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `bad gateway`)
	})
	c := newClientForGraphQLTest(t, srv, httpc)
	_, err := c.GraphQL(context.Background(), "{ viewer { login } }", nil)
	if err == nil {
		t.Fatalf("expected error on 502, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("err did not mention status: %v", err)
	}
}
