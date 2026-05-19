// resolver_daemonstate.go — resolver for Query.daemonState and related
// DaemonState type fields (R6: one file per GraphQL type).
//
// Field resolvers go through the DaemonStateLoader (R3). No Snapshot() call.
package daemonmeta

import (
	"context"
	"fmt"

	"github.com/graph-gophers/dataloader/v7"
)

// DaemonStateResolver holds the dependencies for resolving DaemonState fields.
// Consumers embed or instantiate this; it is NOT a gqlgen generated type.
// In the final wired daemon, this wires into the aggregate gqlgen resolver.
type DaemonStateResolver struct {
	Loader *dataloader.Loader[string, *DaemonState]
}

// NewDaemonStateResolver constructs a DaemonStateResolver from a Service.
// The loader is created here per R3 (loaders batch and cache per-request).
func NewDaemonStateResolver(svc Service) *DaemonStateResolver {
	return &DaemonStateResolver{
		Loader: NewDaemonStateLoader(svc),
	}
}

// QueryDaemonState is the resolver body for Query.daemonState.
// Call this from the gqlgen-generated queryResolver.DaemonState method.
func (r *DaemonStateResolver) QueryDaemonState(ctx context.Context) (*DaemonState, error) {
	ds, err := LoadDaemonState(ctx, r.Loader)
	if err != nil {
		return nil, fmt.Errorf("daemonState: %w", err)
	}
	return ds, nil
}

// DaemonStateProviders is the resolver body for DaemonState.providers.
// Projects from the domain DaemonState to a slice of ProviderHealth maps.
// Thin projection — no additional fetch (R3, O2 lazy).
func DaemonStateProviders(ds *DaemonState) []ProviderHealthEntry {
	if ds == nil {
		return nil
	}
	out := make([]ProviderHealthEntry, 0, len(ds.Providers))
	for i, snap := range ds.Providers {
		name := ""
		if i < len(ds.ProviderNames) {
			name = ds.ProviderNames[i]
		}
		out = append(out, ProviderHealthEntry{
			Name:     name,
			Snapshot: snap,
		})
	}
	return out
}

// ProviderHealthEntry is the projection of a single ProviderHealth entry
// ready for GraphQL field resolution.
type ProviderHealthEntry struct {
	Name     string
	Snapshot ProviderHealthSnapshot
}
