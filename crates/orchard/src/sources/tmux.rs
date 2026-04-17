//! Tmux session data source: local and remote session listing.
//!
//! Fetches active tmux sessions from local and remote hosts via tmux CLI.
//! Sessions are mapped to worktrees and combined with Claude state for the unified dashboard.

/// Fetches local tmux sessions and writes to the local tmux sessions cache.
pub fn refresh_local() -> anyhow::Result<()> {
    crate::cache_sources::refresh_tmux_sessions(None)
}

/// Fetches tmux sessions from a remote host and writes to the remote tmux sessions cache.
pub fn refresh_remote(host: &str) -> anyhow::Result<()> {
    crate::cache_sources::refresh_tmux_sessions(Some(host))
}

/// Fetches tmux sessions from a remote via its adapter and writes per-host caches.
///
/// Routes through `RemoteAdapter::from_config` so `BoxdFork` remotes write one
/// cache file per fork host rather than always using the gateway host. Mirrors
/// the pattern used for worktrees in `sources::worktrees::refresh_remote`.
///
/// The pre-refresh fork-host snapshot is taken internally from the current
/// `remote_worktrees` cache so callers need not plumb it; this loses the
/// ability to detect forks that vanished between the caller's worktree
/// refresh and this call, which is acceptable outside the TUI refresh loop.
pub fn refresh_remote_adapter(
    repo: &crate::global_config::RepoConfig,
    remote: &crate::global_config::RemoteConfig,
) -> anyhow::Result<()> {
    let old_hosts = crate::cache_sources::snapshot_fork_hosts_for_remote(repo, remote);
    crate::cache_sources::refresh_remote_tmux_sessions(repo, remote, &old_hosts)
}
