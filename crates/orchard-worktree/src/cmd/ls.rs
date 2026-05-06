//! `orchard-worktree ls [--json]` — list worktrees in the current repo.

use anyhow::Result;
use clap::Args as ClapArgs;
use serde::Serialize;

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
    worktrees: Vec<JsonWorktree>,
}

#[derive(Serialize)]
struct JsonWorktree {
    path: String,
    branch: Option<String>,
    head: String,
    is_bare: bool,
    has_conflicts: bool,
}

const SCHEMA_VERSION: u32 = 1;

pub fn run(args: Args) -> Result<()> {
    let trees = worktree_core::list_worktrees()?;

    if args.json {
        let output = JsonOutput {
            version: SCHEMA_VERSION,
            worktrees: trees
                .into_iter()
                .map(|t| JsonWorktree {
                    path: t.path,
                    branch: t.branch,
                    head: t.head,
                    is_bare: t.is_bare,
                    has_conflicts: t.has_conflicts,
                })
                .collect(),
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
