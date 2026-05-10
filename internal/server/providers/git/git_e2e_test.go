package git_test

// E2E test for the git provider per Workstream B-git AC6.
//
// What's "E2E" here:
//   - Real `git` binary creates fixture repos and worktrees (shellout
//     for setup is permitted by the briefing).
//   - Real fsnotify watches the temp directories.
//   - Real GraphQL: the provider, resolvers, and gqlgen handler are
//     stitched together inside a httptest.Server, and we POST queries
//     over HTTP.
//   - No mocks. The only "test scaffolding" types are tiny harness
//     helpers around the real components.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
)

// staticReposLister is a fixture-grade resolvers.ReposLister for the
// git e2e test — returns a fixed slice so the test can drive
// Repo.worktrees without standing up the config provider.
type staticReposLister struct {
	records []configprovider.Repo
}

func (s *staticReposLister) List(_ context.Context) ([]configprovider.Repo, error) {
	out := make([]configprovider.Repo, len(s.records))
	copy(out, s.records)
	return out, nil
}

// TestGitProvider_E2E walks the AC6 acceptance criteria end-to-end:
//
//  1. Create a real git repo with `git init` + an initial commit.
//  2. Add two worktrees via `git worktree add`.
//  3. Start the daemon's GraphQL server against the temp repo.
//  4. Assert `query { projects { worktrees { ... } } }` returns the
//     project's main checkout plus the two worktrees with the right
//     branches.
//  5. Add a third worktree; assert fsnotify causes it to appear.
//  6. Remove the third worktree; assert it disappears.
func TestGitProvider_E2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; e2e test requires real git for fixture setup")
	}

	repo := setupRepoWithCommits(t)

	// Two `git worktree add` invocations: feature/a on a new branch,
	// feature/b on a new branch.
	worktreesParent := t.TempDir()
	wtA := filepath.Join(worktreesParent, "a")
	wtB := filepath.Join(worktreesParent, "b")
	runGit(t, repo, "worktree", "add", "-b", "feature/a", wtA)
	runGit(t, repo, "worktree", "add", "-b", "feature/b", wtB)

	// Provider + resolver + httptest GraphQL server.
	provider := gitprovider.NewProvider(nil)
	t.Cleanup(provider.Stop)

	const projectID = "demo"
	if err := provider.AddProject(gitprovider.Project{ID: projectID, Dir: repo}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	repos := &staticReposLister{
		records: []configprovider.Repo{
			{ID: configprovider.RepoID(projectID), Slug: projectID, Path: repo},
		},
	}

	srv := server.New("", nil, server.WithGit(provider), server.WithRepos(repos))
	ts := httptest.NewServer(srv.GraphQLHandler())
	t.Cleanup(ts.Close)

	// Initial assertion: 3 worktrees (main + a + b).
	wantInitial := map[string]string{
		"demo:main": "main",
		"demo:a":    "feature/a",
		"demo:b":    "feature/b",
	}
	gotInitial := queryWorktreeBranches(t, ts.URL)
	if !equalMap(gotInitial, wantInitial) {
		t.Fatalf("initial worktrees mismatch:\n got: %v\nwant: %v", gotInitial, wantInitial)
	}

	// Sanity: every returned worktree has a non-empty path + 40-char head.
	gotInitialFull := queryWorktrees(t, ts.URL)
	for _, w := range gotInitialFull {
		if w.Path == "" {
			t.Errorf("worktree %s has empty path", w.ID)
		}
		if len(w.Head) != 40 {
			t.Errorf("worktree %s has malformed head %q (len=%d)", w.ID, w.Head, len(w.Head))
		}
		if w.Bare {
			t.Errorf("worktree %s unexpectedly bare", w.ID)
		}
	}

	// Add a third worktree mid-flight; assert fsnotify catches it.
	wtC := filepath.Join(worktreesParent, "c")
	runGit(t, repo, "worktree", "add", "-b", "feature/c", wtC)
	wantAfterAdd := map[string]string{
		"demo:main": "main",
		"demo:a":    "feature/a",
		"demo:b":    "feature/b",
		"demo:c":    "feature/c",
	}
	waitFor(t, 5*time.Second, "third worktree to appear", func() bool {
		got := queryWorktreeBranches(t, ts.URL)
		return equalMap(got, wantAfterAdd)
	})

	// Remove the third worktree; assert it disappears.
	runGit(t, repo, "worktree", "remove", "--force", wtC)
	wantAfterRemove := map[string]string{
		"demo:main": "main",
		"demo:a":    "feature/a",
		"demo:b":    "feature/b",
	}
	waitFor(t, 5*time.Second, "third worktree to disappear", func() bool {
		got := queryWorktreeBranches(t, ts.URL)
		return equalMap(got, wantAfterRemove)
	})
}

// setupRepoWithCommits creates a git repo with one initial commit so
// that HEAD resolves cleanly. Returns the absolute path to the working
// tree.
func setupRepoWithCommits(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	// Stable identity so commit creation doesn't fail on a CI box without
	// a global git config.
	runGit(t, repo, "config", "user.email", "ws-b-git@example.com")
	runGit(t, repo, "config", "user.name", "ws-b-git")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

// runGit shells out to git in repo and fails the test on any non-zero
// exit. Briefing explicitly allows shell-outs for fixture setup.
func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
}

// gqlWorktree mirrors the GraphQL Worktree shape, decoded from JSON.
type gqlWorktree struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Bare   bool   `json:"bare"`
}

// queryWorktrees runs the canonical AC6 query against the test server
// and returns the flattened slice of worktrees from all repos.
func queryWorktrees(t *testing.T, url string) []gqlWorktree {
	t.Helper()
	const q = `{ repos { worktrees { id path branch head bare } } }`
	body, _ := json.Marshal(map[string]any{"query": q})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("graphql response: status %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Repos []struct {
				Worktrees []gqlWorktree `json:"worktrees"`
			} `json:"repos"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) > 0 {
		t.Fatalf("graphql errors: %v", out.Errors)
	}
	var all []gqlWorktree
	for _, p := range out.Data.Repos {
		all = append(all, p.Worktrees...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

// queryWorktreeBranches projects the result of queryWorktrees down to a
// {worktree-id: branch} map for compact assertions.
func queryWorktreeBranches(t *testing.T, url string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, w := range queryWorktrees(t, url) {
		out[w.ID] = w.Branch
	}
	return out
}

// waitFor polls cond until it returns true or the timeout elapses, then
// fails. The polling cadence is fast (50ms) so fsnotify-driven tests
// don't sit idle longer than necessary.
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s after %v", desc, timeout)
}

// equalMap is a small deep-equality helper for the assertions; we don't
// pull in cmp for one comparison shape.
func equalMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// silence unused import in case http is removed in a future refactor.
var _ = fmt.Sprintf
