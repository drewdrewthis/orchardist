//! Refresh helpers for `tui::App`.
//!
//! Provides the `state_to_task_rows` helper (converts an already-built
//! [`OrchardState`] into the flat [`WorktreeRow`] list the TUI renders) and the
//! [`NullWorkViewSource`] sentinel used when the daemon client cannot be
//! constructed at startup.

use crate::daemon::{DaemonError, WorkViewSnapshot, WorkViewSource};
use crate::derive::WorktreeRow;
use crate::orchard_state::{ClaudeEnrichment, PrState, RepoState, SessionState, WindowState};
use crate::session::{
    ClaudeRateLimits, ClaudeSessionInfo, EnrichedSession, Host, PaneInfo, SessionStatus,
    TmuxSessionInfo, WindowInfo,
};

// ---------------------------------------------------------------------------
// NullWorkViewSource
// ---------------------------------------------------------------------------

/// A [`WorkViewSource`] that always returns `DaemonError::Unreachable`.
///
/// Used as a sentinel in `App::new` when the daemon client cannot be built
/// (e.g. invalid URL env var). The refresh thread will log a warning and fall
/// back to the cache-driven path for this cycle. The next refresh tick tries
/// again.
pub struct NullWorkViewSource;

impl WorkViewSource for NullWorkViewSource {
    fn work_view(&self) -> Result<WorkViewSnapshot, DaemonError> {
        Err(DaemonError::Unreachable {
            url: "(no client built)".to_string(),
            cause: "client was not constructed at startup".to_string(),
        })
    }
}

// ---------------------------------------------------------------------------
// state_to_task_rows
// ---------------------------------------------------------------------------

/// Converts the repos in an [`OrchardState`] into the flat [`WorktreeRow`] list
/// that the TUI renders as its task list.
///
/// Mirrors the output shape of [`crate::build_state::build_task_rows`] but
/// operates on an already-built (and potentially remote-enriched) `OrchardState`
/// rather than re-reading disk caches.
///
/// Non-bare worktrees are converted to `WorktreeRow`s by calling
/// [`worktree_state_to_row`] on each `(repo_slug, WorktreeState)` pair.
/// Rows are then sorted with the same multi-criteria key used by the derive
/// pipeline.
///
/// # Panics
///
/// Does not panic.
pub fn state_to_task_rows(repos: &[RepoState]) -> Vec<WorktreeRow> {
    let mut rows: Vec<WorktreeRow> = repos
        .iter()
        .flat_map(|repo| {
            repo.worktrees
                .iter()
                .filter(|wt| !wt.is_bare)
                .map(|wt| worktree_state_to_row(&repo.slug, wt))
        })
        .collect();

    rows.sort_by(|a, b| a.sort_key().cmp(&b.sort_key()));
    rows
}

// ---------------------------------------------------------------------------
// Private conversions: WorktreeState → WorktreeRow (and helpers)
// ---------------------------------------------------------------------------

/// Converts a `(repo_slug, WorktreeState)` pair into a [`WorktreeRow`].
///
/// All fields that are present in `WorktreeState` are forwarded directly.
/// Fields that exist in `WorktreeRow` but not in `WorktreeState` (e.g. per-pane
/// detail counts) are set to safe defaults.
fn worktree_state_to_row(repo_slug: &str, wt: &crate::orchard_state::WorktreeState) -> WorktreeRow {
    let issue_number = wt.issue.as_ref().map(|i| i.number);
    let issue_title = wt.issue.as_ref().map(|i| i.title.clone());
    let issue_state = wt.issue.as_ref().map(|i| i.state.clone());
    let issue_labels = wt
        .issue
        .as_ref()
        .map(|i| i.labels.clone())
        .unwrap_or_default();
    let issue_assignees = wt
        .issue
        .as_ref()
        .map(|i| i.assignees.clone())
        .unwrap_or_default();
    let issue_created_at = wt.issue.as_ref().and_then(|i| i.created_at.clone());
    let issue_updated_at = wt.issue.as_ref().and_then(|i| i.updated_at.clone());
    let issue_blocked_by = wt
        .issue
        .as_ref()
        .map(|i| i.blocked_by.clone())
        .unwrap_or_default();
    let issue_sub_issues = wt
        .issue
        .as_ref()
        .map(|i| i.sub_issues.clone())
        .unwrap_or_default();
    let issue_parent = wt.issue.as_ref().and_then(|i| i.parent);

    let pr = wt.pr.as_ref().map(pr_state_to_pr_info);

    let sessions: Vec<EnrichedSession> =
        wt.sessions.iter().map(session_state_to_enriched).collect();

    let (worktree_ahead, worktree_behind) = wt
        .ahead_behind
        .map(|(a, b)| (Some(a), Some(b)))
        .unwrap_or((None, None));

    #[allow(deprecated)]
    WorktreeRow {
        repo_slug: repo_slug.to_string(),
        worktree_path: wt.path.clone(),
        branch: wt.branch.clone(),
        worktree_host: wt.host.clone(),
        issue_number,
        issue_title,
        issue_state,
        issue_labels,
        issue_assignees,
        issue_created_at,
        issue_updated_at,
        issue_blocked_by,
        issue_sub_issues,
        issue_parent,
        worktree_ahead,
        worktree_behind,
        worktree_last_commit_at: wt.last_commit_at.clone(),
        pr,
        sessions,
        display_group: wt.display_group,
        is_main_worktree: wt.is_main_worktree,
        layout: wt.layout,
        discovery_path: wt.discovery_path.clone(),
    }
}

/// Converts a [`PrState`] into a [`crate::derive::PrInfo`].
#[allow(deprecated)]
fn pr_state_to_pr_info(pr: &PrState) -> crate::derive::PrInfo {
    crate::derive::PrInfo {
        number: pr.number,
        branch: pr.branch.clone(),
        state: pr.state.clone(),
        title: pr.title.clone(),
        is_draft: pr.is_draft,
        author: pr.author.clone(),
        requested_reviewers: pr.requested_reviewers.clone(),
        reviews: pr.reviews.clone(),
        review_decision: pr.review_decision.clone(),
        checks_state: pr.checks_state.clone(),
        ci_code_state: pr.ci_code_state.clone(),
        ci_gate_state: pr.ci_gate_state.clone(),
        ci_checks: pr.ci_checks.clone(),
        has_conflicts: pr.has_conflicts,
        unresolved_threads: pr.unresolved_threads,
        labels: pr.labels.clone(),
        additions: pr.additions,
        deletions: pr.deletions,
        created_at: pr.created_at.clone(),
        updated_at: pr.updated_at.clone(),
        last_commit_pushed_at: pr.last_commit_pushed_at.clone(),
        unresolved_thread_comment_timestamps: pr.unresolved_thread_comment_timestamps.clone(),
    }
}

/// Converts a [`SessionState`] into an [`EnrichedSession`].
fn session_state_to_enriched(ss: &SessionState) -> EnrichedSession {
    let host = ss
        .host
        .as_ref()
        .map(|h| Host::Remote(h.clone()))
        .unwrap_or(Host::Local);

    let claude = ss.claude.as_ref().map(claude_enrichment_to_session_info);

    let windows: Vec<WindowInfo> = ss.windows.iter().map(window_state_to_window_info).collect();
    let panes: Vec<PaneInfo> = windows
        .iter()
        .flat_map(|w| w.panes.iter().cloned())
        .collect();

    EnrichedSession {
        tmux: TmuxSessionInfo {
            host,
            name: ss.name.clone(),
            // WorktreeState doesn't carry session status (running/dead);
            // use Running as the conservative default — sessions in state are alive.
            status: SessionStatus::Running { attached: false },
        },
        claude,
        windows,
        panes,
        started_at: ss.started_at,
        last_activity_at: ss.last_activity_at,
    }
}

/// Converts a [`ClaudeEnrichment`] into a [`ClaudeSessionInfo`].
fn claude_enrichment_to_session_info(ce: &ClaudeEnrichment) -> ClaudeSessionInfo {
    ClaudeSessionInfo {
        status: ce.status,
        model: ce.model.clone(),
        last_tool: ce.last_tool.clone(),
        current_task: ce.current_task.clone(),
        session_start_ts: ce.session_start_ts,
        input_tokens: ce.input_tokens,
        output_tokens: ce.output_tokens,
        cache_creation_input_tokens: ce.cache_creation_input_tokens,
        cache_read_input_tokens: ce.cache_read_input_tokens,
        context_window_pct: ce.context_window_pct,
        cost_usd: ce.cost_usd,
        total_duration_ms: ce.total_duration_ms,
        rate_limits: ce.rate_limits.as_ref().map(|rl| ClaudeRateLimits {
            five_hour_used_pct: rl.five_hour_used_pct,
            five_hour_resets_at: rl.five_hour_resets_at.clone(),
            seven_day_used_pct: rl.seven_day_used_pct,
            seven_day_resets_at: rl.seven_day_resets_at.clone(),
        }),
        stop_reason: ce.stop_reason.clone(),
        turn_count: ce.turn_count,
        state_changed_at: ce.state_changed_at,
    }
}

/// Converts a [`WindowState`] into a [`WindowInfo`].
fn window_state_to_window_info(ws: &WindowState) -> WindowInfo {
    let panes = ws
        .panes
        .iter()
        .map(|ps| PaneInfo {
            index: ps.index,
            tmux_target: ps.tmux_target.clone(),
            command: ps.command.clone(),
            title: ps.title.clone(),
            has_claude: ps.has_claude,
            cwd: ps.cwd.clone(),
            is_active: ps.is_active,
        })
        .collect();

    WindowInfo {
        index: ws.index,
        name: ws.name.clone(),
        is_active: ws.is_active,
        panes,
        layout: ws.layout.clone(),
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;
    use crate::cache::WorktreeLayout;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{IssueInfo, OrchardState, PrState, RepoState, WorktreeState};

    // -----------------------------------------------------------------------
    // Test 1 (Unit): state_to_task_rows round-trip
    // -----------------------------------------------------------------------

    /// @unit "start_full_refresh fetches local data via daemon::Client::work_view"
    ///
    /// `state_to_task_rows` correctly converts an `OrchardState` containing one
    /// repo + one non-bare worktree into a `WorktreeRow` with matching fields.
    #[test]
    fn state_to_task_rows_round_trips_one_repo_one_worktree() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![WorktreeState {
                    path: "/repos/owner/repo/.worktrees/issue429".to_string(),
                    branch: "issue429/spec".to_string(),
                    is_bare: false,
                    host: None,
                    issue: Some(IssueInfo {
                        number: 429,
                        title: "Rip cache_sources".to_string(),
                        state: "open".to_string(),
                        labels: vec!["enhancement".to_string()],
                        assignees: vec![],
                        created_at: None,
                        updated_at: None,
                        blocked_by: vec![],
                        sub_issues: vec![],
                        parent: None,
                    }),
                    pr: None,
                    sessions: vec![],
                    display_group: DisplayGroup::Other,
                    is_main_worktree: false,
                    ahead_behind: None,
                    last_commit_at: None,
                    layout: WorktreeLayout::Bare,
                    discovery_path: None,
                }],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
            transitive_errors: vec![],
        };

        let rows = state_to_task_rows(&state.repos);

        assert_eq!(rows.len(), 1, "one non-bare worktree → one row");
        let row = &rows[0];
        assert_eq!(row.repo_slug, "owner/repo");
        assert_eq!(row.worktree_path, "/repos/owner/repo/.worktrees/issue429");
        assert_eq!(row.branch, "issue429/spec");
        assert_eq!(row.issue_number, Some(429));
        assert_eq!(row.issue_title.as_deref(), Some("Rip cache_sources"));
        assert!(row.worktree_host.is_none(), "local worktree has no host");
    }

    /// Bare worktrees are excluded from `state_to_task_rows`.
    #[test]
    fn state_to_task_rows_skips_bare_worktrees() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![
                    WorktreeState {
                        path: "/repos/owner/repo".to_string(),
                        branch: "main".to_string(),
                        is_bare: true, // bare — must be excluded
                        host: None,
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: DisplayGroup::Other,
                        is_main_worktree: true,
                        ahead_behind: None,
                        last_commit_at: None,
                        layout: WorktreeLayout::Bare,
                        discovery_path: None,
                    },
                    WorktreeState {
                        path: "/repos/owner/repo/.worktrees/issue1".to_string(),
                        branch: "issue1/feature".to_string(),
                        is_bare: false,
                        host: None,
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: DisplayGroup::Other,
                        is_main_worktree: false,
                        ahead_behind: None,
                        last_commit_at: None,
                        layout: WorktreeLayout::Bare,
                        discovery_path: None,
                    },
                ],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
            transitive_errors: vec![],
        };

        let rows = state_to_task_rows(&state.repos);
        assert_eq!(rows.len(), 1, "only non-bare worktrees should produce rows");
        assert_eq!(rows[0].branch, "issue1/feature");
    }

    /// PR fields are preserved through the `PrState → PrInfo` conversion.
    #[test]
    fn state_to_task_rows_preserves_pr_fields() {
        #[allow(deprecated)]
        let pr_state = PrState {
            number: 42,
            branch: "feat/pr42".to_string(),
            state: Some("open".to_string()),
            title: Some("My PR".to_string()),
            is_draft: Some(false),
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            review_decision: Some("approved".to_string()),
            checks_state: None,
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: None,
            ci_checks: crate::ci_state::CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            labels: vec!["bug".to_string()],
            additions: Some(100),
            deletions: Some(20),
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
            unresolved_thread_comment_timestamps: vec![],
        };

        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![WorktreeState {
                    path: "/repos/owner/repo/.worktrees/pr42".to_string(),
                    branch: "feat/pr42".to_string(),
                    is_bare: false,
                    host: None,
                    issue: None,
                    pr: Some(pr_state),
                    sessions: vec![],
                    display_group: DisplayGroup::Other,
                    is_main_worktree: false,
                    ahead_behind: None,
                    last_commit_at: None,
                    layout: WorktreeLayout::Bare,
                    discovery_path: None,
                }],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
            transitive_errors: vec![],
        };

        let rows = state_to_task_rows(&state.repos);
        assert_eq!(rows.len(), 1);
        let pr = rows[0].pr.as_ref().expect("PR should be present");
        assert_eq!(pr.number, 42);
        assert_eq!(pr.state.as_deref(), Some("open"));
        assert_eq!(pr.ci_code_state.as_deref(), Some("passing"));
        assert_eq!(pr.review_decision.as_deref(), Some("approved"));
    }

    /// Remote worktrees carry `host` tag through the conversion.
    #[test]
    fn state_to_task_rows_tags_remote_host() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![WorktreeState {
                    path: "/remote/repos/owner/repo/.worktrees/issue1".to_string(),
                    branch: "issue1/feature".to_string(),
                    is_bare: false,
                    host: Some("boxd@vm.boxd.sh".to_string()),
                    issue: None,
                    pr: None,
                    sessions: vec![],
                    display_group: DisplayGroup::Other,
                    is_main_worktree: false,
                    ahead_behind: None,
                    last_commit_at: None,
                    layout: WorktreeLayout::Bare,
                    discovery_path: None,
                }],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
            transitive_errors: vec![],
        };

        let rows = state_to_task_rows(&state.repos);
        assert_eq!(rows.len(), 1);
        assert_eq!(
            rows[0].worktree_host.as_deref(),
            Some("boxd@vm.boxd.sh"),
            "remote host must be preserved"
        );
    }

    // -----------------------------------------------------------------------
    // Test 2 (Integration): local + remote worktrees merge
    // -----------------------------------------------------------------------

    /// @integration "Remote worktrees from cache_sources merge into OrchardState
    /// alongside daemon-fresh local data"
    ///
    /// Build a local OrchardState with two local worktrees, then merge a
    /// remote snapshot containing one additional worktree. The resulting
    /// state_to_task_rows output should contain all three rows.
    #[test]
    fn local_plus_remote_merge_produces_three_rows() {
        use crate::daemon::types::{WorkViewRepo, WorkViewSnapshot, WorkViewWorktree};
        use crate::daemon::work_view_adapter::build_local_state;
        use crate::global_config::GlobalConfig;
        use crate::json_output::{JsonOutput, JsonRepo, JsonWorktree};
        use crate::merge_remote::merge_remote_snapshot;

        // ---- Local data via daemon work_view_adapter -----------------------

        let snapshot = WorkViewSnapshot {
            repos: vec![WorkViewRepo {
                slug: "repo".to_string(),
                path: "/repos/owner/repo".to_string(),
                worktrees: vec![
                    WorkViewWorktree {
                        path: "/repos/owner/repo/.worktrees/issue1".to_string(),
                        branch: "issue1/feat".to_string(),
                        head: "abc".to_string(),
                        bare: false,
                        host: "local".to_string(),
                        repo: "owner/repo".to_string(),
                        pr: None,
                        issue: None,
                    },
                    WorkViewWorktree {
                        path: "/repos/owner/repo/.worktrees/issue2".to_string(),
                        branch: "issue2/feat".to_string(),
                        head: "def".to_string(),
                        bare: false,
                        host: "local".to_string(),
                        repo: "owner/repo".to_string(),
                        pr: None,
                        issue: None,
                    },
                ],
            }],
            tmux_sessions: vec![],
            claude_instances: vec![],
        };

        let mut state =
            build_local_state(&snapshot, &GlobalConfig::default(), &HashMap::new(), None);

        // ---- Remote data via merge_remote_snapshot -------------------------

        let remote_snapshot = JsonOutput {
            version: *crate::json_output::SUPPORTED_JSON_OUTPUT_VERSIONS
                .last()
                .unwrap(),
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![JsonWorktree {
                    path: "/remote/repos/owner/repo/.worktrees/issue3".to_string(),
                    branch: "issue3/feat".to_string(),
                    host: None,
                    layout: "bare".to_string(),
                    ahead_behind: None,
                    last_commit_at: None,
                    issue: None,
                    pr: None,
                    sessions: vec![],
                    display_group: "other".to_string(),
                    status: "ready".to_string(),
                    status_glyph: "\u{1f7e2}".to_string(),
                    is_main_worktree: false,
                    last_activity_at: None,
                    discovery_path: None,
                }],
            }],
            hosts: HashMap::new(),
            errors: vec![],
        };

        merge_remote_snapshot(&mut state, remote_snapshot, "boxd@vm.boxd.sh".to_string());

        // ---- Assertions ----------------------------------------------------

        let rows = state_to_task_rows(&state.repos);

        // 2 local + 1 remote = 3 rows total (all non-bare)
        assert_eq!(
            rows.len(),
            3,
            "expected 3 worktree rows (2 local + 1 remote)"
        );

        let local_rows: Vec<_> = rows.iter().filter(|r| r.worktree_host.is_none()).collect();
        let remote_rows: Vec<_> = rows.iter().filter(|r| r.worktree_host.is_some()).collect();

        assert_eq!(local_rows.len(), 2, "exactly two local rows");
        assert_eq!(remote_rows.len(), 1, "exactly one remote row");
        assert_eq!(
            remote_rows[0].worktree_host.as_deref(),
            Some("boxd@vm.boxd.sh")
        );
    }
}
