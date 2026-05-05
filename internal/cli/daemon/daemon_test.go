package daemon

// AC-2 + AC-3 wiring regression. These tests exercise the same set of
// providers that runStart constructs, mounted on an httptest.Server,
// and assert the GraphQL surface for git worktrees and claude instances
// resolves without "provider not configured" errors.
//
// Why not boot the real daemon? runStart owns a pidfile + signal trap
// + addr binding that fight with parallel tests. The wiring is the
// regression we care about — pidfile mechanics are unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// TestDaemonWiring_Worktrees is the AC-2 regression. Two configured
// projects must surface their main checkout under
// `projects { worktrees { path branch head bare } }` without a "git
// provider not configured" error.
func TestDaemonWiring_Worktrees(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")

	repoOne := initRepo(t, "alpha")
	repoTwo := initRepo(t, "beta")

	writeDaemonConfig(t, cfgPath, []configprovider.ProjectRow{
		{ID: "alpha", Directory: repoOne, Name: "Alpha"},
		{ID: "beta", Directory: repoTwo, Name: "Beta"},
	})

	configProvider := configprovider.NewProvider(
		configprovider.NewJSONFileAdapter(cfgPath, logger),
		logger,
	)
	if err := configProvider.Start(ctx); err != nil {
		t.Fatalf("config Start: %v", err)
	}
	t.Cleanup(func() { _ = configProvider.Stop() })

	gitProvider, err := buildGitProvider(ctx, configProvider, logger)
	if err != nil {
		t.Fatalf("buildGitProvider: %v", err)
	}
	t.Cleanup(gitProvider.Stop)

	srv := server.New("", logger,
		server.WithProjects(configProvider),
		server.WithGit(gitProvider),
	)
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	resp := postQuery(t, ts.URL, `{ projects { id worktrees { path branch head bare } } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	projects, _ := resp.Data["projects"].([]any)
	if len(projects) != 2 {
		t.Fatalf("want 2 projects, got %d (%+v)", len(projects), projects)
	}
	for _, raw := range projects {
		p, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("project payload not a map: %T", raw)
		}
		wts, _ := p["worktrees"].([]any)
		if len(wts) == 0 {
			t.Errorf("project %v: expected ≥1 worktree, got %v", p["id"], p["worktrees"])
		}
		first, ok := wts[0].(map[string]any)
		if !ok {
			t.Fatalf("worktree payload not a map: %T", wts[0])
		}
		if first["path"] == "" {
			t.Errorf("project %v: worktree path empty", p["id"])
		}
		if first["bare"] != false {
			t.Errorf("project %v: bare = %v, want false", p["id"], first["bare"])
		}
	}
}

// TestDaemonWiring_ClaudeInstances is the AC-3 regression.
// `{ claudeInstances { id state } }` must resolve cleanly to an empty
// list when no heartbeats exist, rather than failing with "claudeinstance
// provider not initialised".
func TestDaemonWiring_ClaudeInstances(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	heartbeatDir := t.TempDir()
	provider := claudeinstance.NewWith(
		"local",
		claudeinstance.NewFileReader(heartbeatDir),
		claudeinstance.NewComposer("local", nil, nil, nil),
		nil,
	)
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", logger, server.WithClaudeInstance(provider))
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("claudeinstance Start: %v", err)
	}
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	resp := postQuery(t, ts.URL, `{ claudeInstances { id state } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	instances, ok := resp.Data["claudeInstances"].([]any)
	if !ok {
		t.Fatalf("claudeInstances payload not a list: %T (%+v)", resp.Data["claudeInstances"], resp.Data)
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances when no heartbeats exist, got %d (%+v)", len(instances), instances)
	}
}

// TestDaemonWiring_AllProvidersBoot is the AC-1+AC-2+AC-3 cross-cutting
// boot smoke: build the full opts list runStart constructs, hand it to
// server.New + Run-equivalent (lifecycle hooks via HTTPHandler), and
// confirm a representative query against every wired provider succeeds.
func TestDaemonWiring_AllProvidersBoot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.json")
	repoOne := initRepo(t, "alpha")
	writeDaemonConfig(t, cfgPath, []configprovider.ProjectRow{
		{ID: "alpha", Directory: repoOne, Name: "Alpha"},
	})

	configProvider := configprovider.NewProvider(
		configprovider.NewJSONFileAdapter(cfgPath, logger),
		logger,
	)
	if err := configProvider.Start(ctx); err != nil {
		t.Fatalf("config Start: %v", err)
	}
	t.Cleanup(func() { _ = configProvider.Stop() })

	gitProvider, err := buildGitProvider(ctx, configProvider, logger)
	if err != nil {
		t.Fatalf("buildGitProvider: %v", err)
	}
	t.Cleanup(gitProvider.Stop)

	heartbeatDir := t.TempDir()
	claudeInstance := claudeinstance.NewWith(
		"local",
		claudeinstance.NewFileReader(heartbeatDir),
		claudeinstance.NewComposer("local", nil, nil, nil),
		nil,
	)
	t.Cleanup(func() { _ = claudeInstance.Stop() })
	if err := claudeInstance.Start(ctx); err != nil {
		t.Fatalf("claudeinstance Start: %v", err)
	}

	psProvider := ps.New(ps.NewAdapter("local"), logger)
	tmuxProvider := tmux.New(tmux.NewAdapter("local"), logger)
	claudeProjectsProvider := claudeprojects.New(t.TempDir(), "local", logger)

	fedCfg, err := peerproxy.LoadFederationConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadFederationConfig: %v", err)
	}
	peerProvider := peerproxy.NewProvider(fedCfg, logger)
	localEvents := peerproxy.NewLocalInvalidator()

	srv := server.New("", logger,
		server.WithProjects(configProvider),
		server.WithGit(gitProvider),
		server.WithPS(psProvider),
		server.WithTmux(tmuxProvider),
		server.WithClaudeProjects(claudeProjectsProvider),
		server.WithClaudeAccount(claudeaccount.New("local", logger)),
		server.WithClaudeInstance(claudeInstance),
		server.WithContracts(contracts.New(logger)),
		server.WithPeerProxy(peerProvider),
		server.WithPeerSecret(fedCfg.PeerSecret),
		server.WithLocalEvents(localEvents),
	)
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	const doc = `{
		projects { id worktrees { path bare } }
		claudeInstances { id state }
	}`
	resp := postQuery(t, ts.URL, doc)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
}

// initRepo creates a minimal git repo with one commit so the git
// provider can resolve HEAD. Returns the absolute repo path.
func initRepo(t *testing.T, label string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s/%s: %v\n%s", args, label, dir, err, string(out))
		}
	}
	run("init", "--initial-branch=main", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+label+"\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

// writeDaemonConfig drops a v1 config.json into path so the config
// provider's JSONFileAdapter can read it on Start.
func writeDaemonConfig(t *testing.T, path string, projects []configprovider.ProjectRow) {
	t.Helper()
	f := configprovider.File{Version: 1, Projects: projects}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

type gqlResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func postQuery(t *testing.T, url, doc string) gqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": doc})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestDaemonWiring_PullRequests is the AC-3 regression for issue #395:
// the daemon's runStart now constructs a gh provider and passes
// `server.WithGh(...)`. With the gh CLI shellout stubbed via PATH and
// the GitHub HTTPS API stubbed via httptest, a `pullRequests(repo:
// "alice/repo")` query against the wired stack must return the canned
// rows — not "gh provider not configured".
//
// All fixtures use `alice/repo` / `bob` / `carol` per the briefing's
// no-PII rule.
func TestDaemonWiring_PullRequests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	installFakeGHForDaemonTest(t)
	api := stubGHAPIForDaemonTest(t)

	provider := gh.New(logger, api.URL)
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("gh.Start: %v", err)
	}
	// httptest.NewTLSServer presents a self-signed cert; swap the
	// gh.Client's HTTP client for one that trusts the test CA.
	gh.SetHTTPClientForTest(provider, api.Client())

	srv := server.New("", logger, server.WithGh(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	resp := postQuery(t, ts.URL, `{ pullRequests(repo: "alice/repo", state: OPEN) { number title authorLogin } }`)
	if len(resp.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", resp.Errors)
	}
	prs, _ := resp.Data["pullRequests"].([]any)
	if len(prs) != 2 {
		t.Fatalf("want 2 PRs, got %d (%+v)", len(prs), prs)
	}
	first, _ := prs[0].(map[string]any)
	if first["number"].(float64) != 42 {
		t.Errorf("pr[0].number = %v, want 42", first["number"])
	}
	if first["authorLogin"] != "bob" {
		t.Errorf("pr[0].authorLogin = %v, want bob", first["authorLogin"])
	}
}

// TestDaemonWiring_GhUnauthenticated is the AC-4 regression: when the
// daemon boots without `gh` available on PATH, gh-derived fields must
// surface a per-field GraphQL error (not a top-level error, not a
// daemon crash). Sibling fields like `host` keep resolving — that is
// the ADR-011 §6 / §12 contract.
func TestDaemonWiring_GhUnauthenticated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Empty PATH so exec.LookPath("gh") returns ErrGHNotInstalled — the
	// strongest "gh unavailable" signal the provider models.
	t.Setenv("PATH", "")

	provider := gh.New(logger, "https://example.invalid")
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("gh.Start: %v", err)
	}

	srv := server.New("", logger, server.WithGh(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	// Pair the gh field with `health` — a provider-free sibling that
	// proves the daemon is still serving. AC-4: gh fails per-field, the
	// rest of the schema keeps resolving.
	raw := postQueryRaw(t, ts.URL, `{ health { status } pullRequests(repo: "alice/repo") { number } }`)

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
		t.Errorf("health should still resolve to ok; got %+v\nraw: %s", env.Data.Health, raw)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("expected per-field error on pullRequests, got none. raw: %s", raw)
	}
	found := false
	for _, e := range env.Errors {
		if len(e.Path) == 1 && e.Path[0] == "pullRequests" {
			found = true
			lower := strings.ToLower(e.Message)
			if !strings.Contains(lower, "not installed") &&
				!strings.Contains(lower, "authent") &&
				!strings.Contains(lower, "gh") {
				t.Errorf("error message does not name the auth issue: %q", e.Message)
			}
		}
	}
	if !found {
		t.Errorf("no error with path=[pullRequests] in errors: %+v\nraw: %s", env.Errors, raw)
	}
}

// installFakeGHForDaemonTest writes a `gh` shim into a fresh temp dir
// and prepends it to PATH so the gh provider's auth bootstrap returns
// a canned token without touching the real `gh` binary.
func installFakeGHForDaemonTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	body := "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"token\" ]; then\n  echo \"daemon-test-token\"\n  exit 0\nfi\necho \"unexpected gh: $@\" 1>&2\nexit 2\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubGHAPIForDaemonTest serves the canned GitHub REST payloads the
// AC-3 query needs. Any unexpected URL fails the test — the gh client
// must hit only the paths we anticipate.
func stubGHAPIForDaemonTest(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/alice/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer daemon-test-token" {
			t.Errorf("Authorization header = %q, want Bearer daemon-test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(daemonPullsBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s", r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// daemonPullsBody is the canned GitHub REST response for the AC-3
// query. Two PRs in `alice/repo`, authored by `bob` and `carol` — no
// real-world identities.
const daemonPullsBody = `[
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

func postQueryRaw(t *testing.T, url, doc string) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": doc})
	resp, err := http.Post(url+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := &bytes.Buffer{}
	_, _ = out.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, out.String())
	}
	return out.Bytes()
}
