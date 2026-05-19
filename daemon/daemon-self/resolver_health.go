// Resolver implementations for the daemon-self domain.
//
// Owns: Query.health, Query.version, Query.schemaSDL, Query.node,
//       Health.status, Health.uptimeS.
//
// Rules: R2, R3, R6, R9, L4.
// R3 — Health has no per-node DataLoaders; all reads are O(1) in-process
//       arithmetic or string constant returns. Query.node delegates to
//       the NodeRegistry (daemon/node.go) which owns per-type loaders.
package daemonself

import "context"

// Resolver is the dependency root for the daemon-self domain.
// Callers inject a DaemonSelfReader; resolvers are thin projections (R3).
type Resolver struct {
	svc DaemonSelfReader
}

// NewResolver constructs a Resolver. svc must not be nil.
func NewResolver(svc DaemonSelfReader) *Resolver {
	return &Resolver{svc: svc}
}

// --- Health field resolvers ---

// HealthResolver projects a HealthSnapshot onto its GraphQL fields.
// Each method is a resolver for one scalar field (R6, R3).
type HealthResolver struct{ r *Resolver }

// HealthStatus resolves Health.status.
func (h *HealthResolver) Status(_ context.Context, snap *HealthSnapshot) (string, error) {
	return snap.Status, nil
}

// UptimeS resolves Health.uptimeS.
func (h *HealthResolver) UptimeS(_ context.Context, snap *HealthSnapshot) (int, error) {
	return snap.UptimeS, nil
}

// --- Query resolvers ---

// QueryResolver exposes the four Query fields owned by this domain.
type QueryResolver struct{ r *Resolver }

// Health resolves Query.health. Returns a liveness snapshot.
// Pure in-process computation (L4) — no shellout, no DataLoader needed.
func (q *QueryResolver) Health(_ context.Context) (*HealthSnapshot, error) {
	snap := q.r.svc.Health()
	return &snap, nil
}

// Version resolves Query.version.
func (q *QueryResolver) Version(_ context.Context) (string, error) {
	return q.r.svc.Version(), nil
}

// SchemaSDL resolves Query.schemaSDL (#469 F10).
// Returns the daemon's embedded schema as a single string.
func (q *QueryResolver) SchemaSDL(_ context.Context) (string, error) {
	return q.r.svc.SchemaSDL(), nil
}

// Node resolves Query.node(id). Dispatches through the NodeRegistry
// (daemon/node.go at the shell). Returns (nil, nil) for
// well-formed-but-not-found ids — the schema declares the return type
// as nullable Node.
func (q *QueryResolver) Node(ctx context.Context, id string) (Node, error) {
	reg := q.r.svc.Registry()
	if reg == nil {
		// Graceful degradation on minimal daemons (no registry wired).
		return nil, nil
	}
	return reg.Resolve(ctx, id)
}
