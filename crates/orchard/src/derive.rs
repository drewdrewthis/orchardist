//! Pure functional core: derives display-ready rows from cached data.
//!
//! `derive_all_repos` joins cached issues, PRs, worktrees, and tmux sessions
//! into `WorktreeRow` values with computed `DisplayGroup` sort keys. No I/O occurs
//! here — all input comes from the cache layer, making this fully testable.
//!
//! Join logic lives in [`crate::join`]; display group classification in [`crate::classify`].
use crate::ci_state::CiChecks;
use crate::session::EnrichedSession;

// Re-exports for backward compatibility — callers continue to use `crate::derive::*`.
pub use crate::join::{derive_all_repos, derive_worktree_rows};

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Workflow phase labels applied by the `/gh-tag` skill, in priority order.
///
/// Highest-priority first: `blocked` wins over everything so a blocked PR never
/// silently vanishes from filters when multiple phase labels coexist.
///
/// Source of truth for the label set: `~/.claude/skills/gh-tag/tag.sh`.
/// Keep in sync manually when new phase labels are added to that skill.
pub const PHASE_PRIORITY: &[&str] = &[
    "blocked",
    "in-ai-review",
    "pr-ready",
    "in-progress",
    "needs-repro",
    "needs-plan",
    "investigating",
    "planned",
];

/// Derives the workflow phase from a slice of label strings.
///
/// Iterates `PHASE_PRIORITY` in order and returns the first label whose name
/// appears anywhere in the input slice. Returns `None` if no phase label is
/// present. Matching is case-sensitive and exact.
///
/// # Examples
///
/// ```
/// use orchard::derive::phase_from_labels;
///
/// assert_eq!(phase_from_labels(&[]), None);
/// assert_eq!(phase_from_labels(&["bug".to_string()]), None);
/// assert_eq!(phase_from_labels(&["in-progress".to_string()]), Some("in-progress"));
/// assert_eq!(
///     phase_from_labels(&["in-progress".to_string(), "blocked".to_string()]),
///     Some("blocked"),
/// );
/// ```
pub fn phase_from_labels(labels: &[String]) -> Option<&'static str> {
    PHASE_PRIORITY
        .iter()
        .find(|&&priority_label| labels.iter().any(|l| l == priority_label))
        .copied()
}

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/// Rendering order for worktree rows. Variants are ordered so that `Ord` gives
/// the correct sort order (RepoMain first, Other last).
#[derive(
    Debug,
    Clone,
    Copy,
    Default,
    PartialEq,
    Eq,
    PartialOrd,
    Ord,
    serde::Serialize,
    serde::Deserialize,
)]
#[serde(rename_all = "snake_case")]
pub enum DisplayGroup {
    /// Always first — the repo's main worktree.
    RepoMain,
    /// User-flagged as priority work.
    Prioritized,
    /// Requires human action (blocked, conflicts, review requested).
    NeedsAttention,
    /// A Claude session is actively working in this worktree.
    ClaudeWorking,
    /// PR is approved and checks pass — ready to merge.
    ReadyToMerge,
    /// Worktrees without PRs or other misc work.
    #[default]
    Other,
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
    /// PR title.
    pub title: Option<String>,
    /// Whether the PR is a draft.
    pub is_draft: Option<bool>,
    /// GitHub login of the PR author.
    pub author: Option<String>,
    /// GitHub logins of requested reviewers.
    pub requested_reviewers: Vec<String>,
    /// Reviews submitted on this PR.
    pub reviews: Vec<crate::cache::CachedReview>,
    /// Review decision: "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", etc.
    pub review_decision: Option<String>,
    /// Aggregate CI checks state (legacy union field).
    ///
    /// Deprecated in favour of [`PrInfo::ci_code_state`]. Retained for one release
    /// so downstream consumers are not broken. Will be removed in a future version.
    #[deprecated(note = "Use ci_code_state; this field is retained for one release")]
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks only: "passing", "failing", "pending", or None.
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: "cleared", "blocked", "pending", or None.
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    pub ci_checks: CiChecks,
    /// True when the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// Labels applied to the PR.
    pub labels: Vec<String>,
    /// Number of lines added.
    pub additions: Option<u32>,
    /// Number of lines deleted.
    pub deletions: Option<u32>,
    /// ISO 8601 timestamp when the PR was created.
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the PR was last updated.
    pub updated_at: Option<String>,
    /// ISO 8601 timestamp of when the last commit was pushed to the PR branch.
    pub last_commit_pushed_at: Option<String>,
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
    /// Labels on the linked issue, if any. Empty when no issue is linked or
    /// the issue has no labels.
    pub issue_labels: Vec<String>,
    /// Assignees of the linked issue, if any.
    pub issue_assignees: Vec<String>,
    /// ISO 8601 timestamp when the linked issue was created, if any.
    pub issue_created_at: Option<String>,
    /// Issue numbers blocking the linked issue, if any.
    pub issue_blocked_by: Vec<u32>,
    /// Sub-issues of the linked issue, if any.
    pub issue_sub_issues: Vec<crate::cache::CachedSubIssue>,
    /// Parent issue number of the linked issue, if any.
    pub issue_parent: Option<u32>,
    /// Commits ahead of upstream for this worktree, if available.
    pub worktree_ahead: Option<u32>,
    /// Commits behind upstream for this worktree, if available.
    pub worktree_behind: Option<u32>,
    /// ISO 8601 timestamp of the last commit in this worktree, if available.
    pub worktree_last_commit_at: Option<String>,
    /// Linked pull request, if one exists for this branch.
    pub pr: Option<PrInfo>,
    /// Active tmux sessions associated with this worktree path.
    pub sessions: Vec<EnrichedSession>,
    /// Display group controlling sort order and TUI section.
    pub display_group: DisplayGroup,
    /// True when this is the repo's main worktree.
    pub is_main_worktree: bool,
}

// ---------------------------------------------------------------------------
// Default impls (for test ergonomics — new-field defaults)
// ---------------------------------------------------------------------------

#[allow(deprecated)]
impl Default for PrInfo {
    /// Returns a `PrInfo` with every new enrichment field set to its empty/absent default.
    ///
    /// Intended for test fixtures that set only the fields under test; use struct update
    /// syntax (`PrInfo { field: value, ..PrInfo::default() }`) to override specific fields.
    fn default() -> Self {
        Self {
            number: 0,
            branch: String::new(),
            state: None,
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            review_decision: None,
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: crate::ci_state::CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            labels: vec![],
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
        }
    }
}

// ---------------------------------------------------------------------------
// Staleness helpers
// ---------------------------------------------------------------------------

/// Returns true if a Claude state file timestamp is older than the default threshold.
pub fn is_state_stale_default(timestamp: &str) -> bool {
    // Default threshold matches HOOK_STATE_STALENESS_SECS in join.rs.
    is_state_stale(timestamp, 300)
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

/// Returns `true` if the branch is a well-known default branch name.
///
/// Used to skip default branches when building per-branch PR queries — we only
/// want to look up PRs for feature/issue branches, not the base branch itself.
pub fn is_default_branch(branch: &str) -> bool {
    matches!(branch, "main" | "master" | "develop" | "dev")
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    // -----------------------------------------------------------------------
    // phase_from_labels tests
    // -----------------------------------------------------------------------

    fn ls(labels: &[&str]) -> Vec<String> {
        labels.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn phase_from_labels_empty_returns_none() {
        assert_eq!(phase_from_labels(&[]), None);
    }

    #[test]
    fn phase_from_labels_no_phase_labels_returns_none() {
        assert_eq!(
            phase_from_labels(&ls(&["bug", "enhancement", "good-first-issue"])),
            None
        );
    }

    #[test]
    fn phase_from_labels_single_phase_label_returns_it() {
        assert_eq!(
            phase_from_labels(&ls(&["in-progress"])),
            Some("in-progress")
        );
    }

    #[test]
    fn phase_from_labels_mixed_with_unrelated_returns_phase() {
        assert_eq!(
            phase_from_labels(&ls(&["bug", "planned", "priority-high"])),
            Some("planned")
        );
    }

    #[test]
    fn phase_from_labels_priority_resolves_two_labels() {
        // in-progress (rank 4) beats planned (rank 8)
        assert_eq!(
            phase_from_labels(&ls(&["planned", "in-progress"])),
            Some("in-progress")
        );
    }

    #[test]
    fn phase_from_labels_blocked_wins_over_in_progress() {
        assert_eq!(
            phase_from_labels(&ls(&["in-progress", "blocked"])),
            Some("blocked")
        );
    }

    #[test]
    fn phase_from_labels_blocked_wins_over_in_ai_review() {
        assert_eq!(
            phase_from_labels(&ls(&["in-ai-review", "blocked"])),
            Some("blocked")
        );
    }

    #[test]
    fn phase_from_labels_priority_resolves_three_labels() {
        assert_eq!(
            phase_from_labels(&ls(&["investigating", "needs-plan", "blocked"])),
            Some("blocked")
        );
    }

    #[test]
    fn phase_from_labels_in_ai_review_wins_over_pr_ready() {
        assert_eq!(
            phase_from_labels(&ls(&["pr-ready", "in-ai-review"])),
            Some("in-ai-review")
        );
    }

    #[test]
    fn phase_from_labels_recognizes_investigating() {
        assert_eq!(
            phase_from_labels(&ls(&["investigating"])),
            Some("investigating")
        );
    }

    #[test]
    fn phase_from_labels_recognizes_needs_plan() {
        assert_eq!(phase_from_labels(&ls(&["needs-plan"])), Some("needs-plan"));
    }

    #[test]
    fn phase_from_labels_recognizes_needs_repro() {
        assert_eq!(
            phase_from_labels(&ls(&["needs-repro"])),
            Some("needs-repro")
        );
    }

    #[test]
    fn phase_from_labels_recognizes_planned() {
        assert_eq!(phase_from_labels(&ls(&["planned"])), Some("planned"));
    }

    #[test]
    fn phase_from_labels_recognizes_in_progress() {
        assert_eq!(
            phase_from_labels(&ls(&["in-progress"])),
            Some("in-progress")
        );
    }

    #[test]
    fn phase_from_labels_recognizes_in_ai_review() {
        assert_eq!(
            phase_from_labels(&ls(&["in-ai-review"])),
            Some("in-ai-review")
        );
    }

    #[test]
    fn phase_from_labels_recognizes_pr_ready() {
        assert_eq!(phase_from_labels(&ls(&["pr-ready"])), Some("pr-ready"));
    }

    #[test]
    fn phase_from_labels_recognizes_blocked() {
        assert_eq!(phase_from_labels(&ls(&["blocked"])), Some("blocked"));
    }

    #[test]
    fn phase_from_labels_ignores_unknown_labels() {
        assert_eq!(
            phase_from_labels(&ls(&["wontfix", "duplicate", "question"])),
            None
        );
    }

    #[test]
    fn phase_from_labels_case_sensitive_no_match_for_uppercase() {
        assert_eq!(phase_from_labels(&ls(&["In-Progress"])), None);
    }

    // -----------------------------------------------------------------------
    // is_state_stale tests
    // -----------------------------------------------------------------------

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
}
