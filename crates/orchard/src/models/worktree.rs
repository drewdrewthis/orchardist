//! Canonical worktree type, collapsing CachedWorktree / WorktreeRow / WorktreeState / JsonWorktree.
use serde::{Deserialize, Serialize};

use crate::models::check::DisplayGroup;
use crate::models::issue::Issue;
use crate::models::pr::Pr;
use crate::models::session::Session;

/// Ahead/behind commit counts relative to the upstream branch.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AheadBehind {
    /// Commits ahead of upstream.
    pub ahead: u32,
    /// Commits behind upstream.
    pub behind: u32,
}

/// Canonical git worktree, used from cache layer through to JSON output.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Worktree {
    /// Absolute path to the worktree root.
    pub path: String,
    /// Branch checked out in this worktree.
    pub branch: String,
    /// Whether this is the bare (`.git`) worktree.
    pub is_bare: bool,
    /// Remote host identifier; `None` means the worktree is local.
    pub host: Option<String>,
    /// Whether this is the repo's main worktree (not an additional linked worktree).
    pub is_main_worktree: bool,
    /// Commit distance from upstream, if available.
    pub ahead_behind: Option<AheadBehind>,
    /// ISO 8601 timestamp of the most recent commit in this worktree.
    pub last_commit_at: Option<String>,
    /// Linked GitHub issue, if resolved.
    pub issue: Option<Issue>,
    /// Linked GitHub PR, if resolved.
    pub pr: Option<Pr>,
    /// Active tmux sessions rooted at this worktree's path.
    #[serde(default)]
    pub sessions: Vec<Session>,
    /// TUI sort group derived from joined data at build time. Not stored in cache.
    #[serde(skip_deserializing)]
    pub display_group: DisplayGroup,
}
