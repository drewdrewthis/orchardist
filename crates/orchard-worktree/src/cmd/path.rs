//! `orchard-worktree path <branch>` — print the absolute path of a worktree.
//!
//! Useful in shell pipelines: `cd $(orchard path issue412/foo)`.

use anyhow::Result;
use clap::Args as ClapArgs;

use crate::cmd::resolve_worktree_path;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Branch name of the worktree to look up.
    pub branch: String,
}

pub fn run(args: Args) -> Result<()> {
    let path = resolve_worktree_path(&args.branch)?;
    println!("{path}");
    Ok(())
}
