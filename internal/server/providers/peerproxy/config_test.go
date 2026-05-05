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
	if len(cfg.Peers) != 0 {
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
		"peer_secret": "  legacy-ignored  "
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadFederationConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// `peer_secret` is a removed field (see issue #412); LoadFederationConfig
	// must tolerate it in legacy configs without erroring.
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

// TestLoadFederationConfig_TLSAndSchemes covers AC-1 (explicit TLS
// flag preserved) and AC-4 (scheme prefixes implicitly set TLS and are
// stripped from the stored Address).
func TestLoadFederationConfig_TLSAndSchemes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const body = `{
		"peers": [
			{"name": "plain",    "address": "127.0.0.1:7777"},
			{"name": "explicit", "address": "10.0.0.1:7777", "tls": true},
			{"name": "https",    "address": "https://graphql.peer.example/"},
			{"name": "wss",      "address": "wss://graphql.peer.example"},
			{"name": "http",     "address": "http://localhost:8080/graphql"},
			{"name": "mixed",    "address": "https://peer.example", "tls": false}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadFederationConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []PeerConfig{
		{Name: "plain", Address: "127.0.0.1:7777"},
		{Name: "explicit", Address: "10.0.0.1:7777", TLS: true},
		{Name: "https", Address: "graphql.peer.example", TLS: true},
		{Name: "wss", Address: "graphql.peer.example", TLS: true},
		{Name: "http", Address: "localhost:8080"},
		// `tls=false` in JSON does not undo the implicit `https://` flip:
		// scheme prefix wins. Users who paste a URL get TLS regardless.
		{Name: "mixed", Address: "peer.example", TLS: true},
	}
	if len(cfg.Peers) != len(want) {
		t.Fatalf("got %d peers, want %d: %+v", len(cfg.Peers), len(want), cfg.Peers)
	}
	for i, p := range cfg.Peers {
		if p != want[i] {
			t.Fatalf("peer[%d] = %+v, want %+v", i, p, want[i])
		}
	}
}

// TestSplitAddress isolates the URL-stripping helper used by normalise
// so AC-4 has a focused regression net.
func TestSplitAddress(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantTLS  bool
	}{
		{"127.0.0.1:7777", "127.0.0.1:7777", false},
		{"localhost", "localhost", false},
		{"http://localhost:8080", "localhost:8080", false},
		{"http://localhost:8080/graphql", "localhost:8080", false},
		{"ws://peer.example", "peer.example", false},
		{"https://graphql.peer.example", "graphql.peer.example", true},
		{"HTTPS://graphql.peer.example", "graphql.peer.example", true},
		{"wss://graphql.peer.example/graphql", "graphql.peer.example", true},
		{"https://graphql.peer.example/", "graphql.peer.example", true},
	}
	for _, tc := range cases {
		gotHost, gotTLS := splitAddress(tc.in)
		if gotHost != tc.wantHost || gotTLS != tc.wantTLS {
			t.Errorf("splitAddress(%q) = (%q, %v); want (%q, %v)",
				tc.in, gotHost, gotTLS, tc.wantHost, tc.wantTLS)
		}
	}
}
