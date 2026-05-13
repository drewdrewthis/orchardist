// Manifest-aware merge helpers used by Query.hosts / Query.health.
//
// Kept separate from schema.resolvers.go so gqlgen does not rewrite them
// when the schema regenerates.
package resolvers

import (
	"sort"
	"strings"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/manifest"
)

// applyManifestEntry enriches a Host with manifest-sourced metadata in
// place. `inManifest` is always set — false when entry.Name is empty
// (i.e. no manifest match), true otherwise. Subsequent fields are only
// set when the entry carries a non-empty value, so a live-fleet host
// without manifest coverage stays nullable on the wire.
func applyManifestEntry(h *graphql1.Host, entry manifest.Entry, present bool) {
	if h == nil {
		return
	}
	h.InManifest = present
	if !present {
		return
	}
	if entry.Purpose != "" {
		v := entry.Purpose
		h.Purpose = &v
	}
	if role, ok := mapHostRole(entry.Role); ok {
		r := role
		h.Role = &r
	}
	if entry.OwnerOrchardist != "" {
		v := entry.OwnerOrchardist
		h.OwnerOrchardist = &v
	}
	if entry.DecommissionSignal != "" {
		v := entry.DecommissionSignal
		h.DecommissionSignal = &v
	}
	if entry.LastVerified != "" {
		v := entry.LastVerified
		h.LastVerified = &v
	}
	if h.Address == nil && entry.Address != "" {
		v := entry.Address
		h.Address = &v
	}
}

// mapHostRole projects the manifest's free-form role string into the
// GraphQL enum. Unknown roles return (_, false) so the resolver emits
// a null Role rather than fabricating an enum value — the schema-
// declared values are the contract.
func mapHostRole(raw string) (graphql1.HostRole, bool) {
	switch strings.TrimSpace(raw) {
	case "local_orchardist":
		return graphql1.HostRoleLocalOrchardist, true
	case "boxd_orchardist":
		return graphql1.HostRoleBoxdOrchardist, true
	case "federation_worker":
		return graphql1.HostRoleFederationWorker, true
	case "grinder_pool":
		return graphql1.HostRoleGrinderPool, true
	case "dedicated_grinder":
		return graphql1.HostRoleDedicatedGrinder, true
	case "fork_per_issue":
		return graphql1.HostRoleForkPerIssue, true
	case "daemon_peer":
		return graphql1.HostRoleDaemonPeer, true
	case "external":
		return graphql1.HostRoleExternal, true
	default:
		return "", false
	}
}

// manifestHostStub builds a Host for a manifest-only entry — a host
// catalogued in the YAML that the daemon has not (yet) heard from.
// Reachable is false, LastSeenAt is "" (pair with reachable=false to
// detect "never seen live"), Peers is non-nil to match the schema
// contract (NonNull list).
//
// The stable id uses the manifest name as the machine id, since the
// daemon has no OS-issued machine id for an offline host. This mirrors
// how peerproxy hands out ids for configured-but-unreachable peers
// (`Host:<peer-name>`).
func manifestHostStub(entry manifest.Entry) *graphql1.Host {
	h := &graphql1.Host{
		ID:         "Host:" + entry.Name,
		MachineID:  entry.Name,
		Hostname:   entry.Name,
		Os:         "manifest",
		Reachable:  false,
		LastSeenAt: "",
		Peers:      []*graphql1.Host{},
	}
	applyManifestEntry(h, entry, true)
	return h
}

// hostKey returns the merge key for a Host. Prefers MachineID (used by
// peerproxy + the host provider); falls back to Hostname so manifest-
// only entries dedupe against live-fleet rows that share a hostname.
func hostKey(h *graphql1.Host) string {
	if h == nil {
		return ""
	}
	if h.MachineID != "" {
		return h.MachineID
	}
	return h.Hostname
}

// mergeManifestHosts folds manifest entries into the live-fleet list.
//
// Three buckets emerge:
//   - Hosts seen live AND in the manifest → enriched in place; `inManifest=true`.
//   - Hosts seen live but NOT in the manifest → `inManifest=false` (drift signal).
//   - Manifest entries with no live match → appended as stubs with
//     `reachable=false`, `lastSeenAt=null`.
//
// Stable ordering: live hosts first (in their original order), manifest-
// only hosts appended in manifest declaration order. Callers that need
// a specific ordering can sort after.
func mergeManifestHosts(live []*graphql1.Host, m *manifest.Provider) []*graphql1.Host {
	out := make([]*graphql1.Host, 0, len(live))
	seenLiveByName := make(map[string]struct{}, len(live))
	for _, h := range live {
		if h == nil {
			continue
		}
		// Try several keys when looking up the manifest: machine id,
		// hostname, and (for peerproxy peers whose machineId is the
		// configured peer name) the address. Manifest names are short
		// canonical strings — humans wrote them — so each of these is a
		// plausible alias.
		entry, present := lookupManifestForHost(m, h)
		applyManifestEntry(h, entry, present)
		if present {
			seenLiveByName[entry.Name] = struct{}{}
		}
		out = append(out, h)
	}
	if m == nil {
		return out
	}
	for _, e := range m.Snapshot() {
		if _, ok := seenLiveByName[e.Name]; ok {
			continue
		}
		out = append(out, manifestHostStub(e))
	}
	return out
}

// lookupManifestForHost searches the manifest for an entry matching the
// given host. Match order:
//  1. By MachineID — the canonical identifier when populated.
//  2. By Hostname — fallback for stubs constructed before identity
//     resolution.
//  3. By Address (host-portion) — matches the manifest's `address`
//     field for peers configured as `boxd@<name>.boxd.sh`.
//
// Returns (Entry{}, false) if none of those match.
func lookupManifestForHost(m *manifest.Provider, h *graphql1.Host) (manifest.Entry, bool) {
	if m == nil || h == nil {
		return manifest.Entry{}, false
	}
	if entry, ok := m.LookupByName(h.MachineID); ok {
		return entry, true
	}
	if entry, ok := m.LookupByName(h.Hostname); ok {
		return entry, true
	}
	if h.Address != nil {
		if entry, ok := lookupByAddress(m, *h.Address); ok {
			return entry, true
		}
	}
	return manifest.Entry{}, false
}

// lookupByAddress scans manifest entries whose `address` field matches
// the provided address string. The manifest declares `address` as
// `boxd@<host>.boxd.sh`; peerproxy can hand us either the same string
// or a bare `<host>.boxd.sh` — both shapes match.
func lookupByAddress(m *manifest.Provider, address string) (manifest.Entry, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return manifest.Entry{}, false
	}
	for _, e := range m.Snapshot() {
		if e.Address == "" {
			continue
		}
		if strings.EqualFold(e.Address, address) {
			return e, true
		}
		if strings.EqualFold(stripSSHUser(e.Address), address) {
			return e, true
		}
	}
	return manifest.Entry{}, false
}

// stripSSHUser returns the host portion of `user@host` style addresses.
// `boxd@orchard.boxd.sh` → `orchard.boxd.sh`.
func stripSSHUser(raw string) string {
	if idx := strings.LastIndexByte(raw, '@'); idx >= 0 {
		return raw[idx+1:]
	}
	return raw
}

// buildManifestStatus projects the provider's status into the GraphQL
// `ManifestStatus` shape. When no provider is wired the result is a
// safe-default object so consumers can rely on `health.manifest`
// being non-null.
func buildManifestStatus(m *manifest.Provider) *graphql1.ManifestStatus {
	if m == nil {
		return &graphql1.ManifestStatus{
			Path:      "",
			Loaded:    false,
			HostCount: 0,
		}
	}
	st := m.Status()
	out := &graphql1.ManifestStatus{
		Path:      st.Path,
		Loaded:    st.Loaded,
		HostCount: int64(st.HostCount),
	}
	if !st.LastLoadedAt.IsZero() {
		ts := st.LastLoadedAt.UTC().Format(time.RFC3339Nano)
		out.LastLoadedAt = &ts
	}
	if st.Error != "" {
		msg := st.Error
		out.Error = &msg
	}
	return out
}

// sortHostsByName sorts hosts alphabetically by their hostname. Used by
// tests that need a deterministic order regardless of how the merge
// happened to lay things out.
func sortHostsByName(hosts []*graphql1.Host) {
	sort.SliceStable(hosts, func(i, j int) bool {
		return hosts[i].Hostname < hosts[j].Hostname
	})
}
