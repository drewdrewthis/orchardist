//! Root library crate for Orchard.
//!
//! Re-exports all public modules that make up the functional core and imperative
//! shell of the application. Consumers should import from the top-level module
//! rather than reaching into sub-modules directly.
#![warn(missing_docs)]

mod browser;
pub mod build_state;
pub mod cache;
pub mod cache_sources;
pub mod chat;
pub mod ci_state;
pub mod classify;
pub mod claude_state;
pub mod config;
pub mod derive;
pub mod events;
pub mod git;
pub mod git_parse;
pub mod github;
pub mod global_config;
pub mod heal;
pub mod hook_enrich;
pub mod join;
pub mod json_output;
pub mod logger;
pub mod models;
mod navigation;
pub mod notify;
pub mod orchard_state;
pub mod paths;
pub mod priority;
pub mod remote;
pub mod remote_adapter;
pub mod restore;
pub mod session;
pub mod setup_remote;
pub mod shell;
pub mod signal;
pub mod sources;
pub mod swr;
pub mod tmux;
pub mod toon_output;
pub mod tui;
pub mod types;
pub mod watch;
pub mod webhook;
