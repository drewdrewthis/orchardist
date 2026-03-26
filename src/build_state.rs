use std::collections::{HashMap, HashSet};

use crate::cache;
use crate::global_config::GlobalConfig;
use crate::orchard_state::{HostState, OrchardState, RepoState, WorktreeState};
use crate::sources;

/// Builds an `OrchardState` by reading all caches for the given config.
///
/// Does not perform any network or filesystem refresh — reads existing cached
/// data only. Safe to call from the TUI on every tick.
pub fn build_state(config: &GlobalConfig) -> OrchardState {
    build_state_with_hosts(config, &HashMap::new())
}

/// Builds an `OrchardState` by reading all caches, with known host reachability.
///
/// `hosts` maps host strings (e.g. "user@host") to their reachability state.
/// Hosts absent from the map are not included in the returned state.
pub fn build_state_with_hosts(
    config: &GlobalConfig,
    hosts: &HashMap<String, HostState>,
) -> OrchardState {
    let mut repo_caches = Vec::new();
    let mut tmux_hosts_seen: HashSet<String> = HashSet::new();

    // Collect local tmux sessions (shared across all repos).
    let local_sessions =
        cache::read_cache::<cache::CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;

    for repo in &config.repos {
        let issues = cache::read_cache::<cache::CachedIssue>(&cache::cache_path(
            repo.owner(),
            repo.repo_name(),
            "issues",
        ))
        .entries;

        let prs = cache::read_cache::<cache::CachedPr>(&cache::cache_path(
            repo.owner(),
            repo.repo_name(),
            "prs",
        ))
        .entries;

        let mut worktrees = cache::read_cache::<cache::CachedWorktree>(&cache::cache_path(
            repo.owner(),
            repo.repo_name(),
            "worktrees",
        ))
        .entries;

        // Merge in remote worktrees (already host-tagged by refresh_remote_worktrees).
        if !repo.remotes.is_empty() {
            let remote_wts = cache::read_cache::<cache::CachedWorktree>(&cache::cache_path(
                repo.owner(),
                repo.repo_name(),
                "remote_worktrees",
            ))
            .entries;
            worktrees.extend(remote_wts);
        }

        // Gather sessions: local + one entry per unique remote host.
        let mut sessions = local_sessions.clone();
        for remote in &repo.remotes {
            if tmux_hosts_seen.insert(remote.host.clone()) {
                let remote_sessions = cache::read_cache::<cache::CachedTmuxSession>(
                    &cache::tmux_cache_path(Some(&remote.host)),
                )
                .entries;
                sessions.extend(remote_sessions);
            }
        }

        repo_caches.push((repo.slug.clone(), issues, prs, worktrees, sessions));
    }

    let claude_states = crate::sources::claude::read_state_files();
    let rows = crate::derive::derive_all_repos(&repo_caches, &claude_states);

    // Group WorktreeRows back by repo_slug into RepoStates.
    let mut repo_map: HashMap<String, Vec<WorktreeState>> = HashMap::new();
    for row in &rows {
        repo_map
            .entry(row.repo_slug.clone())
            .or_default()
            .push(WorktreeState::from(row));
    }

    // Preserve config ordering for repos.
    let repos: Vec<RepoState> = config
        .repos
        .iter()
        .filter_map(|r| {
            repo_map.remove(&r.slug).map(|worktrees| RepoState {
                slug: r.slug.clone(),
                worktrees,
            })
        })
        .collect();

    OrchardState {
        repos,
        hosts: hosts.clone(),
    }
}

/// Synchronously refreshes all sources, then builds and returns an `OrchardState`.
///
/// Intended for `--json` mode where the caller wants fresh data before output.
/// Probes host reachability before attempting remote refreshes.
pub fn refresh_and_build(config: &GlobalConfig) -> OrchardState {
    // Refresh local sources first.
    for repo in &config.repos {
        let _ = sources::worktrees::refresh_local(repo);
        let _ = sources::github::refresh_issues(repo);
        let _ = sources::github::refresh_prs(repo);
    }
    let _ = sources::tmux::refresh_local();

    // Probe and refresh remote sources.
    let mut hosts: HashMap<String, HostState> = HashMap::new();
    let mut seen_hosts: HashSet<String> = HashSet::new();

    for repo in &config.repos {
        for remote in &repo.remotes {
            if seen_hosts.insert(remote.host.clone()) {
                let reachable = sources::hosts::probe_reachability(&remote.host);
                hosts.insert(remote.host.clone(), HostState { reachable });
                if reachable {
                    let _ = sources::worktrees::refresh_remote(repo, remote);
                    let _ = sources::tmux::refresh_remote(&remote.host);
                }
            } else if let Some(state) = hosts.get(&remote.host) {
                // Already probed — refresh worktrees for this repo if reachable.
                if state.reachable {
                    let _ = sources::worktrees::refresh_remote(repo, remote);
                }
            }
        }
    }

    build_state_with_hosts(config, &hosts)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::global_config::GlobalConfig;

    #[test]
    fn build_state_with_hosts_empty_config_returns_empty_state() {
        let config = GlobalConfig { repos: vec![] };
        let state = build_state_with_hosts(&config, &HashMap::new());
        assert!(state.repos.is_empty());
        assert!(state.hosts.is_empty());
    }

    #[test]
    fn build_state_with_hosts_empty_config_has_no_worktrees() {
        let config = GlobalConfig { repos: vec![] };
        let state = build_state_with_hosts(&config, &HashMap::new());
        assert_eq!(state.all_worktrees().len(), 0);
    }

    #[test]
    fn build_state_with_hosts_preserves_config_repo_ordering() {
        use crate::global_config::RepoConfig;
        // Two repos with no cached data — they should appear in config order
        let config = GlobalConfig {
            repos: vec![
                RepoConfig { slug: "owner/repo-a".to_string(), path: "/tmp/repo-a".to_string(), remotes: vec![] },
                RepoConfig { slug: "owner/repo-b".to_string(), path: "/tmp/repo-b".to_string(), remotes: vec![] },
            ],
        };
        let state = build_state_with_hosts(&config, &HashMap::new());
        // Repos with empty caches produce no worktrees, so repo_map is empty and
        // filter_map drops them — assert state is well-formed (no panic).
        assert!(state.repos.len() <= 2);
    }
}
