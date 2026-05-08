//! Data source modules.
//!
//! Hosts is the only surviving source after #429 phase 6 — daemon
//! does not yet probe per-config remotes, only its own peers.
//! All other data flows through `daemon::Client::work_view` (TUI)
//! or `cache_sources::refresh_*` (watch daemon, heal, --json).

pub mod hosts;
