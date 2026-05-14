// Helpers used by the federation-aware resolvers (Host.peers,
// Query.node, Subscription.peer). Kept separate from
// schema.resolvers.go so gqlgen does not rewrite them when the schema
// regenerates.
package resolvers

import (
	"fmt"
	"strings"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// isLocalHostNode returns true when the Host being resolved represents
// the local daemon's host. The check is conservative: when no host
// provider is wired we treat the obj as local (single-host setups).
func isLocalHostNode(r *hostResolver, obj *graphql1.Host) bool {
	if obj == nil {
		return true
	}
	if r == nil || r.HostProvider == nil {
		return true
	}
	return obj.MachineID == "" || obj.MachineID == string(r.HostProvider.LocalID())
}

// peerLastSeen formats a peer's last-known reachable timestamp into the
// schema's RFC3339 string. A zero time returns "" so callers can detect
// "I have not yet talked to this peer" without emitting a false "now".
func peerLastSeen(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// projectPeerNode lifts a peerproxy.PeerNode into the gqlgen-generated
// Node interface. The implementation is intentionally shallow: we
// surface the id + typename so callers can match the response, but
// downstream field selections re-issue typed queries against the peer.
//
// Unknown typenames return an error so the caller can decide whether
// to treat the response as nil or propagate. Empty/blank ids return an
// error too — a peer that pushed only a typename gives the local
// resolver nothing to forward.
func projectPeerNode(pn *peerproxy.PeerNode) (graphql1.Node, error) {
	if pn == nil {
		return nil, nil
	}
	id := string(pn.ID)
	if id == "" {
		return nil, fmt.Errorf("peer node has empty id")
	}
	switch pn.TypeName {
	case "Host":
		return &graphql1.Host{ID: id, Peers: []*graphql1.Host{}}, nil
	case "Repo", "Project":
		// Accept legacy "Project" typename for backwards-compatibility
		// with peers that haven't redeployed post-ADR-015.
		return &graphql1.Repo{ID: id}, nil
	case "Worktree":
		return &graphql1.Worktree{ID: id}, nil
	case "Process":
		return &graphql1.Process{ID: id}, nil
	case "TmuxServer":
		return &graphql1.TmuxServer{ID: id}, nil
	case "TmuxSession":
		return &graphql1.TmuxSession{ID: id}, nil
	case "TmuxWindow":
		return &graphql1.TmuxWindow{ID: id}, nil
	case "TmuxPane":
		return &graphql1.TmuxPane{ID: id}, nil
	case "TmuxClient":
		return &graphql1.TmuxClient{ID: id}, nil
	default:
		return nil, fmt.Errorf("unknown peer node typename %q", pn.TypeName)
	}
}

// projectLocalInvalidation lifts a touched id (as published by the
// LocalInvalidator) into a stub Node. The typename is recovered from
// the id prefix so the subscriber can branch on `__typename`.
func projectLocalInvalidation(id string) (graphql1.Node, error) {
	colon := strings.IndexByte(id, ':')
	if colon < 0 {
		return nil, nil
	}
	typeName := id[:colon]
	return projectPeerNode(&peerproxy.PeerNode{
		ID:       peerproxy.NodeID(id),
		TypeName: typeName,
	})
}

// resolveLocalNode dispatches a node id against the local providers.
// The resolver layer doesn't yet have a generic node table, so we
// switch on the typename prefix and project the matching cache row.
//
// Unknown ids return (nil, nil) — the schema field is nullable, and
// the conservative answer is "I don't know this id" rather than an
// error that would mask broken queries.
func resolveLocalNode(r *queryResolver, id string) (graphql1.Node, error) {
	colon := strings.IndexByte(id, ':')
	if colon < 0 {
		return nil, nil
	}
	typeName := id[:colon]
	switch typeName {
	case "Host":
		// Strip the prefix and ask the host provider; if it doesn't
		// know the id we return nil rather than an error so callers
		// can probe without raising.
		if r.HostProvider == nil {
			return nil, nil
		}
		hostID := id[colon+1:]
		if hostID == "" {
			return nil, nil
		}
		return &graphql1.Host{ID: id, MachineID: hostID, Peers: []*graphql1.Host{}}, nil
	case "Repo", "Project", "Worktree", "Process",
		"TmuxServer", "TmuxSession", "TmuxWindow", "TmuxPane", "TmuxClient":
		// Construct a stub node carrying the id; the resolver layer's
		// field-by-field methods will populate the rest on demand.
		// This is enough for the AC: the caller selects `__typename`
		// and `id` and gets back a sensible answer.
		return projectPeerNode(&peerproxy.PeerNode{
			ID:       peerproxy.NodeID(id),
			TypeName: typeName,
		})
	default:
		return nil, nil
	}
}

// purposeForLocalHost returns the `Purpose` of the first peer config that
// matches the local host via the alias chain:
//
//	cfg.Name == local.MachineID || cfg.Name == local.Hostname ||
//	cfg.Address == local.MachineID || cfg.Address == local.Hostname ||
//	localStripSSHUser(cfg.Address) == local.Hostname
//
// Returns "" when no peer matches or when the matched peer carries no Purpose.
// Used by the Hosts resolver to enrich the local host before the manifest
// layer runs (manifest overrides if present; step 3 removes the manifest path).
func purposeForLocalHost(local *graphql1.Host, cfgs []peerproxy.PeerConfig) string {
	if local == nil {
		return ""
	}
	for _, cfg := range cfgs {
		if cfg.Name == local.MachineID ||
			cfg.Name == local.Hostname ||
			cfg.Address == local.MachineID ||
			cfg.Address == local.Hostname ||
			localStripSSHUser(cfg.Address) == local.Hostname {
			return cfg.Purpose
		}
	}
	return ""
}

// localStripSSHUser returns the host portion of a `user@host` style address.
// `boxd@orchard.boxd.sh` → `orchard.boxd.sh`. Defined locally so this file
// does not depend on manifest_merge.go (which will be deleted in step 3).
func localStripSSHUser(raw string) string {
	if idx := strings.LastIndexByte(raw, '@'); idx >= 0 {
		return raw[idx+1:]
	}
	return raw
}
