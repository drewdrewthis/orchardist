// Package contracts implements the read-only Contract provider — the
// orchard daemon's reflection of the tool_use event blocks that the
// claude-contracts plugin writes into session JSONL files.
//
// Per ADR-011 §5.1 and §11, orchard never writes contracts. The
// claude-contracts plugin (or any writer that respects the same session
// JSONL schema) appends open_contract and close_contract tool_use blocks
// to ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl; this provider
// tails those files, folds the blocks into the current contract state, and
// surfaces the result via the Contract node.
//
// # Why a fold
//
// The session JSONL is append-only by design — every open and close is its
// own block. The folded current state is therefore *derived*, not stored.
// Computing it from the session records keeps the plugin as the only
// authority and lets orchard re-derive from scratch any time the cache is cold.
//
// # Layering
//
// Mirrors the host and claudeprojects sibling providers:
//
//   - [ProjectsAdapter] does raw I/O — scans the projects tree of session
//     JSONL files, returns [ProjectsRecord] slices. No state, no caching.
//   - [Provider] holds the fold result, exposes the [adapter.Provider]
//     surface, and runs the watcher loop.
//   - [ProjectsWatcher] uses fsnotify on the two-level projects tree so
//     new project directories and new session files are detected without
//     polling.
//   - [FoldProjectsRecords] is a pure function — exhaustively unit-tested
//     without touching the filesystem.
//
// # Configurable projects directory
//
// The default location is $HOME/.claude/projects — the directory that
// Claude Code writes session JSONL files to. The CLAUDE_PROJECTS_DIR
// environment variable overrides the default for ad-hoc runs and tests.
package contracts
