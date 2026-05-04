package contracts

import (
	"os"
	"path/filepath"
)

// EnvLogPath is the environment variable that overrides the log path.
// Set CLAUDE_CONTRACTS_LOG=/abs/path/contracts.jsonl to point the
// daemon at a non-default file (useful for tests + per-machine
// experiments).
const EnvLogPath = "CLAUDE_CONTRACTS_LOG"

// DefaultLogPath returns the path the contracts provider reads from
// when no override is configured.
//
// Resolution order:
//
//  1. $CLAUDE_CONTRACTS_LOG, if non-empty.
//  2. $XDG_STATE_HOME/claude-contracts/contracts.jsonl, if XDG_STATE_HOME
//     is set.
//  3. $HOME/.local/state/claude-contracts/contracts.jsonl as the
//     XDG fallback.
//  4. ./contracts.jsonl as a last resort when $HOME is unresolvable
//     (should never happen on a real workstation).
//
// Per the brief, the daemon never creates the file or its parent
// directory — that responsibility belongs to the writer. The provider
// tolerates the path being missing.
func DefaultLogPath() string {
	if override := os.Getenv(EnvLogPath); override != "" {
		return override
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "claude-contracts", "contracts.jsonl")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "claude-contracts", "contracts.jsonl")
	}
	return "contracts.jsonl"
}
