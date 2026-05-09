//! CLI subcommand modules — one per verb. Each module exposes `Args` and
//! `run` and returns a `ExitCode` directly (not `Result`) so it can use
//! the spec's exit-code table without adapter glue.

pub mod history;
pub mod join;
pub mod leave;
pub mod list;
pub mod members;
pub mod send;
pub mod tail;
pub mod sender;
