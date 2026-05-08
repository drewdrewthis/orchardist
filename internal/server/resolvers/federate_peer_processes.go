// Federation glue for `Host.processes` on a peer Host. Kept separate
// from schema.resolvers.go so gqlgen does not rewrite it when the
// schema regenerates.
//
// Without this layer, the `Host.processes` resolver in the gqlgen-
// generated path always returns the local daemon's `ps` snapshot
// regardless of which Host the field is being resolved on (#465).
// That is the textbook "confidently wrong" failure mode: the call
// succeeds, the data comes back, but it labels local PIDs as belonging
// to a different host. Federating to the peer daemon over the existing
// peerproxy transport is the smallest change that makes the answer
// match reality.
package resolvers

import (
	"context"
	"encoding/json"
	"fmt"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

// federatePeerProcesses forwards a `host.processes` selection to the
// peer daemon identified by `obj`, then re-tags the returned process
// ids with the peer's host so traversal (`process.host`,
// `splitProcessNodeID`) keeps the peer namespace.
//
// Errors when the peer is unreachable or the proxy isn't wired —
// callers see a typed error rather than a silently-empty list, which
// would be indistinguishable from "this peer genuinely has no matching
// processes."
func federatePeerProcesses(ctx context.Context, r *hostResolver, obj *graphql1.Host, filter *graphql1.ProcessFilter) ([]*graphql1.Process, error) {
	if r.PeerProxy == nil {
		return nil, fmt.Errorf("peer proxy not wired; cannot resolve processes for peer %q", obj.Hostname)
	}
	peerName := peerNameFromHost(obj)
	if peerName == "" {
		return nil, fmt.Errorf("peer host has no name; cannot federate")
	}
	if !r.PeerProxy.HasPeer(peerName) {
		return nil, fmt.Errorf("peer %q is not configured", peerName)
	}
	if reachable, _, ok := r.PeerProxy.Reachability(peerName); ok && !reachable {
		return nil, fmt.Errorf("peer %q is unreachable", peerName)
	}

	const peerProcessesQuery = `query($filter: ProcessFilter) {
  host {
    processes(filter: $filter) {
      id
      pid
      ppid
      command
      startedAt
      cpuPercent
      memBytes
      tty
    }
  }
}`
	vars := map[string]any{}
	if f := buildProcessFilterVars(filter); f != nil {
		vars["filter"] = f
	}
	res, err := r.PeerProxy.Query(ctx, peerName, peerProcessesQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("federate peer %q: %w", peerName, err)
	}
	if err := res.AsError(); err != nil {
		return nil, fmt.Errorf("federate peer %q: %w", peerName, err)
	}
	return decodePeerProcesses(res, peerName)
}

// peerNameFromHost extracts the configured peer name from the Host obj.
// `Host.peers` populates Hostname == MachineID == the peer config name
// (see schema.resolvers.go::Peers); both are kept in sync. Falls back
// to the id suffix when neither field is set, so synthetic Host nodes
// constructed from a node-id (e.g. via `node(id:)`) still resolve.
func peerNameFromHost(obj *graphql1.Host) string {
	if obj == nil {
		return ""
	}
	if obj.MachineID != "" {
		return obj.MachineID
	}
	if obj.Hostname != "" {
		return obj.Hostname
	}
	const prefix = "Host:"
	if len(obj.ID) > len(prefix) && obj.ID[:len(prefix)] == prefix {
		return obj.ID[len(prefix):]
	}
	return ""
}

// buildProcessFilterVars projects the resolver-side ProcessFilter into
// the variable map the peer's GraphQL surface expects. Returns nil when
// the filter is empty so we don't send an empty object the peer might
// treat as "match nothing."
func buildProcessFilterVars(f *graphql1.ProcessFilter) map[string]any {
	if f == nil {
		return nil
	}
	out := map[string]any{}
	if len(f.PidIn) > 0 {
		out["pidIn"] = f.PidIn
	}
	if len(f.CommandIn) > 0 {
		out["commandIn"] = f.CommandIn
	}
	if f.CwdPrefix != nil {
		out["cwdPrefix"] = *f.CwdPrefix
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// decodePeerProcesses converts the QueryResult body into a slice of
// resolver-shaped Process pointers. The peer's process ids carry the
// peer's host segment already (the peer formatted them with its own
// PS.HostID()), so we don't rewrite them — but we DO normalise the
// nested Host pointer so `process.host` resolves to the same peer.
func decodePeerProcesses(res peerproxy.QueryResult, peerName string) ([]*graphql1.Process, error) {
	var body struct {
		Host struct {
			Processes []struct {
				ID         string  `json:"id"`
				Pid        int64   `json:"pid"`
				Ppid       int64   `json:"ppid"`
				Command    string  `json:"command"`
				StartedAt  string  `json:"startedAt"`
				CPUPercent float64 `json:"cpuPercent"`
				MemBytes   int64   `json:"memBytes"`
				Tty        *string `json:"tty"`
			} `json:"processes"`
		} `json:"host"`
	}
	if err := json.Unmarshal(res.Data, &body); err != nil {
		return nil, fmt.Errorf("decode peer %q processes: %w", peerName, err)
	}
	hostID := "Host:" + peerName
	out := make([]*graphql1.Process, 0, len(body.Host.Processes))
	for _, p := range body.Host.Processes {
		out = append(out, &graphql1.Process{
			ID:         p.ID,
			Host:       &graphql1.Host{ID: hostID, MachineID: peerName, Hostname: peerName},
			Pid:        p.Pid,
			Ppid:       p.Ppid,
			Command:    p.Command,
			StartedAt:  p.StartedAt,
			CPUPercent: p.CPUPercent,
			MemBytes:   p.MemBytes,
			Tty:        p.Tty,
		})
	}
	return out, nil
}
