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

/// Workflow phase applied by the `/gh-tag` skill, in priority order.
///
/// Variants are declared highest-priority first: `Blocked` wins over everything
/// so a blocked PR never silently vanishes from filters when multiple phase
/// labels coexist. Iteration via [`WorkflowPhase::VARIANTS`] preserves this order.
///
/// Serde uses kebab-case, so serialized names match the GitHub label strings
/// (`"blocked"`, `"in-ai-review"`, etc.) exactly.
///
/// Source of truth for the label set: `~/.claude/skills/gh-tag/tag.sh`.
/// Keep in sync manually when new phase labels are added to that skill.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum WorkflowPhase {
    /// Work halted, waiting on external resolution (highest priority).
    Blocked,
    /// Implementation complete, awaiting automated/AI review.
    InAiReview,
    /// Review passed, PR ready for human merge.
    PrReady,
    /// Actively being worked on.
    InProgress,
    /// Bug report awaiting reproduction steps.
    NeedsRepro,
    /// Work scoped but awaiting a concrete implementation plan.
    NeedsPlan,
    /// Initial triage — gathering context before planning.
    Investigating,
    /// Plan exists, scheduled but not yet started (lowest priority).
    Planned,
}

impl WorkflowPhase {
    /// All phase variants in priority order (highest first).
    pub const VARIANTS: &'static [WorkflowPhase] = &[
        WorkflowPhase::Blocked,
        WorkflowPhase::InAiReview,
        WorkflowPhase::PrReady,
        WorkflowPhase::InProgress,
        WorkflowPhase::NeedsRepro,
        WorkflowPhase::NeedsPlan,
        WorkflowPhase::Investigating,
        WorkflowPhase::Planned,
    ];

    /// Returns the kebab-case label string for this phase — matches the
    /// GitHub label name applied by `/gh-tag` and the serialized JSON value.
    pub const fn as_label(self) -> &'static str {
        match self {
            WorkflowPhase::Blocked => "blocked",
            WorkflowPhase::InAiReview => "in-ai-review",
            WorkflowPhase::PrReady => "pr-ready",
            WorkflowPhase::InProgress => "in-progress",
            WorkflowPhase::NeedsRepro => "needs-repro",
            WorkflowPhase::NeedsPlan => "needs-plan",
            WorkflowPhase::Investigating => "investigating",
            WorkflowPhase::Planned => "planned",
        }
    }

    /// Parses a label string into a `WorkflowPhase`. Case-sensitive; returns `None` for
    /// unknown labels.
    pub fn from_label(label: &str) -> Option<WorkflowPhase> {
        WorkflowPhase::VARIANTS
            .iter()
            .copied()
            .find(|p| p.as_label() == label)
    }
}

/// Derives the workflow phase from a slice of label strings.
///
/// Iterates [`WorkflowPhase::VARIANTS`] in priority order and returns the first variant
/// whose label appears anywhere in the input slice. Returns `None` if no phase
/// label is present. Matching is case-sensitive and exact.
///
/// # Examples
///
/// ```
/// use orchard::derive::{phase_from_labels, WorkflowPhase};
///
/// assert_eq!(phase_from_labels(&[]), None);
/// assert_eq!(phase_from_labels(&["bug".to_string()]), None);
/// assert_eq!(phase_from_labels(&["in-progress".to_string()]), Some(WorkflowPhase::InProgress));
/// assert_eq!(
///     phase_from_labels(&["in-progress".to_string(), "blocked".to_string()]),
///     Some(WorkflowPhase::Blocked),
/// );
/// ```
pub fn phase_from_labels(labels: &[String]) -> Option<WorkflowPhase> {
    WorkflowPhase::VARIANTS
        .iter()
        .copied()
        .find(|p| labels.iter().any(|l| l == p.as_label()))
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
    /// Per-thread latest-comment timestamps (epoch seconds) for threads that
    /// are unresolved AND not outdated. Populated from `CachedPr` at join time.
    /// Used by the signal layer to compute `since_epoch` for
    /// `PipelineStatus::UnresolvedThreads`.
    pub unresolved_thread_comment_timestamps: Vec<i64>,
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
    /// ISO 8601 timestamp when the linked issue was last updated, if any.
    /// Used by the SINCE column for `Blocked` / `Paused` statuses.
    pub issue_updated_at: Option<String>,
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
    /// Physical layout of this worktree (`Bare` bare-repo+worktrees, or
    /// `Flat` single-clone-per-VM). Propagated from `CachedWorktree.layout`.
    pub layout: crate::cache::WorktreeLayout,
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
            unresolved_thread_comment_timestamps: vec![],
        }
    }
}

// ---------------------------------------------------------------------------
// Sort key
// ---------------------------------------------------------------------------

/// Sort key extracted from a worktree for consistent ordering.
/// Implements Ord so both WorktreeRow and WorktreeState can use the same comparator.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WorktreeSortKey<'a> {
    /// Primary: display group (ascending).
    pub display_group: DisplayGroup,
    /// Secondary: rows where any session has ClaudeState::Input sort first.
    pub has_claude_input: bool,
    /// Tertiary: best available timestamp (ISO 8601, descending — most recent first).
    /// Compared lexicographically, which is correct for ISO 8601 with Z suffix.
    /// All timestamps from GitHub and git use UTC with Z suffix.
    pub best_timestamp: Option<&'a str>,
    /// Quaternary: rows with a PR sort before rows without.
    pub has_pr: bool,
    /// Quinary: issue number ascending; Some before None.
    pub issue_number: Option<u32>,
    /// Senary: branch name alphabetical.
    pub branch: &'a str,
}

impl<'a> PartialOrd for WorktreeSortKey<'a> {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl<'a> Ord for WorktreeSortKey<'a> {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        // Primary: display_group ascending
        self.display_group
            .cmp(&other.display_group)
            // Secondary: claude input first (true before false)
            .then_with(|| other.has_claude_input.cmp(&self.has_claude_input))
            // Tertiary: best_timestamp descending (most recent first); None sorts last
            .then_with(|| match (self.best_timestamp, other.best_timestamp) {
                (Some(a), Some(b)) => b.cmp(a), // reverse: larger timestamp = more recent
                (Some(_), None) => std::cmp::Ordering::Less,
                (None, Some(_)) => std::cmp::Ordering::Greater,
                (None, None) => std::cmp::Ordering::Equal,
            })
            // Quaternary: has_pr true before false
            .then_with(|| other.has_pr.cmp(&self.has_pr))
            // Quinary: issue number ascending; Some before None
            .then_with(|| match (self.issue_number, other.issue_number) {
                (Some(a), Some(b)) => a.cmp(&b),
                (Some(_), None) => std::cmp::Ordering::Less,
                (None, Some(_)) => std::cmp::Ordering::Greater,
                (None, None) => std::cmp::Ordering::Equal,
            })
            // Senary: branch alphabetical
            .then_with(|| self.branch.cmp(other.branch))
    }
}

impl WorktreeRow {
    /// Builds a sort key from this row for use in multi-criteria ordering.
    pub fn sort_key(&self) -> WorktreeSortKey<'_> {
        let has_claude_input = self.sessions.iter().any(|s| {
            s.claude
                .as_ref()
                .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Input)
        });
        let best_timestamp = self
            .pr
            .as_ref()
            .and_then(|pr| pr.last_commit_pushed_at.as_deref())
            .or(self.worktree_last_commit_at.as_deref());
        WorktreeSortKey {
            display_group: self.display_group,
            has_claude_input,
            best_timestamp,
            has_pr: self.pr.is_some(),
            issue_number: self.issue_number,
            branch: &self.branch,
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
            Some(WorkflowPhase::InProgress)
        );
    }

    #[test]
    fn phase_from_labels_mixed_with_unrelated_returns_phase() {
        assert_eq!(
            phase_from_labels(&ls(&["bug", "planned", "priority-high"])),
            Some(WorkflowPhase::Planned)
        );
    }

    #[test]
    fn phase_from_labels_priority_resolves_two_labels() {
        // in-progress (rank 4) beats planned (rank 8)
        assert_eq!(
            phase_from_labels(&ls(&["planned", "in-progress"])),
            Some(WorkflowPhase::InProgress)
        );
    }

    #[test]
    fn phase_from_labels_blocked_wins_over_in_progress() {
        assert_eq!(
            phase_from_labels(&ls(&["in-progress", "blocked"])),
            Some(WorkflowPhase::Blocked)
        );
    }

    #[test]
    fn phase_from_labels_blocked_wins_over_in_ai_review() {
        assert_eq!(
            phase_from_labels(&ls(&["in-ai-review", "blocked"])),
            Some(WorkflowPhase::Blocked)
        );
    }

    #[test]
    fn phase_from_labels_priority_resolves_three_labels() {
        assert_eq!(
            phase_from_labels(&ls(&["investigating", "needs-plan", "blocked"])),
            Some(WorkflowPhase::Blocked)
        );
    }

    #[test]
    fn phase_from_labels_in_ai_review_wins_over_pr_ready() {
        assert_eq!(
            phase_from_labels(&ls(&["pr-ready", "in-ai-review"])),
            Some(WorkflowPhase::InAiReview)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_investigating() {
        assert_eq!(
            phase_from_labels(&ls(&["investigating"])),
            Some(WorkflowPhase::Investigating)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_needs_plan() {
        assert_eq!(
            phase_from_labels(&ls(&["needs-plan"])),
            Some(WorkflowPhase::NeedsPlan)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_needs_repro() {
        assert_eq!(
            phase_from_labels(&ls(&["needs-repro"])),
            Some(WorkflowPhase::NeedsRepro)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_planned() {
        assert_eq!(
            phase_from_labels(&ls(&["planned"])),
            Some(WorkflowPhase::Planned)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_in_progress() {
        assert_eq!(
            phase_from_labels(&ls(&["in-progress"])),
            Some(WorkflowPhase::InProgress)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_in_ai_review() {
        assert_eq!(
            phase_from_labels(&ls(&["in-ai-review"])),
            Some(WorkflowPhase::InAiReview)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_pr_ready() {
        assert_eq!(
            phase_from_labels(&ls(&["pr-ready"])),
            Some(WorkflowPhase::PrReady)
        );
    }

    #[test]
    fn phase_from_labels_recognizes_blocked() {
        assert_eq!(
            phase_from_labels(&ls(&["blocked"])),
            Some(WorkflowPhase::Blocked)
        );
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
    // Phase serialization / label round-trip
    // -----------------------------------------------------------------------

    #[test]
    fn phase_serialize_matches_kebab_case_label() {
        for &phase in WorkflowPhase::VARIANTS {
            let json = serde_json::to_string(&phase).unwrap();
            assert_eq!(json, format!("\"{}\"", phase.as_label()));
        }
    }

    #[test]
    fn phase_from_label_round_trips_every_variant() {
        for &phase in WorkflowPhase::VARIANTS {
            assert_eq!(WorkflowPhase::from_label(phase.as_label()), Some(phase));
        }
    }

    #[test]
    fn phase_from_label_rejects_unknown() {
        assert_eq!(WorkflowPhase::from_label("wontfix"), None);
        assert_eq!(WorkflowPhase::from_label("In-Progress"), None);
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

    // -----------------------------------------------------------------------
    // WorktreeSortKey tests
    // -----------------------------------------------------------------------

    fn base_key<'a>(branch: &'a str) -> WorktreeSortKey<'a> {
        WorktreeSortKey {
            display_group: DisplayGroup::Other,
            has_claude_input: false,
            best_timestamp: None,
            has_pr: false,
            issue_number: None,
            branch,
        }
    }

    #[test]
    fn sort_key_display_group_is_primary_sort() {
        let main = WorktreeSortKey {
            display_group: DisplayGroup::RepoMain,
            ..base_key("main")
        };
        let other = WorktreeSortKey {
            display_group: DisplayGroup::Other,
            ..base_key("feature")
        };
        assert!(main < other, "RepoMain should sort before Other");
    }

    #[test]
    fn sort_key_claude_input_sorts_first_within_group() {
        let with_input = WorktreeSortKey {
            has_claude_input: true,
            ..base_key("feature-a")
        };
        let without_input = WorktreeSortKey {
            has_claude_input: false,
            ..base_key("feature-b")
        };
        assert!(
            with_input < without_input,
            "claude input should sort before no input"
        );
    }

    #[test]
    fn sort_key_recent_timestamp_sorts_before_stale() {
        let recent = WorktreeSortKey {
            best_timestamp: Some("2024-06-01T10:00:00Z"),
            ..base_key("branch-a")
        };
        let stale = WorktreeSortKey {
            best_timestamp: Some("2024-01-01T10:00:00Z"),
            ..base_key("branch-b")
        };
        assert!(recent < stale, "more recent timestamp should sort first");
    }

    #[test]
    fn sort_key_none_timestamp_sorts_last() {
        let has_ts = WorktreeSortKey {
            best_timestamp: Some("2024-01-01T10:00:00Z"),
            ..base_key("branch-a")
        };
        let no_ts = WorktreeSortKey {
            best_timestamp: None,
            ..base_key("branch-b")
        };
        assert!(
            has_ts < no_ts,
            "row with timestamp should sort before row without"
        );
    }

    #[test]
    fn sort_key_same_timestamp_falls_back_to_issue_number() {
        let low_issue = WorktreeSortKey {
            best_timestamp: Some("2024-06-01T10:00:00Z"),
            issue_number: Some(10),
            ..base_key("branch-a")
        };
        let high_issue = WorktreeSortKey {
            best_timestamp: Some("2024-06-01T10:00:00Z"),
            issue_number: Some(20),
            ..base_key("branch-b")
        };
        assert!(
            low_issue < high_issue,
            "lower issue number should sort first"
        );
    }

    #[test]
    fn sort_key_has_pr_sorts_before_no_pr() {
        let with_pr = WorktreeSortKey {
            has_pr: true,
            ..base_key("branch-a")
        };
        let without_pr = WorktreeSortKey {
            has_pr: false,
            ..base_key("branch-b")
        };
        assert!(
            with_pr < without_pr,
            "row with PR should sort before row without"
        );
    }

    #[test]
    fn sort_key_issue_number_some_before_none() {
        let with_issue = WorktreeSortKey {
            issue_number: Some(5),
            ..base_key("branch-a")
        };
        let without_issue = WorktreeSortKey {
            issue_number: None,
            ..base_key("branch-b")
        };
        assert!(
            with_issue < without_issue,
            "row with issue number should sort before row without"
        );
    }

    #[test]
    fn sort_key_branch_alphabetical_as_final_tiebreaker() {
        let aardvark = base_key("aardvark");
        let zebra = base_key("zebra");
        assert!(
            aardvark < zebra,
            "alphabetically earlier branch should sort first"
        );
    }

    #[test]
    fn sort_key_ord_equality() {
        let a = WorktreeSortKey {
            display_group: DisplayGroup::ClaudeWorking,
            has_claude_input: true,
            best_timestamp: Some("2024-06-01T10:00:00Z"),
            has_pr: true,
            issue_number: Some(42),
            branch: "feature/42",
        };
        let b = a.clone();
        assert_eq!(a.cmp(&b), std::cmp::Ordering::Equal);
    }
}
