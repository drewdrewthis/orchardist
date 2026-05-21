// crossdomain.go — Consumer interfaces for cross-domain fields on Worktree.
//
// Per R4 (ISP) and S15b, when this domain (git) adds a field to Worktree
// whose data is owned by another domain, it defines the narrow interface
// it needs here — in its own module. The consuming resolver (resolver_worktree.go)
// depends only on these interfaces, never on the owning domain's concrete types.
//
// The owning domain's service must implement these interfaces, but that
// binding happens at wire-up time in daemon/server.go — not here.
package git

import "context"

// PsReader is what the git domain needs from the ps domain (R4).
// Resolves Worktree.processes.
type PsReader interface {
	// ProcessesByCwd returns processes whose cwd lies under the given path.
	ProcessesByCwd(ctx context.Context, cwd string) ([]Process, error)
}

// Process is a minimal stub of the ps domain's Process type, sufficient
// for the Worktree.processes field projection. The full Process type
// lives in daemon/ps/. This stub prevents importing that package (R5).
// When the resolver projects this, it maps to the GraphQL Process type.
type Process struct {
	ID      string
	Pid     int
	Command string
	Args    []string
	Cwd     string
}

// TmuxReader is what the git domain needs from the tmux domain (R4).
// Resolves Worktree.tmuxPanes and Worktree.tmuxSession.
type TmuxReader interface {
	// PanesByCwd returns tmux panes whose foreground-process cwd lies
	// under the given path (exact match or path + '/' prefix, per #511).
	PanesByCwd(ctx context.Context, cwd string) ([]TmuxPane, error)

	// SessionByPaneID returns the tmux session containing the given pane.
	// Returns nil when the pane has no session.
	SessionByPaneID(ctx context.Context, paneID string) (*TmuxSession, error)
}

// TmuxPane is a minimal stub of the tmux domain's TmuxPane type.
type TmuxPane struct {
	ID        string
	SessionID string
	WindowID  string
	Index     int
	Command   string
	Cwd       string
}

// TmuxSession is a minimal stub of the tmux domain's TmuxSession type.
type TmuxSession struct {
	ID             string
	Name           string
	LastActivityAt string
}

// ClaudeReader is what the git domain needs from the claude-instance domain (R4).
// Resolves Worktree.claudeInstances.
type ClaudeReader interface {
	// InstancesByCwd returns Claude REPL instances whose cwd lies under
	// the given path.
	InstancesByCwd(ctx context.Context, cwd string) ([]ClaudeInstance, error)
}

// ClaudeInstance is a minimal stub of the claude-instance domain's type.
type ClaudeInstance struct {
	ID          string
	SessionUUID string
	Cwd         string
	State       string
}

// GhReader is what the git domain needs from the gh domain (R4).
// Resolves Worktree.pr and Worktree.issue.
type GhReader interface {
	// PRByBranch returns the most-relevant PR for the given repo+branch.
	// Returns nil when no PR exists. Precedence: open PR > most-recent
	// closed/merged PR (per #489 and schema.graphql doc comment).
	PRByBranch(ctx context.Context, repoSlug, branch string) (*PullRequest, error)

	// IssueByBranch returns the issue linked from the branch name
	// (issue<N>/... convention). Returns nil when no issue is linked.
	IssueByBranch(ctx context.Context, repoSlug, branch string) (*Issue, error)
}

// PullRequest is a minimal stub of the gh domain's PullRequest type.
type PullRequest struct {
	ID     string
	Number int
	Title  string
	State  string
	URL    string
}

// Issue is a minimal stub of the gh domain's Issue type.
type Issue struct {
	ID     string
	Number int
	Title  string
	State  string
	URL    string
}

// NopPsReader is a no-op PsReader for daemon startup before the ps
// domain is wired. Returns an empty slice.
type NopPsReader struct{}

func (NopPsReader) ProcessesByCwd(_ context.Context, _ string) ([]Process, error) {
	return nil, nil
}

// NopTmuxReader is a no-op TmuxReader.
type NopTmuxReader struct{}

func (NopTmuxReader) PanesByCwd(_ context.Context, _ string) ([]TmuxPane, error) {
	return nil, nil
}
func (NopTmuxReader) SessionByPaneID(_ context.Context, _ string) (*TmuxSession, error) {
	return nil, nil
}

// NopClaudeReader is a no-op ClaudeReader.
type NopClaudeReader struct{}

func (NopClaudeReader) InstancesByCwd(_ context.Context, _ string) ([]ClaudeInstance, error) {
	return nil, nil
}

// NopGhReader is a no-op GhReader.
type NopGhReader struct{}

func (NopGhReader) PRByBranch(_ context.Context, _, _ string) (*PullRequest, error) { return nil, nil }
func (NopGhReader) IssueByBranch(_ context.Context, _, _ string) (*Issue, error)     { return nil, nil }
