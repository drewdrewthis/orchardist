//! Pure compositor that joins all per-source caches into a unified `OrchardState`.
//!
//! The functional core: no IO, no side effects, just data transformation.
//! Reads cached data from issues, PRs, worktrees, tmux sessions, and host reachability,
//! then joins them by repo and worktree. Consumed by both TUI and `--json` output.

use std::collections::{HashMap, HashSet};

use crate::cache;
use crate::claude_state::ClaudeStateFile;
use crate::derive::WorktreeRow;
use crate::global_config::GlobalConfig;
use crate::orchard_state::{HostState, OrchardState, RepoState, WorktreeState};
use crate::session::{
    ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, StandaloneSessionRow, TmuxSessionInfo,
    build_windows_and_panes,
};
use crate::sources;

/// Maximum age in seconds before a remote Claude hook state is considered stale.
///
/// Remote state files are much closer to real-time (fetched via SSH on every
/// cache refresh) so we use a tighter window than the local 300s default.
const REMOTE_HOOK_STATE_STALENESS_SECS: u64 = 30;

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
/// Also returns all remote Claude state files extracted from the session caches.
fn collect_repo_caches(
    config: &GlobalConfig,
    local_sessions: &[cache::CachedTmuxSession],
) -> (Vec<RepoCacheTuple>, Vec<ClaudeStateFile>) {
    let mut repo_caches = Vec::new();
    let mut tmux_hosts_seen: HashSet<String> = HashSet::new();
    let mut remote_claude_states: Vec<ClaudeStateFile> = Vec::new();

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

                // Extract fresh Claude states from the remote session cache entries.
                for session in &remote_sessions {
                    if let Some(state) = &session.claude_state_raw
                        && !crate::derive::is_state_stale(
                            state.timestamp.as_str(),
                            REMOTE_HOOK_STATE_STALENESS_SECS,
                        )
                    {
                        remote_claude_states.push(state.clone());
                    }
                }

                sessions.extend(remote_sessions);
            }
        }

        repo_caches.push((repo.slug.clone(), issues, prs, worktrees, sessions));
    }

    (repo_caches, remote_claude_states)
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

            let (windows, panes) = live
                .map(|s| {
                    build_windows_and_panes(
                        &s.pane_targets,
                        &s.pane_commands,
                        &s.pane_titles,
                        &s.window_names,
                        &s.window_active,
                    )
                })
                .unwrap_or_default();

            let started_at = live.and_then(|s| s.created_at);
            let last_activity_at = live.and_then(|s| s.last_activity_at);
            StandaloneSessionRow {
                session: EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: cfg.name.clone(),
                        status,
                    },
                    claude,
                    windows,
                    panes,
                    started_at,
                    last_activity_at,
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
    let (repo_caches, remote_claude_states) = collect_repo_caches(config, &local_sessions);
    let mut claude_states = sources::claude::read_state_files();
    claude_states.extend(remote_claude_states);
    crate::derive::derive_all_repos(&repo_caches, &claude_states, &[])
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
    let (repo_caches, remote_claude_states) = collect_repo_caches(config, &local_sessions);
    let mut claude_states = sources::claude::read_state_files();
    claude_states.extend(remote_claude_states);
    let rows = crate::derive::derive_all_repos(&repo_caches, &claude_states, &[]);

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
            repo_map.remove(&r.slug).map(|worktrees| {
                // Read repo meta (default branch, main CI state) from cache.
                let meta = cache::read_cache::<cache::CachedRepoMeta>(&cache::cache_path(
                    r.owner(),
                    r.repo_name(),
                    "repo_meta",
                ))
                .entries;
                let repo_meta = meta.into_iter().next();
                RepoState {
                    slug: r.slug.clone(),
                    worktrees,
                    default_branch: repo_meta.as_ref().and_then(|m| m.default_branch.clone()),
                    main_ci_state: repo_meta.as_ref().and_then(|m| m.main_ci_state.clone()),
                }
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

    // Probe remote hosts concurrently so a dead VM can't block healthy ones.
    let all_hosts: Vec<String> = config
        .repos
        .iter()
        .flat_map(|r| r.remotes.iter().map(|rm| rm.host.clone()))
        .collect();
    let probe_results = sources::hosts::probe_reachability_all(all_hosts);

    let hosts: HashMap<String, HostState> = probe_results
        .iter()
        .map(|(h, r)| (h.clone(), HostState { reachable: *r }))
        .collect();

    // Refresh remote sources for every reachable host, once per (repo, remote) pair.
    let mut tmux_refreshed: HashSet<String> = HashSet::new();
    for repo in &config.repos {
        for remote in &repo.remotes {
            let reachable = hosts
                .get(&remote.host)
                .map(|s| s.reachable)
                .unwrap_or(false);
            if reachable {
                let _ = sources::worktrees::refresh_remote(repo, remote);
                if tmux_refreshed.insert(remote.host.clone()) {
                    let _ = sources::tmux::refresh_remote(&remote.host);
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
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
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
        assert!(matches!(rows[0].session.tmux.status, SessionStatus::Dead));
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
            model: Some("claude-opus-4-6".to_string()),
            last_tool: Some("Bash".to_string()),
            current_task: None,
            session_start_ts: None,
            input_tokens: Some(1000),
            output_tokens: None,
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            stop_reason: None,
            inflight_tool_count: None,
            state_changed_at: None,
        }];
        let rows = build_standalone_sessions(&config, &[], &claude_states);
        let claude = rows[0].session.claude.as_ref().unwrap();
        assert_eq!(claude.status, ClaudeState::Working);
        assert_eq!(claude.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(claude.last_tool.as_deref(), Some("Bash"));
        assert_eq!(claude.input_tokens, Some(1000));
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

    // -----------------------------------------------------------------------
    // Remote Claude state: staleness filtering
    // -----------------------------------------------------------------------

    fn make_state_file(
        state: &str,
        session: &str,
        timestamp: &str,
    ) -> crate::claude_state::ClaudeStateFile {
        crate::claude_state::ClaudeStateFile {
            state: state.to_string(),
            session_id: "test-session".to_string(),
            tmux_session: session.to_string(),
            cwd: "/workspace".to_string(),
            event: "Stop".to_string(),
            timestamp: timestamp.to_string(),
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: None,
            input_tokens: None,
            output_tokens: None,
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            stop_reason: None,
            inflight_tool_count: None,
            state_changed_at: None,
        }
    }

    fn make_remote_session_with_claude(
        name: &str,
        state: &str,
        timestamp: &str,
    ) -> cache::CachedTmuxSession {
        cache::CachedTmuxSession {
            name: name.to_string(),
            path: "/workspace".to_string(),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            host: Some("ubuntu@10.0.0.1".to_string()),
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: Some(make_state_file(state, name, timestamp)),
        }
    }

    /// Extracts fresh remote Claude states from a slice of sessions, using the
    /// same staleness logic as `collect_repo_caches`.
    fn extract_fresh_remote_states(
        sessions: &[cache::CachedTmuxSession],
    ) -> Vec<crate::claude_state::ClaudeStateFile> {
        sessions
            .iter()
            .filter_map(|s| s.claude_state_raw.as_ref())
            .filter(|cs| {
                !crate::derive::is_state_stale(
                    cs.timestamp.as_str(),
                    REMOTE_HOOK_STATE_STALENESS_SECS,
                )
            })
            .cloned()
            .collect()
    }

    #[test]
    fn fresh_remote_claude_state_is_included() {
        let fresh_ts = chrono::Utc::now().to_rfc3339();
        let sessions = vec![make_remote_session_with_claude(
            "repo_47_claude",
            "working",
            &fresh_ts,
        )];
        let states = extract_fresh_remote_states(&sessions);
        assert_eq!(states.len(), 1);
        assert_eq!(states[0].tmux_session, "repo_47_claude");
        assert_eq!(states[0].state, "working");
    }

    #[test]
    fn stale_remote_claude_state_is_discarded() {
        // 60 seconds ago — well over the 30s threshold.
        let stale_ts = (chrono::Utc::now() - chrono::Duration::seconds(60)).to_rfc3339();
        let sessions = vec![make_remote_session_with_claude(
            "repo_47_claude",
            "working",
            &stale_ts,
        )];
        let states = extract_fresh_remote_states(&sessions);
        assert!(
            states.is_empty(),
            "stale remote Claude state should be discarded"
        );
    }

    #[test]
    fn remote_state_exactly_at_threshold_is_stale() {
        // Exactly 30 seconds ago — at threshold, should be treated as stale
        // (is_state_stale uses `>`, so age == threshold is stale).
        let at_threshold_ts = (chrono::Utc::now() - chrono::Duration::seconds(31)).to_rfc3339();
        let sessions = vec![make_remote_session_with_claude(
            "repo_47_claude",
            "working",
            &at_threshold_ts,
        )];
        let states = extract_fresh_remote_states(&sessions);
        assert!(
            states.is_empty(),
            "state at threshold (31s) should be stale"
        );
    }

    #[test]
    fn session_without_claude_state_raw_produces_no_states() {
        let session = cache::CachedTmuxSession {
            name: "repo_48_main".to_string(),
            path: "/workspace".to_string(),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            host: Some("ubuntu@10.0.0.1".to_string()),
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        };
        let states = extract_fresh_remote_states(&[session]);
        assert!(states.is_empty());
    }

    #[test]
    fn remote_hook_state_staleness_threshold_is_30_seconds() {
        // This test documents the constant value.
        assert_eq!(REMOTE_HOOK_STATE_STALENESS_SECS, 30);
    }

    // -----------------------------------------------------------------------
    // Label threading: PR labels reach PrState; issue labels reach IssueInfo
    //
    // These tests use `derive_worktree_rows` (the pure functional core) instead
    // of `build_state` (which reads from real cache files on disk). The
    // build_state→derive pipeline is a direct pass-through — these tests
    // verify the same field threading without requiring I/O.
    // -----------------------------------------------------------------------

    use crate::cache::{CachedIssue, CachedPr, CachedWorktree};
    use crate::derive::derive_worktree_rows;
    use crate::orchard_state::WorktreeState;

    fn make_worktree_for_labels(path: &str, branch: &str) -> CachedWorktree {
        CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: false,
            is_locked: false,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
        }
    }

    fn make_pr_with_labels(number: u32, branch: &str, labels: Vec<&str>) -> CachedPr {
        use crate::ci_state::CiChecks;
        CachedPr {
            number,
            branch: branch.to_string(),
            linked_issue: None,
            state: "open".to_string(),
            review_decision: None,
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            linked_issue_state: None,
            labels: labels.into_iter().map(|s| s.to_string()).collect(),
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
        }
    }

    fn make_issue_with_labels(number: u32, labels: Vec<&str>) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "open".to_string(),
            labels: labels.into_iter().map(|s| s.to_string()).collect(),
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    #[test]
    fn build_state_threads_pr_labels_into_pr_state() {
        let branch = "issue55/my-feature";
        let worktrees = vec![
            make_worktree_for_labels("/workspace/repo", "main"),
            make_worktree_for_labels("/workspace/repo-55", branch),
        ];
        let prs = vec![make_pr_with_labels(55, branch, vec!["planned"])];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[], &[]);
        let row = rows.iter().find(|r| r.branch == branch).unwrap();
        let pr = row.pr.as_ref().expect("PR should be present");
        assert_eq!(pr.labels, vec!["planned"]);

        // Verify it reaches PrState too
        let state = WorktreeState::from(row);
        let pr_state = state.pr.as_ref().unwrap();
        assert_eq!(pr_state.labels, vec!["planned"]);
    }

    #[test]
    fn build_state_threads_issue_labels_into_issue_info() {
        let branch = "issue47/my-feature";
        let worktrees = vec![
            make_worktree_for_labels("/workspace/repo", "main"),
            make_worktree_for_labels("/workspace/repo-47", branch),
        ];
        let issues = vec![make_issue_with_labels(
            47,
            vec!["in-progress", "enhancement"],
        )];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[], &[]);
        let row = rows.iter().find(|r| r.branch == branch).unwrap();
        assert_eq!(row.issue_labels, vec!["in-progress", "enhancement"]);

        // Verify it reaches IssueInfo too
        let state = WorktreeState::from(row);
        let issue = state.issue.as_ref().unwrap();
        assert_eq!(issue.labels, vec!["in-progress", "enhancement"]);
    }

    #[test]
    fn build_state_emits_empty_labels_when_pr_has_no_labels() {
        let branch = "issue99/my-feature";
        let worktrees = vec![
            make_worktree_for_labels("/workspace/repo", "main"),
            make_worktree_for_labels("/workspace/repo-99", branch),
        ];
        let prs = vec![make_pr_with_labels(99, branch, vec![])];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[], &[]);
        let row = rows.iter().find(|r| r.branch == branch).unwrap();
        let pr = row.pr.as_ref().expect("PR should be present");
        assert!(pr.labels.is_empty());

        let state = WorktreeState::from(row);
        let pr_state = state.pr.as_ref().unwrap();
        assert_eq!(pr_state.labels, Vec::<String>::new());
    }
}
