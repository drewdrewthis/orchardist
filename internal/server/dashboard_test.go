package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildDashboardTestMux mirrors the route wiring in server.New
// (server.go, where /health, /graphql, /v1/conversations/ are registered
// alongside the "/" dashboard catch-all). The sibling handlers are
// lightweight stubs — these tests assert ServeMux PRECEDENCE (that adding
// "/" does not shadow the existing routes), not the siblings' internals.
func buildDashboardTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "HEALTH_OK") })
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "GRAPHQL_OK") })
	mux.HandleFunc("/v1/conversations/", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "CONVO_OK") })
	mux.Handle("/", dashboardHandler())
	return mux
}

func TestDashboard_ServesRootWithMarker(t *testing.T) {
	srv := httptest.NewServer(buildDashboardTestMux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want to contain text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `id="orchard-dashboard"`) {
		t.Errorf(`GET / body is missing the marker id="orchard-dashboard"`)
	}
}

func TestDashboard_NotFoundForNonRoot(t *testing.T) {
	// The "/" catch-all must 404 unmatched non-root paths rather than serve
	// the page (guards against an open file server). Exercises the handler's
	// own exact-path guard directly.
	srv := httptest.NewServer(dashboardHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), `id="orchard-dashboard"`) {
		t.Errorf("GET /nope served the dashboard page; it must 404 (no open file server)")
	}
}

func TestDashboard_DoesNotShadowExistingRoutes(t *testing.T) {
	srv := httptest.NewServer(buildDashboardTestMux())
	defer srv.Close()

	cases := []struct {
		method, path, want string
	}{
		{http.MethodGet, "/health", "HEALTH_OK"},
		{http.MethodPost, "/graphql", "GRAPHQL_OK"},
		{http.MethodGet, "/v1/conversations/abc123/jsonl", "CONVO_OK"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s %s status = %d, want 200", c.method, c.path, resp.StatusCode)
		}
		if got := strings.TrimSpace(string(body)); got != c.want {
			t.Errorf("%s %s routed to the wrong handler: body = %q, want %q (did the dashboard \"/\" shadow it?)",
				c.method, c.path, got, c.want)
		}
	}
}
