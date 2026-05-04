package resolvers

import (
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
)

// Resolver is the dependency-injection root for GraphQL resolvers.
//
// gqlgen does NOT regenerate this file, so anything we wire here survives
// schema iteration. Workstream A only needed StartedAt for the health
// resolver; Workstream B-host adds the host Provider; later workstreams
// hang their providers off this struct alongside.
type Resolver struct {
	StartedAt    time.Time
	HostProvider *host.Provider
}

// New constructs a Resolver with the daemon's start time and the host
// Provider. The caller (the daemon entry point) calls this once at boot.
func New(startedAt time.Time, h *host.Provider) *Resolver {
	return &Resolver{StartedAt: startedAt, HostProvider: h}
}
