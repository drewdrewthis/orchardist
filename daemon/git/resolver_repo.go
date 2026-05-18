// resolver_repo.go — Resolvers for the Repo GraphQL type (R6: one type per file).
//
// Each method corresponds to one Repo field. All reads go through the
// WorktreesByProjectLoader (R3: no Snapshot() in field resolvers).
//
// This file also owns the Query.repos root resolver.
package git

import "context"

// RepoResolver holds the loader dependencies needed to resolve Repo fields.
// Consumers wire this at server build time.
type RepoResolver struct {
	svc                    Service
	worktreesByProjectLoad *WorktreesByProjectLoader
}

// NewRepoResolver creates a resolver backed by the service and loader.
func NewRepoResolver(svc Service, wpl *WorktreesByProjectLoader) *RepoResolver {
	return &RepoResolver{svc: svc, worktreesByProjectLoad: wpl}
}

// QueryRepos resolves Query.repos — returns all configured repos.
func (r *RepoResolver) QueryRepos(ctx context.Context) ([]Repo, error) {
	return r.svc.ListRepos(ctx)
}

// RepoID resolves Repo.id — stable identifier derived from slug.
func (r *RepoResolver) RepoID(_ context.Context, repo Repo) (string, error) {
	return string(repo.ID), nil
}

// RepoSlug resolves Repo.slug — GitHub-style owner/repo slug.
func (r *RepoResolver) RepoSlug(_ context.Context, repo Repo) (string, error) {
	return repo.Slug, nil
}

// RepoPath resolves Repo.path — absolute filesystem path.
func (r *RepoResolver) RepoPath(_ context.Context, repo Repo) (string, error) {
	return repo.Path, nil
}

// RepoWorktrees resolves Repo.worktrees — all worktrees for this repo.
// Uses WorktreesByProjectLoader (R3 — no Snapshot(), batches per O1).
func (r *RepoResolver) RepoWorktrees(ctx context.Context, repo Repo) ([]Worktree, error) {
	return r.worktreesByProjectLoad.Load(ctx, string(repo.ID))
}
