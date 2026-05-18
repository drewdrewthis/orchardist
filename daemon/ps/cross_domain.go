package ps

import "context"

// GitService is the narrow interface this domain needs from the git domain
// (R4 ISP — the consumer defines the interface in its own module).
// Used by Process.worktree to find the deepest worktree whose path
// prefixes the process's resolved cwd.
type GitService interface {
	// WorktreeByPath returns the worktree whose path is the longest
	// prefix of the given directory path, or nil if none matches.
	WorktreeByPath(ctx context.Context, path string) (*WorktreeRef, error)
}

// WorktreeRef is the minimal worktree shape this domain needs to return
// Process.worktree. Resolvers project this onto the gqlgen Worktree type.
// We use a local ref type rather than importing the gqlgen-generated
// graphql package directly — keeps the domain package free of codegen deps.
type WorktreeRef struct {
	ID   string
	Path string
}

// ClaudeInstanceService is the narrow interface this domain needs from the
// claude-instance domain (R4 ISP). Used by Process.claudeInstance to
// surface the live Claude REPL that owns this pid.
type ClaudeInstanceService interface {
	// InstanceByPID returns the ClaudeInstance that has this pid as its
	// foreground claude process, or nil if none.
	InstanceByPID(ctx context.Context, pid int) (*ClaudeInstanceRef, error)
}

// ClaudeInstanceRef is the minimal shape needed to return Process.claudeInstance.
type ClaudeInstanceRef struct {
	ID string
}
