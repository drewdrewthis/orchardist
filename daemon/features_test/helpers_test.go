// Package features_test implements plain-Go integration tests for every scenario
// in daemon/features/daemon/**/*.feature.
//
// Test naming rules (from docs/testing-philosophy.md):
//   - Present-tense, action-based names; no "Should" or "should".
//   - Each test fn is annotated with "// @scenario <verbatim scenario title>"
//     directly above the func declaration.
//   - t.Run("when X", ...) groups sub-cases; mirrors Gherkin Given/When/Then.
//
// All tests run against an in-process httptest.Server built from
// internal/server.New() with real git/tmux/ps providers where available.
// The GraphQL boundary is the test boundary (T4). No mocks for domain providers
// except where the provider is unrunnable in a CI environment (e.g. live tmux).
package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server"
	"github.com/drewdrewthis/orchardist/internal/server/providers/claudeprojects"
	"github.com/drewdrewthis/orchardist/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/orchardist/internal/server/providers/git"
	"github.com/drewdrewthis/orchardist/internal/server/providers/host"
	"github.com/drewdrewthis/orchardist/internal/server/resolvers"
)

// ---------------------------------------------------------------------------
// graphQLResponse is the top-level envelope for all GraphQL responses.
// ---------------------------------------------------------------------------

type graphQLResponse struct {
	Data   map[string]any   `json:"data"`
	Errors []map[string]any `json:"errors"`
}

func (r graphQLResponse) hasErrors() bool       { return len(r.Errors) > 0 }
func (r graphQLResponse) errorMessages() string {
	msgs := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		if m, ok := e["message"].(string); ok {
			msgs = append(msgs, m)
		}
	}
	return strings.Join(msgs, "; ")
}

// ---------------------------------------------------------------------------
// Server fixture helpers
// ---------------------------------------------------------------------------

// fixedReposLister satisfies resolvers.ReposLister with an in-memory slice.
type fixedReposLister struct {
	rows []config.Repo
}

func (f *fixedReposLister) List(_ context.Context) ([]config.Repo, error) {
	return f.rows, nil
}

var _ resolvers.ReposLister = (*fixedReposLister)(nil)

// startMinimalServer boots an httptest.Server with just the host provider
// and a claudeprojects provider pointing at a temp dir. Returns the server
// and a cleanup func.
func startMinimalServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hp := host.New()
	if err := hp.Start(ctx); err != nil {
		t.Fatalf("host start: %v", err)
	}

	cpDir := t.TempDir()
	cp := claudeprojects.New(cpDir, "test-host", nil)
	if err := cp.Start(ctx); err != nil {
		t.Fatalf("claudeprojects start: %v", err)
	}

	srv := server.New("", slog.Default(), server.WithClaudeProjects(cp))
	srv.Resolver().HostProvider = hp

	ts := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(ts.Close)
	return ts
}

// startServerWithRepo creates a temp git repo, wires the git provider,
// and starts an httptest.Server. Returns the server and a cleanup func.
func startServerWithRepo(t *testing.T) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dir := t.TempDir()
	gitInitWithCommit(t, dir)

	gp := gitprovider.NewProvider(nil)
	const slug = "demo-repo"
	if err := gp.AddProject(gitprovider.Project{ID: slug, Dir: dir}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	t.Cleanup(gp.Stop)

	hp := host.New()
	if err := hp.Start(ctx); err != nil {
		t.Fatalf("host start: %v", err)
	}
	cpDir := t.TempDir()
	cp := claudeprojects.New(cpDir, "test-host", nil)
	if err := cp.Start(ctx); err != nil {
		t.Fatalf("claudeprojects start: %v", err)
	}

	repos := &fixedReposLister{rows: []config.Repo{{
		ID:   config.RepoID(slug),
		Slug: slug,
		Path: dir,
	}}}

	srv := server.New("", slog.Default(),
		server.WithRepos(repos),
		server.WithGit(gp),
		server.WithClaudeProjects(cp),
	)
	srv.Resolver().HostProvider = hp

	ts := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(ts.Close)
	return ts
}

// startServerWithClaudeProjects boots a server wired to a claudeprojects
// provider watching cpDir. Call t.TempDir() to get cpDir if you need an empty one.
func startServerWithClaudeProjects(t *testing.T, cpDir string) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hp := host.New()
	if err := hp.Start(ctx); err != nil {
		t.Fatalf("host start: %v", err)
	}
	cp := claudeprojects.New(cpDir, "test-host", nil)
	if err := cp.Start(ctx); err != nil {
		t.Fatalf("claudeprojects start: %v", err)
	}

	srv := server.New("", slog.Default(), server.WithClaudeProjects(cp))
	srv.Resolver().HostProvider = hp

	ts := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(ts.Close)
	return ts
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// postGQL fires a GraphQL query against baseURL and decodes the response.
func postGQL(t *testing.T, baseURL, query string) graphQLResponse {
	t.Helper()
	return postGQLVars(t, baseURL, query, nil)
}

// postGQLVars fires a GraphQL query with variables.
func postGQLVars(t *testing.T, baseURL, query string, vars map[string]any) graphQLResponse {
	t.Helper()
	payload := map[string]any{"query": query}
	if vars != nil {
		payload["variables"] = vars
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	resp, err := http.Post(baseURL+"?", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var out graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

// postGQLTimed fires a GQL query and returns elapsed time alongside the response.
func postGQLTimed(t *testing.T, baseURL, query string) (graphQLResponse, time.Duration) {
	t.Helper()
	payload := map[string]any{"query": query}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	start := time.Now()
	resp, err := http.Post(baseURL+"?", "application/json", bytes.NewReader(body))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("http post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out graphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out, elapsed
}

// assertNoErrors fails the test if the response carries GraphQL errors.
func assertNoErrors(t *testing.T, r graphQLResponse) {
	t.Helper()
	if r.hasErrors() {
		t.Fatalf("unexpected GraphQL errors: %s", r.errorMessages())
	}
}

// getAt navigates a dot-separated path into a map[string]any.
// Returns (value, true) on success, (nil, false) if any segment is absent.
func getAt(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = m
	for _, p := range parts {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := mp[p]
		if !exists {
			return nil, false
		}
		cur = val
	}
	return cur, true
}

// asList asserts val is a []any and returns it.
func asList(t *testing.T, val any, fieldName string) []any {
	t.Helper()
	if val == nil {
		t.Fatalf("%s: expected list, got nil", fieldName)
	}
	lst, ok := val.([]any)
	if !ok {
		t.Fatalf("%s: expected []any, got %T", fieldName, val)
	}
	return lst
}

// asMap asserts val is a map[string]any and returns it.
func asMap(t *testing.T, val any, fieldName string) map[string]any {
	t.Helper()
	if val == nil {
		t.Fatalf("%s: expected map, got nil", fieldName)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("%s: expected map[string]any, got %T", fieldName, val)
	}
	return m
}

// requireField fails the test if key is absent in m.
func requireField(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Errorf("missing required field %q", key)
	}
}

// requireFields fails the test if any key in keys is absent in m.
func requireFields(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		requireField(t, m, k)
	}
}

// ---------------------------------------------------------------------------
// Git fixture helpers
// ---------------------------------------------------------------------------

// gitInitWithCommit shells out to git to initialise dir with an initial commit.
// Fixture setup may shell out; production code does not.
func gitInitWithCommit(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "TestUser"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		out, err := runIn(dir, args[0], args[1:]...)
		if err != nil {
			t.Fatalf("git fixture: %v: %v\n%s", args, err, out)
		}
	}
}

// runIn executes name args in dir and returns combined output.
func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---------------------------------------------------------------------------
// Float helpers
// ---------------------------------------------------------------------------

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func mustFloat64(t *testing.T, v any, field string) float64 {
	t.Helper()
	f, ok := toFloat64(v)
	if !ok {
		t.Fatalf("%s: expected numeric, got %T (%v)", field, v, v)
	}
	return f
}

func mustNonNegativeInt(t *testing.T, v any, field string) {
	t.Helper()
	f, ok := toFloat64(v)
	if !ok {
		t.Fatalf("%s: expected numeric, got %T (%v)", field, v, v)
	}
	if f < 0 {
		t.Fatalf("%s: expected non-negative, got %v", field, f)
	}
}

// ---------------------------------------------------------------------------
// JSONL fixture helpers
// ---------------------------------------------------------------------------

// writeJSONLRecord appends one JSON record to a .jsonl file.
func writeJSONLRecord(t *testing.T, path string, record any) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal jsonl record: %v", err)
	}
	data = append(data, '\n')

	f, err := openAppend(path)
	if err != nil {
		t.Fatalf("open jsonl %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
}

func openAppend(path string) (io.WriteCloser, error) {
	return appendFile(path)
}

// appendFile opens path for appending, creating it if absent.
// Defined in a separate file to keep OS-specific flags clear.
func appendFile(path string) (io.WriteCloser, error) {
	return appendFileOS(path)
}

func fmtAddr(ts *httptest.Server) string {
	return fmt.Sprintf("daemon httptest.Server at %s", ts.URL)
}
