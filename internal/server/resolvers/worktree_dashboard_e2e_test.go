package resolvers_test

// End-to-end test for the full Worktree dashboard query — issue #441 Phase 1.
//
// What's "E2E" here:
//   - A real git-worktree on-disk layout is synthesised in a temp directory
//     (no `git` binary required — we write the raw files git worktree add
//     produces).
//   - A real gh.Provider is wired with an httptest stub that serves the
//     canned PR + issue payloads without touching api.github.com.
//   - A real git.Provider reads the tempdir layout.
//   - The resolvers, DataLoader middleware, and gqlgen schema are all live.
//   - We POST a query over HTTP and assert the JSON response shape.
//
// Scenario (feature file line ~181):
//
//	Given  the daemon has one project, one main worktree + one linked worktree
//	       on branch "issue441/foo"
//	And    the GitHub API stub returns PR #8765 with headRef "issue441/foo"
//	And    the GitHub API stub returns issue #441
//	When   we query { projects { worktrees { host repo branch pr { number } issue { number } } } }
//	Then   the linked worktree row has
//	         host  == "local"
//	         repo  == "drewdrewthis/orchard-test"
//	         pr.number  == 8765
//	         issue.number == 441

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/loaders"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/resolvers"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	gqlgen "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// dashboardE2EGHScript is the fake `gh auth token` shim.
// Anything other than `gh auth token` exits 2 so unexpected invocations
// are loud.
const dashboardE2EGHScript = `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "token" ]; then
  echo "test-token-dashboard-e2e"
  exit 0
fi
echo "unexpected gh invocation: $@" 1>&2
exit 2
`

// installDashboardFakeGH writes a `gh` shim into a fresh temp dir and
// prepends it to PATH. Returns cleanup function; also registers t.Cleanup.
func installDashboardFakeGH(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(dashboardE2EGHScript), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
}

// stubGHAPI mounts a tiny mux that serves the canned PR list + single issue.
// Any other path responds 404 and fails the test loudly.
func stubGHAPI(t *testing.T) *httptest.Server {
	t.Helper()
	const (
		prListBody = `[{
			"number": 8765,
			"title": "issue441/foo PR",
			"body": "test PR",
			"state": "open",
			"draft": false,
			"html_url": "https://github.com/drewdrewthis/orchard-test/pull/8765",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-02T00:00:00Z",
			"merged_at": null,
			"user": {"login": "testuser"},
			"base": {"ref": "main"},
			"head": {"ref": "issue441/foo"}
		}]`
		issueBody = `{
			"number": 441,
			"title": "issue 441",
			"body": "the test issue",
			"state": "open",
			"html_url": "https://github.com/drewdrewthis/orchard-test/issues/441",
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-02T00:00:00Z",
			"user": {"login": "testuser"}
		}`
	)

	mux := http.NewServeMux()

	// PR list — called by the PullRequestsForRepo DataLoader.
	mux.HandleFunc("/repos/drewdrewthis/orchard-test/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(prListBody))
	})

	// Single issue — called by Worktree.issue resolver.
	mux.HandleFunc("/repos/drewdrewthis/orchard-test/issues/441", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(issueBody))
	})

	// Catch-all: fail the test for unexpected requests.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected GitHub API request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// buildWorktreeLayout creates a minimal on-disk git worktree layout
// that the git provider can read without shelling out to git.
//
// Layout produced (under tmpdir):
//
//	<tmpdir>/
//	  .git/                         ← main worktree git dir
//	    config                      ← [remote "origin"] URL
//	    HEAD                        ← ref: refs/heads/main
//	    refs/heads/main             ← fake SHA (40 hex chars)
//	    worktrees/
//	      issue441-foo/
//	        HEAD                    ← ref: refs/heads/issue441/foo
//	        gitdir                  ← points at <checkout>/.git
//	        commondir               ← "../.."
//	  issue441-foo-checkout/        ← linked worktree checkout dir
//	    .git                        ← file: "gitdir: <abs>/.git/worktrees/issue441-foo"
//
// Returns (projectDir, checkoutDir).
func buildWorktreeLayout(t *testing.T) (projectDir, checkoutDir string) {
	t.Helper()
	tmpdir := t.TempDir()

	// ── main git directory ──────────────────────────────────────────────
	gitDir := filepath.Join(tmpdir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	// .git/config — origin pointing at the test repo.
	cfg := "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n" +
		"\n[remote \"origin\"]\n\turl = git@github.com:drewdrewthis/orchard-test.git\n" +
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}

	// .git/HEAD — symbolic ref to main (default branch).
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}

	// .git/refs/heads/main — fake but valid 40-char SHA.
	refsHeads := filepath.Join(gitDir, "refs", "heads")
	if err := os.MkdirAll(refsHeads, 0o755); err != nil {
		t.Fatalf("mkdir refs/heads: %v", err)
	}
	fakeSHA := "0000000000000000000000000000000000000001"
	if err := os.WriteFile(filepath.Join(refsHeads, "main"), []byte(fakeSHA+"\n"), 0o644); err != nil {
		t.Fatalf("write refs/heads/main: %v", err)
	}

	// ── linked worktree gitdir entry ────────────────────────────────────
	wtGitDir := filepath.Join(gitDir, "worktrees", "issue441-foo")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees/issue441-foo: %v", err)
	}

	// The checkout directory that will become the linked worktree path.
	checkoutDir = filepath.Join(tmpdir, "issue441-foo-checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout: %v", err)
	}

	// .git/worktrees/issue441-foo/HEAD — branch of the linked worktree.
	wtHead := "ref: refs/heads/issue441/foo\n"
	if err := os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte(wtHead), 0o644); err != nil {
		t.Fatalf("write worktree HEAD: %v", err)
	}

	// .git/worktrees/issue441-foo/gitdir — points to <checkout>/.git
	// (the file that lives in the checkout directory).
	checkoutGitFile := filepath.Join(checkoutDir, ".git")
	if err := os.WriteFile(filepath.Join(wtGitDir, "gitdir"), []byte(checkoutGitFile+"\n"), 0o644); err != nil {
		t.Fatalf("write worktree gitdir: %v", err)
	}

	// .git/worktrees/issue441-foo/commondir — back-reference to main .git.
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644); err != nil {
		t.Fatalf("write commondir: %v", err)
	}

	// ── linked worktree checkout ────────────────────────────────────────
	// <checkout>/.git — a FILE pointing back to the worktree gitdir.
	gitFileContent := fmt.Sprintf("gitdir: %s\n", wtGitDir)
	if err := os.WriteFile(checkoutGitFile, []byte(gitFileContent), 0o644); err != nil {
		t.Fatalf("write checkout .git file: %v", err)
	}

	projectDir = tmpdir
	return projectDir, checkoutDir
}

// staticReposListerE2E is a fixture-grade resolvers.ReposLister that
// returns a fixed slice. Mirrors the identical type in git_e2e_test.go
// (different package so we redefine it here).
type staticReposListerE2E struct {
	records []configprovider.Repo
}

func (s *staticReposListerE2E) List(_ context.Context) ([]configprovider.Repo, error) {
	out := make([]configprovider.Repo, len(s.records))
	copy(out, s.records)
	return out, nil
}

// TestWorktreeDashboard_E2E asserts the full Worktree dashboard query
// returns host, repo, pr, issue for the linked worktree on branch
// "issue441/foo". Resolves issue #441.
func TestWorktreeDashboard_E2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH-substituted shellout test is POSIX-only")
	}

	// ── 1. Install fake `gh auth token` shellout ────────────────────────
	installDashboardFakeGH(t)

	// ── 2. Stub the GitHub REST API ─────────────────────────────────────
	api := stubGHAPI(t)
	tlsClient := api.Client()
	tlsClient.Timeout = 10 * time.Second

	// ── 3. Build the tempdir git worktree layout ─────────────────────────
	projectDir, _ := buildWorktreeLayout(t)

	// ── 4. Start the git provider ─────────────────────────────────────────
	gitProv := gitprovider.NewProvider(nil)
	t.Cleanup(gitProv.Stop)
	const projectID = "proj-1"
	if err := gitProv.AddProject(gitprovider.Project{ID: projectID, Dir: projectDir}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	// ── 5. Wire the gh provider with stubbed HTTP ─────────────────────────
	auth := gh.NewCommandAuthSource()
	ghProv := gh.NewWith(nil, api.URL, auth, time.Now)
	if err := ghProv.Start(context.Background()); err != nil {
		t.Logf("gh provider start (non-fatal): %v", err)
	}
	// AuthError forces lazy client build, then we swap to the TLS-trusting client.
	_ = ghProv.AuthError(context.Background())
	gh.SetHTTPClientForTest(ghProv, tlsClient)

	// ── 6. Build the repos lister ─────────────────────────────────────────
	repos := &staticReposListerE2E{
		records: []configprovider.Repo{
			{ID: configprovider.RepoID(projectID), Slug: projectID, Path: projectDir},
		},
	}

	// ── 7. Wire the resolver + DataLoader middleware + httptest server ─────
	r := resolvers.New(time.Now()).
		WithGit(gitProv).
		WithGH(ghProv).
		WithRepos(repos)

	gqlSrv := handler.New(gqlgen.NewExecutableSchema(gqlgen.Config{Resolvers: r}))
	gqlSrv.AddTransport(transport.POST{})

	ts := httptest.NewServer(loaders.Middleware(r.LoaderBundle(), gqlSrv))
	t.Cleanup(ts.Close)

	// ── 8. Fire the dashboard query ───────────────────────────────────────
	const q = `{
		repos {
			worktrees {
				branch
				host
				repo
				pr { number }
				issue { number }
			}
		}
	}`

	body, _ := json.Marshal(map[string]string{"query": q})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL, bytes.NewReader(body))
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
		t.Fatalf("http status = %d, want 200", resp.StatusCode)
	}

	// ── 9. Decode response ─────────────────────────────────────────────────
	var out struct {
		Data struct {
			Repos []struct {
				Worktrees []struct {
					Branch string  `json:"branch"`
					Host   string  `json:"host"`
					Repo   *string `json:"repo"`
					Pr     *struct {
						Number int64 `json:"number"`
					} `json:"pr"`
					Issue *struct {
						Number int64 `json:"number"`
					} `json:"issue"`
				} `json:"worktrees"`
			} `json:"repos"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", out.Errors)
	}

	// ── 10. Find the linked worktree row (branch == "issue441/foo") ────────
	if len(out.Data.Repos) == 0 {
		t.Fatalf("expected at least one project, got none")
	}

	var target *struct {
		Branch string  `json:"branch"`
		Host   string  `json:"host"`
		Repo   *string `json:"repo"`
		Pr     *struct {
			Number int64 `json:"number"`
		} `json:"pr"`
		Issue *struct {
			Number int64 `json:"number"`
		} `json:"issue"`
	}
	for i := range out.Data.Repos[0].Worktrees {
		wt := &out.Data.Repos[0].Worktrees[i]
		if wt.Branch == "issue441/foo" {
			target = wt
			break
		}
	}
	if target == nil {
		branches := make([]string, 0, len(out.Data.Repos[0].Worktrees))
		for _, wt := range out.Data.Repos[0].Worktrees {
			branches = append(branches, fmt.Sprintf("%q", wt.Branch))
		}
		t.Fatalf("no worktree with branch 'issue441/foo'; got branches: %v", branches)
	}

	// ── 11. Assert the four values ─────────────────────────────────────────
	if got := target.Host; got != "local" {
		t.Errorf("host = %q, want %q", got, "local")
	}
	if target.Repo == nil {
		t.Error("repo is nil, want \"drewdrewthis/orchard-test\"")
	} else if got := *target.Repo; got != "drewdrewthis/orchard-test" {
		t.Errorf("repo = %q, want %q", got, "drewdrewthis/orchard-test")
	}
	if target.Pr == nil {
		t.Error("pr is nil, want {number: 8765}")
	} else if got := target.Pr.Number; got != 8765 {
		t.Errorf("pr.number = %d, want 8765", got)
	}
	if target.Issue == nil {
		t.Error("issue is nil, want {number: 441}")
	} else if got := target.Issue.Number; got != 441 {
		t.Errorf("issue.number = %d, want 441", got)
	}
}
