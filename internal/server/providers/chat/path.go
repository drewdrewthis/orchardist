package chat

import (
	"os"
	"path/filepath"
)

// EnvDir is the environment variable that overrides the chat directory.
// Set ORCHARD_CHAT_DIR=/abs/path to point the daemon at a non-default
// directory of per-room JSONL files (used by tests + per-machine
// experiments). Mirrors chat-core's identically named env var.
const EnvDir = "ORCHARD_CHAT_DIR"

// DefaultDir returns the directory the chat provider scans when no
// override is configured. One JSONL file per room: `<dir>/<room>.jsonl`.
//
// Resolution order:
//
//  1. $ORCHARD_CHAT_DIR, if non-empty.
//  2. $HOME/.orchard/chat — the path chat-core writes to (per #495).
//  3. ./chat as a last resort when $HOME is unresolvable.
//
// The provider does NOT create the directory or its contents — that
// responsibility belongs to chat-core. The provider tolerates the
// directory being missing (returns an empty room list, no error).
func DefaultDir() string {
	if v := os.Getenv(EnvDir); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".orchard", "chat")
	}
	return "chat"
}
