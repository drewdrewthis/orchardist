//! `orchard-worktree new <branch>` — create a worktree.
//!
//! Resolves `<repo_root>/.worktrees/<branch-slug>` and delegates to
//! [`worktree_core::create_worktree`]. Idempotent: if a worktree already
//! exists at the target path for the same branch, succeeds with a "already
//! exists" message.

use anyhow::{Result, anyhow};
use clap::Args as ClapArgs;

use crate::cmd::worktree_path_for;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Branch name to create or check out (e.g. `feature/foo` or `issue123/bar`).
    pub branch: String,
}

pub fn run(args: Args) -> Result<()> {
    let repo_root = worktree_core::find_repo_root();
    if repo_root.is_empty() {
        return Err(anyhow!("not in a git repository"));
    }

    let target = worktree_path_for(&repo_root, &args.branch);
    let target_str = target.to_string_lossy();

    // Idempotency: if a worktree for this branch is already checked out
    // anywhere, succeed and surface its path. This makes `orchard new <issue>`
    // safe to re-run from scripts and matches the ADR-013 §5 contract.
    if let Ok(trees) = worktree_core::list_worktrees()
        && let Some(existing) = trees
            .iter()
            .find(|t| t.branch.as_deref() == Some(args.branch.as_str()))
    {
        println!("already exists at {}", existing.path);
        return Ok(());
    }

    let outcome = worktree_core::create_worktree(
        std::path::Path::new(&repo_root),
        &args.branch,
        &target_str,
    )?;
    match outcome {
        worktree_core::CreateOutcome::NewBranch => {
            println!("created branch '{}' at {target_str}", args.branch);
        }
        worktree_core::CreateOutcome::ExistingBranch => {
            println!("checked out '{}' at {target_str}", args.branch);
        }
    }
    Ok(())
}
