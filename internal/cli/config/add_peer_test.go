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

// TestAddPeerCmd_HelpMentionsPrerequisite asserts that the Long help text of
// `orchard config add-peer` names both prerequisites an operator must satisfy
// before adding a peer VM: the boxd proxy command and the orchard-daemon requirement.
func TestAddPeerCmd_HelpMentionsPrerequisite(t *testing.T) {
	cmd := addPeerCmd()
	long := cmd.Long
	for _, want := range []string{
		"boxd proxy new graphql --vm",
		"orchard-daemon",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("add-peer Long help missing prerequisite string %q", want)
		}
	}
}

// TestAdrMentionsPrerequisite guards ADR-021 against having the prerequisite
// strings accidentally removed. If someone deletes these from the ADR, CI fails.
func TestAdrMentionsPrerequisite(t *testing.T) {
	// internal/cli/config/ is three levels below the repo root.
	adrPath := filepath.Join("..", "..", "..", "docs", "adr", "021-federation-peers-hot-reload.md")
	data, err := os.ReadFile(adrPath)
	if err != nil {
		t.Fatalf("read ADR-021: %v", err)
	}
	for _, want := range []string{
		"boxd proxy new graphql --vm",
		"orchard-daemon",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("ADR-021 missing prerequisite string %q", want)
		}
	}
}

// readPeeredConfig is a test helper — round-trip the on-disk JSON back
// into peeredFile so assertions can read both repos and peer fields.
func readPeeredConfig(t *testing.T, home string) peeredFile {
	t.Helper()
	cfgPath := filepath.Join(home, ".orchard", "config.json")
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
	home := setHomeForTest(t)
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "10.0.0.1:7777", true); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readPeeredConfig(t, home)
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
	home := setHomeForTest(t)
	var buf bytes.Buffer
	if err := runAddPeer(&buf, "peer-1", "https://graphql.peer.example", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfg := readPeeredConfig(t, home)
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

// TestAddPeer_PreservesExistingRepos asserts add-peer doesn't clobber
// the `repos` array — the post-#540 (ADR-015) shape replacement for
// the old `projects` round-trip.
func TestAddPeer_PreservesExistingRepos(t *testing.T) {
	home := setHomeForTest(t)
	cfgPath := filepath.Join(home, ".orchard", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = `{
		"version": 1,
		"repos": [
			{"slug": "team/alpha", "path": "/tmp/alpha"}
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
	repos, ok := raw["repos"].([]any)
	if !ok || len(repos) != 1 {
		t.Fatalf("repos clobbered: %v", raw["repos"])
	}
	peers, ok := raw["peers"].([]any)
	if !ok || len(peers) != 1 {
		t.Fatalf("peer not written: %v", raw["peers"])
	}
}

// TestAddPeer_PreservesUnknownTopLevelKey asserts the Extras catch-all
// round-trips legacy/unknown keys instead of dropping them. (Replaces
// the old TestAddPeer_PreservesLegacyPeerSecret — same contract, with
// the now-truly-unknown peer_secret key as the canary.)
func TestAddPeer_PreservesUnknownTopLevelKey(t *testing.T) {
	home := setHomeForTest(t)
	cfgPath := filepath.Join(home, ".orchard", "config.json")
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
