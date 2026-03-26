/// Fetches open GitHub issues for the repo and writes to the issues cache.
pub fn refresh_issues(repo: &crate::global_config::RepoConfig) -> anyhow::Result<()> {
    crate::cache_sources::refresh_issues(repo)
}

/// Fetches open GitHub PRs for the repo via GraphQL and writes to the PRs cache.
pub fn refresh_prs(repo: &crate::global_config::RepoConfig) -> anyhow::Result<()> {
    crate::cache_sources::refresh_prs(repo)
}
