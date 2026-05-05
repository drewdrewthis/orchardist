// Helpers used by the top-level aggregate resolvers (Query.peers,
// Query.hostServices). Kept separate from schema.resolvers.go so gqlgen
// does not rewrite them when the schema regenerates.
package resolvers

import (
	"strings"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// hostServiceMatchesFilter applies a HostServiceFilter to a single
// HostService. A nil filter matches everything. Multiple filter fields
// are AND-combined.
//
// `host` matches `Host.machineId` exactly. `name` is exact-match.
// `state` is the lifecycle enum.
func hostServiceMatchesFilter(svc *graphql1.HostService, filter *graphql1.HostServiceFilter) bool {
	if filter == nil || svc == nil {
		return svc != nil
	}
	if filter.Host != nil {
		want := strings.TrimSpace(*filter.Host)
		got := ""
		if svc.Host != nil {
			got = svc.Host.MachineID
		}
		if want != got {
			return false
		}
	}
	if filter.Name != nil && *filter.Name != svc.Name {
		return false
	}
	if filter.State != nil && *filter.State != svc.State {
		return false
	}
	return true
}
