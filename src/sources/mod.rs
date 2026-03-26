//! Data source modules: thin facades over external data fetching.
//!
//! Each module owns one data domain (GitHub, git, tmux, SSH, Claude hooks) and provides
//! `refresh_*` functions to fetch and cache data. See `docs/architecture.md` for the full data flow.

pub mod claude;
pub mod github;
pub mod hosts;
pub mod tmux;
pub mod worktrees;
