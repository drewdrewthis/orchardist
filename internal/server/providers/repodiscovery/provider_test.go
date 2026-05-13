package repodiscovery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

// silentLogger discards all log output so tests stay quiet.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeConfigLister struct {
	repos []config.Repo
	err   error
}

func (f *fakeConfigLister) List(_ context.Context) ([]config.Repo, error) {
	return f.repos, f.err
}

type fakeExcludedFetcher struct {
	paths []string
	err   error
}

func (f *fakeExcludedFetcher) Excluded(_ context.Context) ([]string, error) {
	return f.paths, f.err
}

type fakeSource struct {
	roots []string
	err   error
	calls int32
}

func (f *fakeSource) Roots(_ context.Context) ([]string, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.roots, f.err
}

// mkRepo creates dir + dir/.git and returns the symlink-resolved path.
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkrepo: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	return resolved
}

func TestProvider_List_ConfiguredOnly(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "myrepo"))

	p := NewProvider(
		&fakeConfigLister{repos: []config.Repo{
			{ID: "myrepo", Slug: "myrepo", Path: repo},
		}},
		nil,
		silentLogger(),
	)

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d repos, want 1", len(got))
	}
	if got[0].Slug != "myrepo" {
		t.Errorf("got slug %q, want myrepo", got[0].Slug)
	}
}

func TestProvider_List_UnionWithDiscovered(t *testing.T) {
	tmp := t.TempDir()
	configured := mkRepo(t, filepath.Join(tmp, "configured"))
	discovered := mkRepo(t, filepath.Join(tmp, "discovered"))

	p := NewProvider(
		&fakeConfigLister{repos: []config.Repo{
			{ID: "configured", Slug: "configured", Path: configured},
		}},
		nil,
		silentLogger(),
	).AddSource("fake", &fakeSource{roots: []string{discovered}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d repos, want 2: %+v", len(got), got)
	}
	slugs := map[string]config.Repo{}
	for _, r := range got {
		slugs[r.Slug] = r
	}
	if _, ok := slugs["configured"]; !ok {
		t.Errorf("missing configured")
	}
	if r, ok := slugs["discovered"]; !ok || r.Path != discovered {
		t.Errorf("missing discovered (path=%v)", r)
	}
}

func TestProvider_List_DedupesAcrossSources(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "shared"))

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).
		AddSource("tmux", &fakeSource{roots: []string{repo, repo}}).
		AddSource("claude", &fakeSource{roots: []string{repo}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 deduped repo: %+v", len(got), got)
	}
}

func TestProvider_List_DedupesViaSymlink(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "real"))
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).
		AddSource("by-real", &fakeSource{roots: []string{repo}}).
		AddSource("by-link", &fakeSource{roots: []string{link}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("symlink-aliased paths not deduped: %+v", got)
	}
}

func TestProvider_List_ConfiguredWinsOnCollision(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "myrepo"))

	// Configured pinned a custom slug "renamed" for this path.
	p := NewProvider(
		&fakeConfigLister{repos: []config.Repo{
			{ID: "renamed", Slug: "renamed", Path: repo},
		}},
		nil,
		silentLogger(),
	).AddSource("tmux", &fakeSource{roots: []string{repo}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].Slug != "renamed" {
		t.Errorf("collision: got slug %q, want %q (configured wins)", got[0].Slug, "renamed")
	}
}

func TestProvider_List_DropsPhantomConfigured(t *testing.T) {
	tmp := t.TempDir()
	real := mkRepo(t, filepath.Join(tmp, "real"))

	// A configured row pointing at a missing path — phantom.
	phantom := filepath.Join(tmp, "ghost", "TestAddRepo_X")

	p := NewProvider(
		&fakeConfigLister{repos: []config.Repo{
			{ID: "real", Slug: "real", Path: real},
			{ID: "002", Slug: "002", Path: phantom},
		}},
		nil,
		silentLogger(),
	)

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("phantom not dropped: %+v", got)
	}
	if got[0].Slug != "real" {
		t.Errorf("kept wrong row: %+v", got)
	}
}

func TestProvider_List_DropsExcluded(t *testing.T) {
	tmp := t.TempDir()
	keep := mkRepo(t, filepath.Join(tmp, "keep"))
	skip := mkRepo(t, filepath.Join(tmp, "skip"))

	p := NewProvider(
		&fakeConfigLister{},
		&fakeExcludedFetcher{paths: []string{skip}},
		silentLogger(),
	).AddSource("tmux", &fakeSource{roots: []string{keep, skip}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Path != keep {
		t.Errorf("exclude failed: %+v", got)
	}
}

func TestProvider_List_DiscoveryWalksToRepoRoot(t *testing.T) {
	// The tmux source feeds raw pane CWDs (which are often subdirs
	// inside the repo); the Provider must walk up to the .git parent.
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "repo"))
	sub := filepath.Join(repo, "src", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).AddSource("tmux", &fakeSource{roots: []string{sub}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].Path != repo {
		t.Errorf("got path %q, want %q", got[0].Path, repo)
	}
}

func TestProvider_List_SlugCollisionDisambiguatesByParent(t *testing.T) {
	tmp := t.TempDir()
	// Two repos with the same basename in different parent dirs.
	a := mkRepo(t, filepath.Join(tmp, "owner-a", "myrepo"))
	b := mkRepo(t, filepath.Join(tmp, "owner-b", "myrepo"))

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).AddSource("tmux", &fakeSource{roots: []string{a, b}})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	// One should get "myrepo", the other "myrepo-<parent>".
	slugs := map[string]bool{}
	for _, r := range got {
		slugs[r.Slug] = true
	}
	if !slugs["myrepo"] {
		t.Errorf("missing plain myrepo slug: %+v", got)
	}
	// Either "myrepo-owner-a" or "myrepo-owner-b" should be present.
	if !slugs["myrepo-owner-a"] && !slugs["myrepo-owner-b"] {
		t.Errorf("missing parent-suffixed slug: %+v", got)
	}
}

func TestProvider_List_CachesUntilTTLExpires(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "repo"))

	src := &fakeSource{roots: []string{repo}}
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).
		AddSource("tmux", src).
		WithTTL(10 * time.Second).
		WithClock(clock)

	if _, err := p.List(context.Background()); err != nil {
		t.Fatalf("List 1: %v", err)
	}
	if _, err := p.List(context.Background()); err != nil {
		t.Fatalf("List 2: %v", err)
	}
	if got := atomic.LoadInt32(&src.calls); got != 1 {
		t.Errorf("expected one source call within TTL, got %d", got)
	}

	now = now.Add(11 * time.Second)
	if _, err := p.List(context.Background()); err != nil {
		t.Fatalf("List 3: %v", err)
	}
	if got := atomic.LoadInt32(&src.calls); got != 2 {
		t.Errorf("expected one source call after TTL expiry, got %d total", got)
	}
}

func TestProvider_List_SourceErrorDoesNotKillUnion(t *testing.T) {
	tmp := t.TempDir()
	repo := mkRepo(t, filepath.Join(tmp, "repo"))

	p := NewProvider(
		&fakeConfigLister{repos: []config.Repo{{ID: "cfg", Slug: "cfg", Path: repo}}},
		nil,
		silentLogger(),
	).
		AddSource("broken", &fakeSource{err: errors.New("boom")}).
		AddSource("ok", &fakeSource{roots: nil})

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "cfg" {
		t.Errorf("got %+v, want one configured row", got)
	}
}

func TestProvider_List_RestoresFreshCacheOnNextRefresh(t *testing.T) {
	// Demonstrates the TTL boundary: the same call after TTL expiry
	// observes a *new* set of roots from the source.
	tmp := t.TempDir()
	first := mkRepo(t, filepath.Join(tmp, "first"))
	second := mkRepo(t, filepath.Join(tmp, "second"))

	var rootsToReturn atomic.Value
	rootsToReturn.Store([]string{first})

	src := &delegatingSource{provider: func() []string {
		return rootsToReturn.Load().([]string)
	}}

	now := time.Unix(0, 0)
	clock := func() time.Time { return now }

	p := NewProvider(
		&fakeConfigLister{},
		nil,
		silentLogger(),
	).AddSource("tmux", src).WithTTL(time.Second).WithClock(clock)

	got, err := p.List(context.Background())
	if err != nil || len(got) != 1 || got[0].Path != first {
		t.Fatalf("first refresh: got %+v, err %v", got, err)
	}

	rootsToReturn.Store([]string{first, second})
	now = now.Add(2 * time.Second)

	got, err = p.List(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("post-TTL refresh: got %+v, err %v", got, err)
	}
}

type delegatingSource struct {
	provider func() []string
}

func (d *delegatingSource) Roots(_ context.Context) ([]string, error) {
	return d.provider(), nil
}
