// Package git is the git domain module for the orchard daemon.
//
// It owns the Repo and Worktree GraphQL types, the repos query,
// the worktreeChanged subscription, the git(worktreeId, args) pass-through,
// and the worktreeCreate / worktreeRemove / worktreeMove / fetch / pull / push
// mutations. All mutations exec the corresponding scripts/git-<op>.sh per L5.
//
// Cross-domain fields on Worktree (processes, tmuxPanes, tmuxSession,
// claudeInstances, pr, issue) are declared in schema.graphql under
// extend type Worktree and resolved here via consumer-owned interfaces
// (R4, R5, S15b).
//
// Consumers import only this package's Service interface — never provider.go,
// adapter.go, or watcher.go directly (R2).
package git

import (
	"context"
	"fmt"
	"log/slog"
)

// Service is the ONLY API consumers of this domain may call (R2).
// Resolvers, loaders, and other domain modules import this interface
// and depend on its narrow surface — never on the concrete Provider.
type Service interface {
	// ListRepos returns all configured repos.
	ListRepos(ctx context.Context) ([]Repo, error)

	// GetRepo returns a single repo by its ID.
	GetRepo(ctx context.Context, id RepoID) (Repo, error)

	// ListWorktrees returns all worktrees for a given project / repo ID.
	ListWorktrees(ctx context.Context, projectID string) ([]Worktree, error)

	// GetWorktree returns a single worktree by its ID.
	GetWorktree(ctx context.Context, id WorktreeID) (Worktree, error)

	// Subscribe returns a channel that emits WorktreeID values whenever
	// a worktree's state may have changed. Channel closes when ctx is done.
	Subscribe(ctx context.Context) <-chan WorktreeInvalidation
}

// WorktreeInvalidation is emitted by the subscription channel.
type WorktreeInvalidation struct {
	WorktreeID WorktreeID
	ProjectID  string
	Reason     string
}

// service is the concrete implementation of Service. It wraps the git
// Provider (for worktrees) and the config/discovery layer (for repos).
type service struct {
	provider  *Provider
	repoStore RepoStore
	logger    *slog.Logger
}

// RepoStore is the narrow read interface the service needs to list repos.
// Satisfied by the configProvider wrapper in config_provider.go.
type RepoStore interface {
	List(ctx context.Context) ([]Repo, error)
	Get(ctx context.Context, id RepoID) (Repo, error)
}

// NewService wires a Provider and RepoStore into the Service implementation.
// This is the only constructor consumers should call at daemon bootstrap.
func NewService(p *Provider, rs RepoStore, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &service{provider: p, repoStore: rs, logger: logger}
}

func (s *service) ListRepos(ctx context.Context) ([]Repo, error) {
	repos, err := s.repoStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("git: list repos: %w", err)
	}
	return repos, nil
}

func (s *service) GetRepo(ctx context.Context, id RepoID) (Repo, error) {
	repo, err := s.repoStore.Get(ctx, id)
	if err != nil {
		return Repo{}, fmt.Errorf("git: get repo %q: %w", id, err)
	}
	return repo, nil
}

func (s *service) ListWorktrees(ctx context.Context, projectID string) ([]Worktree, error) {
	wts, err := s.provider.ListByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("git: list worktrees for project %q: %w", projectID, err)
	}
	return wts, nil
}

func (s *service) GetWorktree(ctx context.Context, id WorktreeID) (Worktree, error) {
	wt, _, err := s.provider.Get(ctx, id)
	if err != nil {
		return Worktree{}, fmt.Errorf("git: get worktree %q: %w", id, err)
	}
	return wt, nil
}

func (s *service) Subscribe(ctx context.Context) <-chan WorktreeInvalidation {
	raw := s.provider.Subscribe(ctx)
	out := make(chan WorktreeInvalidation, 32)
	go func() {
		defer close(out)
		for ev := range raw {
			projectID, _, ok := splitID(ev.Key)
			if !ok {
				projectID = ""
			}
			inv := WorktreeInvalidation{
				WorktreeID: ev.Key,
				ProjectID:  projectID,
				Reason:     ev.Reason,
			}
			select {
			case out <- inv:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
