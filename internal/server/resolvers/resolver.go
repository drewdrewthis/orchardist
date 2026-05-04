// Package resolvers wires the gqlgen-generated GraphQL surface to the
// providers under internal/server/providers.
package resolvers

import (
	"context"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// ProjectsLister is the narrow read-side contract the project resolver depends on.
type ProjectsLister interface {
	List(ctx context.Context) ([]configprovider.Project, error)
}

// Resolver is the dependency-injection root for GraphQL resolvers.
type Resolver struct {
	StartedAt        time.Time
	HostProvider     *host.Provider
	ProjectsProvider ProjectsLister
	Git              *gitprovider.Provider
	PS               *ps.Provider
	Tmux             *tmux.Provider
	ClaudeProjects   *claudeprojects.Provider
	ClaudeAccount    *claudeaccount.Provider
}

// New constructs a Resolver with the daemon's start time captured.
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

// WithGit wires the git provider.
func (r *Resolver) WithGit(g *gitprovider.Provider) *Resolver {
	r.Git = g
	return r
}

// WithPS wires the ps provider.
func (r *Resolver) WithPS(p *ps.Provider) *Resolver {
	r.PS = p
	return r
}

// WithTmux wires the tmux provider.
func (r *Resolver) WithTmux(p *tmux.Provider) *Resolver {
	r.Tmux = p
	return r
}

// WithClaudeProjects wires the claudeprojects provider.
func (r *Resolver) WithClaudeProjects(p *claudeprojects.Provider) *Resolver {
	r.ClaudeProjects = p
	return r
}

// WithClaudeAccount wires the claudeaccount provider.
func (r *Resolver) WithClaudeAccount(p *claudeaccount.Provider) *Resolver {
	r.ClaudeAccount = p
	return r
}
