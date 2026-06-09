package hostidentity

import (
	"context"

	graphql "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// HostResolver handles resolver methods on the Host type that this domain owns.
// Per R6: one file per GraphQL type. Per S15b: cross-domain fields (processes,
// hostServices) live in the ps and host-services domains respectively — not here.
//
// Fields resolved here:
//   - Host.peers     (v1: always empty; federation shell adds real peers)
//   - Host.version   (always nil here; the daemon-self domain owns version)
//
// NOTE: In the final wired-up aggregate resolver, Host.peers and Host.version
// are resolved by the daemon shell (schema.resolvers.go peerproxy and version
// injection). This resolver provides the domain-local implementations that the
// daemon shell composes from.
type HostResolver struct {
	svc Service
}

// NewHostResolver constructs a HostResolver.
func NewHostResolver(svc Service) *HostResolver {
	return &HostResolver{svc: svc}
}

// Peers resolves Host.peers. v1: always empty for the local host. The daemon
// transport shell (peerproxy) extends this for federated peers.
func (h *HostResolver) Peers(_ context.Context, _ *graphql.Host) ([]*graphql.Host, error) {
	return []*graphql.Host{}, nil
}

// Version resolves Host.version. Returns nil here; the daemon-self domain
// owns the version field and injects it via the aggregate resolver wiring.
// This stub ensures the interface is satisfied when the domain is tested in isolation.
func (h *HostResolver) Version(_ context.Context, _ *graphql.Host) (*string, error) {
	return nil, nil
}
