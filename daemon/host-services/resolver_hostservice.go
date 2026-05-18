// resolver_hostservice.go — resolvers for the HostService GraphQL type.
//
// Per R6 this file owns exactly one GraphQL type. All field reads go
// through loaders (R3); no Snapshot() calls here.
package hostservices

import (
	"context"
	"fmt"
)

// HostServiceResolver projects a HostServiceSnapshot into its GraphQL
// fields. Resolvers receive a snapshot pre-loaded by the loader; this
// is pure projection with no additional I/O (L4).
type HostServiceResolver struct {
	Snap HostServiceSnapshot
}

// ID returns the stable GraphQL id: "HostService:<machineID>:<name>".
// Per S2 every Node has a globally unique id.
func (r *HostServiceResolver) ID() string {
	return fmt.Sprintf("HostService:%s:%s", r.Snap.MachineID, r.Snap.Name)
}

// Name returns the service name as configured.
func (r *HostServiceResolver) Name() string { return r.Snap.Name }

// StateName returns the HostServiceState enum value as a string.
// Resolvers map from the internal State type to the GraphQL enum at
// the boundary — no gqlgen type leaks into the domain layer.
func (r *HostServiceResolver) StateName() string { return string(r.Snap.State) }

// Since returns the RFC 3339 string of when the service entered its
// current state, or nil when the OS didn't report a timestamp.
func (r *HostServiceResolver) Since() *string {
	if r.Snap.Since == nil {
		return nil
	}
	s := r.Snap.Since.Format("2006-01-02T15:04:05Z07:00")
	return &s
}

// ExitCode returns the most recent exit code, or nil.
func (r *HostServiceResolver) ExitCode() *int { return r.Snap.ExitCode }

// LogTail returns the last 20 log lines, or nil.
func (r *HostServiceResolver) LogTail() *string { return r.Snap.LogTail }

// QueryHostServicesArgs carries the optional filter for Query.hostServices.
type QueryHostServicesArgs struct {
	Filter *HostServiceFilterInput
}

// HostServiceFilterInput mirrors the schema's HostServiceFilter input type.
type HostServiceFilterInput struct {
	Host  *string
	Name  *string
	State *string
}

// HostID returns the machineID this service belongs to. Used by resolvers
// that need to delegate to the HostIdentityReader for the Host back-edge.
func (r *HostServiceResolver) HostID() string { return r.Snap.MachineID }

// ResolveHostServices resolves Query.hostServices(filter).
//
// Fetches all snapshots from the service and applies optional filter
// clauses (AND-combined per schema contract). Each result is projected
// through the loader path — no direct provider access (R3).
func ResolveHostServices(ctx context.Context, svc ServiceReader, args QueryHostServicesArgs) ([]*HostServiceResolver, error) {
	snaps, err := svc.Snapshots(ctx)
	if err != nil {
		return nil, fmt.Errorf("hostServices: %w", err)
	}

	var out []*HostServiceResolver
	for _, snap := range snaps {
		if args.Filter != nil {
			f := args.Filter
			if f.Host != nil && snap.MachineID != *f.Host {
				continue
			}
			if f.Name != nil && snap.Name != *f.Name {
				continue
			}
			if f.State != nil && string(snap.State) != *f.State {
				continue
			}
		}
		out = append(out, &HostServiceResolver{Snap: snap})
	}
	return out, nil
}
