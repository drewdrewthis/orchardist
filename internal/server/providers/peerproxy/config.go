package peerproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// PeerConfig is one row in the daemon's `peers` configuration block.
//
// Name is the host identifier the local daemon uses when proxying — it
// must match the prefix in node ids the resolvers want to forward
// (e.g. a node id `TmuxPane:peer-1:%26` is routed to the peer with
// Name == "peer-1"). Address is the host:port of the remote orchard
// daemon's GraphQL endpoint (without scheme — the client picks ws/wss
// based on TLS configuration).
type PeerConfig struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// FederationConfig is the slice of the daemon's on-disk config that
// peerproxy cares about. It lives next to the existing project list in
// ~/.config/orchard/config.json under the `peers` and `peer_secret`
// keys; older configs without these keys yield an empty list and an
// empty secret (local-dev mode — no auth required).
//
// The struct is intentionally tolerant of unknown fields so workstreams
// can grow the config schema without breaking peerproxy.
type FederationConfig struct {
	Peers      []PeerConfig `json:"peers"`
	PeerSecret string       `json:"peer_secret"`
}

// LoadFederationConfig reads cfgPath and extracts the federation slice.
// A missing file is not an error — it returns an empty FederationConfig
// so the daemon boots cleanly on a fresh machine.
func LoadFederationConfig(cfgPath string) (FederationConfig, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FederationConfig{}, nil
		}
		return FederationConfig{}, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	if len(data) == 0 {
		return FederationConfig{}, nil
	}
	var fc FederationConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		return FederationConfig{}, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	return fc.normalise(), nil
}

// normalise drops blank/duplicate entries so downstream code can trust
// the list is well-formed. The first entry for a given Name wins —
// later duplicates are silently dropped (consistent with how the
// projects list de-dupes by directory).
func (f FederationConfig) normalise() FederationConfig {
	out := FederationConfig{PeerSecret: strings.TrimSpace(f.PeerSecret)}
	seen := make(map[string]struct{}, len(f.Peers))
	for _, p := range f.Peers {
		name := strings.TrimSpace(p.Name)
		addr := strings.TrimSpace(p.Address)
		if name == "" || addr == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out.Peers = append(out.Peers, PeerConfig{Name: name, Address: addr})
	}
	return out
}

// PeerByName returns the configured peer with the given name, or false
// if no such peer exists. Used by the provider to route node ids whose
// host prefix matches a known peer.
func (f FederationConfig) PeerByName(name string) (PeerConfig, bool) {
	for _, p := range f.Peers {
		if p.Name == name {
			return p, true
		}
	}
	return PeerConfig{}, false
}
