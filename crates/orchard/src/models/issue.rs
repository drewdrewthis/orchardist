//! Canonical issue type, collapsing CachedIssue / IssueInfo / JsonIssue into one.
use serde::{Deserialize, Serialize};

use crate::derive::WorkflowPhase;

/// A child issue in a parent-sub-issue relationship.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SubIssue {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state: `open` or `closed`.
    pub state: String,
}

/// Canonical GitHub issue, used from cache layer through to JSON output.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Issue {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state: `open` or `closed`.
    pub state: String,
    /// Labels applied to this issue.
    #[serde(default)]
    pub labels: Vec<String>,
    /// GitHub logins of users assigned to this issue.
    #[serde(default)]
    pub assignees: Vec<String>,
    /// ISO 8601 timestamp when the issue was created.
    pub created_at: Option<String>,
    /// Issue numbers that block this issue.
    #[serde(default)]
    pub blocked_by: Vec<u32>,
    /// Child issues under this issue.
    #[serde(default)]
    pub sub_issues: Vec<SubIssue>,
    /// Parent issue number, if this issue is a sub-issue.
    pub parent: Option<u32>,
    /// Workflow phase derived from labels at join time. Not stored in cache.
    #[serde(skip_deserializing)]
    pub phase: Option<WorkflowPhase>,
}
