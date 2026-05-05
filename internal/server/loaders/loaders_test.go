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

	projects := &fixedLister{rows: []configprovider.Project{
		{ID: "demo", Directory: dir, Name: "demo"},
	}}
	bundle := &loaders.ProvidersBundle{Git: gitProv, Projects: projects}
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
	rows []configprovider.Project
}

func (f *fixedLister) List(_ context.Context) ([]configprovider.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]configprovider.Project, len(f.rows))
	copy(out, f.rows)
	return out, nil
}
