// Single source of truth for all `Json*` wire-format types.
//
// This file is `include!`-ed in TWO places:
//   1. `json_output.rs`  — provides the types the binary serializes to JSON.
//   2. `build.rs`        — provides the types the schema generator derives from.
//
// Because both callers use the same physical file, the schema always describes
// the exact shape that `orchard --json` emits.  Drift is structurally impossible.
//
// Constraints:
//   - Must be self-contained: only `serde`, `schemars`, and `std::collections::HashMap`
//     are available to `build.rs` (no other orchard modules).
//   - `use` lines for `schemars::JsonSchema`, `serde::{Serialize, Deserialize}`, and
//     `std::collections::HashMap` are provided by the enclosing scope in each
//     caller and must NOT be repeated here.
//   - `CiChecks`, `CheckInfo`, and `WorkflowPhase` are defined locally here
//     (wire-format copies) so this file has no crate-internal dependencies.
//     `json_output.rs` converts from `crate::ci_state::CiChecks` etc. at the
//     mapping boundary.

// ---------------------------------------------------------------------------
// Local mirrors of ci_state and derive types (wire-format compatible)
// ---------------------------------------------------------------------------

/// A single CI check with its normalized state (wire-format mirror for schema gen).
#[derive(Serialize, Deserialize, JsonSchema)]
pub struct CheckInfo {
    /// Name of the check.
    pub name: String,
    /// Normalized state: `"passing"`, `"failing"`, or `"pending"`.
    pub state: String,
    /// URL to the check run details page, if available.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub details_url: Option<String>,
}

/// Two-bucket classification of all CI checks on a PR (wire-format mirror for schema gen).
#[derive(Serialize, Deserialize, JsonSchema)]
pub struct CiChecks {
    /// Checks classified as code CI.
    pub code: Vec<CheckInfo>,
    /// Checks classified as gate/policy checks.
    pub gate: Vec<CheckInfo>,
}

/// Workflow phase derived from issue/PR labels (wire-format mirror for schema gen).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum WorkflowPhase {
    /// Work halted, waiting on external resolution.
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
    /// Plan exists, scheduled but not yet started.
    Planned,
}

// ---------------------------------------------------------------------------
// JSON output types (versioned, camelCase) — mirrors of json_output.rs
// ---------------------------------------------------------------------------

/// Top-level versioned JSON output for `orchard --json`.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonOutput {
    /// Schema version number for forward compatibility.
    pub version: u32,
    /// Standalone tmux sessions not tied to any worktree.
    pub tmux_sessions: Vec<JsonSession>,
    /// All repositories in the output.
    pub repos: Vec<JsonRepo>,
    /// Reachability state keyed by SSH host name.
    pub hosts: HashMap<String, JsonHostState>,
}

/// A single repository in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonRepo {
    /// Repository slug in `owner/repo` format.
    pub slug: String,
    /// Default branch name (e.g. `main`), if known.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub default_branch: Option<String>,
    /// Rollup CI state of the default branch, if known.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub main_ci_state: Option<String>,
    /// All worktrees belonging to this repository.
    pub worktrees: Vec<JsonWorktree>,
}

/// Commit distance from the upstream branch.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonAheadBehind {
    /// Commits ahead of upstream.
    pub ahead: u32,
    /// Commits behind upstream.
    pub behind: u32,
}

/// A single worktree in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonWorktree {
    /// Absolute path to the worktree on disk.
    pub path: String,
    /// Git branch checked out in this worktree.
    pub branch: String,
    /// Remote SSH host this worktree lives on, or `null` for local.
    pub host: Option<String>,
    /// Physical layout: `"bare"` or `"flat"`.
    pub layout: String,
    /// Commit distance from upstream, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ahead_behind: Option<JsonAheadBehind>,
    /// ISO 8601 timestamp of the most recent commit, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_commit_at: Option<String>,
    /// Linked GitHub issue, if any.
    pub issue: Option<JsonIssue>,
    /// Linked pull request, if any.
    pub pr: Option<JsonPr>,
    /// Active tmux sessions associated with this worktree.
    pub sessions: Vec<JsonSession>,
    /// Display group as a snake_case string.
    pub display_group: String,
    /// Pipeline status as a stable snake_case string (e.g. `"unresolved_threads"`, `"ready"`).
    ///
    /// Stable external contract: downstream scripts parse this value. New variants
    /// may be added but existing strings will never change.
    pub status: String,
    /// Single-glyph representation of the pipeline status (e.g. `"💬"`, `"🟢"`).
    pub status_glyph: String,
    /// True when this is the repo's main worktree.
    pub is_main_worktree: bool,
    /// ISO 8601 timestamp of the most recent activity.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_activity_at: Option<String>,
}

/// A child issue nested under a parent in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonSubIssue {
    /// GitHub issue number.
    pub number: u32,
    /// Sub-issue title.
    pub title: String,
    /// Sub-issue state.
    pub state: String,
}

/// Issue information in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonIssue {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state: "open", "closed", or "completed".
    pub state: String,
    /// Assignees of this issue.
    pub assignees: Vec<String>,
    /// ISO 8601 timestamp when the issue was created.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    /// Issue numbers that block this issue.
    pub blocked_by: Vec<u32>,
    /// Child issues of this issue.
    pub sub_issues: Vec<JsonSubIssue>,
    /// Parent issue number, if this is a sub-issue.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent: Option<u32>,
    /// All GitHub labels on this issue.
    pub labels: Vec<String>,
    /// Workflow phase derived from labels.
    pub phase: Option<WorkflowPhase>,
}

/// A single review on a pull request in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonReview {
    /// GitHub login of the reviewer.
    pub author: String,
    /// Review state (e.g. `APPROVED`, `CHANGES_REQUESTED`).
    pub state: String,
    /// ISO 8601 timestamp when the review was submitted.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub submitted_at: Option<String>,
}

/// Pull request information in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonPr {
    /// GitHub PR number.
    pub number: u32,
    /// Head branch name for this PR.
    pub branch: String,
    /// PR title, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    /// Whether the PR is a draft.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub is_draft: Option<bool>,
    /// GitHub login of the PR author.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub author: Option<String>,
    /// Logins of users and teams requested as reviewers.
    pub requested_reviewers: Vec<String>,
    /// Reviews submitted on this PR.
    pub reviews: Vec<JsonReview>,
    /// PR state: "OPEN", "CLOSED", or "MERGED".
    pub state: Option<String>,
    /// Review decision.
    pub review_decision: Option<String>,
    /// Deprecated: use ci_code_state.
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks only.
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks.
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    pub ci_checks: CiChecks,
    /// True when the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// Forward-compatibility alias for `unresolved_threads`.
    pub unresolved_review_threads: u32,
    /// ISO 8601 timestamp of the most recent top-level review, or null.
    pub last_review_comment_at: Option<String>,
    /// GitHub login of the author of the most recent top-level review, or null.
    pub last_review_comment_author: Option<String>,
    /// True iff a non-author review is more recent than the last author push on an open PR.
    pub has_unaddressed_author_comment: bool,
    /// All GitHub labels on this PR.
    pub labels: Vec<String>,
    /// Lines added by this PR.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub additions: Option<u32>,
    /// Lines deleted by this PR.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub deletions: Option<u32>,
    /// ISO 8601 timestamp when the PR was created.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the PR was last updated.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
    /// ISO 8601 timestamp of when the last commit was pushed to this PR.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_commit_pushed_at: Option<String>,
    /// Workflow phase derived from labels.
    pub phase: Option<WorkflowPhase>,
}

/// Session information in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonSession {
    /// tmux session name.
    pub name: String,
    /// Host as a string: "local" or the SSH target.
    pub host: String,
    /// Session status: "running" or "dead".
    pub status: String,
    /// ISO 8601 timestamp when the session was created.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub started_at: Option<String>,
    /// ISO 8601 timestamp of the last activity in this session.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_activity_at: Option<String>,
    /// Claude enrichment data, or `null` when no Claude process is active.
    pub claude: Option<JsonClaudeInfo>,
    /// Window hierarchy for this session.
    pub windows: Vec<JsonWindow>,
}

/// Window within a session in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonWindow {
    /// Tmux's stable window index.
    pub index: usize,
    /// Window name from tmux.
    pub name: String,
    /// Whether this is the active window in the session.
    pub is_active: bool,
    /// Tmux layout string.
    pub layout: String,
    /// Panes belonging to this window.
    pub panes: Vec<JsonPane>,
}

/// Individual pane within a window in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonPane {
    /// Zero-based sequential index in the flat pane list.
    pub index: usize,
    /// Tmux window.pane target address (e.g., "0.1").
    pub tmux_target: String,
    /// Command running in this pane.
    pub command: String,
    /// Tmux pane title.
    pub title: String,
    /// True when the pane is running a Claude process.
    pub has_claude: bool,
    /// Working directory at the time of snapshot.
    pub cwd: String,
    /// Whether this pane is the focused/active pane in its window.
    pub is_active: bool,
}

/// Claude enrichment data in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "camelCase")]
pub struct JsonClaudeInfo {
    /// Claude state as a string: "working", "idle", "input", or "none".
    pub status: String,
    /// Model name, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    /// Last tool invoked, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_tool: Option<String>,
    /// First line of the last user prompt (≤80 chars), if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub current_task: Option<String>,
    /// Elapsed seconds since the session started.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session_age_sec: Option<u64>,
    /// Total input tokens from the most recent assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub input_tokens: Option<u64>,
    /// Total output tokens from the most recent assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output_tokens: Option<u64>,
    /// Cache creation input tokens from the most recent assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_creation_input_tokens: Option<u64>,
    /// Cache read input tokens from the most recent assistant message.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_read_input_tokens: Option<u64>,
    /// Context window usage percentage.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context_window_pct: Option<f64>,
    /// Total cost in USD.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cost_usd: Option<f64>,
    /// Total session duration in milliseconds.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total_duration_ms: Option<u64>,
    /// Stop reason from the last Stop event.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stop_reason: Option<String>,
    /// Number of assistant turns in the conversation.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub turn_count: Option<u32>,
    /// Elapsed seconds since the current state was entered.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state_elapsed_sec: Option<u64>,
}

/// Host reachability information in JSON output.
#[derive(Serialize, Deserialize, JsonSchema)]
pub struct JsonHostState {
    /// True when the SSH host responded to the last reachability check.
    pub reachable: bool,
}
