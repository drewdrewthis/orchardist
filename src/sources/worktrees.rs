/// Fetches local git worktrees for `repo.path` and writes to the worktrees cache.
pub fn refresh_local(repo: &crate::global_config::RepoConfig) -> anyhow::Result<()> {
    crate::cache_sources::refresh_worktrees(repo)
}

/// Fetches git worktrees from a single remote host and writes to the remote worktrees cache.
pub fn refresh_remote(
    repo: &crate::global_config::RepoConfig,
    remote: &crate::global_config::RemoteConfig,
) -> anyhow::Result<()> {
    crate::cache_sources::refresh_remote_worktrees(repo, remote)
}
