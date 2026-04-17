//! Shared domain types used throughout Orchard.
//!
//! Defines the core data structures (`Worktree`, `PrInfo`, `TmuxSession`, etc.)
//! and the `resolve_pr_status` helper that maps raw PR fields to a displayable
//! `PrStatus`. Both the TUI and JSON output paths consume these types.
use serde::{Deserialize, Serialize};

/// A single git worktree, enriched with PR, tmux, and issue data.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct Worktree {
    /// Absolute filesystem path to the worktree root.
    pub path: String,
    /// The branch checked out in this worktree, if any.
    pub branch: Option<String>,
    /// The short commit SHA at HEAD.
    pub head: String,
    /// Whether this is the bare worktree (the `.git` root).
    pub is_bare: bool,
    /// Whether the worktree has unresolved merge conflicts.
    pub has_conflicts: bool,
    /// The associated pull request, if one exists for this branch.
    pub pr: Option<PrInfo>,
    /// True while PR data is being fetched asynchronously.
    pub pr_loading: bool,
    /// Name of the tmux session attached to this worktree, if any.
    pub tmux_session: Option<String>,
    /// Whether the tmux session is currently attached to a terminal.
    pub tmux_attached: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// Title of the active tmux pane in this worktree's session.
    pub tmux_pane_title: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// Remote host identifier if this worktree lives on a remote machine.
    pub remote: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// GitHub issue number linked to this worktree's branch, if any.
    pub issue_number: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// Current state of the linked GitHub issue.
    pub issue_state: Option<IssueState>,
}

/// Snapshot of a GitHub pull request's status and review state.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PrInfo {
    /// GitHub PR number.
    pub number: u32,
    /// PR lifecycle state: `"open"`, `"merged"`, or `"closed"`.
    pub state: String,
    /// PR title as shown on GitHub.
    pub title: String,
    /// URL to the PR on GitHub.
    pub url: String,
    /// The overall review decision (approved, changes requested, etc.).
    pub review_decision: ReviewDecision,
    /// Number of review threads that have not been resolved.
    pub unresolved_threads: u32,
    /// Aggregated status of all CI checks on the PR.
    pub checks_status: ChecksStatus,
    /// Whether the PR branch has merge conflicts with its base.
    pub has_conflicts: bool,
    /// Rollup state for code CI checks only: "passing", "failing", "pending", or None.
    ///
    /// When `Some`, `resolve_pr_status` prefers this over `checks_status`.
    /// None means either the PR has no code CI checks, or the data comes from
    /// an older cache file that predates split CI state (slice 2 of issue #218).
    #[serde(default)]
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: "cleared", "blocked", "pending", or None.
    ///
    /// A "blocked" gate check means a human action is required, not that code is broken.
    /// `resolve_pr_status` does NOT treat a blocked gate as `Failing`.
    #[serde(default)]
    pub ci_gate_state: Option<String>,
}

/// Derived single-value status for a PR, used to drive display and sorting.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub enum PrStatus {
    /// The branch has merge conflicts with its base.
    Conflict,
    /// One or more CI checks have failed.
    Failing,
    /// There are unresolved review threads.
    Unresolved,
    /// A reviewer has requested changes.
    ChangesRequested,
    /// A review has been requested but not yet submitted.
    ReviewNeeded,
    /// CI checks are still running.
    PendingCi,
    /// The PR is approved and CI is passing.
    Approved,
    /// The PR has been merged.
    Merged,
    /// The PR was closed without merging.
    Closed,
}

impl PrStatus {
    /// Returns the icon and label used to render this status in the TUI.
    pub fn display(self) -> StatusDisplay {
        match self {
            Self::Conflict => StatusDisplay {
                icon: "✖",
                label: "conflict",
            },
            Self::Failing => StatusDisplay {
                icon: "✖",
                label: "failing",
            },
            Self::Unresolved => StatusDisplay {
                icon: "◯",
                label: "unresolved",
            },
            Self::ChangesRequested => StatusDisplay {
                icon: "✖",
                label: "changes",
            },
            Self::ReviewNeeded => StatusDisplay {
                icon: "◯",
                label: "review",
            },
            Self::PendingCi => StatusDisplay {
                icon: "◯",
                label: "pending",
            },
            Self::Approved => StatusDisplay {
                icon: "✓",
                label: "ready",
            },
            Self::Merged => StatusDisplay {
                icon: "●",
                label: "merged",
            },
            Self::Closed => StatusDisplay {
                icon: "●",
                label: "closed",
            },
        }
    }
}

/// Icon and text label pair used to render a `PrStatus` in the TUI.
pub struct StatusDisplay {
    /// Single-character (or short) symbol representing the status visually.
    pub icon: &'static str,
    /// Short human-readable label for the status.
    pub label: &'static str,
}

/// Derives the canonical `PrStatus` from a `PrInfo` by applying a priority-ordered
/// set of rules (merged > closed > conflict > unresolved > changes requested > failing
/// > review needed > pending CI > approved).
///
/// When `ci_code_state` is `Some`, it is preferred over the legacy `checks_status`
/// for the failing/pending branches. A `ci_gate_state` of `"blocked"` or `"pending"`
/// is deliberately NOT mapped to `Failing` — a blocked gate check means a human
/// action is required, not that code is broken (issue #218).
///
/// When `ci_code_state` is `None` (old cache files or data predating split CI state),
/// the function falls back to the legacy `checks_status` field so existing consumers
/// continue to work correctly.
pub fn resolve_pr_status(pr: &PrInfo) -> PrStatus {
    if pr.state == "merged" {
        return PrStatus::Merged;
    }
    if pr.state == "closed" {
        return PrStatus::Closed;
    }
    if pr.has_conflicts {
        return PrStatus::Conflict;
    }
    if pr.unresolved_threads > 0 {
        return PrStatus::Unresolved;
    }
    if pr.review_decision == ReviewDecision::ChangesRequested {
        return PrStatus::ChangesRequested;
    }

    // Prefer ci_code_state when present; fall back to legacy checks_status.
    // A blocked/pending gate is NOT treated as Failing — it is a human-action
    // signal, not a broken-code signal.
    let is_code_failing = pr.ci_code_state.as_deref() == Some("failing")
        || (pr.ci_code_state.is_none() && pr.checks_status == ChecksStatus::Fail);
    let is_code_pending = pr.ci_code_state.as_deref() == Some("pending")
        || (pr.ci_code_state.is_none() && pr.checks_status == ChecksStatus::Pending);
    // A pending gate (but code passing) should surface as PendingCi rather than
    // Failing. We reuse PendingCi to avoid a cascade of enum-variant changes
    // across every match on PrStatus (icons, labels, TUI rendering).
    let is_gate_pending =
        pr.ci_gate_state.as_deref() == Some("pending") && !is_code_failing && !is_code_pending;

    if is_code_failing {
        return PrStatus::Failing;
    }
    if pr.review_decision == ReviewDecision::ReviewRequired {
        return PrStatus::ReviewNeeded;
    }
    if is_code_pending || is_gate_pending {
        return PrStatus::PendingCi;
    }
    if pr.review_decision == ReviewDecision::Approved || pr.review_decision == ReviewDecision::None
    {
        return PrStatus::Approved;
    }
    PrStatus::ReviewNeeded
}

/// The aggregated review decision returned by the GitHub API for a pull request.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
pub enum ReviewDecision {
    #[default]
    #[serde(rename = "")]
    /// No review has been requested or submitted yet.
    None,
    #[serde(rename = "APPROVED")]
    /// All required reviewers have approved the PR.
    Approved,
    #[serde(rename = "CHANGES_REQUESTED")]
    /// At least one reviewer has requested changes.
    ChangesRequested,
    #[serde(rename = "REVIEW_REQUIRED")]
    /// A review is required before the PR can be merged.
    ReviewRequired,
}

/// Aggregated result of all CI checks associated with a pull request.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum ChecksStatus {
    /// All checks completed successfully.
    Pass,
    /// One or more checks failed.
    Fail,
    /// Checks are queued or currently running.
    Pending,
    #[default]
    /// No checks are configured or data is unavailable.
    None,
}

/// The lifecycle state of a GitHub issue.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum IssueState {
    /// The issue is open and active.
    Open,
    /// The issue was closed without being completed (e.g., won't fix).
    Closed,
    /// The issue was closed as completed.
    Completed,
}

/// A live tmux session discovered on the local or remote host.
#[derive(Debug, Clone)]
pub struct TmuxSession {
    /// The tmux session name.
    pub name: String,
    /// The working directory of the session's first window (frozen at session creation).
    pub path: String,
    /// Whether a client is currently attached to this session.
    pub attached: bool,
    /// Title of the active pane, if available.
    pub pane_title: Option<String>,
    /// The live cwd of the active pane, if available.
    ///
    /// Users `cd` inside sessions, so this reflects where the user actually is
    /// right now — which may differ from the frozen `path`. When `Some`, heal
    /// classification should prefer this over `path`.
    pub active_pane_cwd: Option<String>,
}

/// Connection details for a remote machine that hosts worktrees.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct RemoteConfig {
    /// SSH target in `user@host` format.
    pub host: String,
    /// Absolute path to the repository root on the remote machine.
    pub repo_path: String,
    #[serde(default = "default_shell")]
    /// Shell command used to connect (defaults to `"ssh"`).
    pub shell: String,
}

fn default_shell() -> String {
    "ssh".to_string()
}

/// Project-level Orchard configuration, loaded from `.orchard.json`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct OrchardConfig {
    /// Optional remote machine configuration for SSH-backed worktrees.
    pub remote: Option<RemoteConfig>,
    /// Optional path to a setup script executed after creating a new worktree.
    /// Resolved relative to the repo root; executed with cwd set to the new worktree.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub setup_script: Option<String>,
}

/// Parameters for switching the terminal to an existing or new tmux session.
pub struct SwitchToSessionOptions {
    /// Name of the tmux session to switch to or create.
    pub session_name: String,
    /// Filesystem path of the worktree the session is rooted in.
    pub worktree_path: String,
    /// Branch associated with the session, used for display purposes.
    pub branch: Option<String>,
    /// PR associated with the worktree, passed through for context display.
    pub pr: Option<PrInfo>,
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Helper to build a baseline open PrInfo with no conflicts, no threads, and no CI data.
    fn open_pr() -> PrInfo {
        PrInfo {
            number: 1,
            state: "open".into(),
            title: String::new(),
            url: String::new(),
            review_decision: ReviewDecision::None,
            unresolved_threads: 0,
            checks_status: ChecksStatus::None,
            has_conflicts: false,
            ci_code_state: None,
            ci_gate_state: None,
        }
    }

    #[test]
    fn conflict_beats_failing_when_open() {
        let pr = PrInfo {
            checks_status: ChecksStatus::Fail,
            has_conflicts: true,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Conflict);
    }

    #[test]
    fn merged_beats_conflicts_and_failing_ci() {
        let pr = PrInfo {
            state: "merged".into(),
            checks_status: ChecksStatus::Fail,
            has_conflicts: true,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Merged);
    }

    #[test]
    fn failing_ci() {
        let pr = PrInfo {
            checks_status: ChecksStatus::Fail,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Failing);
    }

    #[test]
    fn approved() {
        let pr = PrInfo {
            review_decision: ReviewDecision::Approved,
            checks_status: ChecksStatus::Pass,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Approved);
    }

    #[test]
    fn merged() {
        let pr = PrInfo {
            state: "merged".into(),
            review_decision: ReviewDecision::Approved,
            checks_status: ChecksStatus::Pass,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Merged);
    }

    #[test]
    fn pending_ci() {
        let pr = PrInfo {
            review_decision: ReviewDecision::Approved,
            checks_status: ChecksStatus::Pending,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::PendingCi);
    }

    #[test]
    fn no_review_required_with_passing_ci_is_approved() {
        let pr = PrInfo {
            checks_status: ChecksStatus::Pass,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Approved);
    }

    #[test]
    fn no_review_required_with_pending_ci_is_pending() {
        let pr = PrInfo {
            checks_status: ChecksStatus::Pending,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::PendingCi);
    }

    #[test]
    fn review_required_is_review_needed() {
        let pr = PrInfo {
            review_decision: ReviewDecision::ReviewRequired,
            checks_status: ChecksStatus::Pass,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::ReviewNeeded);
    }

    // -----------------------------------------------------------------------
    // Split CI state tests (issue #218, slice 3)
    // -----------------------------------------------------------------------

    /// Task #16: code-green gate-blocked approved PR must NOT be Failing.
    #[test]
    fn resolve_pr_status_prefers_ci_code_state_over_legacy_gate_blocked() {
        // ci_code_state=passing, ci_gate_state=blocked, legacy checks_status=Fail.
        // resolve_pr_status must NOT return Failing — the gate being blocked is
        // not a code problem, it is waiting on a human action.
        let pr = PrInfo {
            review_decision: ReviewDecision::Approved,
            checks_status: ChecksStatus::Fail, // legacy would say failing
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: Some("blocked".to_string()),
            ..open_pr()
        };
        let status = resolve_pr_status(&pr);
        assert_ne!(
            status,
            PrStatus::Failing,
            "code-green gate-blocked PR must not resolve to Failing"
        );
        // Approved + code passing + gate blocked = Approved (waiting on gate, but
        // code is fine and review is approved).
        assert_eq!(status, PrStatus::Approved);
    }

    /// Task #16 (part 2): resolve_pr_status does NOT return Failing for gate-blocked
    /// PR even when legacy checks_status would imply failure.
    #[test]
    fn resolve_pr_status_gate_pending_resolves_to_pending_ci_not_failing() {
        // ci_code_state=passing, ci_gate_state=pending, legacy=None.
        // Should surface as PendingCi (reused) not Failing.
        let pr = PrInfo {
            review_decision: ReviewDecision::Approved,
            checks_status: ChecksStatus::None,
            ci_code_state: Some("passing".to_string()),
            ci_gate_state: Some("pending".to_string()),
            ..open_pr()
        };
        let status = resolve_pr_status(&pr);
        assert_ne!(status, PrStatus::Failing);
        assert_eq!(status, PrStatus::PendingCi);
    }

    /// Legacy fallback: when ci_code_state is None, checks_status still drives the result.
    #[test]
    fn resolve_pr_status_falls_back_to_legacy_when_ci_code_state_none() {
        let pr = PrInfo {
            checks_status: ChecksStatus::Fail,
            ci_code_state: None,
            ci_gate_state: None,
            ..open_pr()
        };
        assert_eq!(resolve_pr_status(&pr), PrStatus::Failing);
    }

    #[test]
    fn display_entries_exist() {
        let statuses = [
            PrStatus::Conflict,
            PrStatus::Failing,
            PrStatus::Unresolved,
            PrStatus::ChangesRequested,
            PrStatus::ReviewNeeded,
            PrStatus::PendingCi,
            PrStatus::Approved,
            PrStatus::Merged,
            PrStatus::Closed,
        ];
        for s in statuses {
            let d = s.display();
            assert!(!d.icon.is_empty());
            assert!(!d.label.is_empty());
        }
    }
}
