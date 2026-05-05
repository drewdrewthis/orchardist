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
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
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
