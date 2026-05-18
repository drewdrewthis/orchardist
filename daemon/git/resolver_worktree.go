// resolver_worktree.go — Resolvers for the Worktree GraphQL type (R6: one type per file).
//
// Each method corresponds to one Worktree field. Scalar fields are
// trivial projections; cross-domain fields delegate to the consumer
// interfaces defined in crossdomain.go (R4, R5, S15b).
//
// All reads go through the WorktreeLoader (R3: no Snapshot() in field resolvers).
package git

import "context"

// WorktreeResolver holds the loader and cross-domain dependencies for
// resolving Worktree fields. Consumers wire this at server build time.
type WorktreeResolver struct {
	loader  *WorktreeLoader
	psR     PsReader
	tmuxR   TmuxReader
	claudeR ClaudeReader
	ghR     GhReader
}

// NewWorktreeResolver creates a resolver with the given dependencies.
func NewWorktreeResolver(
	loader *WorktreeLoader,
	ps PsReader,
	tmux TmuxReader,
	claude ClaudeReader,
	gh GhReader,
) *WorktreeResolver {
	return &WorktreeResolver{
		loader:  loader,
		psR:     ps,
		tmuxR:   tmux,
		claudeR: claude,
		ghR:     gh,
	}
}

// WorktreeID resolves Worktree.id.
func (r *WorktreeResolver) WorktreeID(_ context.Context, wt Worktree) (string, error) {
	return string(wt.ID), nil
}

// WorktreePath resolves Worktree.path.
func (r *WorktreeResolver) WorktreePath(_ context.Context, wt Worktree) (string, error) {
	return wt.Path, nil
}

// WorktreeBranch resolves Worktree.branch.
func (r *WorktreeResolver) WorktreeBranch(_ context.Context, wt Worktree) (string, error) {
	return wt.Branch, nil
}

// WorktreeHead resolves Worktree.head.
func (r *WorktreeResolver) WorktreeHead(_ context.Context, wt Worktree) (string, error) {
	return wt.Head, nil
}

// WorktreeBare resolves Worktree.bare.
func (r *WorktreeResolver) WorktreeBare(_ context.Context, wt Worktree) (bool, error) {
	return wt.Bare, nil
}

// WorktreeHost resolves Worktree.host — always "local" in v1.
func (r *WorktreeResolver) WorktreeHost(_ context.Context, _ Worktree) (string, error) {
	return "local", nil
}

// WorktreeRepo resolves Worktree.repo — owner/repo slug derived from origin remote.
// Returns nil when origin is not a GitHub URL.
func (r *WorktreeResolver) WorktreeRepo(_ context.Context, wt Worktree) (*string, error) {
	return wt.RepoSlug, nil
}

// WorktreeAhead resolves Worktree.ahead.
func (r *WorktreeResolver) WorktreeAhead(_ context.Context, wt Worktree) (*int, error) {
	if wt.Ahead == nil {
		return nil, nil
	}
	v := int(*wt.Ahead)
	return &v, nil
}

// WorktreeBehind resolves Worktree.behind.
func (r *WorktreeResolver) WorktreeBehind(_ context.Context, wt Worktree) (*int, error) {
	if wt.Behind == nil {
		return nil, nil
	}
	v := int(*wt.Behind)
	return &v, nil
}

// --- Cross-domain fields (S15b: declared in this domain's partial, resolved here) ---

// WorktreeProcesses resolves Worktree.processes.
// Calls PsReader.ProcessesByCwd — the ps domain's service interface (R4, R5).
func (r *WorktreeResolver) WorktreeProcesses(ctx context.Context, wt Worktree) ([]Process, error) {
	return r.psR.ProcessesByCwd(ctx, wt.Path)
}

// WorktreeTmuxPanes resolves Worktree.tmuxPanes.
// Calls TmuxReader.PanesByCwd — the tmux domain's service interface (R4, R5).
func (r *WorktreeResolver) WorktreeTmuxPanes(ctx context.Context, wt Worktree) ([]TmuxPane, error) {
	return r.tmuxR.PanesByCwd(ctx, wt.Path)
}

// WorktreeTmuxSession resolves Worktree.tmuxSession — the most-recently-active
// tmux session among the panes returned by tmuxPanes (per schema.graphql doc).
// Calls TmuxReader.PanesByCwd then TmuxReader.SessionByPaneID on the first pane.
func (r *WorktreeResolver) WorktreeTmuxSession(ctx context.Context, wt Worktree) (*TmuxSession, error) {
	panes, err := r.tmuxR.PanesByCwd(ctx, wt.Path)
	if err != nil || len(panes) == 0 {
		return nil, err
	}
	// Use the first pane; the tmux service applies the lastActivityAt ordering.
	return r.tmuxR.SessionByPaneID(ctx, panes[0].ID)
}

// WorktreeClaudeInstances resolves Worktree.claudeInstances.
// Calls ClaudeReader.InstancesByCwd — the claude-instance domain's service interface.
func (r *WorktreeResolver) WorktreeClaudeInstances(ctx context.Context, wt Worktree) ([]ClaudeInstance, error) {
	return r.claudeR.InstancesByCwd(ctx, wt.Path)
}

// WorktreePR resolves Worktree.pr.
// Calls GhReader.PRByBranch — the gh domain's service interface (R4, R5).
func (r *WorktreeResolver) WorktreePR(ctx context.Context, wt Worktree) (*PullRequest, error) {
	if wt.Branch == "" || wt.RepoSlug == nil {
		return nil, nil
	}
	return r.ghR.PRByBranch(ctx, *wt.RepoSlug, wt.Branch)
}

// WorktreeIssue resolves Worktree.issue.
// Calls GhReader.IssueByBranch — the gh domain's service interface (R4, R5).
func (r *WorktreeResolver) WorktreeIssue(ctx context.Context, wt Worktree) (*Issue, error) {
	if wt.Branch == "" || wt.RepoSlug == nil {
		return nil, nil
	}
	return r.ghR.IssueByBranch(ctx, *wt.RepoSlug, wt.Branch)
}
