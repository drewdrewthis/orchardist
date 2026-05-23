package hostidentity

import (
	"context"
	"fmt"
	"time"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// Service is the R2 contract — the ONLY API consumers (resolvers, loaders,
// other domains) may import. Resolvers depend on this interface; they never
// import Provider or any internal type directly.
//
// Per R4 (ISP): consumers define the subset they need. This interface is the
// host-identity module's full read surface.
type Service interface {
	// Host returns the GraphQL Host for the given machine id key.
	// Returns an error when the key is unknown (v1: only the local machine id).
	Host(ctx context.Context, key HostID) (*graphql.Host, error)

	// LocalID returns the cache key for the local machine.
	LocalID() HostID

	// Hosts returns all known hosts. v1: just the local host.
	Hosts(ctx context.Context) ([]*graphql.Host, error)
}

// serviceImpl wraps the Provider and projects HostSnapshot → graphql.Host.
// It is the only concrete implementation; tests may provide their own stub
// via the Service interface.
type serviceImpl struct {
	p *Provider
}

// NewService constructs the production service backed by the given Provider.
// Per R11: returns the concrete type; consumers receive a Service interface.
func NewService(p *Provider) Service {
	return &serviceImpl{p: p}
}

// Host implements Service. Returns the GraphQL Host node for the requested key.
func (s *serviceImpl) Host(ctx context.Context, key HostID) (*graphql.Host, error) {
	snap, _, err := s.p.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("host-identity: get %q: %w", key, err)
	}
	return projectHost(snap), nil
}

// LocalID implements Service.
func (s *serviceImpl) LocalID() HostID {
	return s.p.LocalID()
}

// Hosts implements Service. v1: returns a single-element slice (local only).
func (s *serviceImpl) Hosts(ctx context.Context) ([]*graphql.Host, error) {
	local, err := s.Host(ctx, s.LocalID())
	if err != nil {
		return nil, err
	}
	return []*graphql.Host{local}, nil
}

// projectHost converts a HostSnapshot to the gqlgen Host type.
// Pure function; all mutable state stays in Provider.
func projectHost(snap *HostSnapshot) *graphql.Host {
	id := snap.Identity
	host := &graphql.Host{
		ID:         "Host:" + id.MachineID,
		MachineID:  id.MachineID,
		Hostname:   id.Hostname,
		Os:         id.OS,
		Reachable:  true,
		Peers:      []*graphql.Host{},
		LastSeenAt: snap.SampledAt.UTC().Format(time.RFC3339Nano),
	}
	if id.Kernel != "" {
		k := id.Kernel
		host.Kernel = &k
	}
	if snap.LoadKnown {
		host.ResourceLoad = &graphql.ResourceLoad{
			CPUPercent:  snap.Load.CPUPercent,
			MemPercent:  snap.Load.MemPercent,
			DiskPercent: snap.Load.DiskPercent,
			LoadAvg1m:   snap.Load.LoadAvg1m,
			LoadAvg5m:   snap.Load.LoadAvg5m,
			LoadAvg15m:  snap.Load.LoadAvg15m,
		}
		host.LastSeenAt = snap.LoadAt.UTC().Format(time.RFC3339Nano)
	}
	return host
}
