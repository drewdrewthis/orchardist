//! Canonical PR type, collapsing CachedPr / PrInfo / PrState / JsonPr into one.
use serde::{Deserialize, Serialize};

use crate::derive::WorkflowPhase;
use crate::models::check::CiChecks;

/// A single reviewer's response to a PR.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Review {
    /// GitHub login of the reviewer.
    pub author: String,
    /// Review state: `APPROVED`, `CHANGES_REQUESTED`, or `COMMENTED`.
    pub state: String,
    /// ISO 8601 timestamp when the review was submitted.
    pub submitted_at: Option<String>,
}

/// Canonical pull request, used from cache layer through to JSON output.
#[allow(deprecated)]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pr {
    /// GitHub PR number.
    pub number: u32,
    /// Branch the PR was opened from.
    pub branch: String,
    /// PR title.
    pub title: Option<String>,
    /// PR state: `OPEN`, `MERGED`, or `CLOSED`.
    pub state: Option<String>,
    /// Whether the PR is in draft mode.
    pub is_draft: Option<bool>,
    /// GitHub login of the PR author.
    pub author: Option<String>,
    /// Aggregated review decision from GitHub (e.g. `APPROVED`).
    pub review_decision: Option<String>,
    /// GitHub logins of requested reviewers.
    #[serde(default)]
    pub requested_reviewers: Vec<String>,
    /// Individual review responses.
    #[serde(default)]
    pub reviews: Vec<Review>,
    /// Total number of review comments.
    pub review_comments: Option<u32>,
    /// Legacy union CI state — mirrors `ci_code_state` only. Retained for cache compat.
    #[deprecated(note = "Use ci_code_state; retained for one release")]
    #[serde(default)]
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks: `passing`, `failing`, `pending`, or absent.
    #[serde(default)]
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: `cleared`, `blocked`, `pending`, or absent.
    #[serde(default)]
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    #[serde(default)]
    pub ci_checks: CiChecks,
    /// Whether the PR has merge conflicts with its base branch.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// Labels applied to this PR.
    #[serde(default)]
    pub labels: Vec<String>,
    /// Lines added in this PR.
    pub additions: Option<u32>,
    /// Lines removed in this PR.
    pub deletions: Option<u32>,
    /// ISO 8601 timestamp when the PR was created.
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the PR was last updated.
    pub updated_at: Option<String>,
    /// ISO 8601 timestamp of the last commit pushed to this PR.
    pub last_commit_pushed_at: Option<String>,
    /// Workflow phase derived from labels at join time. Not stored in cache.
    #[serde(skip_deserializing)]
    pub phase: Option<WorkflowPhase>,
}
