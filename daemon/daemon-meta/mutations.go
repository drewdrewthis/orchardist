// mutations.go — Mutation.daemonReload resolver (L5 exception, L8, M1).
//
// daemonReload is the canonical exception to L5 ("mutations exec scripts").
// It reloads ~/.orchard/config.json in-process, re-evaluates provider health,
// and returns the post-reload DaemonState so callers can verify the new
// state took effect (L8, S8).
//
// No script is exec'd. The operation affects daemon-internal state
// (loaded config + provider registration), not external truth.
// Documented criterion from L5's carve-out text and the README.
package daemonmeta

import (
	"context"
	"fmt"
)

// MutationResolver holds the dependencies for daemonReload.
// Wire this into the aggregate gqlgen mutationResolver.
type MutationResolver struct {
	Service Service
}

// NewMutationResolver constructs a MutationResolver backed by svc.
func NewMutationResolver(svc Service) *MutationResolver {
	return &MutationResolver{Service: svc}
}

// DaemonReload is the resolver body for Mutation.daemonReload.
// Call this from the gqlgen-generated mutationResolver.DaemonReload method.
//
// Idempotent (M5): re-reading the same config file multiple times has the
// same effect as reading it once. Callers may retry safely.
func (r *MutationResolver) DaemonReload(ctx context.Context) (*DaemonState, error) {
	ds, err := r.Service.Reload(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemonReload: %w", err)
	}
	return ds, nil
}
