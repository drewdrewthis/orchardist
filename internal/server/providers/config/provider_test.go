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

func writeConfig(t *testing.T, path string, projects []ProjectRow) {
	t.Helper()
	f := File{Version: 1, Projects: projects}
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

func TestProvider_FetchAll_NormalisesRows(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []ProjectRow{
		{Directory: "/abs/path/to/orchard"},                                 // id+name from directory
		{Directory: "/abs/foo", Name: "Foo Project"},                        // id from name slug
		{ID: "explicit", Directory: "/abs/bar", Name: "Custom"},             // all explicit
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

	byID := map[ProjectID]Project{}
	for _, p := range got {
		byID[p.ID] = p
	}
	if p, ok := byID["orchard"]; !ok || p.Name != "orchard" {
		t.Errorf("expected slug 'orchard' with name 'orchard', got %+v", p)
	}
	if p, ok := byID["foo-project"]; !ok || p.Directory != "/abs/foo" {
		t.Errorf("expected slug 'foo-project' for /abs/foo, got %+v", byID)
	}
	if p, ok := byID["explicit"]; !ok || p.Name != "Custom" {
		t.Errorf("expected explicit id 'explicit' with name 'Custom', got %+v", p)
	}
}

func TestProvider_GetMany_CoalescesDuplicateKeys(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	keys := []ProjectID{"alpha", "alpha", "beta", "alpha"}
	out, fresh, err := p.GetMany(context.Background(), keys)
	if err != nil {
		t.Fatalf("getmany: %v", err)
	}
	if len(out) != 2 || len(fresh) != 2 {
		t.Fatalf("expected coalesced 2 entries, got %d/%d", len(out), len(fresh))
	}
	if out["alpha"].Directory != "/abs/alpha" {
		t.Errorf("alpha dir wrong: %+v", out["alpha"])
	}
	if out["beta"].Name != "Beta" {
		t.Errorf("beta name wrong: %+v", out["beta"])
	}
}

func TestProvider_FsnotifyReload(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
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

	// Modify the file with a second project; fsnotify should reload.
	writeConfig(t, cfgPath, []ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
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
			if len(ks) == 2 && ks[0] == "alpha" && ks[1] == "beta" {
				return
			}
		}
	}
}

func TestProvider_FsnotifyRemoveProject(t *testing.T) {
	dir := t.TempDir()
	p, cfgPath := newProviderForTest(t, dir)
	writeConfig(t, cfgPath, []ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	sub := p.Subscribe(ctx)

	// Drop beta from disk.
	writeConfig(t, cfgPath, []ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
	})

	deadline := time.After(fsnotifySettleTimeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("expected beta removal; cache=%v", mustList(t, p))
		case <-sub:
			ks, _ := p.Keys(ctx)
			if len(ks) == 1 && ks[0] == "alpha" {
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

func mustList(t *testing.T, p *Provider) []Project {
	t.Helper()
	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return got
}
