package peerproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFederationConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadFederationConfig(filepath.Join(dir, "no-such-file.json"))
	if err != nil {
		t.Fatalf("expected nil err for missing file, got %v", err)
	}
	if len(cfg.Peers) != 0 || cfg.PeerSecret != "" {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestLoadFederationConfig_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadFederationConfig(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(cfg.Peers) != 0 {
		t.Fatalf("expected no peers, got %v", cfg.Peers)
	}
}

func TestLoadFederationConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const body = `{
		"version": 1,
		"projects": [],
		"peers": [
			{"name": "peer-1", "address": "127.0.0.1:7777"},
			{"name": " ", "address": "ignored"},
			{"name": "peer-1", "address": "duplicate-dropped"},
			{"name": "peer-2", "address": "127.0.0.1:7778"}
		],
		"peer_secret": "  shhh  "
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadFederationConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PeerSecret != "shhh" {
		t.Fatalf("expected trimmed secret 'shhh', got %q", cfg.PeerSecret)
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("expected 2 peers after dedupe/blank-drop, got %v", cfg.Peers)
	}
	want := []PeerConfig{
		{Name: "peer-1", Address: "127.0.0.1:7777"},
		{Name: "peer-2", Address: "127.0.0.1:7778"},
	}
	for i, p := range cfg.Peers {
		if p != want[i] {
			t.Fatalf("peer[%d] = %+v, want %+v", i, p, want[i])
		}
	}
	if _, ok := cfg.PeerByName("peer-1"); !ok {
		t.Fatalf("peer-1 lookup failed")
	}
	if _, ok := cfg.PeerByName("peer-3"); ok {
		t.Fatalf("peer-3 lookup should have failed")
	}
}
