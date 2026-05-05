// Package contracts implements the read-only Contract provider — the
// orchard daemon's reflection of the JSONL event log that the
// claude-contracts plugin authors.
//
// Per ADR-011 §5.1 and §11, orchard never writes contracts. The
// claude-contracts plugin (or any writer that respects the same JSONL
// schema) appends events to a log on disk; this provider tails the
// file, folds events into the current contract state, and surfaces
// the result via the Contract node.
//
// # Why a fold
//
// The plugin's log is append-only by design — every status change,
// criterion addition, question, and cancel ack is its own line. The
// folded current state is therefore *derived*, not stored. Computing
// it once on disk would couple two writers; computing it from events
// keeps the plugin as the only authority and lets orchard re-derive
// from scratch any time the cache is cold.
//
// # Layering
//
// Mirrors the host and claudeprojects sibling providers:
//
//   - [Adapter] does raw I/O — scans the directory of per-contract
//     jsonl files, returns events. No state, no caching.
//   - [Provider] holds the fold result, exposes the [adapter.Provider]
//     surface, and runs the watcher loop.
//   - [Watcher] uses fsnotify on the directory (always) and on each
//     `.jsonl` file under it, so a fresh install picks up the
//     directory's first creation without polling.
//   - [Fold] is a pure function — exhaustively unit-tested without
//     touching the filesystem.
//
// # Configurable log directory
//
// The default location is $HOME/.claude/contracts — the directory
// that the claude-contracts plugin writes per-contract jsonl files
// to. The CLAUDE_CONTRACTS_DIR environment variable overrides the
// default for ad-hoc runs and tests.
package contracts
