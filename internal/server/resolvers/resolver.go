package resolvers

import "time"

// Resolver is the dependency-injection root for GraphQL resolvers.
//
// gqlgen does NOT regenerate this file, so anything we wire here survives
// schema iteration. Workstream A only needs StartedAt for the health
// resolver; later workstreams hang their providers off this struct.
type Resolver struct {
	StartedAt time.Time
}

// New constructs a Resolver with the daemon's start time captured. The
// caller (the daemon entry point) calls this once at boot.
func New(startedAt time.Time) *Resolver {
	return &Resolver{StartedAt: startedAt}
}
