// Package resolvers wires the gqlgen-generated GraphQL surface to the
// providers under internal/server/providers. One file per node type
// hangs off this Resolver root; the root holds dependencies the field
// resolvers need.
package resolvers

import (
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// Resolver is the dependency-injection root for GraphQL resolvers.
//
// gqlgen does NOT regenerate this file, so anything we wire here survives
// schema iteration. The pattern: every provider added by a workstream
// gets a field on this struct, and the field resolvers reach for it.
type Resolver struct {
	// StartedAt powers Health.uptimeS. Captured at daemon boot.
	StartedAt time.Time

	// PS surfaces the Process node and Host.processes. Optional only so
	// older tests (Workstream A) that constructed a Resolver{} in
	// isolation keep compiling; production Resolver always has it set.
	PS *ps.Provider
}

// New constructs a Resolver with the daemon's start time captured. The
// caller (the daemon entry point) calls this once at boot.
func New(startedAt time.Time) *Resolver {
	return &Resolver{StartedAt: startedAt}
}

// WithPS attaches a ps provider. Returns the receiver for chaining at
// the daemon entry point.
func (r *Resolver) WithPS(p *ps.Provider) *Resolver {
	r.PS = p
	return r
}
