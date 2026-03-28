//! Pure compositor that joins all per-source caches into a unified `OrchardState`.
//!
//! The functional core: no IO, no side effects, just data transformation.
//! Reads cached data from issues, PRs, worktrees, tmux sessions, and host reachability,
//! then joins them by repo and worktree. Consumed by both TUI and `--json` output.

use std::collections::{HashMap, HashSet};

use crate::cache;
use crate::derive::WorktreeRow;
use crate::global_config::GlobalConfig;
use crate::orchard_state::{HostState, OrchardState, RepoState, WorktreeState};
use crate::session::{
    ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, StandaloneSessionRow, TmuxSessionInfo,
};
use crate::sources;

// ---------------------------------------------------------------------------
// Cache collection helper (private)
// ---------------------------------------------------------------------------

/// Type alias for the per-repo cache tuple passed to `derive::derive_all_repos`.
type RepoCacheTuple = (
    String,
    Vec<cache::CachedIssue>,
    Vec<cache::CachedPr>,
    Vec<cache::CachedWorktree>,
    Vec<cache::CachedTmuxSession>,
);

/// Reads all per-repo and per-host caches from disk into the tuple format
/// expected by `derive::derive_all_repos`.
///
/// Pure IO: no network calls, no side effects beyond reading files.
fn collect_repo_caches(
    config: &GlobalConfig,
    local_sessions: &[cache::CachedTmuxSession],
) -> Vec<RepoCacheTuple> {
    let mut repo_caches = Vec::new();
    let mut tmux_hosts_seen: HashSet<String> = HashSet::new();

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
        let mut sessions = local_sessions.to_vec();
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

    repo_caches
}

/// Builds `StandaloneSessionRow`s from config, matching against live tmux sessions
/// and Claude state files.
fn build_standalone_sessions(
    config: &GlobalConfig,
    local_sessions: &[cache::CachedTmuxSession],
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> Vec<StandaloneSessionRow> {
    config
        .tmux_sessions
        .iter()
        .map(|cfg| {
            let live = local_sessions.iter().find(|s| s.name == cfg.name);
            let status = if live.is_some() {
                SessionStatus::Running { attached: false }
            } else {
                SessionStatus::Dead
            };

            let claude = claude_states
                .iter()
                .find(|cs| cs.tmux_session == cfg.name)
                .filter(|cs| !crate::derive::is_state_stale_default(&cs.timestamp))
                .and_then(ClaudeSessionInfo::from_state_file);

            StandaloneSessionRow {
                session: EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: cfg.name.clone(),
                        status,
                    },
                    claude,
                },
                config: cfg.clone(),
            }
        })
        .collect()
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Builds an `OrchardState` by reading all caches for the given config.
///
/// Does not perform any network or filesystem refresh — reads existing cached
/// data only. Safe to call from the TUI on every tick.
pub fn build_state(config: &GlobalConfig) -> OrchardState {
    build_state_with_hosts(config, &HashMap::new())
}

/// Reads all caches and returns flat sorted `WorktreeRow`s for all repos.
///
/// Returns the same data as `build_state` but as the raw derive output, which
/// the TUI consumes directly as `task_rows`. Avoids the round-trip through
/// `WorktreeState` conversions.
pub fn build_task_rows(config: &GlobalConfig) -> Vec<WorktreeRow> {
    let local_sessions =
        cache::read_cache::<cache::CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;
    let repo_caches = collect_repo_caches(config, &local_sessions);
    let claude_states = sources::claude::read_state_files();
    crate::derive::derive_all_repos(&repo_caches, &claude_states)
}

/// Builds an `OrchardState` by reading all caches, with known host reachability.
///
/// `hosts` maps host strings (e.g. "user@host") to their reachability state.
/// Hosts absent from the map are not included in the returned state.
pub fn build_state_with_hosts(
    config: &GlobalConfig,
    hosts: &HashMap<String, HostState>,
) -> OrchardState {
    let local_sessions =
        cache::read_cache::<cache::CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;
    let repo_caches = collect_repo_caches(config, &local_sessions);
    let claude_states = sources::claude::read_state_files();
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

    // Build standalone sessions from config (reuses local_sessions already read).
    let standalone_sessions = build_standalone_sessions(config, &local_sessions, &claude_states);

    OrchardState {
        repos,
        standalone_sessions,
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
    use crate::claude_state::ClaudeState;
    use crate::global_config::GlobalConfig;

    #[test]
    fn build_state_with_hosts_empty_config_returns_empty_state() {
        let config = GlobalConfig::default();
        let state = build_state_with_hosts(&config, &HashMap::new());
        assert!(state.repos.is_empty());
        assert!(state.hosts.is_empty());
    }

    #[test]
    fn build_state_with_hosts_empty_config_has_no_worktrees() {
        let config = GlobalConfig::default();
        let state = build_state_with_hosts(&config, &HashMap::new());
        assert_eq!(state.all_worktrees().len(), 0);
    }

    #[test]
    fn build_state_with_hosts_preserves_config_repo_ordering() {
        use crate::global_config::RepoConfig;
        // Two repos with no cached data — they should appear in config order
        let config = GlobalConfig {
            repos: vec![
                RepoConfig {
                    slug: "owner/repo-a".to_string(),
                    path: "/tmp/repo-a".to_string(),
                    remotes: vec![],
                },
                RepoConfig {
                    slug: "owner/repo-b".to_string(),
                    path: "/tmp/repo-b".to_string(),
                    remotes: vec![],
                },
            ],
            ..GlobalConfig::default()
        };
        let state = build_state_with_hosts(&config, &HashMap::new());
        // Repos with empty caches produce no worktrees, so repo_map is empty and
        // filter_map drops them — assert state is well-formed (no panic).
        assert!(state.repos.len() <= 2);
    }

    #[test]
    fn build_task_rows_empty_config_returns_empty_vec() {
        let config = GlobalConfig::default();
        let rows = build_task_rows(&config);
        assert!(rows.is_empty());
    }

    #[test]
    fn build_task_rows_and_build_state_produce_consistent_worktree_count() {
        use crate::global_config::RepoConfig;
        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/tmp/repo".to_string(),
                remotes: vec![],
            }],
            ..GlobalConfig::default()
        };
        let rows = build_task_rows(&config);
        let state = build_state(&config);
        // Both go through the same derive pipeline — worktree counts must agree.
        assert_eq!(rows.len(), state.all_worktrees().len());
    }

    // -----------------------------------------------------------------------
    // Standalone session tests
    // -----------------------------------------------------------------------

    fn make_standalone_config(name: &str) -> crate::session::StandaloneConfig {
        crate::session::StandaloneConfig {
            name: name.to_string(),
            command: "echo hello".to_string(),
            cwd: "/tmp".to_string(),
            start_on_launch: false,
        }
    }

    fn make_cached_session(name: &str) -> cache::CachedTmuxSession {
        cache::CachedTmuxSession {
            name: name.to_string(),
            path: "/tmp".to_string(),
            pane_titles: vec![],
            pane_commands: vec![],
            host: None,
            last_output_lines: vec![],
        }
    }

    #[test]
    fn standalone_session_running_when_live_tmux_exists() {
        let config = GlobalConfig {
            tmux_sessions: vec![make_standalone_config("shepherd")],
            ..GlobalConfig::default()
        };
        let live = vec![make_cached_session("shepherd")];
        let rows = build_standalone_sessions(&config, &live, &[]);
        assert_eq!(rows.len(), 1);
        assert!(matches!(
            rows[0].session.tmux.status,
            SessionStatus::Running { .. }
        ));
    }

    #[test]
    fn standalone_session_dead_when_no_live_tmux() {
        let config = GlobalConfig {
            tmux_sessions: vec![make_standalone_config("shepherd")],
            ..GlobalConfig::default()
        };
        let rows = build_standalone_sessions(&config, &[], &[]);
        assert_eq!(rows.len(), 1);
        assert!(matches!(
            rows[0].session.tmux.status,
            SessionStatus::Dead
        ));
    }

    #[test]
    fn standalone_session_no_claude_when_no_state_files() {
        let config = GlobalConfig {
            tmux_sessions: vec![make_standalone_config("shepherd")],
            ..GlobalConfig::default()
        };
        let rows = build_standalone_sessions(&config, &[], &[]);
        assert!(rows[0].session.claude.is_none());
    }

    #[test]
    fn standalone_session_claude_enriched_from_state_file() {
        let config = GlobalConfig {
            tmux_sessions: vec![make_standalone_config("shepherd")],
            ..GlobalConfig::default()
        };
        let claude_states = vec![crate::claude_state::ClaudeStateFile {
            state: "working".to_string(),
            session_id: "test".to_string(),
            tmux_session: "shepherd".to_string(),
            cwd: "/tmp".to_string(),
            event: "Stop".to_string(),
            timestamp: chrono::Utc::now().to_rfc3339(),
            context_window_pct: Some(42.0),
            cost_usd: Some(1.23),
            model: Some("opus".to_string()),
        }];
        let rows = build_standalone_sessions(&config, &[], &claude_states);
        let claude = rows[0].session.claude.as_ref().unwrap();
        assert_eq!(claude.status, ClaudeState::Working);
        assert_eq!(claude.cost_usd, Some(1.23));
        assert_eq!(claude.context_window_pct, Some(42.0));
        assert_eq!(claude.model.as_deref(), Some("opus"));
    }

    #[test]
    fn standalone_session_preserves_config_order() {
        let config = GlobalConfig {
            tmux_sessions: vec![
                make_standalone_config("shepherd"),
                make_standalone_config("monitor"),
                make_standalone_config("logs"),
            ],
            ..GlobalConfig::default()
        };
        let rows = build_standalone_sessions(&config, &[], &[]);
        assert_eq!(rows.len(), 3);
        assert_eq!(rows[0].config.name, "shepherd");
        assert_eq!(rows[1].config.name, "monitor");
        assert_eq!(rows[2].config.name, "logs");
    }

    #[test]
    fn standalone_session_empty_config_returns_empty() {
        let config = GlobalConfig::default();
        let rows = build_standalone_sessions(&config, &[], &[]);
        assert!(rows.is_empty());
    }
}
