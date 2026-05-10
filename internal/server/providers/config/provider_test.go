package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// fsnotifySettleTimeout is how long tests wait for an fsnotify event to
// propagate. macOS in particular is sometimes laggy; one second is more
// than enough on every platform we run on without making failures slow.
const fsnotifySettleTimeout = 2 * time.Second

func writeConfig(t *testing.T, path string, repos []RepoRow) {
	t.Helper()
	f := File{Version: 1, Repos: repos}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func newProviderForTest(t *testing.T, dir string) (*Provider, string) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.json")
	a := NewJSONFileAdapter(cfgPath, nil)
	p := NewProvider(a, nil)
	t.Cleanup(func() { _ = p.Stop() })
	return p, cfgPath
}

func TestProvider_ColdBoot_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p, _ := newProviderForTest(t, dir)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

// TestProvider_FetchAll_NormalisesRows verifies that ToRepo() yields
// the canonical {ID derived from Slug, Path verbatim} shape per ADR-015.
func TestProvider_FetchAll_NormalisesRows(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "drewdrewthis/git-orchard-rs", Path: "/abs/path/to/orchard"},
		{Slug: "langwatch/scenario", Path: "/abs/scenario"},
		{Slug: "", Path: "/abs/no-slug"}, // slug derived from path basename
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d (%v)", len(got), got)
	}

	byID := map[RepoID]Repo{}
	for _, r := range got {
		byID[r.ID] = r
	}
	if r, ok := byID["drewdrewthis/git-orchard-rs"]; !ok || r.Path != "/abs/path/to/orchard" {
		t.Errorf("expected slug 'drewdrewthis/git-orchard-rs' with /abs/path/to/orchard, got %+v", r)
	}
	if r, ok := byID["langwatch/scenario"]; !ok || r.Path != "/abs/scenario" {
		t.Errorf("expected slug 'langwatch/scenario', got %+v", r)
	}
	if r, ok := byID["no-slug"]; !ok || r.Path != "/abs/no-slug" {
		t.Errorf("expected slug 'no-slug' derived from path basename, got %+v in %+v", r, byID)
	}
}

func TestProvider_GetMany_CoalescesDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
		{Slug: "team/beta", Path: "/abs/beta"},
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	keys := []RepoID{"team/alpha", "team/alpha", "team/beta", "team/alpha"}
	out, fresh, err := p.GetMany(context.Background(), keys)
	if err != nil {
		t.Fatalf("getmany: %v", err)
	}
	if len(out) != 2 || len(fresh) != 2 {
		t.Fatalf("expected coalesced 2 entries, got %d/%d", len(out), len(fresh))
	}
	if out["team/alpha"].Path != "/abs/alpha" {
		t.Errorf("alpha path wrong: %+v", out["team/alpha"])
	}
	if out["team/beta"].DisplayName() != "beta" {
		t.Errorf("beta display name wrong: %+v", out["team/beta"])
	}
}

func TestProvider_FsnotifyReload(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	sub := p.Subscribe(ctx)

	// Sanity — initial state.
	keys, _ := p.Keys(ctx)
	if len(keys) != 1 {
		t.Fatalf("expected 1 cold-load key, got %v", keys)
	}

	// Modify the file with a second repo; fsnotify should reload.
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
		{Slug: "team/beta", Path: "/abs/beta"},
	})

	deadline := time.After(fsnotifySettleTimeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("fsnotify reload timed out; cache=%v", mustList(t, p))
		case ev := <-sub:
			t.Logf("invalidation: %s reason=%s", ev.Key, ev.Reason)
			ks, _ := p.Keys(ctx)
			sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
			if len(ks) == 2 && ks[0] == "team/alpha" && ks[1] == "team/beta" {
				return
			}
		}
	}
}

func TestProvider_FsnotifyRemoveRepo(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
		{Slug: "team/beta", Path: "/abs/beta"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	sub := p.Subscribe(ctx)

	// Drop beta from disk.
	writeConfig(t, cfgPath, []RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
	})

	deadline := time.After(fsnotifySettleTimeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("expected beta removal; cache=%v", mustList(t, p))
		case <-sub:
			ks, _ := p.Keys(ctx)
			if len(ks) == 1 && ks[0] == "team/alpha" {
				return
			}
		}
	}
}

func TestSlug_HandlesPunctuation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello, World!", "hello-world"},
		{"  Foo  Bar  ", "foo-bar"},
		{"snake_case", "snake-case"},
		{"git-orchard-rs", "git-orchard-rs"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := slug(c.in); got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugOrHash_FallsBackOnNonAscii(t *testing.T) {
	got := slugOrHash("日本語", "/abs/path")
	if len(got) != 12 {
		t.Errorf("expected 12-char hash, got %q", got)
	}
}

// TestRepo_DisplayName covers the basename + slug-suffix derivation.
func TestRepo_DisplayName(t *testing.T) {
	cases := []struct {
		in   Repo
		want string
	}{
		{Repo{Slug: "drewdrewthis/git-orchard-rs", Path: "/Users/x/git-orchard-rs"}, "git-orchard-rs"},
		{Repo{Slug: "langwatch/scenario", Path: ""}, "scenario"},
		{Repo{Slug: "lone", Path: "/"}, "lone"},
	}
	for _, c := range cases {
		if got := c.in.DisplayName(); got != c.want {
			t.Errorf("(%+v).DisplayName() = %q, want %q", c.in, got, c.want)
		}
	}
}

func mustList(t *testing.T, p *Provider) []Repo {
	t.Helper()
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return got
}
