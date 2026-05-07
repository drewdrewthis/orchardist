//! `orchard-worktree mv <branch> <host>` — cross-host worktree transfer.
//!
//! Stub: the remote coordinator (snapshot locally → ssh-create remotely →
//! sync state → kill local) is non-trivial and depends on `setup-remote`
//! plumbing. Until it lands, this command exits 3 with a clear message so
//! `orchard mv` doesn't dead-end through the dispatcher with a confusing
//! error.

use anyhow::{Result, bail};
use clap::Args as ClapArgs;

#[derive(ClapArgs, Debug)]
pub struct Args {
    /// Branch of the worktree to transfer.
    pub branch: String,

    /// Target host (must already be configured via `orchard remote setup`).
    pub host: String,
}

pub fn run(args: Args) -> Result<()> {
    bail!(
        "orchard-worktree mv is not yet implemented (would transfer '{}' to '{}'). \
         See ADR-013 follow-up; track via the remote-coordinator issue.",
        args.branch,
        args.host
    );
}
