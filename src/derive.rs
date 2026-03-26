use crate::cache::{CachedIssue, CachedPr, CachedTmuxSession, CachedWorktree};
use crate::github;

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/// Rendering order for worktree rows. Variants are ordered so that `Ord` gives
/// the correct sort order (Shepherd first, Other last).
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum DisplayGroup {
    Shepherd,       // 0th — always first (the repo_main session)
    NeedsAttention, // 1st
    ClaudeWorking,  // 2nd
    ReadyToMerge,   // 3rd
    Other,          // 4th (worktrees without PRs, misc)
}

/// Lightweight PR summary attached to a worktree row.
#[derive(Debug, Clone)]
pub struct PrInfo {
    pub number: u32,
    pub branch: String,
    pub review_decision: Option<String>,
    pub checks_state: Option<String>,
    pub has_conflicts: bool,
    pub unresolved_threads: u32,
}

/// Lightweight tmux session summary attached to a worktree row.
#[derive(Debug, Clone)]
pub struct SessionInfo {
    pub name: String,
    pub host: Option<String>,
    pub has_claude_active: bool,
    /// True when Claude is actively working (spinner/activity indicator visible).
    pub claude_is_working: bool,
    /// True when Claude appears to be waiting for user input.
    pub claude_needs_input: bool,
    /// Structured Claude state from hook files (replaces booleans when available).
    pub claude_state: crate::claude_state::ClaudeState,
    /// Context window usage percentage from hook state enrichment.
    pub context_window_pct: Option<f64>,
    /// Cumulative session cost in USD from hook state enrichment.
    pub cost_usd: Option<f64>,
    /// Model name from hook state enrichment (e.g., "opus", "sonnet").
    pub model: Option<String>,
}

/// One row in the derived worktree view. Corresponds to one non-bare worktree,
/// enriched with PR/issue metadata and tmux session info.
#[derive(Debug, Clone)]
pub struct WorktreeRow {
    pub repo_slug: String,
    pub worktree_path: String,
    pub branch: String,
    pub worktree_host: Option<String>,
    pub issue_number: Option<u32>,
    pub issue_title: Option<String>,
    /// State of the linked issue ("open", "closed", "completed"), if any.
    /// Used to detect stale worktrees whose issue has been resolved without a PR.
    pub issue_state: Option<String>,
    pub pr: Option<PrInfo>,
    pub sessions: Vec<SessionInfo>,
    pub display_group: DisplayGroup,
    pub is_shepherd: bool,
}

/// Alias to minimize cascading changes in TUI code.
pub type TaskRow = WorktreeRow;

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
/// 6. Detect shepherd (first non-bare worktree or session name ending `_main`).
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

        let session_infos: Vec<SessionInfo> = sessions
            .iter()
            .filter(|s| s.path == wt.path)
            .map(|s| session_info_from(s, claude_states))
            .collect();

        let issue_number = github::extract_issue_number(&wt.branch);
        let linked_issue = issue_number.and_then(|num| issues.iter().find(|i| i.number == num));
        let issue_title = linked_issue.map(|i| i.title.clone());
        // Only populate issue_state when there is no PR — mirrors the legacy collector
        // logic in `apply_issue_states`, where PRs take precedence over issue state.
        let issue_state = if pr_info.is_none() {
            linked_issue.map(|i| i.state.clone())
        } else {
            None
        };

        let is_shepherd = is_first_non_bare
            || session_infos.iter().any(|s| s.name.ends_with("_main"));

        let display_group = if is_shepherd {
            DisplayGroup::Shepherd
        } else {
            derive_display_group(pr_info.as_ref(), &session_infos)
        };

        rows.push(WorktreeRow {
            repo_slug: repo_slug.to_string(),
            worktree_path: wt.path.clone(),
            branch: wt.branch.clone(),
            worktree_host: wt.host.clone(),
            issue_number,
            issue_title,
            issue_state,
            pr: pr_info,
            sessions: session_infos,
            display_group,
            is_shepherd,
        });

        is_first_non_bare = false;
    }

    rows
}

/// Alias for backward compatibility.
pub fn derive_task_rows(
    issues: &[CachedIssue],
    prs: &[CachedPr],
    worktrees: &[CachedWorktree],
    sessions: &[CachedTmuxSession],
    repo_slug: &str,
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> Vec<WorktreeRow> {
    derive_worktree_rows(issues, prs, worktrees, sessions, repo_slug, claude_states)
}

// ---------------------------------------------------------------------------
// Multi-repo aggregation
// ---------------------------------------------------------------------------

/// Derives and sorts worktree rows across all configured repositories.
///
/// Each tuple is `(repo_slug, issues, prs, worktrees, sessions)`. Rows are
/// sorted: Shepherd first, then by display group, then by issue number
/// (worktrees without issue numbers sort by branch name).
pub fn derive_all_repos(
    repo_caches: &[(
        String,
        Vec<CachedIssue>,
        Vec<CachedPr>,
        Vec<CachedWorktree>,
        Vec<CachedTmuxSession>,
    )],
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> Vec<WorktreeRow> {
    let mut rows: Vec<WorktreeRow> = repo_caches
        .iter()
        .flat_map(|(slug, issues, prs, worktrees, sessions)| {
            derive_worktree_rows(issues, prs, worktrees, sessions, slug, claude_states)
        })
        .collect();

    rows.sort_by(|a, b| {
        a.display_group.cmp(&b.display_group).then_with(|| {
            match (a.issue_number, b.issue_number) {
                (Some(a_num), Some(b_num)) => a_num.cmp(&b_num),
                (Some(_), None) => std::cmp::Ordering::Less,
                (None, Some(_)) => std::cmp::Ordering::Greater,
                (None, None) => a.branch.cmp(&b.branch),
            }
        })
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
        review_decision: pr.review_decision.clone(),
        checks_state: pr.checks_state.clone(),
        has_conflicts: pr.has_conflicts,
        unresolved_threads: pr.unresolved_threads,
    }
}

fn session_info_from(
    session: &CachedTmuxSession,
    claude_states: &[crate::claude_state::ClaudeStateFile],
) -> SessionInfo {
    use crate::claude_state::{ClaudeState, state_for_session};

    // Hook-first: check if a fresh state file exists for this session.
    let hook_state = state_for_session(claude_states, &session.name);
    if let Some(state_file) = hook_state {
        let is_stale = is_state_stale(&state_file.timestamp, 300);
        if !is_stale {
            let claude_state = ClaudeState::from_str(&state_file.state);
            return SessionInfo {
                name: session.name.clone(),
                host: session.host.clone(),
                has_claude_active: claude_state != ClaudeState::None,
                claude_is_working: claude_state == ClaudeState::Working,
                claude_needs_input: claude_state == ClaudeState::Input,
                claude_state,
                context_window_pct: state_file.context_window_pct,
                cost_usd: state_file.cost_usd,
                model: state_file.model.clone(),
            };
        }
    }

    // Fallback: terminal scraping.
    session_info_from_scraping(session)
}

/// Derives session info by scraping terminal output (fallback when no hook state).
fn session_info_from_scraping(session: &CachedTmuxSession) -> SessionInfo {
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
    let claude_is_working = has_claude_active
        && last_content.iter().any(|line| line.contains("tokens)"));

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

    SessionInfo {
        name: session.name.clone(),
        host: session.host.clone(),
        has_claude_active,
        claude_is_working,
        claude_needs_input,
        claude_state,
        context_window_pct: None,
        cost_usd: None,
        model: None,
    }
}

/// Returns true if the ISO 8601 timestamp is older than `max_age_secs` seconds.
fn is_state_stale(timestamp: &str, max_age_secs: u64) -> bool {
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

/// Derives the display group for a worktree row. Priority order:
/// NeedsAttention > ClaudeWorking > ReadyToMerge > Other.
///
/// Never returns `Shepherd` — that is set separately based on `is_shepherd`.
fn derive_display_group(pr: Option<&PrInfo>, sessions: &[SessionInfo]) -> DisplayGroup {
    // Claude waiting for input = needs your attention (highest priority, before PR state).
    if sessions.iter().any(|s| s.claude_needs_input) {
        return DisplayGroup::NeedsAttention;
    }

    if let Some(pr) = pr {
        if is_needs_attention(pr) {
            return DisplayGroup::NeedsAttention;
        }

        if sessions.iter().any(|s| s.claude_is_working) {
            return DisplayGroup::ClaudeWorking;
        }

        if is_ready_to_merge(pr) {
            return DisplayGroup::ReadyToMerge;
        }
    } else {
        // No PR — check if Claude is actively working in sessions.
        if sessions.iter().any(|s| s.claude_is_working) {
            return DisplayGroup::ClaudeWorking;
        }
    }

    DisplayGroup::Other
}

fn is_default_branch(branch: &str) -> bool {
    matches!(branch, "main" | "master" | "develop" | "dev")
}

fn is_needs_attention(pr: &PrInfo) -> bool {
    pr.review_decision.as_deref() == Some("changes_requested")
        || pr.has_conflicts
        || pr.checks_state.as_deref() == Some("failing")
        || pr.unresolved_threads > 0
}

fn is_ready_to_merge(pr: &PrInfo) -> bool {
    pr.review_decision.as_deref() == Some("approved")
        && pr.checks_state.as_deref() == Some("passing")
        && !pr.has_conflicts
        && pr.unresolved_threads == 0
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
        CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            pane_titles: vec![],
            pane_commands: pane_commands.into_iter().map(|s| s.to_string()).collect(),
            host: None,
            last_output_lines: vec![],
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
        let sessions = vec![session(
            "webapp_47",
            "/workspace/webapp-47",
            vec!["bash"],
        )];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].sessions.len(), 1);
        assert_eq!(rows[0].sessions[0].name, "webapp_47");
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
        let names: Vec<&str> = rows[0].sessions.iter().map(|s| s.name.as_str()).collect();
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
    // Shepherd detection tests
    // -----------------------------------------------------------------------

    #[test]
    fn first_non_bare_worktree_is_shepherd() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert!(rows[0].is_shepherd);
        assert!(!rows[1].is_shepherd);
    }

    #[test]
    fn shepherd_gets_shepherd_display_group() {
        let worktrees = vec![worktree("/workspace/repo", "main")];
        let rows = derive_worktree_rows(&[], &[], &worktrees, &[], "owner/repo", &[]);

        assert_eq!(rows[0].display_group, DisplayGroup::Shepherd);
    }

    #[test]
    fn session_ending_with_main_is_shepherd() {
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/something"),
        ];
        let sessions = vec![session(
            "webapp_main",
            "/workspace/repo-feat",
            vec!["bash"],
        )];

        let rows = derive_worktree_rows(&[], &[], &worktrees, &sessions, "owner/repo", &[]);

        assert!(rows[0].is_shepherd); // first non-bare
        assert!(rows[1].is_shepherd); // session name ends with _main
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
        assert!(rows[0].is_shepherd);  // first non-bare
        assert!(!rows[1].is_shepherd);
    }

    // -----------------------------------------------------------------------
    // Display group tests
    // -----------------------------------------------------------------------

    #[test]
    fn display_group_needs_attention_changes_requested() {
        let prs = vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let worktrees = vec![worktree("/workspace/repo-feat", "feat/branch")];

        // Use second worktree to avoid shepherd
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktrees[0].clone(),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn display_group_needs_attention_conflicts() {
        let prs = vec![CachedPr {
            has_conflicts: true,
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn display_group_needs_attention_failing_ci() {
        let prs = vec![CachedPr {
            checks_state: Some("failing".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn display_group_needs_attention_unresolved_threads() {
        let prs = vec![CachedPr {
            unresolved_threads: 2,
            ..pr_for_branch(55, "feat/branch")
        }];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn display_group_claude_working_with_pr() {
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

        assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
    }

    #[test]
    fn display_group_claude_working_without_pr() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/workspace/repo-47", vec!["claude"])
        }];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
    }

    #[test]
    fn display_group_ready_to_merge() {
        let prs = vec![approved_passing_pr_for_branch(55, "feat/branch")];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
    }

    #[test]
    fn display_group_other_no_pr() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &[], &all_wts, &[], "owner/repo", &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Other);
    }

    #[test]
    fn display_group_ordering() {
        assert!(DisplayGroup::Shepherd < DisplayGroup::NeedsAttention);
        assert!(DisplayGroup::NeedsAttention < DisplayGroup::ClaudeWorking);
        assert!(DisplayGroup::ClaudeWorking < DisplayGroup::ReadyToMerge);
        assert!(DisplayGroup::ReadyToMerge < DisplayGroup::Other);
    }

    #[test]
    fn claude_needs_input_false_for_idle_prompt() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["❯ ".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(!info.claude_needs_input);
    }

    #[test]
    fn claude_needs_input_detected_from_yes_no_prompt() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to continue? (y/n)".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(info.claude_needs_input);
    }

    #[test]
    fn claude_needs_input_detected_from_question_mark() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to proceed?".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(info.claude_needs_input);
    }

    #[test]
    fn claude_needs_input_false_when_claude_not_active() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["❯ ".to_string()],
            pane_commands: vec!["bash".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(!info.claude_needs_input);
    }

    #[test]
    fn claude_needs_input_false_when_no_prompt_patterns() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Compiling project...".to_string()],
            pane_commands: vec!["claude".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(!info.claude_needs_input);
    }

    #[test]
    fn pane_title_containing_claude_sets_has_claude_active() {
        let s = CachedTmuxSession {
            pane_titles: vec!["Claude Code - my-project".to_string()],
            pane_commands: vec!["node".to_string()],
            ..session("s", "/path", vec![])
        };
        let info = session_info_from_scraping(&s);
        assert!(info.has_claude_active);
    }

    #[test]
    fn display_group_needs_attention_when_claude_needs_input() {
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

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn claude_needs_input_takes_priority_over_claude_working() {
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

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn needs_attention_takes_priority_over_claude_working() {
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

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
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

        assert_eq!(rows[0].display_group, DisplayGroup::Shepherd);
        assert!(rows[0].is_shepherd);
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

        let rows = derive_all_repos(&repo_caches, &[]);

        // Shepherds first (sorted by issue number / branch)
        let shepherd_rows: Vec<&WorktreeRow> = rows.iter().filter(|r| r.display_group == DisplayGroup::Shepherd).collect();
        assert_eq!(shepherd_rows.len(), 2);

        // ReadyToMerge before Other
        let non_shepherd: Vec<&WorktreeRow> = rows.iter().filter(|r| r.display_group != DisplayGroup::Shepherd).collect();
        assert_eq!(non_shepherd[0].display_group, DisplayGroup::ReadyToMerge);
        assert_eq!(non_shepherd[0].issue_number, Some(100));

        // Other rows sorted by issue number
        let other_rows: Vec<&WorktreeRow> = rows.iter().filter(|r| r.display_group == DisplayGroup::Other).collect();
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

        let rows = derive_all_repos(&repo_caches, &[]);

        let other_rows: Vec<&WorktreeRow> = rows.iter().filter(|r| r.display_group == DisplayGroup::Other).collect();
        assert_eq!(other_rows.len(), 2);
        assert_eq!(other_rows[0].branch, "a-feature");
        assert_eq!(other_rows[1].branch, "z-feature");
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
        }
    }

    #[test]
    fn hook_state_working_maps_to_claude_is_working() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "working")];
        let info = session_info_from(&s, &states);
        assert_eq!(info.claude_state, crate::claude_state::ClaudeState::Working);
        assert!(info.claude_is_working);
        assert!(!info.claude_needs_input);
    }

    #[test]
    fn hook_state_idle_maps_to_has_claude_active() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "idle")];
        let info = session_info_from(&s, &states);
        assert_eq!(info.claude_state, crate::claude_state::ClaudeState::Idle);
        assert!(info.has_claude_active);
        assert!(!info.claude_is_working);
        assert!(!info.claude_needs_input);
    }

    #[test]
    fn hook_state_input_maps_to_claude_needs_input() {
        let s = session("repo_47", "/path", vec![]);
        let states = vec![fresh_state_file("repo_47", "input")];
        let info = session_info_from(&s, &states);
        assert_eq!(info.claude_state, crate::claude_state::ClaudeState::Input);
        assert!(info.claude_needs_input);
    }

    #[test]
    fn hook_state_enrichment_fields_propagate() {
        let s = session("repo_47", "/path", vec![]);
        let mut state = fresh_state_file("repo_47", "working");
        state.context_window_pct = Some(73.0);
        state.cost_usd = Some(0.42);
        state.model = Some("opus".to_string());
        let info = session_info_from(&s, &[state]);
        assert_eq!(info.context_window_pct, Some(73.0));
        assert_eq!(info.cost_usd, Some(0.42));
        assert_eq!(info.model.as_deref(), Some("opus"));
    }

    #[test]
    fn stale_hook_state_falls_back_to_terminal_scraping() {
        // Stale state file says "idle" but terminal shows working
        let s = CachedTmuxSession {
            last_output_lines: vec!["✢ Thinking... (1m 5s · ↑ 2.3k tokens)".to_string()],
            ..session("repo_47", "/path", vec!["claude"])
        };
        let states = vec![stale_state_file("repo_47", "idle")];
        let info = session_info_from(&s, &states);
        // Should use scraping result (working), not stale hook (idle)
        assert!(info.claude_is_working, "expected scraping fallback to detect working");
    }

    #[test]
    fn no_hook_state_falls_back_to_terminal_scraping() {
        let s = CachedTmuxSession {
            last_output_lines: vec!["Do you want to proceed? (y/n)".to_string()],
            ..session("s", "/path", vec!["claude"])
        };
        let info = session_info_from(&s, &[]);
        assert!(info.claude_needs_input);
        assert_eq!(info.context_window_pct, None);
    }

    #[test]
    fn hook_state_display_group_input_becomes_needs_attention() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![fresh_state_file("repo_47_claude", "input")];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn hook_state_display_group_working_becomes_claude_working() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![fresh_state_file("repo_47_claude", "working")];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states);

        assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
    }

    #[test]
    fn is_state_stale_returns_true_for_very_old_timestamp() {
        let old = "2020-01-01T00:00:00Z";
        assert!(is_state_stale(old, 300));
    }

    #[test]
    fn is_state_stale_returns_false_for_fresh_timestamp() {
        let now = chrono::Utc::now().format("%Y-%m-%dT%H:%M:%SZ").to_string();
        assert!(!is_state_stale(&now, 300));
    }

    #[test]
    fn is_state_stale_returns_true_for_unparseable_timestamp() {
        assert!(is_state_stale("not-a-timestamp", 300));
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
    fn issue_state_none_when_worktree_has_pr() {
        // When a PR exists, issue_state should be None (PR takes precedence).
        let issues = vec![completed_issue(200)];
        let prs = vec![pr_for_branch(55, "feat/issue-200-my-feature")];
        let worktrees = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-200", "feat/issue-200-my-feature"),
        ];

        let rows = derive_worktree_rows(&issues, &prs, &worktrees, &[], "owner/repo", &[]);

        assert!(rows[1].pr.is_some(), "PR should be matched");
        assert!(rows[1].issue_state.is_none(), "issue_state suppressed by PR");
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
}
