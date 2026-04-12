//! Canonical repo type, the top-level container in OrchardState.
use serde::{Deserialize, Serialize};

use crate::models::worktree::Worktree;

/// A data-fetch error from one source during a refresh cycle.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FetchError {
    /// Which source failed (e.g. `github`, `tmux`, `git`).
    pub source: String,
    /// Human-readable error message.
    pub message: String,
}

/// Canonical repository, the top-level grouping for all worktree data.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Repo {
    /// Owner/repo slug (e.g. `acme/webapp`).
    pub slug: String,
    /// Default branch name (e.g. `main`).
    pub default_branch: Option<String>,
    /// Rollup CI state of the default branch: `passing`, `failing`, or `pending`.
    pub main_ci_state: Option<String>,
    /// ISO 8601 timestamp when `main_ci_state` was last checked.
    pub main_ci_checked_at: Option<String>,
    /// ISO 8601 timestamp of the last successful data fetch for this repo.
    pub last_fetched_at: Option<String>,
    /// Fetch errors encountered during the last refresh cycle.
    #[serde(default)]
    pub errors: Vec<FetchError>,
    /// Worktrees belonging to this repo.
    #[serde(skip)]
    pub worktrees: Vec<Worktree>,
}
