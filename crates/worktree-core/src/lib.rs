//! Core git worktree operations.
//!
//! This crate is the single source of truth for worktree mutations. It backs
//! the `orchard-worktree` CLI binary (added in a later PR) and the `orchard-tui`
//! dialogs. The TUI no longer owns worktree mutation logic — it collects user
//! intent and calls into this library.
//!
//! # Scope
//!
//! - `list` — list all worktrees in the current repo, parse porcelain output,
//!   detect merge conflicts.
//! - `create` — `git worktree add` for a new branch, with fall-through to
//!   checkout-existing.
//! - `destroy` — `git worktree remove`, with fallback to `rm -rf` + `prune`
//!   when the worktree is in a broken state.
//! - `repo` — repo-root resolution (`git rev-parse --show-toplevel`) and
//!   repo-name derivation.
//!
//! Higher-level concerns — tmux session management, remote SSH dispatch,
//! GitHub PR/issue linkage, setup-script execution — live in the consuming
//! binaries (`orchard-tui`, `orchard-worktree`). This library is local-first
//! and pure-ish: every public function shells out to `git` and nothing else.
//!
//! # Type model
//!
//! [`WorktreeEntry`] carries only the fields that come from `git worktree
//! list --porcelain`. Higher layers (orchard) wrap or convert this into their
//! own enriched types that join PR/issue/tmux data.

pub mod create;
pub mod destroy;
pub mod list;
pub mod repo;

pub use create::{CreateOutcome, create_worktree};
pub use destroy::remove_worktree;
pub use list::{
    WorktreeEntry, list_worktrees, parse_porcelain, worktree_has_conflicts,
};
pub use repo::{find_repo_root, get_repo_name};
