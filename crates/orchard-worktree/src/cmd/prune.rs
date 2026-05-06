//! `orchard-worktree prune` — bulk worktree removal.
//!
//! Filter modes:
//! - `--all`: remove every non-bare, non-main worktree in the repo.
//!
//! Future modes (`--merged`, `--stale <days>`) need PR/issue enrichment from
//! the daemon and live in higher layers — not in this binary today. Filed as
//! follow-up.

use anyhow::Result;
use clap::Args as ClapArgs;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Remove every non-bare, non-main worktree in the current repo.
    #[arg(long)]
    pub all: bool,

    /// Pass `--force` to each `git worktree remove`.
    #[arg(long)]
    pub force: bool,
}

pub fn run(args: Args) -> Result<()> {
    if !args.all {
        anyhow::bail!("specify --all (other filter modes pending; see ADR-013 follow-up)");
    }

    let trees = worktree_core::list_worktrees()?;
    let repo_root = worktree_core::find_repo_root();

    // Skip the bare repo and the main worktree (path == repo_root).
    let targets: Vec<&str> = trees
        .iter()
        .filter(|t| !t.is_bare && t.path != repo_root)
        .map(|t| t.path.as_str())
        .collect();

    if targets.is_empty() {
        println!("no worktrees to prune");
        return Ok(());
    }

    let outcomes = worktree_core::prune(&targets, args.force);
    let mut failed = 0;
    for (path, result) in &outcomes {
        match result {
            Ok(()) => println!("removed: {path}"),
            Err(e) => {
                eprintln!("failed: {path}: {e}");
                failed += 1;
            }
        }
    }

    if failed > 0 {
        anyhow::bail!("{failed} of {} worktrees failed to remove", outcomes.len());
    }
    Ok(())
}
