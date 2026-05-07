//! `orchard-worktree ls [--json]` — list worktrees in the current repo.

use anyhow::Result;
use clap::Args as ClapArgs;
use serde::Serialize;
use worktree_core::WorktreeEntry;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Emit JSON instead of a human-readable table.
    #[arg(long)]
    pub json: bool,
}

#[derive(Serialize)]
struct JsonOutput {
    /// Schema version. Bump on breaking changes.
    version: u32,
    worktrees: Vec<WorktreeEntry>,
}

const SCHEMA_VERSION: u32 = 1;

pub fn run(args: Args) -> Result<()> {
    let trees = worktree_core::list_worktrees()?;

    if args.json {
        let output = JsonOutput {
            version: SCHEMA_VERSION,
            worktrees: trees,
        };
        println!("{}", serde_json::to_string_pretty(&output)?);
    } else {
        for t in &trees {
            let branch = t.branch.as_deref().unwrap_or("(detached)");
            let conflict_marker = if t.has_conflicts { " [CONFLICTS]" } else { "" };
            let bare_marker = if t.is_bare { " [bare]" } else { "" };
            println!("{branch}\t{}{bare_marker}{conflict_marker}", t.path);
        }
    }
    Ok(())
}
