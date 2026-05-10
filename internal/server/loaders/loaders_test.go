package loaders_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/loaders"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	ghprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	hostprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// TestProcessLoaderBatchesByPid asserts the n+1 ACs for the
// [TmuxPane].process edge: many concurrent process loads must invoke
// the batch fn exactly once.
func TestProcessLoaderBatchesByPid(t *testing.T) {
	const hostID = "machine-1"
	const N = 50
	runner := &fakePsRunner{
		header: "PID PPID USER TTY %CPU RSS STARTED COMMAND",
		lines:  syntheticPsLines(N, 100),
	}
	psProv := psprovider.New(psprovider.NewAdapter(hostID).WithRunner(runner).WithPollInterval(time.Hour), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := psProv.Start(ctx); err != nil {
		t.Fatalf("ps Start: %v", err)
	}

	bundle := &loaders.ProvidersBundle{Ps: psProv}
	l := loaders.NewLoaders(bundle)

	thunks := make([]func() (*graphql1.Process, error), 0, N)
	for i := 0; i < N; i++ {
		thunks = append(thunks, l.Process.Load(ctx, loaders.ProcessKey{HostID: hostID, Pid: 100 + i}))
	}
	for i, thunk := range thunks {
		got, err := thunk()
		if err != nil {
			t.Fatalf("thunk %d error: %v", i, err)
		}
		if got == nil || got.Pid != int64(100+i) {
			t.Fatalf("thunk %d returned %#v", i, got)
		}
	}

	if got, want := l.ProcessBatchCount(), 1; got != want {
		t.Fatalf("Process loader batched %d times, want %d (n+1 detection)", got, want)
	}
}

// TestHostLoaderBatchesByID asserts the n+1 AC for the [Process].host
// edge: many concurrent host loads collapse into one batch.
func TestHostLoaderBatchesByID(t *testing.T) {
	provider := hostprovider.NewWith(staticIdentityReader{}, staticLoadReader{}, time.Now)
	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("start host: %v", err)
	}

	bundle := &loaders.ProvidersBundle{Host: provider}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 32
	thunks := make([]func() (*graphql1.Host, error), 0, N)
	id := string(provider.LocalID())
	for i := 0; i < N; i++ {
		thunks = append(thunks, l.Host.Load(ctx, id))
	}
	for i, thunk := range thunks {
		got, err := thunk()
		if err != nil {
			t.Fatalf("thunk %d error: %v", i, err)
		}
		if got == nil {
			t.Fatalf("thunk %d returned nil host", i)
		}
	}

	if got, want := l.HostBatchCount(), 1; got != want {
		t.Fatalf("Host loader batched %d times, want %d", got, want)
	}
}

// TestWorktreeLoaderBatchesByCwd asserts the n+1 AC for the
// [Process].worktree edge: many cwd lookups collapse into one batch.
func TestWorktreeLoaderBatchesByCwd(t *testing.T) {
	dir := t.TempDir()
	gitProv := gitprovider.NewProvider(nil)
	t.Cleanup(gitProv.Stop)
	if err := gitProv.AddProject(gitprovider.Project{ID: "demo", Dir: dir}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	repos := &fixedLister{rows: []configprovider.Repo{
		{ID: "demo", Slug: "demo", Path: dir},
	}}
	bundle := &loaders.ProvidersBundle{Git: gitProv, Repos: repos}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 25
	thunks := make([]func() (*graphql1.Worktree, error), 0, N)
	for i := 0; i < N; i++ {
		thunks = append(thunks, l.WorktreeForCwd.Load(ctx, dir))
	}
	for i, thunk := range thunks {
		_, err := thunk()
		if err != nil {
			t.Fatalf("thunk %d error: %v", i, err)
		}
	}

	if got, want := l.WorktreeBatchCount(), 1; got != want {
		t.Fatalf("Worktree loader batched %d times, want %d", got, want)
	}
}

// fakePsRunner satisfies ps.CommandRunner; the test uses it to feed the
// adapter canned `ps` output instead of shelling out.
type fakePsRunner struct {
	header string
	lines  []string
}

func (f *fakePsRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ps" {
		return nil, fmt.Errorf("fake ps runner: unexpected command %q %v", name, args)
	}
	body := f.header + "\n" + strings.Join(f.lines, "\n") + "\n"
	return []byte(body), nil
}

// syntheticPsLines produces N rows of synthetic `ps` output with
// pids in [startPid, startPid+N). Used by the loader batch tests so
// the fake adapter has predictable keys to look up.
func syntheticPsLines(n, startPid int) []string {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		pid := startPid + i
		lines = append(lines, fmt.Sprintf("%d 1 alice ?? 0.1 1024 Sun May  4 10:00:00 2026 synthetic-%d", pid, i))
	}
	return lines
}

// staticIdentityReader / staticLoadReader satisfy the host provider's
// reader interfaces without OS calls.
type staticIdentityReader struct{}

func (staticIdentityReader) Read(_ context.Context) (hostprovider.Identity, error) {
	return hostprovider.Identity{
		MachineID: "test-machine",
		Hostname:  "test-host",
		OS:        "darwin",
	}, nil
}

type staticLoadReader struct{}

func (staticLoadReader) Read(_ context.Context) (hostprovider.Load, error) {
	return hostprovider.Load{
		CPUPercent:  10,
		MemPercent:  20,
		DiskPercent: 30,
		LoadAvg1m:   0.1,
		LoadAvg5m:   0.2,
		LoadAvg15m:  0.3,
	}, nil
}

// fixedLister implements configprovider.Lister with a static slice.
type fixedLister struct {
	mu   sync.Mutex
	rows []configprovider.Repo
}

func (f *fixedLister) List(_ context.Context) ([]configprovider.Repo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]configprovider.Repo, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

// --- PullRequestsForRepo loader tests ---

// TestPullRequestsForRepo_BatchesIntraRepo asserts that many concurrent
// loads for the same repo collapse into a single batch and one provider
// call.
func TestPullRequestsForRepo_BatchesIntraRepo(t *testing.T) {
	stub := &prStub{prs: map[string]int{}}
	stub.prs["owner/repo"] = 2 // 2 PRs for this repo

	bundle := &loaders.ProvidersBundle{GH: stub}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 8
	thunks := make([]func() ([]*graphql1.PullRequest, error), 0, N)
	for i := 0; i < N; i++ {
		thunks = append(thunks, l.PullRequestsForRepo.Load(ctx, loaders.RepoKey{Owner: "owner", Name: "repo"}))
	}
	for i, thunk := range thunks {
		got, err := thunk()
		if err != nil {
			t.Fatalf("thunk %d error: %v", i, err)
		}
		if len(got) != 2 {
			t.Fatalf("thunk %d returned %d PRs, want 2", i, len(got))
		}
	}

	if got := l.PullRequestsForRepoBatchCount(); got != 1 {
		t.Errorf("batch count = %d, want 1 (n+1 detection)", got)
	}
	if got := stub.CallCount(); got != 1 {
		t.Errorf("provider calls = %d, want 1", got)
	}
}

// TestPullRequestsForRepo_BatchesAcrossRepos asserts that concurrent
// loads for 5 distinct repos collapse into 1 batch and 5 provider calls
// (one per unique repo).
func TestPullRequestsForRepo_BatchesAcrossRepos(t *testing.T) {
	repos := []string{
		"org/alpha",
		"org/beta",
		"org/gamma",
		"org/delta",
		"org/epsilon",
	}
	stub := &prStub{prs: map[string]int{}}
	for _, r := range repos {
		stub.prs[r] = 1
	}

	bundle := &loaders.ProvidersBundle{GH: stub}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const perRepo = 8
	thunks := make([]func() ([]*graphql1.PullRequest, error), 0, len(repos)*perRepo)
	for _, slug := range repos {
		parts := strings.SplitN(slug, "/", 2)
		owner, name := parts[0], parts[1]
		for i := 0; i < perRepo; i++ {
			thunks = append(thunks, l.PullRequestsForRepo.Load(ctx, loaders.RepoKey{Owner: owner, Name: name}))
		}
	}
	for i, thunk := range thunks {
		if _, err := thunk(); err != nil {
			t.Fatalf("thunk %d error: %v", i, err)
		}
	}

	if got := l.PullRequestsForRepoBatchCount(); got != 1 {
		t.Errorf("batch count = %d, want 1 (all repos coalesced into one batch)", got)
	}
	if got := stub.CallCount(); got != len(repos) {
		t.Errorf("provider calls = %d, want %d (one per repo)", got, len(repos))
	}
}

// TestPullRequestsForRepo_PerRequestScoped asserts that two separate
// Loaders instances do not share state: each fires its own batch, so
// the provider receives two calls for the same repo.
func TestPullRequestsForRepo_PerRequestScoped(t *testing.T) {
	stub := &prStub{prs: map[string]int{"owner/repo": 1}}

	ctx := context.Background()

	// First "request"
	bundle1 := &loaders.ProvidersBundle{GH: stub}
	l1 := loaders.NewLoaders(bundle1)
	if _, err := l1.PullRequestsForRepo.Load(ctx, loaders.RepoKey{Owner: "owner", Name: "repo"})(); err != nil {
		t.Fatalf("l1 load error: %v", err)
	}

	// Second "request" — separate Loaders instance.
	bundle2 := &loaders.ProvidersBundle{GH: stub}
	l2 := loaders.NewLoaders(bundle2)
	if _, err := l2.PullRequestsForRepo.Load(ctx, loaders.RepoKey{Owner: "owner", Name: "repo"})(); err != nil {
		t.Fatalf("l2 load error: %v", err)
	}

	// Each loader should have batched exactly once.
	if got := l1.PullRequestsForRepoBatchCount(); got != 1 {
		t.Errorf("l1 batch count = %d, want 1", got)
	}
	if got := l2.PullRequestsForRepoBatchCount(); got != 1 {
		t.Errorf("l2 batch count = %d, want 1", got)
	}
	// Provider should have been called twice — once per loader.
	if got := stub.CallCount(); got != 2 {
		t.Errorf("provider calls = %d, want 2 (per-request scoping)", got)
	}
}

// prStub implements loaders.GHPullRequestLister with a call counter.
// prs maps "owner/name" to the number of dummy PRs to return.
type prStub struct {
	mu    sync.Mutex
	calls int
	prs   map[string]int // slug → PR count
}

func (s *prStub) ListPullRequests(_ context.Context, owner, name string, _ ghprovider.PullRequestState) ([]ghprovider.PullRequest, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	slug := owner + "/" + name
	n := s.prs[slug]
	out := make([]ghprovider.PullRequest, n)
	for i := range out {
		out[i] = ghprovider.PullRequest{
			RepoOwner: owner,
			RepoName:  name,
			Number:    100 + i,
			HeadRef:   fmt.Sprintf("feature/%s-%d", name, i),
			State:     ghprovider.PullRequestStateOpen,
		}
	}
	return out, nil
}

func (s *prStub) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}
