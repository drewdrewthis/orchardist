//! `orchard-worktree` — worktree mutation CLI.
//!
//! Thin clap wrapper on [`worktree_core`]. Backs the `orchard worktree` (and
//! bare-verb shortcut `orchard new`/`orchard rm`/etc.) verbs in the orchard
//! dispatcher.
//!
//! # Exit codes
//!
//! Stable across releases per ADR-013 §5:
//!
//! | Code | Meaning |
//! |------|---------|
//! | 0    | Success |
//! | 2    | Invalid arguments (clap default) |
//! | 3    | Precondition failed (dirty worktree, missing branch, etc.) |
//! | 4    | (reserved) Remote unreachable |
//! | 5    | (reserved) Conflict (mid-merge, mid-rebase) |
//! | 1    | Generic failure |
//!
//! # JSON output
//!
//! `ls --json` emits a versioned `JsonOutput` struct. Schema version starts
//! at 1; bump on breaking changes.

use std::process::ExitCode;

use clap::Parser;

mod cmd;

#[derive(Parser, Debug)]
#[command(
    name = "orchard-worktree",
    about = "Manage git worktrees (new, rm, prune, ls, path).",
    version
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Parser, Debug)]
enum Command {
    /// Create a new worktree at `worktrees/<branch>` for the given branch
    /// (creates the branch if it doesn't exist; checks it out if it does).
    New(cmd::new::Args),

    /// Remove a worktree by branch name.
    Rm(cmd::rm::Args),

    /// Remove worktrees matching a filter.
    Prune(cmd::prune::Args),

    /// List worktrees in the current repo.
    Ls(cmd::ls::Args),

    /// Print the absolute path of a worktree by branch name.
    Path(cmd::path::Args),
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let result = match cli.command {
        Command::New(args) => cmd::new::run(args),
        Command::Rm(args) => cmd::rm::run(args),
        Command::Prune(args) => cmd::prune::run(args),
        Command::Ls(args) => cmd::ls::run(args),
        Command::Path(args) => cmd::path::run(args),
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("orchard-worktree: {e:#}");
            // 3 = precondition failed (e.g. not in a repo, branch missing, etc.).
            // We don't yet distinguish 4/5 — those land when remote/transfer ship.
            ExitCode::from(3)
        }
    }
}
