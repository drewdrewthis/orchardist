//! Pure functional core: derives display-ready rows from cached data.
//!
//! `derive_all_repos` joins cached issues, PRs, worktrees, and tmux sessions
//! into `WorktreeRow` values with computed `DisplayGroup` sort keys. No I/O occurs
//! here — all input comes from the cache layer, making this fully testable.
use crate::cache::{CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};
use crate::github;
use crate::paths::paths_match;
use crate::session::{
    ClaudeSessionInfo, EnrichedSession, Host, PaneInfo, SessionStatus, TmuxSessionInfo, WindowInfo,
    build_windows_and_panes,
};

/// Tuple type for per-repo cache data passed to [`derive_all_repos`].
///
/// Fields: `(repo_slug, issues, prs, worktrees, sessions)`.
type RepoCacheEntry = (
    String,
    Vec<CachedIssue>,
    Vec<CachedPr>,
    Vec<CachedWorktree>,
    Vec<CachedTmuxSession>,
);

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Maximum age (in seconds) before a Claude hook state file is considered stale.
const HOOK_STATE_STALENESS_SECS: u64 = 300;

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/// Rendering order for worktree rows. Variants are ordered so that `Ord` gives
/// the correct sort order (RepoMain first, Done last).
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, serde::Serialize)]
#[serde(rename_all = "snake_case")]
pub enum DisplayGroup {
    /// Always first — the repo's main worktree.
    RepoMain,
    /// Has any running tmux session (Claude working, input needed, etc.).
    Active,
    /// Everything else that's still open/in-progress.
    Normal,
    /// Issue is closed AND PR is merged/closed, or issue closed with no PR.
    Done,
}

/// Lightweight PR summary attached to a worktree row.
#[derive(Debug, Clone)]
pub struct PrInfo {
    /// GitHub PR number.
    pub number: u32,
    /// Head branch name for this PR.
    pub branch: String,
    /// PR state: "OPEN", "CLOSED", or "MERGED".
    pub state: Option<String>,
    /// Review decision: "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", etc.
    pub review_decision: Option<String>,
    /// Aggregate CI checks state: "SUCCESS", "FAILURE", "PENDING", etc.
    pub checks_state: Option<String>,
    /// True when the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// Individual non-passing CI checks, before non-blocking exclusion filtering.
    pub failing_checks: Vec<crate::orchard_state::FailedCheck>,
    /// Labels applied to this PR.
    pub labels: Vec<String>,
    /// Whether the PR is a draft (not ready for review).
    pub is_draft: bool,
}

/// One row in the derived worktree view. Corresponds to one non-bare worktree,
/// enriched with PR/issue metadata and tmux session info.
#[derive(Debug, Clone)]
pub struct WorktreeRow {
    /// Repository slug in `owner/repo` format.
    pub repo_slug: String,
    /// Absolute path to the worktree on disk.
    pub worktree_path: String,
    /// Git branch checked out in this worktree.
    pub branch: String,
    /// Remote SSH host this worktree lives on, or `None` for local.
    pub worktree_host: Option<String>,
    /// GitHub issue number extracted from the branch name, if any.
    pub issue_number: Option<u32>,
    /// Title of the linked GitHub issue, if resolved.
    pub issue_title: Option<String>,
    /// State of the linked issue ("open", "closed", "completed"), if any.
    /// Used to detect stale worktrees whose issue has been resolved without a PR.
    pub issue_state: Option<String>,
    /// Labels applied to the linked issue, if any.
    pub issue_labels: Vec<String>,
    /// Linked pull request, if one exists for this branch.
    pub pr: Option<PrInfo>,
    /// Active tmux sessions associated with this worktree path.
    pub sessions: Vec<EnrichedSession>,
    /// Display group controlling sort order and TUI section.
    pub display_group: DisplayGroup,
    /// True when this is the repo's main worktree.
    pub is_main_worktree: bool,
    /// Unix timestamp (seconds) of the most recent tmux activity across all
    /// sessions attached to this worktree. `None` when no sessions are
    /// attached or none reported activity.
    ///
    /// Used as a sort tiebreaker within a display group: rows with more recent
    /// activity sort before those with older or no activity.
    pub last_activity: Option<u64>,
}

// ---------------------------------------------------------------------------
// Single-repo derivation
// ---------------------------------------------------------------------------

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
) -> Vec<WorktreeRow> {
    let mut rows = Vec::new();
    let mut is_first_non_bare = true;

    for wt in worktrees.iter().filter(|w| !w.is_bare) {
        // Don't match PRs to default/mainline branches — a PR with head "main"
        // is not meaningful work associated with the main worktree.
        let pr = if is_default_branch(&wt.branch) {
            None
        } else {
            prs.iter().find(|p| p.branch == wt.branch)
        };
        let pr_info = pr.map(pr_info_from);

        let matched_sessions: Vec<&CachedTmuxSession> = sessions
            .iter()
            .filter(|s| paths_match(&s.path, &wt.path))
            .collect();

        let last_activity = matched_sessions
            .iter()
            .filter_map(|s| s.last_activity)
            .max();

        let session_infos: Vec<EnrichedSession> = matched_sessions
            .iter()
            .map(|s| enrich_session(s, claude_states))
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

        let is_main_worktree =
            is_first_non_bare || session_infos.iter().any(|s| s.tmux.name.ends_with("_main"));

        let display_group = if is_main_worktree {
            DisplayGroup::RepoMain
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
            pr: pr_info,
            sessions: session_infos,
            display_group,
            is_main_worktree,
            last_activity,
        });

        is_first_non_bare = false;
    }

    rows
}

// ---------------------------------------------------------------------------
// Multi-repo aggregation
// ---------------------------------------------------------------------------

/// Derives and sorts worktree rows across all configured repositories.
///
/// Each tuple is `(repo_slug, issues, prs, worktrees, sessions)`. Rows are
/// sorted: RepoMain first, then by display group, then by issue number
/// (worktrees without issue numbers sort by branch name).
pub fn derive_all_repos(
    repo_caches: &[RepoCacheEntry],
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> Vec<WorktreeRow> {
    let mut rows: Vec<WorktreeRow> = repo_caches
        .iter()
        .flat_map(|(slug, issues, prs, worktrees, sessions)| {
            derive_worktree_rows(issues, prs, worktrees, sessions, slug, claude_states)
        })
        .collect();

    rows.sort_by(|a, b| {
        // Primary: group order (RepoMain < Active < Normal < Done)
        let group_ord = a.display_group.cmp(&b.display_group);
        if group_ord != std::cmp::Ordering::Equal {
            return group_ord;
        }
        // Secondary: focus score — "what needs human attention most?"
        let a_focus = focus_score(a);
        let b_focus = focus_score(b);
        let focus_ord = a_focus.cmp(&b_focus);
        if focus_ord != std::cmp::Ordering::Equal {
            return focus_ord;
        }
        // Tertiary: last tmux activity — more recent activity sorts first.
        // Rows with no activity sort after rows with activity.
        let activity_ord = match (a.last_activity, b.last_activity) {
            (Some(a_ts), Some(b_ts)) => b_ts.cmp(&a_ts), // reverse: higher timestamp first
            (Some(_), None) => std::cmp::Ordering::Less,
            (None, Some(_)) => std::cmp::Ordering::Greater,
            (None, None) => std::cmp::Ordering::Equal,
        };
        if activity_ord != std::cmp::Ordering::Equal {
            return activity_ord;
        }
        // Quaternary: issue number ascending
        match (a.issue_number, b.issue_number) {
            (Some(a_num), Some(b_num)) => a_num.cmp(&b_num),
            (Some(_), None) => std::cmp::Ordering::Less,
            (None, Some(_)) => std::cmp::Ordering::Greater,
            (None, None) => a.branch.cmp(&b.branch),
        }
    });

    rows
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

fn pr_info_from(pr: &CachedPr) -> PrInfo {
    PrInfo {
        number: pr.number,
        branch: pr.branch.clone(),
        state: Some(pr.state.clone()),
        review_decision: pr.review_decision.clone(),
        checks_state: pr.checks_state.clone(),
        has_conflicts: pr.has_conflicts,
        unresolved_threads: pr.unresolved_threads,
        failing_checks: pr.failing_checks.clone(),
        labels: pr.labels.clone(),
        is_draft: pr.is_draft,
    }
}

fn enrich_session(
    session: &CachedTmuxSession,
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> EnrichedSession {
    use crate::claude_state::state_for_session;

    let host = match &session.host {
        Some(h) => Host::Remote(h.clone()),
        None => Host::Local,
    };
    let tmux = TmuxSessionInfo {
        host,
        name: session.name.clone(),
        status: SessionStatus::Running { attached: false },
    };

    let (windows, panes) = build_windows_and_panes(
        &session.pane_targets,
        &session.pane_commands,
        &session.pane_titles,
        &session.window_names,
        &session.window_active,
    );

    // Hook-first: check if a fresh state file exists for this session.
    let hook_state = state_for_session(claude_states, &session.name);
    if let Some(state_file) = hook_state {
        let is_stale = is_state_stale(&state_file.timestamp, HOOK_STATE_STALENESS_SECS);
        if !is_stale {
            let claude = ClaudeSessionInfo::from_state_file(state_file);
            return EnrichedSession {
                tmux,
                claude,
                windows,
                panes,
            };
        }
    }

    // Fallback: terminal scraping.
    enrich_session_from_scraping(session, tmux, windows, panes)
}

/// Derives session info by scraping terminal output (fallback when no hook state).
fn enrich_session_from_scraping(
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
            cost_usd: None,
            context_window_pct: None,
            model: None,
        })
    } else {
        None
    };

    EnrichedSession {
        tmux,
        claude,
        windows,
        panes,
    }
}

/// Returns true if a Claude state file timestamp is older than the default threshold.
pub fn is_state_stale_default(timestamp: &str) -> bool {
    is_state_stale(timestamp, HOOK_STATE_STALENESS_SECS)
}

/// Returns true if the ISO 8601 timestamp is older than `max_age_secs` seconds.
pub fn is_state_stale(timestamp: &str, max_age_secs: u64) -> bool {
    use chrono::Utc;
    match chrono::DateTime::parse_from_rfc3339(timestamp)
        .or_else(|_| chrono::DateTime::parse_from_str(timestamp, "%Y-%m-%dT%H:%M:%SZ"))
    {
        Ok(ts) => {
            let age = Utc::now().signed_duration_since(ts.with_timezone(&Utc));
            age.num_seconds() > max_age_secs as i64
        }
        Err(_) => true, // Can't parse = treat as stale
    }
}

/// Derives the display group for a worktree row.
///
/// Never returns `RepoMain` — that is set separately based on `is_main_worktree`.
fn derive_display_group(
    pr: Option<&PrInfo>,
    sessions: &[EnrichedSession],
    issue_state: Option<&str>,
) -> DisplayGroup {
    // Done: closed issue with merged/closed PR, or closed issue with no PR.
    let issue_closed = issue_state.is_some_and(|s| s == "closed" || s == "completed");
    let pr_done = pr.is_some_and(|p| {
        p.state.as_deref() == Some("merged")
            || p.state.as_deref() == Some("closed")
            || p.state.as_deref() == Some("MERGED")
            || p.state.as_deref() == Some("CLOSED")
    });
    if issue_closed && (pr_done || pr.is_none()) {
        return DisplayGroup::Done;
    }

    // Active: any running tmux session with a Claude process.
    if sessions.iter().any(|s| s.claude.is_some()) {
        return DisplayGroup::Active;
    }

    // Everything else is Normal.
    DisplayGroup::Normal
}

/// Returns a focus score for sorting within a display group. Lower = needs
/// human attention more urgently.
///
/// The score answers: "what needs a human to do something right now?"
/// - Tier 0-9: Blocked on human action (Claude waiting, changes requested, conflicts)
/// - Tier 10-19: Needs attention soon (CI failing, unresolved threads, ready to merge)
/// - Tier 20-29: In progress, no action needed (Claude working, waiting on review)
/// - Tier 30+: No urgency signals
///
/// Within a tier, priority labels (P0/P1) serve as tiebreakers.
fn focus_score(row: &WorktreeRow) -> u32 {
    use crate::claude_state::ClaudeState;

    // Check Claude session states
    let has_input_needed = row.sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Input)
    });
    let has_claude_working = row.sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Working)
    });

    // Check PR states
    let changes_requested = row
        .pr
        .as_ref()
        .is_some_and(|p| p.review_decision.as_deref() == Some("CHANGES_REQUESTED"));
    let has_conflicts = row.pr.as_ref().is_some_and(|p| p.has_conflicts);
    let ci_failing = row
        .pr
        .as_ref()
        .is_some_and(|p| p.checks_state.as_deref() == Some("FAILURE"));
    let has_unresolved = row.pr.as_ref().is_some_and(|p| p.unresolved_threads > 0);
    let ready_to_merge = row.pr.as_ref().is_some_and(|p| {
        p.review_decision.as_deref() == Some("APPROVED")
            && !p.has_conflicts
            && p.checks_state.as_deref() == Some("SUCCESS")
    });
    let pr_waiting_review = row.pr.as_ref().is_some_and(|p| {
        p.review_decision.as_deref() == Some("REVIEW_REQUIRED")
            || p.review_decision.is_none()
    });
    let has_pr = row.pr.is_some();

    // Compute base score by urgency tier
    let base = if has_input_needed {
        0 // Claude is blocked on you — act now
    } else if changes_requested {
        1 // Reviewer is waiting on you
    } else if has_conflicts {
        2 // Blocking progress, needs resolution
    } else if ci_failing {
        10 // Something broke, needs investigation
    } else if has_unresolved {
        11 // Active conversation, needs response
    } else if ready_to_merge {
        12 // Quick win — one click to merge
    } else if has_claude_working {
        20 // In progress, no action needed
    } else if pr_waiting_review {
        21 // Waiting on others, nothing to do
    } else if has_pr {
        22 // PR exists but unclear state
    } else {
        30 // No PR, no sessions — lowest urgency
    };

    // Tiebreaker: priority labels shift within the tier (max shift of 4)
    let label_bonus = label_priority_weight(
        &row.issue_labels,
        row.pr.as_ref().map(|p| &p.labels),
    );
    // label_bonus is 0-3 for P0-P3, 100 for no label. Clamp to 0-4 range.
    let tiebreak = if label_bonus <= 3 { label_bonus } else { 4 };

    base * 10 + tiebreak
}

/// Returns a priority weight from labels. Lower = higher priority.
/// Used as a tiebreaker within focus score tiers.
fn label_priority_weight(issue_labels: &[String], pr_labels: Option<&Vec<String>>) -> u32 {
    let all_labels = issue_labels.iter().chain(pr_labels.into_iter().flatten());
    let mut best = 100u32;
    for label in all_labels {
        let lower = label.to_lowercase();
        let weight = match lower.as_str() {
            "p0" | "critical" | "urgent" => 0,
            "p1" | "high" => 1,
            "p2" | "medium" => 2,
            "p3" | "low" => 3,
            _ => continue,
        };
        best = best.min(weight);
    }
    best
}

fn is_default_branch(branch: &str) -> bool {
    matches!(branch, "main" | "master" | "develop" | "dev")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cache::{CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};

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
        let (windows, panes) = build_windows_and_panes(
            &session.pane_targets,
            &session.pane_commands,
            &session.pane_titles,
            &session.window_names,
            &session.window_active,
        );
        enrich_session_from_scraping(session, tmux, windows, panes)
    }

    fn open_issue(number: u32) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "open".to_string(),
            labels: vec![],
        }
    }

    fn pr_for_branch(pr_number: u32, branch: &str) -> CachedPr {
        CachedPr {
            number: pr_number,
            branch: branch.to_string(),
            linked_issue: None,
            state: "open".to_string(),
            review_decision: None,
            checks_state: None,
            has_conflicts: false,
            unresolved_threads: 0,
            linked_issue_state: None,
            failing_checks: vec![],
            labels: vec![],
            is_draft: false,
        }
    }

    fn approved_passing_pr_for_branch(pr_number: u32, branch: &str) -> CachedPr {
        CachedPr {
            number: pr_number,
            branch: branch.to_string(),
            linked_issue: None,
            state: "open".to_string(),
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()),
            has_conflicts: false,
            unresolved_threads: 0,
            linked_issue_state: None,
            failing_checks: vec![],
            labels: vec![],
            is_draft: false,
        }
    }

    fn worktree(path: &str, branch: &str) -> CachedWorktree {
        CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: false,
            is_locked: false,
            host: None,
        }
    }

    fn bare_worktree(path: &str, branch: &str) -> CachedWorktree {
        CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare: true,
            is_locked: false,
            host: None,
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
            host: None,
            last_output_lines: vec![],
            claude_state_raw: None,
            last_activity: None,
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
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

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
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].branch, "feat/something");
    }

    #[test]
    fn pr_matches_worktree_by_branch() {
        let worktrees = vec![worktree("/workspace/repo-47", "feat/task-centric")];
        let prs = vec![pr_for_branch(55, "feat/task-centric")];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        let pr = rows[0].pr.as_ref().expect("PR should be present");
        assert_eq!(pr.number, 55);
    }

    #[test]
    fn worktree_without_pr_still_shows() {
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/something")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert!(rows[0].pr.is_none());
    }

    #[test]
    fn tmux_session_joins_via_worktree_path() {
        let worktrees = vec![worktree("/workspace/webapp-47", "feat/task-centric")];
        let sessions = vec![session("webapp_47", "/workspace/webapp-47", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].sessions.len(), 1);
        assert_eq!(rows[0].sessions[0].tmux.name, "webapp_47");
    }

    #[test]
    fn session_does_not_match_worktree_with_longer_path_sharing_prefix() {
        // Regression guard for bug #191: session at "/workspace/langwatch" must
        // NOT match the worktree at "/workspace/langwatch-sdk". The paths must
        // be equal, not merely prefix-sharing.
        let worktrees = vec![
            worktree("/workspace/langwatch-sdk", "main"),
            worktree("/workspace/langwatch-sdk-feat", "feat/branch"),
        ];
        let sessions = vec![session("langwatch", "/workspace/langwatch", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

        assert_eq!(rows.len(), 2);
        // Neither worktree should have the session attached — path is different.
        assert!(
            rows[0].sessions.is_empty(),
            "langwatch-sdk must not receive the langwatch session"
        );
        assert!(
            rows[1].sessions.is_empty(),
            "langwatch-sdk-feat must not receive the langwatch session"
        );
    }

    #[test]
    fn session_does_not_match_worktree_when_path_is_substring() {
        // "main" session must NOT match "/workspace/domain-main-service" worktree.
        let worktrees = vec![worktree("/workspace/domain-main-service", "feat/branch")];
        let sessions = vec![session("main", "/workspace/main", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert!(
            rows[0].sessions.is_empty(),
            "domain-main-service must not receive the main session"
        );
    }

    #[test]
    fn session_with_trailing_slash_joins_correct_worktree() {
        // Tmux on some versions appends a trailing slash to #{session_path}.
        // After normalization, it should still match exactly.
        let worktrees = vec![worktree("/workspace/webapp-47", "feat/branch")];
        // Trailing slash is stripped by normalize_path when building CachedTmuxSession,
        // so the stored path is "/workspace/webapp-47" — we pass the normalized form here.
        let normalized_sessions = vec![session("webapp_47", "/workspace/webapp-47", vec!["bash"])];

        let rows = derive_worktree_rows(
            &[],
            &[],
            &worktrees,
            &normalized_sessions,
            "owner/repo",
            &[],
        );

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].sessions.len(), 1);
    }

    #[test]
    fn multiple_sessions_at_same_path_all_join() {
        let worktrees = vec![worktree("/workspace/webapp-47", "feat/task-centric")];
        let sessions = vec![
            session("webapp_47_main", "/workspace/webapp-47", vec!["bash"]),
            session("webapp_47_claude", "/workspace/webapp-47", vec!["claude"]),
        ];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].issue_number, Some(2478));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #2478"));
    }

    #[test]
    fn issue_title_none_when_issue_not_in_cache() {
        let worktrees = vec![worktree("/workspace/webapp-2478", "webapp-2478")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].issue_number, Some(2478));
        assert!(rows[0].issue_title.is_none());
    }

    #[test]
    fn no_issue_number_for_plain_branch() {
        let worktrees = vec![worktree("/workspace/repo", "main")];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[0].issue_number, Some(42));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #42"));
    }

    #[test]
    fn issue_number_falls_back_to_branch_name_when_pr_has_no_linked_issue() {
        let issues = vec![open_issue(200)];
        let prs = vec![pr_for_branch(10, "feat/200-my-feature")]; // linked_issue: None
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[0].issue_number, Some(200));
        assert_eq!(rows[0].issue_title.as_deref(), Some("Issue #200"));
    }

    #[test]
    fn issue_number_falls_back_to_branch_name_when_no_pr() {
        let issues = vec![open_issue(200)];
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/200-my-feature")];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

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
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert!(rows[0].is_main_worktree);
        assert!(!rows[1].is_main_worktree);
    }

    #[test]
    fn main_worktree_gets_repo_main_display_group() {
        let worktrees = vec![worktree("/workspace/repo", "main")];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[0].display_group, DisplayGroup::RepoMain);
    }

    #[test]
    fn session_ending_with_main_is_main_worktree() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let sessions = vec![session("webapp_main", "/workspace/repo-feat", vec!["bash"])];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows.len(), 2);
        assert!(rows[0].is_main_worktree); // first non-bare
        assert!(!rows[1].is_main_worktree);
    }

    // -----------------------------------------------------------------------
    // Display group tests
    // -----------------------------------------------------------------------

    #[test]
    fn display_group_normal_changes_requested_no_session() {
        // Without an active session, changes_requested PR → Normal
        let prs = vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let worktrees = [worktree("/workspace/repo-feat", "feat/branch")];

        // Use second worktree to avoid shepherd
        let all_wts = vec![worktree("/workspace/repo", "main"), worktrees[0].clone()];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_normal_conflicts_no_session() {
        let prs = vec![CachedPr {
            has_conflicts: true,
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_normal_failing_ci_no_session() {
        let prs = vec![CachedPr {
            checks_state: Some("failing".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_normal_unresolved_threads_no_session() {
        let prs = vec![CachedPr {
            unresolved_threads: 2,
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_active_claude_working_with_pr() {
        let prs = vec![pr_for_branch(55, "feat/branch")];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/workspace/repo-47", vec!["claude"])
        }];

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn display_group_active_claude_working_without_pr() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/workspace/repo-47", vec!["claude"])
        }];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn display_group_normal_ready_to_merge_no_session() {
        // Ready-to-merge with no active session → Normal
        let prs = vec![approved_passing_pr_for_branch(55, "feat/branch")];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_normal_no_pr() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &[], &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
    }

    #[test]
    fn display_group_done_closed_issue_no_pr() {
        let issues = vec![CachedIssue {
            number: 47,
            title: "done issue".to_string(),
            state: "closed".to_string(),
            labels: vec![],
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/issue-47"),
        ];
        let rows = derive_worktree_rows(&issues, &[], &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Done);
    }

    #[test]
    fn display_group_done_closed_issue_with_merged_pr() {
        let issues = vec![CachedIssue {
            number: 47,
            title: "done issue".to_string(),
            state: "closed".to_string(),
            labels: vec![],
        }];
        let prs = vec![CachedPr {
            state: "merged".to_string(),
            ..pr_for_branch(55, "feat/issue-47")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/issue-47"),
        ];
        let rows = derive_worktree_rows(&issues, &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Done);
    }

    #[test]
    fn merged_pr_in_cache_links_to_worktree_branch() {
        // Regression: merged PRs must be matched to their worktree by branch name
        // so that `orchard --json` shows the PR on the worktree row. This requires
        // the PR cache to contain merged PRs (fixed in refresh_prs: states: [OPEN, MERGED]).
        let prs = vec![CachedPr {
            state: "merged".to_string(),
            ..pr_for_branch(99, "feat/issue-99")
        }];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-99", "feat/issue-99"),
        ];

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

        let feat_row = rows
            .iter()
            .find(|r| r.branch == "feat/issue-99")
            .expect("worktree row for feat/issue-99 must exist");
        let pr = feat_row
            .pr
            .as_ref()
            .expect("merged PR must be linked to the worktree");
        assert_eq!(pr.number, 99);
        assert_eq!(pr.state.as_deref(), Some("merged"));
    }

    #[test]
    fn merged_pr_with_closed_issue_is_done() {
        // When a merged PR is in the cache (bug #95 fixed), and its linked issue
        // is closed, the worktree row must derive DisplayGroup::Done (bug #152).
        let issues = vec![CachedIssue {
            number: 99,
            title: "completed feature".to_string(),
            state: "closed".to_string(),
            labels: vec![],
        }];
        let prs = vec![CachedPr {
            linked_issue: Some(99),
            state: "merged".to_string(),
            ..pr_for_branch(55, "feat/issue-99")
        }];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-99", "feat/issue-99"),
        ];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

        let feat_row = rows
            .iter()
            .find(|r| r.branch == "feat/issue-99")
            .expect("worktree row for feat/issue-99 must exist");
        assert_eq!(
            feat_row.display_group,
            DisplayGroup::Done,
            "merged PR + closed issue must derive DisplayGroup::Done"
        );
    }

    #[test]
    fn display_group_ordering() {
        assert!(DisplayGroup::RepoMain < DisplayGroup::Active);
        assert!(DisplayGroup::Active < DisplayGroup::Normal);
        assert!(DisplayGroup::Normal < DisplayGroup::Done);
    }

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

    #[test]
    fn display_group_active_when_claude_needs_input() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![CachedTmuxSession {
            last_output_lines: vec!["Do you want to proceed?".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("repo_47", "/workspace/repo-47", vec![])
        }];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn active_when_claude_needs_input_with_pr() {
        let prs = vec![pr_for_branch(55, "feat/branch")];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![CachedTmuxSession {
            last_output_lines: vec!["Allow Read tool? (y/n)".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("repo_47", "/workspace/repo-47", vec![])
        }];

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn active_when_claude_session_present_despite_pr_issues() {
        // Session presence trumps PR state — any active Claude session = Active
        let prs = vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47", "/workspace/repo-47", vec!["claude"])];

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
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

        let rows = derive_all_repos(&repo_caches, &[]);

        assert_eq!(rows[0].display_group, DisplayGroup::RepoMain);
        assert!(rows[0].is_main_worktree);
        // Ready-to-merge with no session → Normal
        assert_eq!(rows[1].display_group, DisplayGroup::Normal);
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

        let rows = derive_all_repos(&repo_caches, &[]);

        // RepoMain rows come first
        let shepherd_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::RepoMain)
            .collect();
        assert_eq!(shepherd_rows.len(), 2);

        // All non-main rows are Normal (no sessions, no closed issues)
        let non_shepherd: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group != DisplayGroup::RepoMain)
            .collect();
        assert!(non_shepherd
            .iter()
            .all(|r| r.display_group == DisplayGroup::Normal));

        // Normal rows sorted by issue number
        let normal_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::Normal)
            .collect();
        assert_eq!(normal_rows.len(), 3);
        assert_eq!(normal_rows[0].issue_number, Some(100));
        assert_eq!(normal_rows[1].issue_number, Some(300));
        assert_eq!(normal_rows[2].issue_number, Some(500));
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

        let rows = derive_all_repos(&repo_caches, &[]);

        let normal_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::Normal)
            .collect();
        assert_eq!(normal_rows.len(), 2);
        assert_eq!(normal_rows[0].branch, "a-feature");
        assert_eq!(normal_rows[1].branch, "z-feature");
    }

    #[test]
    fn label_priority_weight_p0_is_lowest() {
        assert_eq!(label_priority_weight(&["p0".to_string()], None), 0);
    }

    #[test]
    fn label_priority_weight_p1_is_1() {
        assert_eq!(label_priority_weight(&["p1".to_string()], None), 1);
    }

    #[test]
    fn label_priority_weight_no_label_is_100() {
        assert_eq!(label_priority_weight(&["bugfix".to_string()], None), 100);
    }

    #[test]
    fn label_priority_weight_pr_labels_checked() {
        assert_eq!(
            label_priority_weight(&[], Some(&vec!["critical".to_string()])),
            0
        );
    }

    #[test]
    fn label_priority_weight_best_wins() {
        assert_eq!(
            label_priority_weight(&["p3".to_string(), "p1".to_string()], None),
            1
        );
    }

    #[test]
    fn derive_all_repos_sorts_by_label_weight_within_group() {
        let repo_caches = vec![(
            "owner/repo".to_string(),
            vec![
                CachedIssue {
                    number: 1,
                    title: "low priority".to_string(),
                    state: "open".to_string(),
                    labels: vec!["p3".to_string()],
                },
                CachedIssue {
                    number: 2,
                    title: "critical".to_string(),
                    state: "open".to_string(),
                    labels: vec!["p0".to_string()],
                },
            ],
            vec![],
            vec![
                worktree("/workspace/repo", "main"),
                worktree("/workspace/repo-1", "feat/issue-1"),
                worktree("/workspace/repo-2", "feat/issue-2"),
            ],
            vec![],
        )];

        let rows = derive_all_repos(&repo_caches, &[]);

        let normal_rows: Vec<&WorktreeRow> = rows
            .iter()
            .filter(|r| r.display_group == DisplayGroup::Normal)
            .collect();
        // p0 (issue 2) should sort before p3 (issue 1) despite higher issue number
        assert_eq!(normal_rows[0].issue_number, Some(2));
        assert_eq!(normal_rows[1].issue_number, Some(1));
    }

    // -----------------------------------------------------------------------
    // Hook-first state derivation tests
    // -----------------------------------------------------------------------

    fn fresh_state_file(tmux_session: &str, state: &str) -> crate::claude_state::ClaudeStateFile {
        crate::claude_state::ClaudeStateFile {
            state: state.to_string(),
            session_id: "sess-abc".to_string(),
            tmux_session: tmux_session.to_string(),
            cwd: "/workspace/repo".to_string(),
            event: "PreToolUse".to_string(),
            timestamp: chrono::Utc::now().format("%Y-%m-%dT%H:%M:%SZ").to_string(),
            context_window_pct: None,
            cost_usd: None,
            model: None,
            stop_reason: None,
            inflight_tool_count: None,
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
            context_window_pct: None,
            cost_usd: None,
            model: None,
            stop_reason: None,
            inflight_tool_count: None,
        }
    }

    #[test]
    fn hook_state_working_maps_to_claude_working() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "working")];
        let info = enrich_session(&s, &states);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Working);
    }

    #[test]
    fn hook_state_idle_maps_to_claude_idle() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "idle")];
        let info = enrich_session(&s, &states);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Idle);
    }

    #[test]
    fn hook_state_input_maps_to_claude_input() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "input")];
        let info = enrich_session(&s, &states);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Input);
    }

    #[test]
    fn hook_state_enrichment_fields_propagate() {
        let s = session("repo_47", "/path", vec![]);
        let mut state = fresh_state_file("repo_47", "working");
        state.context_window_pct = Some(73.0);
        state.cost_usd = Some(0.42);
        state.model = Some("opus".to_string());
        let info = enrich_session(&s, &[state]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.context_window_pct, Some(73.0));
        assert_eq!(claude.cost_usd, Some(0.42));
        assert_eq!(claude.model.as_deref(), Some("opus"));
    }

    #[test]
    fn stale_hook_state_falls_back_to_terminal_scraping() {
        // Stale state file says "idle" but terminal shows working
        let s = CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/path", vec!["claude"])
        };
        let states = vec![stale_state_file("repo_47", "idle")];
        let info = enrich_session(&s, &states);
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
        let info = enrich_session(&s, &[]);
        let claude = info.claude.as_ref().unwrap();
        assert_eq!(claude.status, crate::claude_state::ClaudeState::Input);
        assert_eq!(claude.context_window_pct, None);
    }

    #[test]
    fn hook_state_display_group_input_becomes_active() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![fresh_state_file("repo_47_claude", "input")];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn hook_state_display_group_working_becomes_active() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![fresh_state_file("repo_47_claude", "working")];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states);

        assert_eq!(rows[1].display_group, DisplayGroup::Active);
    }

    #[test]
    fn is_state_stale_returns_true_for_very_old_timestamp() {
        let old = "2020-01-01T00:00:00Z";
        assert!(is_state_stale(old, HOOK_STATE_STALENESS_SECS));
    }

    #[test]
    fn is_state_stale_returns_false_for_fresh_timestamp() {
        let now = chrono::Utc::now().format("%Y-%m-%dT%H:%M:%SZ").to_string();
        assert!(!is_state_stale(&now, HOOK_STATE_STALENESS_SECS));
    }

    #[test]
    fn is_state_stale_returns_true_for_unparseable_timestamp() {
        assert!(is_state_stale("not-a-timestamp", HOOK_STATE_STALENESS_SECS));
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
        }
    }

    fn completed_issue(number: u32) -> CachedIssue {
        CachedIssue {
            number,
            title: format!("Issue #{number}"),
            state: "completed".to_string(),
            labels: vec![],
        }
    }

    #[test]
    fn issue_state_populated_from_cached_issue() {
        let issues = vec![closed_issue(200)];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[1].issue_state.as_deref(), Some("closed"));
    }

    #[test]
    fn issue_state_populated_for_open_issue() {
        let issues = vec![open_issue(200)];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[1].issue_state.as_deref(), Some("open"));
    }

    #[test]
    fn issue_state_none_when_issue_not_in_cache() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

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

        let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

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

            let rows = derive_worktree_rows(&[], &prs, &worktrees, &[], "owner/repo", &[]);

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
            host: None,
            last_output_lines: vec![],
            claude_state_raw: None,
            last_activity: None,
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
            host: None,
            last_output_lines: vec![],
            claude_state_raw: None,
            last_activity: None,
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
}
