package resolvers

import (
	"context"
	"time"

	gitprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/git"
)

// ProjectsSource is the read-side handle the resolver layer uses to
// list projects. Workstream B-config owns the real implementation
// (config-file backed); ws-b-git defines the interface so the git
// provider can resolve `Project.worktrees` without depending on the
// config provider's concrete type.
//
// Tests use [StaticProjectsSource]; production hands in the config
// provider once ws-b-config lands.
type ProjectsSource interface {
	List(ctx context.Context) ([]ProjectRecord, error)
}

// ProjectRecord is the minimal projection of a Project node the resolvers
// need. Wider fields (per ADR-011 §5.1) live on the GraphQL Project
// type and are populated as additional providers fill them in.
type ProjectRecord struct {
	ID        string
	Directory string
	Name      string
}

// Resolver is the dependency-injection root for GraphQL resolvers.
//
// gqlgen does NOT regenerate this file, so anything we wire here
// survives schema iteration. Each workstream hangs its providers off
// this struct; resolvers depend on Provider/Adapter interfaces only,
// never concrete types (ADR-011 §6 / SOLID-D).
//
// Field naming: the embedded queryResolver gains methods that match
// GraphQL field names (`Projects`, `Health`, …). To avoid the resolver
// method shadowing a wired-in source we suffix struct fields with their
// role — `ProjectsSrc`, not `Projects`.
type Resolver struct {
	StartedAt   time.Time
	ProjectsSrc ProjectsSource
	Git         *gitprovider.Provider
}

// New constructs a Resolver with the daemon's start time captured. The
// caller (the daemon entry point) calls this once at boot. Providers
// default to nil and resolve to per-field GraphQL errors when the
// owning workstream hasn't wired them yet.
func New(startedAt time.Time) *Resolver {
	return &Resolver{StartedAt: startedAt}
}

// StaticProjectsSource is a fixture-grade ProjectsSource for tests and
// for the daemon's bootstrap path until ws-b-config ships. It returns
// a copy of its slice on every call.
type StaticProjectsSource struct {
	Records []ProjectRecord
}

// List implements ProjectsSource.
func (s *StaticProjectsSource) List(_ context.Context) ([]ProjectRecord, error) {
	out := make([]ProjectRecord, len(s.Records))
	copy(out, s.Records)
	return out, nil
}
