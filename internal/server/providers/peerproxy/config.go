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
// daemon's GraphQL endpoint (no scheme — the scheme is selected by the
// TLS field). For convenience, users may write a full URL like
// `https://graphql.host.example` in the config; normalise() strips the
// scheme and flips TLS to true automatically.
//
// TLS controls the wire protocol: when true the client speaks HTTPS for
// queries and WSS for subscriptions, and verifies certs against the
// system trust store. Default false preserves the original plaintext
// transport for trusted-LAN deployments.
//
// Purpose is a free-form human description surfaced as `Host.purpose`
// for any host matched via the alias chain in purposeForLocalHost.
type PeerConfig struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	TLS     bool   `json:"tls,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// FederationConfig is the slice of the daemon's on-disk config that
// peerproxy cares about. It lives next to the existing project list in
// ~/.orchard/config.json under the `peers` key; older configs
// without it yield an empty list (no peers configured).
//
// The struct is intentionally tolerant of unknown fields so workstreams
// can grow the config schema without breaking peerproxy. In particular,
// any legacy `peer_secret` key is ignored — the bearer-secret guard was
// removed in favour of TLS + boxd-fronted auth (issue #412).
type FederationConfig struct {
	Peers []PeerConfig `json:"peers"`
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
//
// The Address field is stripped of any leading scheme (`http://`,
// `https://`, `ws://`, `wss://`); when an `https://` or `wss://` prefix
// is detected the TLS bit is implicitly set to true so users who paste
// a URL get HTTPS without an extra flag. An explicit TLS=true in the
// config is honoured even when the Address is bare host:port.
func (f FederationConfig) normalise() FederationConfig {
	out := FederationConfig{}
	seen := make(map[string]struct{}, len(f.Peers))
	for _, p := range f.Peers {
		name := strings.TrimSpace(p.Name)
		raw := strings.TrimSpace(p.Address)
		if name == "" || raw == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		host, tls := splitAddress(raw)
		if host == "" {
			continue
		}
		out.Peers = append(out.Peers, PeerConfig{
			Name:    name,
			Address: host,
			TLS:     p.TLS || tls,
			Purpose: p.Purpose,
		})
	}
	return out
}

// splitAddress parses a peer address that may carry a scheme prefix.
// Returns the bare `host` or `host:port` plus a TLS hint:
//   - `https://h` or `wss://h` → (h, true)
//   - `http://h` or `ws://h`   → (h, false)
//   - `h:port` or `h`          → (h:port, false)  (TLS unchanged)
//
// Trailing slashes and any path components are dropped so the URL
// builder in client.go can append `/graphql` cleanly.
func splitAddress(raw string) (host string, tls bool) {
	rest := raw
	switch {
	case strings.HasPrefix(strings.ToLower(rest), "https://"):
		rest = rest[len("https://"):]
		tls = true
	case strings.HasPrefix(strings.ToLower(rest), "wss://"):
		rest = rest[len("wss://"):]
		tls = true
	case strings.HasPrefix(strings.ToLower(rest), "http://"):
		rest = rest[len("http://"):]
	case strings.HasPrefix(strings.ToLower(rest), "ws://"):
		rest = rest[len("ws://"):]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), tls
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
