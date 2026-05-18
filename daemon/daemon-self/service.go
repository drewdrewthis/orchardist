// Package daemonself owns daemon liveness, version, embedded schemaSDL,
// and the Query.node entry point.
//
// This is a leaf domain — it has no dependencies on other daemon domains.
// The only external dependency is the NodeRegistry interface (satisfied by
// daemon/node.go at the shell layer), which is injected at construction.
//
// Rules: R1, R2, R4, R9, R11, L4, L10, S15a.
package daemonself

import (
	"context"
	"time"
)

// Node is the GraphQL Node interface value returned from Query.node.
// Redeclared here (not imported from daemon/graphql) so the domain
// compiles standalone without depending on generated code per R4 ISP.
type Node interface {
	IsNode()
}

// NodeRegistry is the consumer-defined interface (R4) that the shell's
// daemon/node.go satisfies. Implementations register per-type prefix
// lookups; Query.node dispatches through this registry.
type NodeRegistry interface {
	// Resolve parses id, dispatches to the matching domain lookup, and
	// returns the node or (nil, nil) for well-formed-but-not-found ids.
	// Context propagation (R9) is the caller's responsibility.
	Resolve(ctx context.Context, id string) (Node, error)
}

// HealthSnapshot holds a point-in-time view of daemon liveness.
// All fields are cheaply derived from the daemon's start time.
type HealthSnapshot struct {
	// Status is "ok" when the daemon is serving.
	Status string
	// UptimeS is seconds elapsed since the daemon started.
	UptimeS int
}

// DaemonSelfReader is the narrow service interface (R2) that resolvers
// may depend on. Defined here so consumers import only what they need.
type DaemonSelfReader interface {
	// Health returns a liveness snapshot.
	Health() HealthSnapshot
	// Version returns the daemon binary version baked in at build time.
	// Returns "dev" when no -ldflags were used.
	Version() string
	// SchemaSDL returns the daemon's embedded schema as a single string.
	// The bytes are baked in at build time via go:embed; they reflect
	// whatever schema the running binary was compiled against.
	SchemaSDL() string
	// Registry returns the node-prefix dispatcher. May be nil on minimal
	// daemons; callers handle nil gracefully.
	Registry() NodeRegistry
}

// DaemonSelfService is the concrete implementation of DaemonSelfReader.
// Construct via New; do not embed or extend.
type DaemonSelfService struct {
	startedAt time.Time
	version   string
	schemaSDL string
	registry  NodeRegistry
}

// New constructs a DaemonSelfService.
//   - startedAt: when the daemon process started (used for uptimeS).
//   - version: binary version string (e.g. "1.4.2" or "dev").
//   - schemaSDL: the embedded schema source (concatenated partials).
//   - registry: the node-prefix dispatcher (may be nil; gracefully handled).
func New(startedAt time.Time, version, schemaSDL string, registry NodeRegistry) *DaemonSelfService {
	if version == "" {
		version = "dev"
	}
	return &DaemonSelfService{
		startedAt: startedAt,
		version:   version,
		schemaSDL: schemaSDL,
		registry:  registry,
	}
}

// Health satisfies DaemonSelfReader. Returns a fresh snapshot each call
// (uptime is computed from wall clock; no caching needed — it's O(1)).
func (s *DaemonSelfService) Health() HealthSnapshot {
	return HealthSnapshot{
		Status:  "ok",
		UptimeS: int(time.Since(s.startedAt).Seconds()),
	}
}

// Version satisfies DaemonSelfReader.
func (s *DaemonSelfService) Version() string { return s.version }

// SchemaSDL satisfies DaemonSelfReader.
func (s *DaemonSelfService) SchemaSDL() string { return s.schemaSDL }

// Registry satisfies DaemonSelfReader. Returns nil when no registry is wired.
func (s *DaemonSelfService) Registry() NodeRegistry { return s.registry }
