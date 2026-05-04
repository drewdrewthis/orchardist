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

	"github.com/drewdrewthis/git-orchard-rs/internal/server"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
)

const e2eDeadline = 5 * time.Second

func writeConfigE2E(t *testing.T, path string, projects []configprovider.ProjectRow) {
	t.Helper()
	f := configprovider.File{Version: 1, Projects: projects}
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

func gqlQuery(t *testing.T, ts *httptest.Server, doc string) []map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": doc})
	resp, err := http.Post(ts.URL+"/graphql", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Projects []map[string]any `json:"projects"`
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
	out := env.Data.Projects
	sort.Slice(out, func(i, j int) bool { return out[i]["id"].(string) < out[j]["id"].(string) })
	return out
}

func TestConfigProvider_E2E_RoundTrip(t *testing.T) {
	// Real tempdir, real config file, real fsnotify, real GraphQL
	// server — no mocks. This is the integration contract for the
	// provider end of the feature.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Seed with two projects.
	writeConfigE2E(t, cfgPath, []configprovider.ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
	})

	// Build the provider stack the daemon would build.
	adapter := configprovider.NewJSONFileAdapter(cfgPath, nil)
	provider := configprovider.NewProvider(adapter, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider start: %v", err)
	}
	t.Cleanup(func() { _ = provider.Stop() })

	// Spin the GraphQL HTTP handler in httptest.Server.
	srv := server.New("", nil, server.WithProjects(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	// Initial state: both projects present.
	got := gqlQuery(t, ts, `{ projects { id directory name } }`)
	if len(got) != 2 {
		t.Fatalf("want 2 projects, got %d (%+v)", len(got), got)
	}
	if got[0]["id"] != "alpha" || got[0]["directory"] != "/abs/alpha" || got[0]["name"] != "Alpha" {
		t.Errorf("alpha row wrong: %+v", got[0])
	}
	if got[1]["id"] != "beta" {
		t.Errorf("beta row wrong: %+v", got[1])
	}

	// Modify config — append gamma. fsnotify should reload, GraphQL
	// should report three on next query. Poll for up to e2eDeadline
	// because fsnotify timing varies across platforms.
	writeConfigE2E(t, cfgPath, []configprovider.ProjectRow{
		{ID: "alpha", Directory: "/abs/alpha", Name: "Alpha"},
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
		{ID: "gamma", Directory: "/abs/gamma", Name: "Gamma"},
	})

	deadline := time.Now().Add(e2eDeadline)
	for {
		got = gqlQuery(t, ts, `{ projects { id directory name } }`)
		if len(got) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 3 projects after fsnotify roundtrip, got %d (%+v)", len(got), got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got[0]["id"] != "alpha" || got[1]["id"] != "beta" || got[2]["id"] != "gamma" {
		t.Errorf("post-modify ordering wrong: %+v", got)
	}
	if got[2]["directory"] != "/abs/gamma" || got[2]["name"] != "Gamma" {
		t.Errorf("gamma row wrong: %+v", got[2])
	}

	// And remove a project — alpha goes away, two remain.
	writeConfigE2E(t, cfgPath, []configprovider.ProjectRow{
		{ID: "beta", Directory: "/abs/beta", Name: "Beta"},
		{ID: "gamma", Directory: "/abs/gamma", Name: "Gamma"},
	})
	deadline = time.Now().Add(e2eDeadline)
	for {
		got = gqlQuery(t, ts, `{ projects { id directory name } }`)
		if len(got) == 2 && got[0]["id"] == "beta" && got[1]["id"] == "gamma" {
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

	srv := server.New("", nil, server.WithProjects(provider))
	ts := httptest.NewServer(srv.HTTPHandler())
	t.Cleanup(ts.Close)

	got := gqlQuery(t, ts, `{ projects { id } }`)
	if len(got) != 0 {
		t.Fatalf("want empty before file, got %+v", got)
	}

	writeConfigE2E(t, cfgPath, []configprovider.ProjectRow{
		{ID: "first", Directory: "/abs/first", Name: "First"},
	})

	deadline := time.Now().Add(e2eDeadline)
	for {
		got = gqlQuery(t, ts, `{ projects { id } }`)
		if len(got) == 1 && got[0]["id"] == "first" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected first project after file appears, got %+v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
