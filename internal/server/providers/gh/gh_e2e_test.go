// Package gh — end-to-end test of the gh provider.
//
// **No PII fixtures.** Repos are `alice/repo`, users are `bob`/`carol`,
// PR numbers and run ids are made up. Never use a real repo identity
// here even with a revoked token — leaks are forever.
//
// **No real API calls.** The GitHub HTTPS API is stubbed with
// httptest.NewTLSServer; the gh CLI shellout is stubbed by writing a
// fake `gh` script into a temp directory and prepending it to PATH.
// Both stubs are torn down by t.Cleanup so a flaky test cannot leak
// into another.
package gh_test

import (
	"context"
	"encoding/json"
	"io"
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

// fakeGHTokenScript is the body of the temp `gh` shim. When run as
// `gh auth token`, it writes a canned token to stdout and exits 0.
// Anything else exits 2 so unexpected invocations fail loudly.
const fakeGHTokenScript = `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "token" ]; then
  echo "test-token-fixture"
  exit 0
fi
echo "unexpected gh invocation: $@" 1>&2
exit 2
`

// installFakeGH writes a `gh` shim into a fresh temp dir and prepends
// that dir to PATH for the test. Returns the script path; t.Cleanup
// restores the original PATH.
func installFakeGH(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(fakeGHTokenScript), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
	return script
}

// stubAPI mounts a tiny multiplexer that serves the canned bodies the
// test asserts on. Any unexpected path responds with 404 so the test
// fails loudly when the client crawls the wrong URL.
func stubAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		state := r.URL.Query().Get("state")
		if state != "open" {
			t.Errorf("expected state=open, got %q", state)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonPullsBody))
	})
	mux.HandleFunc("/repos/alice/repo/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonOnePullBody))
	})
	mux.HandleFunc("/repos/alice/repo/issues", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		state := r.URL.Query().Get("state")
		if state != "open" {
			t.Errorf("expected state=open, got %q", state)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonIssuesBody))
	})
	mux.HandleFunc("/repos/alice/repo/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonRunsBody))
	})
	mux.HandleFunc("/repos/alice/repo/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonReviewsBody))
	})
	mux.HandleFunc("/repos/alice/repo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonCommentsBody))
	})
	mux.HandleFunc("/repos/alice/repo/issues/100/comments", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonCommentsBody))
	})
	mux.HandleFunc("/repos/alice/repo/issues/100", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonOneIssueBody))
	})
	mux.HandleFunc("/repos/alice/repo/actions/runs/9999", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonOneRunBody))
	})
	mux.HandleFunc("/repos/alice/repo", func(w http.ResponseWriter, r *http.Request) {
		assertAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(canonRepoBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// assertAuth fails the test if the caller did not send the expected
// bearer token. AC1: hybrid auth must put the token on every call.
func assertAuth(t *testing.T, r *http.Request) {
	t.Helper()
	got := r.Header.Get("Authorization")
	if got != "Bearer test-token-fixture" {
		t.Errorf("Authorization header = %q, want Bearer test-token-fixture", got)
	}
}

// canon* — the canned payloads. JSON kept inline so the test is self-
// contained; deliberately minimal because asserting every field bloats
// without adding signal.
const canonPullsBody = `[
  {
    "number": 42,
    "title": "Add widget API",
    "body": "Adds the widget endpoint",
    "state": "open",
    "draft": false,
    "html_url": "https://github.com/alice/repo/pull/42",
    "created_at": "2026-04-01T10:00:00Z",
    "updated_at": "2026-04-02T11:00:00Z",
    "merged_at": null,
    "user": {"login": "bob"},
    "base": {"ref": "main"},
    "head": {"ref": "feature/widget"}
  },
  {
    "number": 7,
    "title": "Refactor parser",
    "body": "Splits the parser into discrete passes",
    "state": "open",
    "draft": true,
    "html_url": "https://github.com/alice/repo/pull/7",
    "created_at": "2026-03-15T08:30:00Z",
    "updated_at": "2026-03-15T09:00:00Z",
    "merged_at": null,
    "user": {"login": "carol"},
    "base": {"ref": "main"},
    "head": {"ref": "refactor/parser"}
  }
]`

const canonOnePullBody = `{
  "number": 42,
  "title": "Add widget API",
  "body": "Adds the widget endpoint",
  "state": "open",
  "draft": false,
  "html_url": "https://github.com/alice/repo/pull/42",
  "created_at": "2026-04-01T10:00:00Z",
  "updated_at": "2026-04-02T11:00:00Z",
  "merged_at": null,
  "user": {"login": "bob"},
  "base": {"ref": "main"},
  "head": {"ref": "feature/widget"}
}`

const canonIssuesBody = `[
  {
    "number": 100,
    "title": "Bug: parse failure",
    "body": "Reproduction steps",
    "state": "open",
    "html_url": "https://github.com/alice/repo/issues/100",
    "created_at": "2026-04-10T08:00:00Z",
    "updated_at": "2026-04-10T08:30:00Z",
    "user": {"login": "carol"}
  },
  {
    "number": 99,
    "title": "PR appearing as issue",
    "body": "Should be filtered",
    "state": "open",
    "html_url": "https://github.com/alice/repo/issues/99",
    "created_at": "2026-04-09T08:00:00Z",
    "updated_at": "2026-04-09T08:30:00Z",
    "user": {"login": "bob"},
    "pull_request": {"url": "https://api.github.com/repos/alice/repo/pulls/99"}
  }
]`

const canonOneIssueBody = `{
  "number": 100,
  "title": "Bug: parse failure",
  "body": "Reproduction steps",
  "state": "open",
  "html_url": "https://github.com/alice/repo/issues/100",
  "created_at": "2026-04-10T08:00:00Z",
  "updated_at": "2026-04-10T08:30:00Z",
  "user": {"login": "carol"}
}`

const canonRunsBody = `{
  "workflow_runs": [
    {
      "id": 9999,
      "name": "CI",
      "path": ".github/workflows/ci.yml",
      "status": "completed",
      "conclusion": "success",
      "head_branch": "main",
      "head_sha": "0123456789abcdef0123456789abcdef01234567",
      "html_url": "https://github.com/alice/repo/actions/runs/9999",
      "created_at": "2026-04-15T13:00:00Z",
      "updated_at": "2026-04-15T13:05:00Z"
    }
  ]
}`

const canonOneRunBody = `{
  "id": 9999,
  "name": "CI",
  "path": ".github/workflows/ci.yml",
  "status": "completed",
  "conclusion": "success",
  "head_branch": "main",
  "head_sha": "0123456789abcdef0123456789abcdef01234567",
  "html_url": "https://github.com/alice/repo/actions/runs/9999",
  "created_at": "2026-04-15T13:00:00Z",
  "updated_at": "2026-04-15T13:05:00Z"
}`

const canonReviewsBody = `[
  {
    "id": 5001,
    "state": "APPROVED",
    "body": "LGTM",
    "submitted_at": "2026-04-02T10:30:00Z",
    "user": {"login": "carol"}
  }
]`

const canonCommentsBody = `[
  {
    "id": 7001,
    "body": "Worth retesting on linux",
    "created_at": "2026-04-02T11:00:00Z",
    "updated_at": "2026-04-02T11:00:00Z",
    "user": {"login": "carol"}
  }
]`

const canonRepoBody = `{
  "description": "test fixtures for orchard",
  "private": false,
  "fork": false,
  "archived": false,
  "default_branch": "main",
  "html_url": "https://github.com/alice/repo",
  "updated_at": "2026-04-15T13:05:00Z"
}`

// httpClientForTLS returns an http.Client that trusts the test
// httptest.NewTLSServer's self-signed cert. We can't just use
// http.DefaultTransport because the test server uses a generated CA.
func httpClientForTLS(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	c := srv.Client()
	c.Timeout = 10 * time.Second
	return c
}

// newGHProviderForTest wires a Provider against the stub API + fake
// `gh auth token`. Caller passes the httptest base URL (which the
// resolver layer writes via GH_API_BASE_URL in production but the
// provider takes directly here).
func newGHProviderForTest(t *testing.T, baseURL string, auth gh.AuthSource, httpClient *http.Client) *gh.Provider {
	t.Helper()
	p := gh.NewWith(nil, baseURL, auth, time.Now)
	if err := p.Start(context.Background()); err != nil {
		t.Logf("provider start (non-fatal): %v", err)
	}
	// Inject the trusting HTTP client into the underlying Client by
	// triggering construction once via a low-cost call, then swapping.
	// The Provider builds its Client lazily on first request; we
	// reach through Start to force the build, then rewrite HTTP.
	if httpClient != nil {
		// Drive a no-op auth resolve to materialise the client.
		_ = p.AuthError(context.Background())
		// Use the package-internal hook: the Provider exposes the
		// client via newGHProviderForTest by composing a custom
		// constructor. We cheat by hitting an internal field through
		// reflection... but the Provider already has a public knob:
		// it uses the GH_API_BASE_URL env. The cleaner approach is to
		// let Provider build its own *http.Client and then surgically
		// inject. See the testHTTPInjector helper below.
		injectHTTPClient(t, p, httpClient)
	}
	return p
}

// injectHTTPClient is a test-only door to swap the Provider's HTTP
// client for one that trusts the test server. Implemented via the
// Provider's exported testing seam, gh.SetHTTPClientForTest.
func injectHTTPClient(t *testing.T, p *gh.Provider, c *http.Client) {
	t.Helper()
	gh.SetHTTPClientForTest(p, c)
}

// TestGH_E2E_PullRequestsCannedShape boots the GraphQL stack with the
// gh provider wired against a stubbed GitHub API + fake `gh` shellout
// and asserts the canonical pullRequests query returns the canned PR.
func TestGH_E2E_PullRequestsCannedShape(t *testing.T) {
	installFakeGH(t)
	api := stubAPI(t)
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)

	srv := newDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query {
		pullRequests(repo: "alice/repo", state: OPEN) {
			id
			repoOwner
			repoName
			number
			title
			state
			draft
			authorLogin
			baseRef
			headRef
			url
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.PullRequests); got != 2 {
		t.Fatalf("expected 2 PRs, got %d", got)
	}
	pr := resp.Data.PullRequests[0]
	if pr.Number != 42 {
		t.Errorf("pr[0].number = %d, want 42", pr.Number)
	}
	if pr.AuthorLogin != "bob" {
		t.Errorf("pr[0].authorLogin = %q, want bob", pr.AuthorLogin)
	}
	if pr.State != "OPEN" {
		t.Errorf("pr[0].state = %q, want OPEN", pr.State)
	}
	if pr.RepoOwner != "alice" || pr.RepoName != "repo" {
		t.Errorf("repo identity = %s/%s, want alice/repo", pr.RepoOwner, pr.RepoName)
	}
	if pr.ID != "PullRequest:alice/repo#42" {
		t.Errorf("id = %q, want PullRequest:alice/repo#42", pr.ID)
	}
	if pr.BaseRef != "main" || pr.HeadRef != "feature/widget" {
		t.Errorf("base/head = %s/%s", pr.BaseRef, pr.HeadRef)
	}
}

// TestGH_E2E_IssuesFiltersPRs asserts the issues endpoint filters out
// the GitHub-disguised pull-request rows. The canned issues body has
// two entries, one of which has `pull_request: {...}` — it should not
// surface.
func TestGH_E2E_IssuesFiltersPRs(t *testing.T) {
	installFakeGH(t)
	api := stubAPI(t)
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)
	srv := newDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query {
		issues(repo: "alice/repo", state: OPEN) {
			id
			number
			title
			state
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.Issues); got != 1 {
		t.Fatalf("expected 1 issue (PR filtered), got %d", got)
	}
	if resp.Data.Issues[0].Number != 100 {
		t.Errorf("number = %d, want 100", resp.Data.Issues[0].Number)
	}
}

// TestGH_E2E_WorkflowRuns asserts the workflowRuns endpoint surfaces
// the canned run.
func TestGH_E2E_WorkflowRuns(t *testing.T) {
	installFakeGH(t)
	api := stubAPI(t)
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)
	srv := newDaemon(t, provider)
	defer srv.Close()

	resp := postQuery(t, srv.URL, `query {
		workflowRuns(repo: "alice/repo") {
			id
			runId
			name
			status
			conclusion
		}
	}`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	if got := len(resp.Data.WorkflowRuns); got != 1 {
		t.Fatalf("expected 1 run, got %d", got)
	}
	if resp.Data.WorkflowRuns[0].RunID != 9999 {
		t.Errorf("runId = %d, want 9999", resp.Data.WorkflowRuns[0].RunID)
	}
	if resp.Data.WorkflowRuns[0].Conclusion != "success" {
		t.Errorf("conclusion = %q, want success", resp.Data.WorkflowRuns[0].Conclusion)
	}
}

// TestGH_E2E_NotAuthenticated proves the per-field GraphQL error path:
// when `gh auth token` produces no token, the resolver surfaces an
// error in the `pullRequests` field while sibling fields (`health`)
// still resolve. AC9 / ADR-011 §6, §12.
func TestGH_E2E_NotAuthenticated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	// Empty PATH so `gh` is not found at all. The provider's auth
	// bootstrap returns ErrGHNotInstalled, which the resolver surfaces
	// per-field.
	t.Setenv("PATH", "")

	api := stubAPI(t)
	tlsClient := httpClientForTLS(t, api)

	auth := gh.NewCommandAuthSource()
	provider := newGHProviderForTest(t, api.URL, auth, tlsClient)
	srv := newDaemon(t, provider)
	defer srv.Close()

	// Composite query: pullRequests should error per-field, health
	// should still resolve.
	raw := postQueryRaw(t, srv.URL, `query {
		health { status }
		pullRequests(repo: "alice/repo") { id }
	}`)

	var env struct {
		Data struct {
			Health *struct {
				Status string `json:"status"`
			} `json:"health"`
			PullRequests []map[string]any `json:"pullRequests"`
		} `json:"data"`
		Errors []struct {
			Message string   `json:"message"`
			Path    []string `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, raw)
	}
	if env.Data.Health == nil || env.Data.Health.Status != "ok" {
		t.Errorf("expected health.status = ok, got %+v\nraw: %s", env.Data.Health, raw)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("expected per-field error on pullRequests, got none. raw: %s", raw)
	}
	found := false
	for _, e := range env.Errors {
		if len(e.Path) > 0 && e.Path[0] == "pullRequests" {
			found = true
			if !strings.Contains(strings.ToLower(e.Message), "not installed") &&
				!strings.Contains(strings.ToLower(e.Message), "authent") &&
				!strings.Contains(strings.ToLower(e.Message), "gh") {
				t.Errorf("error message does not name the auth issue: %q", e.Message)
			}
		}
	}
	if !found {
		t.Errorf("no error with path=pullRequests in errors: %+v", env.Errors)
	}
}

// TestGH_Webhook_PRInvalidates posts a canned pull_request webhook to
// /webhook/github and asserts (a) the handler returns 200 with the
// expected node id and (b) a Subscriber receives the event.
func TestGH_Webhook_PRInvalidates(t *testing.T) {
	provider := gh.NewWith(nil, "https://api.github.com", &gh.StaticAuthSource{TokenValue: "tok"}, time.Now)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := provider.Subscribe(subCtx)

	handler := gh.NewWebhookHandler(provider, "")
	mux := http.NewServeMux()
	mux.Handle("/webhook/github", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{
		"action": "opened",
		"pull_request": {"number": 7},
		"repository": {"name": "repo", "owner": {"login": "alice"}}
	}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/webhook/github", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	req.Header.Set("X-GitHub-Event", "pull_request")
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

	select {
	case ev := <-sub:
		if ev.Key != "PullRequest:alice/repo#7" {
			t.Errorf("event.Key = %q, want PullRequest:alice/repo#7", ev.Key)
		}
		if ev.Reason != "webhook:pull_request" {
			t.Errorf("event.Reason = %q", ev.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no invalidation event received")
	}
}

// TestGH_Webhook_HMAC asserts the HMAC validator rejects bad signatures.
func TestGH_Webhook_HMAC(t *testing.T) {
	provider := gh.NewWith(nil, "https://api.github.com", &gh.StaticAuthSource{TokenValue: "tok"}, time.Now)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	handler := gh.NewWebhookHandler(provider, "secret")
	mux := http.NewServeMux()
	mux.Handle("/webhook/github", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/webhook/github", strings.NewReader(`{}`))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad sig, got %d", resp.StatusCode)
	}
}

// newDaemon stands up an httptest.Server with the GraphQL handler and
// the gh provider wired in. Tests use the returned URL as the daemon
// URL — the resolver root sees the gh provider for that one process.
func newDaemon(t *testing.T, p *gh.Provider) *httptest.Server {
	t.Helper()
	cfg := gqlgen.Config{Resolvers: resolvers.New(time.Now()).WithGH(p)}
	gqlSrv := handler.New(gqlgen.NewExecutableSchema(cfg))
	gqlSrv.AddTransport(transport.POST{})
	gqlSrv.AddTransport(transport.GET{})
	mux := http.NewServeMux()
	mux.Handle("/graphql", gqlSrv)
	return httptest.NewServer(mux)
}

// graphqlResponse is the typed shape we assert on. Each test only fills
// in the Data field for the query it ran; missing fields are zero-valued.
type graphqlResponse struct {
	Data struct {
		PullRequests []prNode      `json:"pullRequests"`
		Issues       []issueNode   `json:"issues"`
		WorkflowRuns []runNode     `json:"workflowRuns"`
		Health       *healthNode   `json:"health"`
	} `json:"data"`
	Errors []map[string]any `json:"errors,omitempty"`
}

type prNode struct {
	ID          string `json:"id"`
	RepoOwner   string `json:"repoOwner"`
	RepoName    string `json:"repoName"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"`
	Draft       bool   `json:"draft"`
	AuthorLogin string `json:"authorLogin"`
	BaseRef     string `json:"baseRef"`
	HeadRef     string `json:"headRef"`
	URL         string `json:"url"`
}

type issueNode struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type runNode struct {
	ID         string `json:"id"`
	RunID      int64  `json:"runId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type healthNode struct {
	Status string `json:"status"`
}

func postQuery(t *testing.T, url, query string) graphqlResponse {
	t.Helper()
	raw := postQueryRaw(t, url, query)
	var out graphqlResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out
}

func postQueryRaw(t *testing.T, url, query string) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest(http.MethodPost, url+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build: %v", err)
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	return raw
}
