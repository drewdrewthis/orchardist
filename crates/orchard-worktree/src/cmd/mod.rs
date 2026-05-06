//! Subcommand modules.
//!
//! Each subcommand has its own file containing the clap `Args` struct and a
//! `run(args) -> anyhow::Result<()>` entry point. Keep each file ≤ 100 lines.

pub mod ls;
pub mod new;
pub mod path;
pub mod prune;
pub mod rm;

use std::path::PathBuf;

use anyhow::{Result, anyhow};

/// Resolve the path that a new worktree for `branch` should live at.
///
/// Convention: `<repo_root>/.worktrees/<branch-with-slashes-replaced>`.
/// Slashes become hyphens because `.worktrees/foo/bar` would imply a
/// directory hierarchy that doesn't exist.
pub fn worktree_path_for(repo_root: &str, branch: &str) -> PathBuf {
    let slug = branch.replace('/', "-");
    PathBuf::from(repo_root).join(".worktrees").join(slug)
}

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
