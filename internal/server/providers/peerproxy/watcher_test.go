package peerproxy_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// TestConfigWatcher_EditTriggersReload verifies that writing a new config
// to the watched path causes the Provider's peer set to be updated within
// the 2-second window required by the feature scenario.
//
// Steps:
//  1. Start two fake peers (orchard.boxd.sh, lw-fed-c).
//  2. Write an initial config containing only "orchard.boxd.sh".
//  3. Construct Provider from that config; Start it.
//  4. Construct ConfigWatcher pointing at the config file; Start it.
//  5. Wait for the initial peer to begin probing.
//  6. Snapshot Peers() length (== 1) and SpawnCount("orchard.boxd.sh") (== 1).
//  7. Atomically write a new config with both peers.
//  8. Poll up to 2 seconds for Peers() to grow to 2.
//  9. Assert both peers are present.
// 10. Assert SpawnCount("orchard.boxd.sh") is still 1 (not restarted).
// 11. Assert SpawnCount("lw-fed-c") is 1 (started exactly once).
func TestConfigWatcher_EditTriggersReload(t *testing.T) {
	// 1. Fake peers for the two addresses.
	fakeBoxd := newFakePeer(t)
	fakeFedC := newFakePeer(t)

	// 2. Write the initial config — only orchard.boxd.sh.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	writeConfig(t, cfgPath, []peerproxy.PeerConfig{
		{Name: "orchard.boxd.sh", Address: fakeBoxd.addr(), TLS: false},
	})

	// 3. Construct provider from the initial config.
	initialCfg := loadConfig(t, cfgPath)
	p := peerproxy.NewProvider(initialCfg, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Provider.Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })

	// 4. Construct and start the ConfigWatcher.
	cw := peerproxy.NewConfigWatcher(cfgPath, p, slog.Default())
	if err := cw.Start(ctx); err != nil {
		t.Fatalf("ConfigWatcher.Start: %v", err)
	}
	t.Cleanup(func() { _ = cw.Close() })

	// 5. Wait for the initial peer to begin probing (confirms goroutine is live).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fakeBoxd.pingCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fakeBoxd.pingCount.Load() < 1 {
		t.Fatalf("orchard.boxd.sh probe goroutine did not issue a Ping within 500ms")
	}

	// 6. Snapshot the initial state.
	if got := len(p.Peers()); got != 1 {
		t.Fatalf("initial Peers() len = %d, want 1", got)
	}
	initialBoxdSpawnCount := p.SpawnCount("orchard.boxd.sh")
	if initialBoxdSpawnCount != 1 {
		t.Fatalf("initial SpawnCount(orchard.boxd.sh) = %d, want 1", initialBoxdSpawnCount)
	}

	// 7. Atomically write the new config with both peers.
	// Use write-then-rename for atomicity (same pattern as `orchard config add-peer`).
	writeConfigAtomic(t, cfgPath, []peerproxy.PeerConfig{
		{Name: "orchard.boxd.sh", Address: fakeBoxd.addr(), TLS: false},
		{Name: "lw-fed-c", Address: fakeFedC.addr(), TLS: false},
	})

	// 8. Poll up to 2 seconds for Peers() to grow to 2.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.Peers()) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 9. Assert both peers are present.
	peers := p.Peers()
	if len(peers) != 2 {
		t.Fatalf("Peers() len = %d after config edit, want 2; peers=%v", len(peers), peerNames(peers))
	}
	peerSet := make(map[string]bool, len(peers))
	for _, peer := range peers {
		peerSet[peer.Name] = true
	}
	if !peerSet["orchard.boxd.sh"] {
		t.Errorf("Peers() missing %q; got %v", "orchard.boxd.sh", peerNames(peers))
	}
	if !peerSet["lw-fed-c"] {
		t.Errorf("Peers() missing %q; got %v", "lw-fed-c", peerNames(peers))
	}

	// 10. orchard.boxd.sh must NOT have been restarted — SpawnCount unchanged.
	if got := p.SpawnCount("orchard.boxd.sh"); got != initialBoxdSpawnCount {
		t.Fatalf("SpawnCount(orchard.boxd.sh) = %d after reload, want %d (peer was restarted unexpectedly)",
			got, initialBoxdSpawnCount)
	}

	// 11. lw-fed-c was newly added — SpawnCount must be exactly 1.
	if got := p.SpawnCount("lw-fed-c"); got != 1 {
		t.Fatalf("SpawnCount(lw-fed-c) = %d after reload, want 1", got)
	}
}

// writeConfig serialises peers into the federation config format and writes
// it directly to path. Use writeConfigAtomic for production-style atomicity.
func writeConfig(t *testing.T, path string, peers []peerproxy.PeerConfig) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"peers": peers})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
}

// writeConfigAtomic writes the config to a temp file then renames it into
// place, mimicking the atomic-rename approach used by `orchard config add-peer`
// and most editors.
func writeConfigAtomic(t *testing.T, path string, peers []peerproxy.PeerConfig) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"peers": peers})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatalf("write tmp config %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename config %s → %s: %v", tmp, path, err)
	}
}

// loadConfig reads and parses the federation config at path for test setup.
func loadConfig(t *testing.T, path string) peerproxy.FederationConfig {
	t.Helper()
	cfg, err := peerproxy.LoadFederationConfig(path)
	if err != nil {
		t.Fatalf("LoadFederationConfig(%s): %v", path, err)
	}
	return cfg
}
