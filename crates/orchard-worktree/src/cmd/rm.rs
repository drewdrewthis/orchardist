//! `orchard-worktree rm <branch>` — remove a worktree by branch name.

use anyhow::Result;
use clap::Args as ClapArgs;

use crate::cmd::resolve_worktree_path;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Branch of the worktree to remove.
    pub branch: String,

    /// Pass `--force` to `git worktree remove`.
    #[arg(long)]
    pub force: bool,
}

pub fn run(args: Args) -> Result<()> {
    let path = resolve_worktree_path(&args.branch)?;
    worktree_core::remove_worktree(&path, args.force)?;
    println!("removed: {path}");
    Ok(())
}
