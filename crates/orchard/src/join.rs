//! Join logic: matches worktrees → PRs, sessions → worktrees, issues → branches.
//!
//! Builds [`WorktreeRow`] values from raw cache data by joining across sources.
//! No I/O — pure functions only. All input comes from the cache layer.

use crate::cache::{CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};
use crate::classify::derive_display_group;
use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};
use crate::github;
use crate::session::{
    ClaudeSessionInfo, EnrichedSession, Host, PaneColumns, PaneInfo, SessionStatus,
    TmuxSessionInfo, WindowInfo, build_windows_and_panes,
};

/// Tuple type for per-repo cache data passed to [`derive_all_repos`].
///
/// Fields: `(repo_slug, issues, prs, worktrees, sessions)`.
pub type RepoCacheEntry = (
    String,
    Vec<CachedIssue>,
    Vec<CachedPr>,
    Vec<CachedWorktree>,
    Vec<CachedTmuxSession>,
);

/// Maximum age (in seconds) before a Claude hook state file is considered stale.
pub(crate) const HOOK_STATE_STALENESS_SECS: u64 = 300;

/// Derives worktree rows for a single repository from its four source caches.
///
/// Join chain (worktree-first):
/// 1. Start from non-bare worktrees.
/// 2. For each worktree, match branch → PR (find a PR whose branch matches).
/// 3. For each worktree path, match → tmux sessions (by path equality).
/// 4. Extract issue number from branch name by naming convention.
/// 5. Look up issue title from issues cache if issue number found.
/// 6. Detect main worktree (first non-bare worktree or session name ending `_main`).
/// 7. Derive display group from the joined data.
pub fn derive_worktree_rows(
    issues: &[CachedIssue],
    prs: &[CachedPr],
    worktrees: &[CachedWorktree],
    sessions: &[CachedTmuxSession],
    repo_slug: &str,
    claude_states: &[crate::claude_state::ClaudeStateFile],
    statusline_files: &[crate::claude_state::StatusLineFile],
) -> Vec<WorktreeRow> {
    let mut rows = Vec::new();
    let mut is_first_non_bare = true;

    for wt in worktrees.iter().filter(|w| !w.is_bare) {
        // Don't match PRs to default/mainline branches — a PR with head "main"
        // is not meaningful work associated with the main worktree.
        let pr = if crate::derive::is_default_branch(&wt.branch) {
            None
        } else {
            prs.iter().find(|p| p.branch == wt.branch)
        };
        let pr_info = pr.map(pr_info_from);

        let session_infos: Vec<EnrichedSession> = sessions
            .iter()
            .filter(|s| s.path == wt.path)
            .map(|s| enrich_session(s, claude_states, statusline_files))
            .collect();

        // Two-tier issue linking: prefer the authoritative GitHub link from
        // the PR's closingIssuesReferences, fall back to branch-name regex.
        let issue_number = pr
            .and_then(|p| p.linked_issue)
            .or_else(|| github::extract_issue_number(&wt.branch));
        let linked_issue = issue_number.and_then(|num| issues.iter().find(|i| i.number == num));
        let issue_title = linked_issue.map(|i| i.title.clone());
        let issue_state = linked_issue.map(|i| i.state.clone());
        let issue_labels = linked_issue.map(|i| i.labels.clone()).unwrap_or_default();
        let issue_assignees = linked_issue
            .map(|i| i.assignees.clone())
            .unwrap_or_default();
        let issue_created_at = linked_issue.and_then(|i| i.created_at.clone());
        let issue_updated_at = linked_issue.and_then(|i| i.updated_at.clone());
        let issue_blocked_by = linked_issue
            .map(|i| i.blocked_by.clone())
            .unwrap_or_default();
        let issue_sub_issues = linked_issue
            .map(|i| i.sub_issues.clone())
            .unwrap_or_default();
        let issue_parent = linked_issue.and_then(|i| i.parent);

        let is_main_worktree =
            is_first_non_bare || session_infos.iter().any(|s| s.tmux.name.ends_with("_main"));

        let display_group = if is_main_worktree {
            DisplayGroup::RepoMain
        } else if crate::priority::is_prioritized(&wt.path) {
            DisplayGroup::Prioritized
        } else {
            derive_display_group(pr_info.as_ref(), &session_infos, issue_state.as_deref())
        };

        rows.push(WorktreeRow {
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
            worktree_ahead: wt.ahead,
            worktree_behind: wt.behind,
            worktree_last_commit_at: wt.last_commit_at.clone(),
            pr: pr_info,
            sessions: session_infos,
            display_group,
            is_main_worktree,
        });

        is_first_non_bare = false;
    }

    rows
}

/// Derives and sorts worktree rows across all configured repositories.
///
/// Each tuple is `(repo_slug, issues, prs, worktrees, sessions)`. Rows are
/// sorted: RepoMain first, then by display group, then by issue number
/// (worktrees without issue numbers sort by branch name).
pub fn derive_all_repos(
    repo_caches: &[RepoCacheEntry],
    claude_states: &[crate::claude_state::ClaudeStateFile],
    statusline_files: &[crate::claude_state::StatusLineFile],
) -> Vec<WorktreeRow> {
    let mut rows: Vec<WorktreeRow> = repo_caches
        .iter()
        .flat_map(|(slug, issues, prs, worktrees, sessions)| {
            derive_worktree_rows(
                issues,
                prs,
                worktrees,
                sessions,
                slug,
                claude_states,
                statusline_files,
            )
        })
        .collect();

    // Pipeline-severity sort (issue #251): status hierarchy primary, priority
    // floats rows within a status group, older SINCE timestamps first.
    rows.sort_by(|a, b| crate::signal::sort_key_row(a).cmp(&crate::signal::sort_key_row(b)));

    rows
}

pub(crate) fn pr_info_from(pr: &CachedPr) -> PrInfo {
    // Writing the deprecated legacy `checks_state` field is the intended
    // backcompat bridge for one release — suppressed here with a local allow.
    #[allow(deprecated)]
    PrInfo {
        number: pr.number,
        branch: pr.branch.clone(),
        state: Some(pr.state.clone()),
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
    }
}

pub(crate) fn enrich_session(
    session: &CachedTmuxSession,
    claude_states: &[crate::claude_state::ClaudeStateFile],
    statusline_files: &[crate::claude_state::StatusLineFile],
) -> EnrichedSession {
    use crate::claude_state::{state_for_session, statusline_for_session};
    use crate::derive::is_state_stale;

    let host = match &session.host {
        Some(h) => Host::Remote(h.clone()),
        None => Host::Local,
    };
    let tmux = TmuxSessionInfo {
        host,
        name: session.name.clone(),
        status: SessionStatus::Running { attached: false },
    };

    let (windows, panes) = build_windows_and_panes(PaneColumns::from_cached(session));

    // Hook-first: check if a fresh state file exists for this session.
    let hook_state = state_for_session(claude_states, &session.name);
    if let Some(state_file) = hook_state {
        let is_stale = is_state_stale(&state_file.timestamp, HOOK_STATE_STALENESS_SECS);
        if !is_stale {
            let statusline = statusline_for_session(statusline_files, &session.name);
            let claude = ClaudeSessionInfo::from_state_file_with_statusline(state_file, statusline);
            return EnrichedSession {
                tmux,
                claude,
                windows,
                panes,
                started_at: session.created_at,
                last_activity_at: session.last_activity_at,
            };
        }
    }

    // Fallback: terminal scraping.
    enrich_session_from_scraping(session, tmux, windows, panes)
}

/// Derives session info by scraping terminal output (fallback when no hook state).
pub(crate) fn enrich_session_from_scraping(
    session: &CachedTmuxSession,
    tmux: TmuxSessionInfo,
    windows: Vec<WindowInfo>,
    panes: Vec<PaneInfo>,
) -> EnrichedSession {
    use crate::claude_state::ClaudeState;

    let has_claude_active = session
        .pane_commands
        .iter()
        .any(|cmd| cmd.to_lowercase().contains("claude"))
        || session
            .pane_titles
            .iter()
            .any(|t| t.to_lowercase().contains("claude"));

    let last_content: Vec<&str> = session
        .last_output_lines
        .iter()
        .rev()
        .map(|s| s.trim())
        .filter(|s| !s.is_empty())
        .take(3)
        .collect();

    // Claude Code shows a spinner + activity text while working, e.g.:
    //   "✢ Whirlpooling... (2m 36s · ↑ 1.9k tokens)"
    // The spinner character animates, so match on the token/time suffix instead.
    let claude_is_working =
        has_claude_active && last_content.iter().any(|line| line.contains("tokens)"));

    let claude_needs_input = has_claude_active && !claude_is_working && {
        last_content.iter().any(|line| {
            // Yes/no prompts
            line.contains("(y/n)")
            || line.contains("[Y/n]")
            || line.contains("[y/N]")
            // Open questions from Claude
            || line.contains('?')
        })
    };

    let claude_state = if claude_needs_input {
        ClaudeState::Input
    } else if claude_is_working {
        ClaudeState::Working
    } else if has_claude_active {
        ClaudeState::Idle
    } else {
        ClaudeState::None
    };

    let claude = if claude_state != ClaudeState::None {
        Some(ClaudeSessionInfo {
            status: claude_state,
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: None,
            input_tokens: None,
            output_tokens: None,
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            context_window_pct: None,
            cost_usd: None,
            total_duration_ms: None,
            rate_limits: None,
            stop_reason: None,
            turn_count: None,
            state_changed_at: None,
        })
    } else {
        None
    };

    EnrichedSession {
        tmux,
        claude,
        windows,
        panes,
        started_at: session.created_at,
        last_activity_at: session.last_activity_at,
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
#[allow(deprecated)] // PrInfo.checks_state — fixtures still populate the legacy field for now
mod tests {
    use super::*;
    use crate::cache::{CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};
    use crate::ci_state::CiChecks;
    use crate::derive::DisplayGroup;

    // -----------------------------------------------------------------------
    // Builder helpers
    // -----------------------------------------------------------------------

    /// Test wrapper: builds a `TmuxSessionInfo` from a `CachedTmuxSession` and
    /// calls the real `enrich_session_from_scraping`.
    fn enrich_session_from_scraping_for_test(session: &CachedTmuxSession) -> EnrichedSession {
        let host = match &session.host {
            Some(h) => Host::Remote(h.clone()),
            None => Host::Local,
        };
        let tmux = TmuxSessionInfo {
            host,
            name: session.name.clone(),
            status: SessionStatus::Running { attached: false },
        };
        let (windows, panes) = build_windows_and_panes(PaneColumns::from_cached(session));
        enrich_session_from_scraping(session, tmux, windows, panes)
    }

    fn open_issue(number: u32) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "open".to_string(),
            labels: vec![],
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    fn pr_for_branch(pr_number: u32, branch: &str) -> CachedPr {
        CachedPr {
            number: pr_number,
            branch: branch.to_string(),
            linked_issue: None,
            state: "open".to_string(),
            review_decision: None,
            labels: vec![],
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            linked_issue_state: None,
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

    fn approved_passing_pr_for_branch(pr_number: u32, branch: &str) -> CachedPr {
        CachedPr {
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()),
            ci_code_state: Some("passing".to_string()),
            ..pr_for_branch(pr_number, branch)
        }
    }

    fn worktree(path: &str, branch: &str) -> CachedWorktree {
        CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: false,
            is_locked: false,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
        }
    }

    fn bare_worktree(path: &str, branch: &str) -> CachedWorktree {
        CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: true,
            is_locked: false,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
        }
    }

    fn session(name: &str, path: &str, pane_commands: Vec<&str>) -> CachedTmuxSession {
        let targets: Vec<String> = (0..pane_commands.len()).map(|i| format!("0.{i}")).collect();
        CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            pane_targets: targets,
            pane_titles: vec![],
            pane_commands: pane_commands.into_iter().map(|s| s.to_string()).collect(),
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    fn fresh_state_file(tmux_session: &str, state: &str) -> crate::claude_state::ClaudeStateFile {
        crate::claude_state::ClaudeStateFile {
            state: state.to_string(),
            session_id: "sess-abc".to_string(),
            tmux_session: tmux_session.to_string(),
            cwd: "/workspace/repo".to_string(),
            event: "PreToolUse".to_string(),
            timestamp: chrono::Utc::now().format("%Y-%m-%dT%H:%M:%SZ").to_string(),
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

    fn stale_state_file(tmux_session: &str, state: &str) -> crate::claude_state::ClaudeStateFile {
        use chrono::TimeZone;
        let old_ts = chrono::Utc.with_ymd_and_hms(2020, 1, 1, 0, 0, 0).unwrap();
        crate::claude_state::ClaudeStateFile {
            state: state.to_string(),
            session_id: "sess-abc".to_string(),
            tmux_session: tmux_session.to_string(),
            cwd: "/workspace/repo".to_string(),
            event: "Stop".to_string(),
            timestamp: old_ts.format("%Y-%m-%dT%H:%M:%SZ").to_string(),
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

    // -----------------------------------------------------------------------
    // Worktree-first join tests
    // -----------------------------------------------------------------------

    #[test]
    fn worktrees_are_base_rows() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 2);
        assert_eq!(rows[0].worktree_path, "/workspace/repo");
        assert_eq!(rows[0].branch, "main");
        assert_eq!(rows[1].worktree_path, "/workspace/repo-feat");
        assert_eq!(rows[1].branch, "feat/something");
    }

    #[test]
    fn bare_worktrees_are_skipped() {
        let worktrees = vec![
            bare_worktree("/workspace/repo.git", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].branch, "feat/something");
    }

    #[test]
    fn pr_matches_worktree_by_branch() {
        let worktrees = vec![worktree("/workspace/repo-47", "feat/task-centric")];
        let prs = vec![pr_for_branch(55, "feat/task-centric")];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        let pr = rows[0].pr.as_ref().expect("PR should be present");
        assert_eq!(pr.number, 55);
    }

    #[test]
    fn worktree_without_pr_still_shows() {
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/something")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert!(rows[0].pr.is_none());
    }

    #[test]
    fn tmux_session_joins_via_worktree_path() {
        let worktrees = vec![worktree("/workspace/webapp-47", "feat/task-centric")];
        let sessions = vec![session("webapp_47", "/workspace/webapp-47", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].sessions.len(), 1);
        assert_eq!(rows[0].sessions[0].tmux.name, "webapp_47");
    }

    #[test]
    fn multiple_sessions_at_same_path_all_join() {
        let worktrees = vec![worktree("/workspace/webapp-47", "feat/task-centric")];
        let sessions = vec![
            session("webapp_47_main", "/workspace/webapp-47", vec!["bash"]),
            session("webapp_47_claude", "/workspace/webapp-47", vec!["claude"]),
        ];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].sessions.len(), 2);
        let names: Vec<&str> = rows[0]
            .sessions
            .iter()
            .map(|s| s.tmux.name.as_str())
            .collect();
        assert!(names.contains(&"webapp_47_main"));
        assert!(names.contains(&"webapp_47_claude"));
    }

    #[test]
    fn issue_number_extracted_from_branch_name() {
        let issues = vec![open_issue(2478)];
        let worktrees = vec![worktree("/workspace/webapp-2478", "webapp-2478")];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].issue_number, Some(2478));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #2478"));
    }

    #[test]
    fn issue_title_none_when_issue_not_in_cache() {
        let worktrees = vec![worktree("/workspace/webapp-2478", "webapp-2478")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].issue_number, Some(2478));
        assert!(rows[0].issue_title.is_none());
    }

    #[test]
    fn no_issue_number_for_plain_branch() {
        let worktrees = vec![worktree("/workspace/repo", "main")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 1);
        assert!(rows[0].issue_number.is_none());
        assert!(rows[0].issue_title.is_none());
    }

    // -----------------------------------------------------------------------
    // Two-tier issue linking tests
    // -----------------------------------------------------------------------

    #[test]
    fn issue_number_from_pr_linked_issue_takes_priority() {
        // PR's linked_issue (42) should win over branch-name extraction (200).
        let issues = vec![open_issue(42)];
        let prs = vec![CachedPr {
            linked_issue: Some(42),
            ..pr_for_branch(10, "feat/200-my-feature")
        }];
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[0].issue_number, Some(42));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #42"));
    }

    #[test]
    fn issue_number_falls_back_to_branch_name_when_pr_has_no_linked_issue() {
        let issues = vec![open_issue(200)];
        let prs = vec![pr_for_branch(10, "feat/200-my-feature")]; // linked_issue: None
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[0].issue_number, Some(200));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #200"));
    }

    #[test]
    fn issue_number_falls_back_to_branch_name_when_no_pr() {
        let issues = vec![open_issue(200)];
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[0].issue_number, Some(200));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #200"));
    }

    #[test]
    fn issue_state_populated_when_pr_exists() {
        // After removing the suppression guard, issue_state should be present
        // even when a PR exists for the same worktree.
        let issues = vec![open_issue(200)];
        let prs = vec![pr_for_branch(10, "feat/200-my-feature")];
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[0].issue_state.as_deref(), Some("open"));
        assert!(rows[0].pr.is_some());
    }

    // -----------------------------------------------------------------------
    // Main worktree detection tests
    // -----------------------------------------------------------------------

    #[test]
    fn first_non_bare_worktree_is_main_worktree() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert!(rows[0].is_main_worktree);
        assert!(!rows[1].is_main_worktree);
    }

    #[test]
    fn main_worktree_gets_repo_main_display_group() {
        let worktrees = vec![worktree("/workspace/repo", "main")];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[0].display_group, DisplayGroup::RepoMain);
    }

    #[test]
    fn session_ending_with_main_is_main_worktree() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let sessions = vec![session("webapp_main", "/workspace/repo-feat", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[], &[]);

        assert!(rows[0].is_main_worktree); // first non-bare
        assert!(rows[1].is_main_worktree); // session name ends with _main
    }

    #[test]
    fn bare_worktree_before_non_bare_does_not_count_as_first() {
        let worktrees = vec![
            bare_worktree("/workspace/repo.git", "main"),
            worktree("/workspace/repo-checkout", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows.len(), 2);
        assert!(rows[0].is_main_worktree); // first non-bare
        assert!(!rows[1].is_main_worktree);
    }

    // -----------------------------------------------------------------------
    // Multi-repo aggregation tests
    // -----------------------------------------------------------------------

    #[test]
    fn derive_all_repos_sorts_shepherd_first() {
        let repo_caches = vec![(
            "owner/repo-a".to_string(),
            vec![],
            vec![approved_passing_pr_for_branch(10, "feat/fix")],
            vec![
                worktree("/workspace/repo-a", "main"),
                worktree("/workspace/repo-a-fix", "feat/fix"),
            ],
            vec![],
        )];

        let rows = derive_all_repos(&repo_caches, &[], &[]);

        assert_eq!(rows[0].display_group, DisplayGroup::RepoMain);
        assert!(rows[0].is_main_worktree);
        assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
    }

    #[test]
    fn derive_all_repos_sorts_by_group_then_issue_number() {
        let repo_caches = vec![
            (
                "owner/repo-a".to_string(),
                vec![],
                vec![],
                vec![
                    worktree("/workspace/repo-a", "main"),
                    worktree("/workspace/repo-a-5", "feat/issue-500"),
                    worktree("/workspace/repo-a-3", "feat/issue-300"),
                ],
                vec![],
            ),
            (
                "owner/repo-b".to_string(),
                vec![],
                vec![approved_passing_pr_for_branch(10, "feat/issue-100")],
                vec![
                    worktree("/workspace/repo-b", "main"),
                    worktree("/workspace/repo-b-fix", "feat/issue-100"),
                ],
                vec![],
            ),
        ];

        let rows = derive_all_repos(&repo_caches, &[], &[]);

        // RepoMain first (sorted by issue number / branch)
        let shepherd_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::RepoMain)
            .collect();
        assert_eq!(shepherd_rows.len(), 2);

        // Issue #251 changed the primary sort to pipeline-status severity:
        // "Coding" (active work, no PR yet) outranks "Ready" (approved +
        // passing CI waiting on merge), because ready rows aren't blocking
        // anyone — active rows have merge-blockers to resolve. So the "Other"
        // worktrees (Coding status) now come before the ReadyToMerge row.
        let non_shepherd: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group != DisplayGroup::RepoMain)
            .collect();
        assert_eq!(non_shepherd.len(), 3);

        // All three non-main rows are accounted for — two Other (Coding) plus
        // one ReadyToMerge (Ready). Verify the ReadyToMerge row is present.
        let ready = non_shepherd
            .iter()
            .find(|r| r.display_group == DisplayGroup::ReadyToMerge)
            .expect("should have a ReadyToMerge row");
        assert_eq!(ready.issue_number, Some(100));

        // Coding rows (no PR) sort by issue number ascending within the group.
        let other_rows: Vec<&WorktreeRow> = non_shepherd
            .iter()
            .filter(|r| r.display_group == DisplayGroup::Other)
            .copied()
            .collect();
        assert_eq!(other_rows.len(), 2);
        assert_eq!(other_rows[0].issue_number, Some(300));
        assert_eq!(other_rows[1].issue_number, Some(500));
    }

    #[test]
    fn worktrees_without_issue_numbers_sort_by_branch() {
        let repo_caches = vec![(
            "owner/repo".to_string(),
            vec![],
            vec![],
            vec![
                worktree("/workspace/repo", "main"),
                worktree("/workspace/repo-z", "z-feature"),
                worktree("/workspace/repo-a", "a-feature"),
            ],
            vec![],
        )];

        let rows = derive_all_repos(&repo_caches, &[], &[]);

        let other_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::Other)
            .collect();
        assert_eq!(other_rows.len(), 2);
        assert_eq!(other_rows[0].branch, "a-feature");
        assert_eq!(other_rows[1].branch, "z-feature");
    }

    // -----------------------------------------------------------------------
    // Hook-first state derivation tests
    // -----------------------------------------------------------------------

    #[test]
    fn hook_state_working_maps_to_claude_working() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "working")];
        let info = enrich_session(&s, &states, &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Working);
    }

    #[test]
    fn hook_state_idle_maps_to_claude_idle() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "idle")];
        let info = enrich_session(&s, &states, &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Idle);
    }

    #[test]
    fn hook_state_input_maps_to_claude_input() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "input")];
        let info = enrich_session(&s, &states, &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Input);
    }

    #[test]
    fn hook_state_enrichment_fields_propagate() {
        let s = session("repo_47", "/path", vec![]);
        let mut state = fresh_state_file("repo_47", "working");
        state.model = Some("claude-opus-4-6".to_string());
        state.last_tool = Some("Bash".to_string());
        state.current_task = Some("fix the bug".to_string());
        state.input_tokens = Some(1000);
        state.output_tokens = Some(50);
        let info = enrich_session(&s, &[state], &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(claude.last_tool.as_deref(), Some("Bash"));
        assert_eq!(claude.current_task.as_deref(), Some("fix the bug"));
        assert_eq!(claude.input_tokens, Some(1000));
        assert_eq!(claude.output_tokens, Some(50));
    }

    #[test]
    fn stale_hook_state_falls_back_to_terminal_scraping() {
        // Stale state file says "idle" but terminal shows working
        let s = CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/path", vec!["claude"])
        };
        let states = vec![stale_state_file("repo_47", "idle")];
        let info = enrich_session(&s, &states, &[]);
        // Should use scraping result (working), not stale hook (idle)
        assert_eq!(
            info.claude.as_ref().unwrap().status,
            crate::claude_state::ClaudeState::Working,
            "expected scraping fallback to detect working"
        );
    }

    #[test]
    fn no_hook_state_falls_back_to_terminal_scraping() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to proceed? (y/n)".to_string()],
            ..session("s", "/path", vec!["claude"])
        };
        let info = enrich_session(&s, &[], &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Input);
        assert!(claude.input_tokens.is_none());
    }

    // -----------------------------------------------------------------------
    // Terminal scraping tests
    // -----------------------------------------------------------------------

    #[test]
    fn claude_needs_input_false_for_idle_prompt() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["❯ ".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        // Idle Claude: has claude info with Idle status, not Input
        assert!(
            info.claude
                .as_ref()
                .is_none_or(|c| c.status != crate::claude_state::ClaudeState::Input)
        );
    }

    #[test]
    fn claude_needs_input_detected_from_yes_no_prompt() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to continue? (y/n)".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        assert_eq!(
            info.claude.as_ref().unwrap().status,
            crate::claude_state::ClaudeState::Input
        );
    }

    #[test]
    fn claude_needs_input_detected_from_question_mark() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to proceed?".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        assert_eq!(
            info.claude.as_ref().unwrap().status,
            crate::claude_state::ClaudeState::Input
        );
    }

    #[test]
    fn claude_needs_input_false_when_claude_not_active() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["❯ ".to_string()],
            pane_commands: vec!["bash".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        assert!(info.claude.is_none());
    }

    #[test]
    fn claude_needs_input_false_when_no_prompt_patterns() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Compiling project...".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        // Claude is active but idle (not input or working), so status is Idle
        assert_eq!(
            info.claude.as_ref().unwrap().status,
            crate::claude_state::ClaudeState::Idle
        );
    }

    #[test]
    fn pane_title_containing_claude_sets_has_claude_active() {
        let s = CachedTmuxSession {
            pane_titles: vec!["Claude Code - my-project".to_string()],
            pane_commands: vec!["node".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = enrich_session_from_scraping_for_test(&s);
        assert!(info.claude.is_some());
    }

    // -----------------------------------------------------------------------
    // issue_state population tests
    // -----------------------------------------------------------------------

    fn closed_issue(number: u32) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "closed".to_string(),
            labels: vec![],
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    fn completed_issue(number: u32) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "completed".to_string(),
            labels: vec![],
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    #[test]
    fn issue_state_populated_from_cached_issue() {
        let issues = vec![closed_issue(200)];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[1].issue_state.as_deref(), Some("closed"));
    }

    #[test]
    fn issue_state_populated_for_open_issue() {
        let issues = vec![open_issue(200)];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[1].issue_state.as_deref(), Some("open"));
    }

    #[test]
    fn issue_state_none_when_issue_not_in_cache() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[], &[]);

        assert!(rows[1].issue_state.is_none());
    }

    #[test]
    fn issue_state_present_when_worktree_has_pr() {
        // issue_state is always populated regardless of PR presence.
        let issues = vec![completed_issue(200)];
        let prs = vec![pr_for_branch(55, "feat/issue-200-my-feature")];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert!(rows[1].pr.is_some(), "PR should be matched");
        assert_eq!(
            rows[1].issue_state.as_deref(),
            Some("completed"),
            "issue_state should be populated even when PR exists"
        );
    }

    // -----------------------------------------------------------------------
    // Default branch PR exclusion tests
    // -----------------------------------------------------------------------

    #[test]
    fn main_branch_not_matched_to_pr() {
        let prs = vec![pr_for_branch(2379, "main")];
        let worktrees = vec![worktree("/workspace/repo", "main")];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[], &[]);

        assert!(
            rows[0].pr.is_none(),
            "main should not be matched to a PR targeting main"
        );
    }

    #[test]
    fn default_branches_never_matched_to_prs() {
        for branch in &["main", "master", "develop", "dev"] {
            let prs = vec![pr_for_branch(1, branch)];
            let worktrees = vec![worktree("/workspace/repo", branch)];

            let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[], &[]);

            assert!(
                rows[0].pr.is_none(),
                "branch '{branch}' should not be matched to a PR"
            );
        }
    }

    // -----------------------------------------------------------------------
    // PaneInfo population during session enrichment
    // -----------------------------------------------------------------------

    fn session_with_panes(
        name: &str,
        path: &str,
        pane_commands: Vec<&str>,
        pane_titles: Vec<&str>,
    ) -> CachedTmuxSession {
        let count = pane_commands.len().max(pane_titles.len());
        let targets: Vec<String> = (0..count).map(|i| format!("0.{i}")).collect();
        CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            pane_targets: targets,
            pane_titles: pane_titles.into_iter().map(|s| s.to_string()).collect(),
            pane_commands: pane_commands.into_iter().map(|s| s.to_string()).collect(),
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    #[test]
    fn enrich_session_populates_pane_infos() {
        let sess = session_with_panes(
            "my-session",
            "/workspace/repo",
            vec!["claude", "nvim", "cargo watch -x test"],
            vec!["claude", "nvim", "cargo"],
        );
        let enriched = enrich_session_from_scraping_for_test(&sess);
        assert_eq!(enriched.panes.len(), 3);
        assert_eq!(enriched.panes[0].index, 0);
        assert!(enriched.panes[0].has_claude);
        assert_eq!(enriched.panes[0].command, "claude");
        assert_eq!(enriched.panes[1].index, 1);
        assert!(!enriched.panes[1].has_claude);
        assert_eq!(enriched.panes[2].index, 2);
        assert!(!enriched.panes[2].has_claude);
        assert_eq!(enriched.panes[2].command, "cargo watch -x test");
    }

    #[test]
    fn enrich_session_empty_panes() {
        let sess = CachedTmuxSession {
            name: "empty".to_string(),
            path: "/workspace/repo".to_string(),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        };
        let enriched = enrich_session_from_scraping_for_test(&sess);
        assert!(enriched.panes.is_empty());
    }

    #[test]
    fn enrich_session_pane_claude_detection_case_insensitive() {
        let sess = session_with_panes(
            "my-session",
            "/workspace/repo",
            vec!["Claude --model opus"],
            vec!["bash"],
        );
        let enriched = enrich_session_from_scraping_for_test(&sess);
        assert_eq!(enriched.panes.len(), 1);
        assert!(enriched.panes[0].has_claude);
    }

    // -----------------------------------------------------------------------
    // pr_info_from: split CI state propagation (task #24)
    // -----------------------------------------------------------------------

    /// Verifies that pr_info_from propagates all three split-CI fields from CachedPr into PrInfo.
    #[test]
    fn pr_info_from_propagates_split_ci_state_fields() {
        use crate::ci_state::CheckInfo;

        let gate_check = CheckInfo {
            name: "check-approval-or-label".to_string(),
            state: "failing".to_string(),
            details_url: None,
        };
        let code_check = CheckInfo {
            name: "test-unit".to_string(),
            state: "passing".to_string(),
            details_url: None,
        };

        let cached_pr = CachedPr {
            checks_state: Some("passing".to_string()),
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: Some("blocked".to_string()),
            ci_checks: CiChecks {
                code: vec![code_check.clone()],
                gate: vec![gate_check.clone()],
            },
            ..pr_for_branch(99, "feat/issue-99")
        };

        let pr_info = pr_info_from(&cached_pr);

        assert_eq!(
            pr_info.ci_code_state.as_deref(),
            Some("passing"),
            "ci_code_state must be propagated from CachedPr"
        );
        assert_eq!(
            pr_info.ci_gate_state.as_deref(),
            Some("blocked"),
            "ci_gate_state must be propagated from CachedPr"
        );
        assert_eq!(
            pr_info.ci_checks.code,
            vec![code_check],
            "ci_checks.code must be propagated from CachedPr"
        );
        assert_eq!(
            pr_info.ci_checks.gate,
            vec![gate_check],
            "ci_checks.gate must be propagated from CachedPr"
        );
        // Legacy checks_state mirrors ci_code_state, not union semantics.
        assert_eq!(
            pr_info.checks_state.as_deref(),
            Some("passing"),
            "legacy checks_state must mirror ci_code_state (not union)"
        );
    }
}
