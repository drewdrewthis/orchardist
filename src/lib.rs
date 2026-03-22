// Library crate root — exposes modules for integration tests in tests/.
// The binary entry point remains in main.rs.

pub mod collector;
pub mod config;
pub mod git;
pub mod github;
pub mod logger;
pub mod paths;
pub mod remote;
pub mod tmux;
pub mod types;

// Private modules required for the above to compile.
mod browser;
mod events;
mod issue_sync;
mod navigation;
mod session_discovery;
mod shell;
mod state;
mod status;
mod transfer;
mod tui;
