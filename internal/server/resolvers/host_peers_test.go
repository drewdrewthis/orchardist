// Tests for peer-sourced Host.purpose via the Peers resolver (AC #2 — step 2b).
//
// Scenario: a peer configured with `purpose` in PeerConfig surfaces that
// purpose on the Host object returned by { host { peers { purpose } } }.
package resolvers_test

import (
	"log/slog"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/providers/peerproxy"
)

// TestHostPeers_PurposeFromConfig asserts that a peer Host returned by
// Host.peers carries the purpose that was set in PeerConfig.
//
// BDD: "Host.peers[].purpose is populated from the peer's PeerConfig.Purpose".
func TestHostPeers_PurposeFromConfig(t *testing.T) {
	const peerName = "orchard"
	const peerPurpose = "boxd_orchardist"

	// We still need a reachable peer endpoint so peerproxy doesn't error
	// constructing the peer list. Use the same dead-address trick — the
	// Peers() resolver doesn't need a live connection to return the struct.
	cfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: peerName, Address: "127.0.0.1:1", TLS: false, Purpose: peerPurpose},
		},
	}
	peerProv := peerproxy.NewProvider(cfg, slog.Default())
	ts := newLocalDaemon(t, "dev", peerProv)

	resp := hostVersionPost(t, ts.URL+"/graphql", `{ host { peers { purpose } } }`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) == 0 {
		t.Fatal("host.peers is empty — expected at least one peer")
	}
	peer, _ := peers[0].(map[string]any)
	got, ok := peer["purpose"].(string)
	if !ok {
		t.Fatalf("peers[0].purpose is not a string: %v", peer["purpose"])
	}
	if got != peerPurpose {
		t.Errorf("peers[0].purpose = %q; want %q", got, peerPurpose)
	}
}

// TestHostPeers_PurposeNullWhenNotSet asserts that peers[0].purpose is null
// when the peer's PeerConfig carries no purpose.
//
// BDD: "Host.peers[].purpose is null when PeerConfig.Purpose is empty".
func TestHostPeers_PurposeNullWhenNotSet(t *testing.T) {
	const peerName = "orchard-no-purpose"

	cfg := peerproxy.FederationConfig{
		Peers: []peerproxy.PeerConfig{
			{Name: peerName, Address: "127.0.0.1:1", TLS: false},
		},
	}
	peerProv := peerproxy.NewProvider(cfg, slog.Default())
	ts := newLocalDaemon(t, "dev", peerProv)

	resp := hostVersionPost(t, ts.URL+"/graphql", `{ host { peers { purpose } } }`)

	if errs, ok := resp["errors"]; ok {
		t.Fatalf("unexpected GraphQL errors: %v", errs)
	}

	data, _ := resp["data"].(map[string]any)
	host, _ := data["host"].(map[string]any)
	peers, _ := host["peers"].([]any)
	if len(peers) == 0 {
		t.Fatal("host.peers is empty — expected at least one peer")
	}
	peer, _ := peers[0].(map[string]any)
	if v, exists := peer["purpose"]; exists && v != nil {
		t.Errorf("peers[0].purpose = %v; want null", v)
	}
}
