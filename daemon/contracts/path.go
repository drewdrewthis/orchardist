package contracts

import (
	"os"
	"path/filepath"
)

// EnvLogDir is the environment variable that overrides the log directory.
// Set CLAUDE_CONTRACTS_DIR=/abs/path to point the daemon at a non-default
// directory of per-contract jsonl files (useful for tests + per-machine
// experiments).
const EnvLogDir = "CLAUDE_CONTRACTS_DIR"

// DefaultLogDir returns the directory the contracts provider scans when no
// override is configured. Each contract is one file inside the directory:
// `<dir>/<contract-id>.jsonl`.
//
// Resolution order:
//  1. $CLAUDE_CONTRACTS_DIR, if non-empty.
//  2. $HOME/.claude/contracts — the path the claude-contracts plugin writes to.
//  3. ./contracts as a last resort when $HOME is unresolvable.
func DefaultLogDir() string {
	if override := os.Getenv(EnvLogDir); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".claude", "contracts")
	}
	return "contracts"
}
