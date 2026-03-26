/// Fetches local tmux sessions and writes to the local tmux sessions cache.
pub fn refresh_local() -> anyhow::Result<()> {
    crate::cache_sources::refresh_tmux_sessions(None)
}

/// Fetches tmux sessions from a remote host and writes to the remote tmux sessions cache.
pub fn refresh_remote(host: &str) -> anyhow::Result<()> {
    crate::cache_sources::refresh_tmux_sessions(Some(host))
}
