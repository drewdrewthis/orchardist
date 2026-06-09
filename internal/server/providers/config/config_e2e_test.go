package config_test

// E2E test for the config provider — no mocks. Spins the GraphQL
// daemon's HTTP handler in an httptest.Server with a real config
// JSONFileAdapter watching a real tempdir, runs real fsnotify, and
// performs real GraphQL roundtrips. The "modify the file → requery"
// step proves the watcher → resolver pipeline reflects edits without a
// mutation API.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server"
	configprovider "github.com/drewdrewthis/orchardist/internal/server/providers/config"
)

const e2eDeadline = 5 * time.Second

func writeConfigE2E(t *testing.T, path string, repos []configprovider.RepoRow) {
	t.Helper()
	f := configprovider.File{Version: 1, Repos: repos}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Atomic write — mirrors what `orchard config add-repo` does so the
	// fsnotify event semantics match production.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

// queryRepos issues `{ repos { id slug path } }` and returns the result
// rows sorted by id for deterministic assertions.
func queryRepos(t *testing.T, ts *httptest.Server) []map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": "{ repos { id slug path } }"})
	resp, err := http.Post(ts.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Repos []map[string]any `json:"repos"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("graphql errors: %+v", env.Errors)
	}
	out := env.Data.Repos
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

func TestConfigProvider_E2E_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	writeConfigE2E(t, cfgPath, []configprovider.RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
		{Slug: "team/beta", Path: "/abs/beta"},
	})

	adapter := configprovider.NewJSONFileAdapter(cfgPath, nil)
	provider := configprovider.NewProvider(adapter, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider start: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", nil, server.WithRepos(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	got := queryRepos(t, ts)
	if len(got) != 2 {
		t.Fatalf("want 2 repos, got %d (%+v)", len(got), got)
	}
	if got[0]["id"] != "team/alpha" || got[0]["path"] != "/abs/alpha" || got[0]["slug"] != "team/alpha" {
		t.Errorf("alpha row wrong: %+v", got[0])
	}
	if got[1]["id"] != "team/beta" {
		t.Errorf("beta row wrong: %+v", got[1])
	}

	// Append gamma. fsnotify should reload, GraphQL should report three.
	writeConfigE2E(t, cfgPath, []configprovider.RepoRow{
		{Slug: "team/alpha", Path: "/abs/alpha"},
		{Slug: "team/beta", Path: "/abs/beta"},
		{Slug: "team/gamma", Path: "/abs/gamma"},
	})

	deadline := time.Now().Add(e2eDeadline)
	for {
		got = queryRepos(t, ts)
		if len(got) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 3 repos after fsnotify roundtrip, got %d (%+v)", len(got), got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got[0]["id"] != "team/alpha" || got[1]["id"] != "team/beta" || got[2]["id"] != "team/gamma" {
		t.Errorf("post-modify ordering wrong: %+v", got)
	}
	if got[2]["path"] != "/abs/gamma" {
		t.Errorf("gamma row wrong: %+v", got[2])
	}

	// Remove alpha — two remain.
	writeConfigE2E(t, cfgPath, []configprovider.RepoRow{
		{Slug: "team/beta", Path: "/abs/beta"},
		{Slug: "team/gamma", Path: "/abs/gamma"},
	})
	deadline = time.Now().Add(e2eDeadline)
	for {
		got = queryRepos(t, ts)
		if len(got) == 2 && got[0]["id"] == "team/beta" && got[1]["id"] == "team/gamma" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected alpha removal; got %+v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestConfigProvider_E2E_ColdBootBeforeFile(t *testing.T) {
	// Daemon starts with no config file — fsnotify must fire when the
	// file appears. Verifies the parent-directory-watch contract.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	adapter := configprovider.NewJSONFileAdapter(cfgPath, nil)
	provider := configprovider.NewProvider(adapter, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	srv := server.New("", nil, server.WithRepos(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	got := queryRepos(t, ts)
	if len(got) != 0 {
		t.Fatalf("want empty before file, got %+v", got)
	}

	writeConfigE2E(t, cfgPath, []configprovider.RepoRow{
		{Slug: "team/first", Path: "/abs/first"},
	})

	deadline := time.Now().Add(e2eDeadline)
	for {
		got = queryRepos(t, ts)
		if len(got) == 1 && got[0]["id"] == "team/first" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected first repo after file appears, got %+v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
