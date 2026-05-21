package contracts

import (
	"os"
	"path/filepath"
)

// EnvProjectsDir is the environment variable that overrides the session
// JSONL projects root. Set CLAUDE_PROJECTS_DIR=/abs/path to point the
// ContractFold at a non-default directory.
const EnvProjectsDir = "CLAUDE_PROJECTS_DIR"

// DefaultProjectsDir returns the directory the contracts provider scans
// for session JSONL files. Layout:
//
//	<root>/<encoded-cwd>/<session-uuid>.jsonl
//
// Resolution order:
//
//  1. $CLAUDE_PROJECTS_DIR, if non-empty.
//  2. $HOME/.claude/projects — the directory Claude Code writes session
//     JSONL files to on every machine.
//  3. ./projects as a last resort when $HOME is unresolvable.
func DefaultProjectsDir() string {
	if override := os.Getenv(EnvProjectsDir); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "projects")
	}
	return "projects"
}
