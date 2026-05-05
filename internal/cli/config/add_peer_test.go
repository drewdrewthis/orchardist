package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// readPeeredConfig is a test helper — round-trip the on-disk JSON back
// into peeredFile so assertions can read both projects and peer fields.
func readPeeredConfig(t *testing.T, dir string) peeredFile {
	t.Helper()
	cfgPath := filepath.Join(dir, "config", "orchard", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var f peeredFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return f
}

func TestAddPeer_EmptyConfig_AllFlags(t *testing.T) {
	dir := setHomeForTest(t)
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "10.0.0.1:7777", true); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readPeeredConfig(t, dir)
	if len(cfg.Peers) != 1 {
		t.Fatalf("want 1 peer, got %d", len(cfg.Peers))
	}
	want := peerproxy.PeerConfig{Name: "peer-1", Address: "10.0.0.1:7777", TLS: true}
	if cfg.Peers[0] != want {
		t.Errorf("peer mismatch: got %+v, want %+v", cfg.Peers[0], want)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
}

func TestAddPeer_RequiresName(t *testing.T) {
	setHomeForTest(t)
	var buf bytes.Buffer
	err := runAddPeer(&buf, "", "10.0.0.1:7777", false)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected name-required error, got %v", err)
	}
}

func TestAddPeer_RequiresAddress(t *testing.T) {
	setHomeForTest(t)
	var buf bytes.Buffer
	err := runAddPeer(&buf, "peer-1", "", false)
	if err == nil || !strings.Contains(err.Error(), "address") {
		t.Fatalf("expected address-required error, got %v", err)
	}
}

func TestAddPeer_DuplicateNameFails(t *testing.T) {
	setHomeForTest(t)
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "10.0.0.1:7777", false); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := runAddPeer(&buf, "peer-1", "10.0.0.2:7778", false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestAddPeer_HTTPSAddressFlipsTLS(t *testing.T) {
	dir := setHomeForTest(t)
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "https://graphql.peer.example", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readPeeredConfig(t, dir)
	if len(cfg.Peers) != 1 {
		t.Fatalf("want 1 peer, got %d", len(cfg.Peers))
	}
	got := cfg.Peers[0]
	if got.Address != "graphql.peer.example" {
		t.Errorf("address = %q, want stripped host", got.Address)
	}
	if !got.TLS {
		t.Errorf("expected TLS=true after https:// prefix")
	}
}

// Verify that legacy `peer_secret` keys in pre-#412 configs round-trip
// through the Extras catch-all instead of being silently dropped.
func TestAddPeer_PreservesLegacyPeerSecret(t *testing.T) {
	dir := setHomeForTest(t)
	cfgPath := filepath.Join(dir, "config", "orchard", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{"version": 1, "peer_secret": "legacy-shhh"}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "10.0.0.1:7777", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if raw["peer_secret"] != "legacy-shhh" {
		t.Errorf("legacy peer_secret dropped: %v", raw["peer_secret"])
	}
}

func TestAddPeer_PreservesExistingProjects(t *testing.T) {
	dir := setHomeForTest(t)
	cfgPath := filepath.Join(dir, "config", "orchard", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{
		"version": 1,
		"projects": [
			{"id": "alpha", "directory": "/tmp/alpha", "name": "Alpha"}
		]
	}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "10.0.0.1:7777", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	projects, ok := raw["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("projects clobbered: %v", raw["projects"])
	}
	peers, ok := raw["peers"].([]any)
	if !ok || len(peers) != 1 {
		t.Fatalf("peer not written: %v", raw["peers"])
	}
}
