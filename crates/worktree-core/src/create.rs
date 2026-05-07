//! Worktree creation (`git worktree add`).
//!
//! Tries `git worktree add -b <branch> <path>` first (creates a new branch).
//! On failure, falls back to `git worktree add <path> <branch>` (checks out
//! an existing branch). This matches what the TUI used to inline.
//!
//! Tmux session creation, setup-script execution, and remote dispatch are
//! concerns of the consuming binary, not this library.

use std::path::Path;
use std::process::Command;

use anyhow::{Context, Result, anyhow};

/// Outcome of a successful [`create_worktree`] call.
///
/// Distinguishes whether a new branch was created vs an existing branch was
/// checked out. Callers that surface "created branch X" vs "checked out
/// existing branch X" branch on this.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CreateOutcome {
    /// `git worktree add -b <branch> <path>` succeeded — new branch created.
    NewBranch,
    /// Fall-through: `git worktree add <path> <branch>` succeeded — existing branch checked out.
    ExistingBranch,
}

/// Creates a git worktree at `worktree_path` for `branch`, rooted at `repo_root`.
///
/// First attempts to create a new branch via `git worktree add -b <branch>
/// <path>`. If that fails (typically because the branch already exists),
/// falls back to `git worktree add <path> <branch>` to check out the
/// existing branch.
///
/// # Errors
///
/// Returns `Err` if both attempts fail. The error contains the stderr from
/// the second attempt verbatim so callers can surface it to the user.
pub fn create_worktree(
    repo_root: &Path,
    branch: &str,
    worktree_path: &str,
) -> Result<CreateOutcome> {
    let new_branch_result = Command::new("git")
        .args(["worktree", "add", "-b", branch, worktree_path])
        .current_dir(repo_root)
        .output();

    if matches!(&new_branch_result, Ok(out) if out.status.success()) {
        return Ok(CreateOutcome::NewBranch);
    }

    let out = Command::new("git")
        .args(["worktree", "add", worktree_path, branch])
        .current_dir(repo_root)
        .output()
        .context("git worktree add (existing branch)")?;

    if out.status.success() {
        Ok(CreateOutcome::ExistingBranch)
    } else {
        let stderr = String::from_utf8_lossy(&out.stderr);
        Err(anyhow!("{}", stderr.trim()))
    }
}
