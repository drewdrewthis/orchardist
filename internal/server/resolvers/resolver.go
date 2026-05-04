// Package resolvers wires the gqlgen-generated GraphQL surface to the
// providers under internal/server/providers. One file per node type
// hangs off this Resolver root; the root holds dependencies the field
// resolvers need.
package resolvers

import (
	"context"
	"time"

	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// ProjectsLister is the narrow read-side contract the project resolver
// depends on. Defined here (consumer-side) so the resolver doesn't reach
// into the provider package's full surface — accept interfaces, return
// concretes.
type ProjectsLister interface {
	List(ctx context.Context) ([]configprovider.Project, error)
}

// Resolver is the dependency-injection root for GraphQL resolvers.
//
// gqlgen does NOT regenerate this file, so anything we wire here survives
// schema iteration. Provider fields are suffixed with `Provider` to
// avoid colliding with the generated field-resolver method names that
// embed this struct (e.g. queryResolver.Projects(ctx)). Optional
// dependencies are wired via With* setters so callers can swap
// implementations in tests.
type Resolver struct {
	StartedAt        time.Time
	HostProvider     *host.Provider
	ProjectsProvider ProjectsLister
	Git              *gitprovider.Provider
	PS               *ps.Provider
}

// New constructs a Resolver with the daemon's start time captured. The
// caller (the daemon entry point) calls this once at boot. Optional
// dependencies (the providers) are wired with the With* setters below
// so callers can swap implementations in tests.
func New(startedAt time.Time) *Resolver {
	return &Resolver{StartedAt: startedAt}
}

// WithHost wires the host provider.
func (r *Resolver) WithHost(h *host.Provider) *Resolver {
	r.HostProvider = h
	return r
}

// WithProjects wires the projects-listing dependency.
func (r *Resolver) WithProjects(p ProjectsLister) *Resolver {
	r.ProjectsProvider = p
	return r
}

// WithGit wires the git provider that backs Project.worktrees and
// Worktree.* resolvers.
func (r *Resolver) WithGit(g *gitprovider.Provider) *Resolver {
	r.Git = g
	return r
}

// WithPS wires the ps provider that backs Host.processes and Process
// node resolution.
func (r *Resolver) WithPS(p *ps.Provider) *Resolver {
	r.PS = p
	return r
}
