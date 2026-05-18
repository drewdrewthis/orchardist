// resolver_host.go — resolvers for the `extend type Host` back-edge.
//
// Per S15b: domain A (host-services) adds a field to a type owned by
// domain B (host-identity). Both the `extend type` declaration AND the
// resolver live in A. A imports B's service interface (HostIdentityReader)
// — never B's provider (R5).
//
// Per R6 this file owns exactly one GraphQL type extension.
package hostservices

import (
	"context"
	"fmt"
)

// ResolveHostHostServices resolves the `Host.hostServices` back-edge.
//
// hostID is the machineID of the Host node being resolved. The loader
// batches this per-request so one Host.hostServices expansion does not
// generate one OS shellout per field access (R3, O1).
func ResolveHostHostServices(
	ctx context.Context,
	loader *LoaderByMachineID,
	machineID string,
) ([]*HostServiceResolver, error) {
	snaps, err := loader.Load(ctx, machineID)
	if err != nil {
		return nil, fmt.Errorf("Host.hostServices(%s): %w", machineID, err)
	}
	out := make([]*HostServiceResolver, 0, len(snaps))
	for _, snap := range snaps {
		s := snap // copy to avoid loop-variable aliasing
		out = append(out, &HostServiceResolver{Snap: s})
	}
	return out, nil
}
