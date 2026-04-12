//! Canonical CI check types and display group classification.
//!
//! Re-exports `CheckInfo` and `CiChecks` from `ci_state` for use through
//! the models module. Defines `DisplayGroup` as the canonical type — the
//! identical definition in `derive.rs` will be removed in a later phase.
use serde::{Deserialize, Serialize};

// Re-export CI check types from their authoritative location.
pub use crate::ci_state::{CheckInfo, CiChecks};

/// Rendering order for worktree rows in the TUI list view.
///
/// Variants are ordered so that `Ord` gives the correct sort order
/// (`RepoMain` first, `Other` last). `Default` is `Other` so that
/// `#[serde(skip_deserializing)]` on `Worktree.display_group` produces a
/// valid zero-cost placeholder before join-time computation fills it in.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Default, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DisplayGroup {
    /// Always first — the repo's main worktree.
    RepoMain,
    /// User-flagged as priority work.
    Prioritized,
    /// Requires human action (blocked, conflicts, review requested).
    NeedsAttention,
    /// A Claude session is actively working in this worktree.
    ClaudeWorking,
    /// PR is approved and checks pass — ready to merge.
    ReadyToMerge,
    /// Worktrees without PRs or other misc work. Default placeholder value.
    #[default]
    Other,
}
