package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/drewdrewthis/git-orchard-rs/internal/orchpaths"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// peeredFile is the on-disk view of ~/.orchard/config.json that
// `add-peer` cares about. Per ADR-015 the file has exactly three
// top-level keys: `version`, `repos`, `peers`. The `repos` array is
// owned by add-repo; this writer round-trips it opaquely so add-peer
// never clobbers it.
//
// Legacy / unknown top-level keys go through Extras so we don't drop
// them on rewrite — eventually a migration command will scrub them, but
// until then we preserve them silently so users can re-edit without
// losing data.
type peeredFile struct {
	Version int                        `json:"version,omitempty"`
	Repos   json.RawMessage            `json:"repos,omitempty"`
	Peers   []peerproxy.PeerConfig     `json:"peers,omitempty"`
	Extras  map[string]json.RawMessage `json:"-"`
}

// MarshalJSON re-emits the canonical fields followed by any unknown
// fields stored in Extras. Output order is: version, repos, peers, then
// extras alphabetically.
func (f peeredFile) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	if f.Version != 0 {
		b, err := json.Marshal(f.Version)
		if err != nil {
			return nil, err
		}
		out["version"] = b
	}
	if len(f.Repos) > 0 {
		out["repos"] = f.Repos
	}
	if len(f.Peers) > 0 {
		b, err := json.Marshal(f.Peers)
		if err != nil {
			return nil, err
		}
		out["peers"] = b
	}
	for k, v := range f.Extras {
		if _, taken := out[k]; taken {
			continue
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// UnmarshalJSON pulls the canonical fields and stashes any other
// top-level keys in Extras so we don't drop them on rewrite.
func (f *peeredFile) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Extras = map[string]json.RawMessage{}
	for k, v := range raw {
		switch k {
		case "version":
			if err := json.Unmarshal(v, &f.Version); err != nil {
				return err
			}
		case "repos":
			f.Repos = v
		case "peers":
			if err := json.Unmarshal(v, &f.Peers); err != nil {
				return err
			}
		default:
			f.Extras[k] = v
		}
	}
	return nil
}

// addPeerCmd wires `orchard config add-peer` into the cobra tree.
//
//	orchard config add-peer --name <name> --address <host[:port]> [--tls]
func addPeerCmd() *cobra.Command {
	var (
		name    string
		address string
		tls     bool
	)
	c := &cobra.Command{
		Use:   "add-peer",
		Short: "Append a peer to ~/.orchard/config.json",
		Long: "Validate the supplied name + address, append a peer entry,\n" +
			"and rely on the running daemon's fsnotify watcher to pick up\n" +
			"the change. Pass --tls (or use an `https://`/`wss://` address)\n" +
			"for TLS-fronted peers.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAddPeer(cmd.OutOrStdout(), name, address, tls)
		},
	}
	c.Flags().StringVar(&name, "name", "", "peer identifier (matches node-id host segment)")
	c.Flags().StringVar(&address, "address", "", "host[:port] (or https://host for TLS)")
	c.Flags().BoolVar(&tls, "tls", false, "speak HTTPS/WSS to this peer")
	return c
}

// runAddPeer is the testable core. Validates flags, mutates the file,
// writes atomically. Returns an error if name/address are missing or
// the peer already exists.
func runAddPeer(w io.Writer, name, address string, tls bool) error {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if address == "" {
		return fmt.Errorf("--address is required")
	}

	cfgPath, err := orchpaths.ConfigFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	file, err := loadOrInitPeeredFile(cfgPath)
	if err != nil {
		return err
	}

	// Reject duplicate names — add-peer is strictly an append. Update
	// flows belong to a future `update-peer`.
	for _, existing := range file.Peers {
		if existing.Name == name {
			return fmt.Errorf("peer %q already exists; remove or rename it first", name)
		}
	}

	normalised := normalisePeerInput(name, address, tls)
	if normalised.Address == "" {
		return fmt.Errorf("address %q has no host component", address)
	}

	file.Peers = append(file.Peers, normalised)
	if file.Version == 0 {
		file.Version = 1
	}

	if err := writePeeredFileAtomic(cfgPath, file); err != nil {
		return err
	}
	scheme := "http/ws"
	if normalised.TLS {
		scheme = "https/wss"
	}
	fmt.Fprintf(w, "added peer %s (%s, %s) to %s\n",
		normalised.Name, normalised.Address, scheme, cfgPath)
	return nil
}

// normalisePeerInput mirrors peerproxy.normalise's per-row behaviour
// without going through file I/O. Strips any scheme prefix from address
// and flips TLS true on `https://`/`wss://`.
func normalisePeerInput(name, address string, tls bool) peerproxy.PeerConfig {
	host, schemeTLS := splitAddressForCLI(address)
	return peerproxy.PeerConfig{
		Name:    strings.TrimSpace(name),
		Address: host,
		TLS:     tls || schemeTLS,
	}
}

// splitAddressForCLI is a thin wrapper around the same logic peerproxy
// uses. Kept local to avoid adding a new exported surface; the two
// are covered by their own unit tests.
func splitAddressForCLI(raw string) (string, bool) {
	rest := strings.TrimSpace(raw)
	tls := false
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

// loadOrInitPeeredFile reads cfgPath. A missing file yields an empty
// peeredFile with version=1 so add-peer also serves as implicit init.
func loadOrInitPeeredFile(cfgPath string) (peeredFile, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return peeredFile{Version: 1, Extras: map[string]json.RawMessage{}}, nil
		}
		return peeredFile{}, fmt.Errorf("read %s: %w", cfgPath, err)
	}
	if len(data) == 0 {
		return peeredFile{Version: 1, Extras: map[string]json.RawMessage{}}, nil
	}
	var f peeredFile
	if err := json.Unmarshal(data, &f); err != nil {
		return peeredFile{}, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Extras == nil {
		f.Extras = map[string]json.RawMessage{}
	}
	return f, nil
}

// writePeeredFileAtomic writes f via tmp + rename so fsnotify observes
// a single atomic event (matches add-repo's writer).
func writePeeredFileAtomic(cfgPath string, f peeredFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(cfgPath), ".config.*.json")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, cfgPath); err != nil {
		cleanup()
		return fmt.Errorf("rename %s → %s: %w", tmpName, cfgPath, err)
	}
	return nil
}
