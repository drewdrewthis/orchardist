package hostidentity

import (
	"context"
	"fmt"

	graphql "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// QueryResolver handles the Query.{host, hosts, peers} fields owned by the
// host-identity domain. Per R6: one file per GraphQL type; this file owns
// the query root methods. Per R3: delegates to loaders, not the service directly,
// for any field that might be requested in a batched context.
//
// The QueryResolver is a thin delegation layer — business logic lives in
// Service (R2); data access shape lives in Loaders (R3).
type QueryResolver struct {
	svc     Service
	loaders *Loaders
}

// NewQueryResolver constructs a QueryResolver. Loaders may be nil during
// internal calls (e.g. subscription emissions); the resolver falls back to
// direct service calls in that case.
func NewQueryResolver(svc Service, loaders *Loaders) *QueryResolver {
	return &QueryResolver{svc: svc, loaders: loaders}
}

// Host resolves Query.host — the local machine running this daemon.
// Identity is one-shot at boot; resource load refreshes on 5s TTL.
//
// Per R3: uses HostByID loader when available.
func (q *QueryResolver) Host(ctx context.Context) (*graphql.Host, error) {
	localID := string(q.svc.LocalID())
	if localID == "" {
		return nil, fmt.Errorf("host provider not started")
	}
	if q.loaders != nil {
		return q.loaders.HostByID.Load(ctx, localID)()
	}
	return q.svc.Host(ctx, q.svc.LocalID())
}

// Hosts resolves Query.hosts — all hosts known to this daemon.
// v1: only the local host. Federation adds remote peers via the transport layer.
func (q *QueryResolver) Hosts(ctx context.Context) ([]*graphql.Host, error) {
	local, err := q.Host(ctx)
	if err != nil {
		return nil, err
	}
	return []*graphql.Host{local}, nil
}

// Peers resolves Query.peers — all peer hosts known to this daemon, flattened
// across hosts[*].peers. v1: always empty. Federation populates via the
// peerproxy transport layer (daemon shell concern, not this domain).
func (q *QueryResolver) Peers(_ context.Context) ([]*graphql.Host, error) {
	// v1: no peers. The daemon shell wires federation peers via the peerproxy
	// provider, which extends this resolver in the aggregate server.
	return []*graphql.Host{}, nil
}
