//! Subcommand modules.
//!
//! Each subcommand has its own file containing the clap `Args` struct and a
//! `run(args) -> anyhow::Result<()>` entry point. Keep each file ≤ 100 lines.

pub mod ls;
pub mod mv;
pub mod new;
pub mod path;
pub mod prune;
pub mod rm;

use anyhow::{Result, anyhow};

/// Resolve a worktree by branch name to its absolute path.
///
/// Returns `Err` if no worktree is checked out on `branch`.
pub fn resolve_worktree_path(branch: &str) -> Result<String> {
    let trees = worktree_core::list_worktrees()?;
    trees
        .iter()
        .find(|t| t.branch.as_deref() == Some(branch))
        .map(|t| t.path.clone())
        .ok_or_else(|| anyhow!("no worktree found for branch '{branch}'"))
}
