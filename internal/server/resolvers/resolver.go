// Package resolvers wires the gqlgen-generated GraphQL surface to the
// providers under internal/server/providers.
package resolvers

import (
	"context"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/loaders"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeaccount"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
	configprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/config"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/contracts"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/gh"
	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/host"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/hostservice"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// ReposLister is the narrow read-side contract the repo resolver depends on.
type ReposLister interface {
	List(ctx context.Context) ([]configprovider.Repo, error)
}

// Resolver is the dependency-injection root for GraphQL resolvers.
type Resolver struct {
	StartedAt time.Time
	// DaemonVersion is the binary version, injected at boot from the
	// -ldflags -X main.version=<semver> bake. Named DaemonVersion (not
	// Version) to avoid shadowing the Query.Version resolver method on
	// the embedded queryResolver. Defaults to "dev" when no ldflags
	// were used (plain `go build`).
	DaemonVersion          string
	HostProvider           *host.Provider
	ReposProvider          ReposLister
	Git                    *gitprovider.Provider
	PS                     *ps.Provider
	Tmux                   *tmux.Provider
	ClaudeProjects         *claudeprojects.Provider
	ClaudeAccount       *claudeaccount.Provider
	HostServiceProvider *hostservice.Provider
	ContractsProvider   *contracts.Provider
	GH                  *gh.Provider
	PeerProxy              *peerproxy.Provider
	LocalEvents            *peerproxy.LocalInvalidator
}

// New constructs a Resolver with the daemon's start time captured.
// DaemonVersion defaults to "dev"; call WithVersion to inject a build-time semver.
func New(startedAt time.Time) *Resolver {
	return &Resolver{StartedAt: startedAt, DaemonVersion: "dev"}
}

// WithVersion injects the daemon binary version into the resolver so
// Query.version can surface it. The value is set by -ldflags at release
// time; callers pass the package-level `version` variable from main.
func (r *Resolver) WithVersion(v string) *Resolver {
	r.DaemonVersion = v
	return r
}

// WithHost wires the host provider.
func (r *Resolver) WithHost(h *host.Provider) *Resolver {
	r.HostProvider = h
	return r
}

// WithRepos wires the repos-listing dependency.
func (r *Resolver) WithRepos(p ReposLister) *Resolver {
	r.ReposProvider = p
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

// WithHostService wires the hostservice provider.
func (r *Resolver) WithHostService(p *hostservice.Provider) *Resolver {
	r.HostServiceProvider = p
	return r
}

// WithContracts wires the contracts provider.
func (r *Resolver) WithContracts(p *contracts.Provider) *Resolver {
	r.ContractsProvider = p
	return r
}

// WithGH wires the gh provider.
func (r *Resolver) WithGH(p *gh.Provider) *Resolver {
	r.GH = p
	return r
}

// WithPeerProxy wires the federation provider that backs Host.peers,
// Subscription.peer, and the node-id forwarder behind Query.node.
func (r *Resolver) WithPeerProxy(p *peerproxy.Provider) *Resolver {
	r.PeerProxy = p
	return r
}

// WithLocalEvents wires the local-invalidation broker. When set, the
// `Subscription.peer(host: "*")` resolver streams local events out
// over the federation surface — this is what upstream peers
// subscribe to via their peerproxy adapter.
func (r *Resolver) WithLocalEvents(l *peerproxy.LocalInvalidator) *Resolver {
	r.LocalEvents = l
	return r
}

// LoaderBundle returns the read-side surface the request-scoped
// dataloaders need. Used as a fallback when no middleware-installed
// Loaders is on the context (e.g. internal subscription emissions).
func (r *Resolver) LoaderBundle() *loaders.ProvidersBundle {
	return &loaders.ProvidersBundle{
		Host:       r.HostProvider,
		Git:        r.Git,
		Ps:         r.PS,
		Tmux:       r.Tmux,
		Repos:      r.ReposProvider,
		GH:         r.GH,
		GHEnricher: r.GH,
	}
}
