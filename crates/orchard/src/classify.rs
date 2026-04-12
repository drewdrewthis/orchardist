//! Display group classification from joined worktree/PR/session data.
//!
//! Takes fully joined data (PR info + enriched sessions + issue state) and returns
//! the [`DisplayGroup`] that controls row ordering in the TUI and JSON output.
//! No I/O — pure functions only.

use crate::derive::{DisplayGroup, PrInfo};
use crate::session::EnrichedSession;

/// Derives the display group for a worktree row. Priority order:
/// NeedsAttention > ClaudeWorking > ReadyToMerge > Other.
///
/// Never returns `RepoMain` — that is set separately based on `is_main_worktree`.
pub(crate) fn derive_display_group(
    pr: Option<&PrInfo>,
    sessions: &[EnrichedSession],
    issue_state: Option<&str>,
) -> DisplayGroup {
    use crate::claude_state::ClaudeState;

    // Claude waiting for input = needs your attention (highest priority, before PR state).
    if sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Input)
    }) {
        return DisplayGroup::NeedsAttention;
    }

    // Closed/completed issue with no PR = stale worktree, needs cleanup.
    if pr.is_none()
        && let Some(state) = issue_state
        && (state == "closed" || state == "completed")
    {
        return DisplayGroup::NeedsAttention;
    }

    if let Some(pr) = pr {
        // Merged/closed PR = stale worktree.
        if pr.state.as_deref() == Some("merged") || pr.state.as_deref() == Some("closed") {
            return DisplayGroup::NeedsAttention;
        }

        if is_needs_attention(pr) {
            return DisplayGroup::NeedsAttention;
        }

        if sessions.iter().any(|s| {
            s.claude
                .as_ref()
                .is_some_and(|c| c.status == ClaudeState::Working)
        }) {
            return DisplayGroup::ClaudeWorking;
        }

        if is_ready_to_merge(pr) {
            return DisplayGroup::ReadyToMerge;
        }
    } else {
        // No PR — check if Claude is actively working in sessions.
        if sessions.iter().any(|s| {
            s.claude
                .as_ref()
                .is_some_and(|c| c.status == ClaudeState::Working)
        }) {
            return DisplayGroup::ClaudeWorking;
        }
    }

    DisplayGroup::Other
}

/// Returns true when the PR requires human intervention before it can merge.
pub(crate) fn is_needs_attention(pr: &PrInfo) -> bool {
    if pr.review_decision.as_deref() == Some("changes_requested")
        || pr.has_conflicts
        || pr.unresolved_threads > 0
    {
        return true;
    }

    // Prefer ci_code_state when present: only code failures require session action.
    // A blocked gate check means a human must approve, not that code is broken —
    // do NOT fire NeedsAttention on gate-blocked alone (issue #218).
    if let Some(code_state) = pr.ci_code_state.as_deref() {
        return code_state == "failing";
    }

    // Fallback for cache files predating split CI state (ci_code_state absent).
    #[allow(deprecated)] // checks_state: retained for one release per issue #218
    {
        pr.checks_state.as_deref() == Some("failing")
    }
}

/// Returns true when the PR is approved, passing, and conflict-free.
pub(crate) fn is_ready_to_merge(pr: &PrInfo) -> bool {
    if pr.review_decision.as_deref() != Some("approved")
        || pr.has_conflicts
        || pr.unresolved_threads > 0
    {
        return false;
    }

    // Prefer ci_code_state + ci_gate_state when present.
    // Both code must be passing AND gate must not be blocked/pending.
    if let Some(code_state) = pr.ci_code_state.as_deref() {
        return code_state == "passing"
            && pr.ci_gate_state.as_deref() != Some("blocked")
            && pr.ci_gate_state.as_deref() != Some("pending");
    }

    // Fallback for cache files predating split CI state.
    #[allow(deprecated)] // checks_state: retained for one release per issue #218
    {
        pr.checks_state.as_deref() == Some("passing")
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
#[allow(deprecated)] // PrInfo.checks_state — fixtures still populate the legacy field for now
mod tests {
    use crate::cache::{CachedPr, CachedTmuxSession, CachedWorktree};
    use crate::ci_state::CiChecks;
    use crate::derive::{DisplayGroup, derive_worktree_rows};

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
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    /// Builds a non-main worktree row with the given CachedPr at branch "feat/test-218".
    fn row_with_pr(pr: CachedPr) -> crate::derive::WorktreeRow {
        let wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/test-218"),
        ];
        let prs = vec![pr];
        let mut rows = derive_worktree_rows(&[], &prs, &wts, &[], "owner/repo", &[], &[]);
        // Second row is the feature worktree.
        rows.remove(1)
    }

    #[test]
    fn display_group_needs_attention_changes_requested() {
        let prs = vec![CachedPr {
            review_decision: Some("changes_requested".to_string()),
            ..pr_for_branch(55, "feat/branch")
        }];
        let worktrees = [worktree("/workspace/repo-feat", "feat/branch")];

        // Use second worktree to avoid shepherd
        let all_wts = vec![worktree("/workspace/repo", "main"), worktrees[0].clone()];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[], &[]);

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
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[], &[]);

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
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[], &[]);

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
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[], &[]);

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

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[], &[]);

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

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &[], &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
    }

    #[test]
    fn display_group_ready_to_merge() {
        let prs = vec![approved_passing_pr_for_branch(55, "feat/branch")];
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &prs, &all_wts, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::ReadyToMerge);
    }

    #[test]
    fn display_group_other_no_pr() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-feat", "feat/branch"),
        ];
        let rows = derive_worktree_rows(&[], &[], &all_wts, &[], "owner/repo", &[], &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::Other);
    }

    #[test]
    fn display_group_ordering() {
        assert!(DisplayGroup::RepoMain < DisplayGroup::NeedsAttention);
        assert!(DisplayGroup::NeedsAttention < DisplayGroup::ClaudeWorking);
        assert!(DisplayGroup::ClaudeWorking < DisplayGroup::ReadyToMerge);
        assert!(DisplayGroup::ReadyToMerge < DisplayGroup::Other);
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

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &[], &[]);

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

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[], &[]);

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

        let rows = derive_worktree_rows(&[], &prs, &all_wts, &sessions, "owner/repo", &[], &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn hook_state_display_group_input_becomes_needs_attention() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![crate::claude_state::ClaudeStateFile {
            state: "input".to_string(),
            session_id: "sess-abc".to_string(),
            tmux_session: "repo_47_claude".to_string(),
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
        }];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states, &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::NeedsAttention);
    }

    #[test]
    fn hook_state_display_group_working_becomes_claude_working() {
        let all_wts = vec![
            worktree("/workspace/repo", "main"),
            worktree("/workspace/repo-47", "feat/branch"),
        ];
        let sessions = vec![session("repo_47_claude", "/workspace/repo-47", vec![])];
        let states = vec![crate::claude_state::ClaudeStateFile {
            state: "working".to_string(),
            session_id: "sess-abc".to_string(),
            tmux_session: "repo_47_claude".to_string(),
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
        }];

        let rows = derive_worktree_rows(&[], &[], &all_wts, &sessions, "owner/repo", &states, &[]);

        assert_eq!(rows[1].display_group, DisplayGroup::ClaudeWorking);
    }

    /// Task #16: is_needs_attention does NOT fire when code is green and only the gate is blocked.
    /// A code-green gate-blocked PR must NOT cascade into NeedsAttention.
    #[test]
    fn display_group_not_needs_attention_when_code_green_gate_blocked() {
        let pr = CachedPr {
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()), // legacy mirrors ci_code_state
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: Some("blocked".to_string()),
            ..pr_for_branch(218, "feat/test-218")
        };
        let row = row_with_pr(pr);
        assert_ne!(
            row.display_group,
            DisplayGroup::NeedsAttention,
            "code-green gate-blocked PR must not be NeedsAttention"
        );
        // Also not ReadyToMerge because the gate is blocked.
        assert_ne!(
            row.display_group,
            DisplayGroup::ReadyToMerge,
            "gate-blocked PR must not be ReadyToMerge"
        );
    }

    /// Task #17: is_ready_to_merge fires when code is green and gate is cleared.
    #[test]
    fn display_group_ready_to_merge_when_code_green_gate_cleared() {
        let pr = CachedPr {
            review_decision: Some("approved".to_string()),
            checks_state: Some("passing".to_string()),
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: Some("cleared".to_string()),
            ..pr_for_branch(218, "feat/test-218")
        };
        let row = row_with_pr(pr);
        assert_eq!(row.display_group, DisplayGroup::ReadyToMerge);
    }

    /// Task #18: is_needs_attention still fires when code is failing.
    #[test]
    fn display_group_needs_attention_when_code_failing() {
        let pr = CachedPr {
            checks_state: Some("failing".to_string()),
            ci_code_state: Some("failing".to_string()),
            ci_gate_state: Some("cleared".to_string()),
            ..pr_for_branch(218, "feat/test-218")
        };
        let row = row_with_pr(pr);
        assert_eq!(row.display_group, DisplayGroup::NeedsAttention);
    }

    /// Task #19: a docs-only PR with null/null CI is NOT NeedsAttention.
    #[test]
    fn display_group_not_needs_attention_for_docs_only_pr_with_no_ci() {
        let pr = CachedPr {
            review_decision: Some("approved".to_string()),
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ..pr_for_branch(218, "feat/test-218")
        };
        let row = row_with_pr(pr);
        assert_ne!(
            row.display_group,
            DisplayGroup::NeedsAttention,
            "docs-only PR with no CI checks must not be NeedsAttention"
        );
    }
}
