//! Versioned JSON output mapping for the `--json` flag.
//!
//! Decouples the public JSON API from internal `OrchardState`, allowing internal refactors
//! without breaking scripts. All output is camelCase, version-numbered, and backed by tests.
//! Consumed directly by external tools and scripts that call `orchard --json`.

use std::collections::HashMap;

use serde::Serialize;

use crate::claude_state::ClaudeState;
use crate::derive::{DisplayGroup, phase_from_labels};
use crate::orchard_state::{
    IssueInfo, OrchardState, PrState, RepoState, SessionState, WindowState, WorktreeState,
};
use crate::session::StandaloneSessionRow;

// ---------------------------------------------------------------------------
// JSON output types (versioned, camelCase)
// ---------------------------------------------------------------------------

/// Top-level versioned JSON output for `orchard --json`.
///
/// Contains a version number (for forward compatibility) and collections of repos and hosts.
#[derive(Serialize)]
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
///
/// Contains the repo slug and all worktrees within it.
#[derive(Serialize)]
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
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonAheadBehind {
    /// Commits ahead of upstream.
    pub ahead: u32,
    /// Commits behind upstream.
    pub behind: u32,
}

/// A single worktree in JSON output.
///
/// Represents a git worktree with its path, branch, host, linked issue/PR, active sessions, and display group.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonWorktree {
    /// Absolute path to the worktree on disk.
    pub path: String,
    /// Git branch checked out in this worktree.
    pub branch: String,
    /// Remote SSH host this worktree lives on, or `null` for local.
    pub host: Option<String>,
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
    /// Active Claude sessions associated with this worktree.
    pub sessions: Vec<JsonSession>,
    /// Display group as a snake_case string (e.g., "needs_attention").
    pub display_group: String,
    /// True when this is the repo's main worktree.
    pub is_main_worktree: bool,
    /// ISO 8601 timestamp of the most recent activity: `pr.last_commit_pushed_at` if set,
    /// otherwise the worktree's own `last_commit_at`. `null` when neither exists.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_activity_at: Option<String>,
}

/// A child issue nested under a parent in JSON output.
#[derive(Serialize)]
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
///
/// Subset of GitHub issue data: number, title, state, and computed workflow phase.
#[derive(Serialize)]
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
    /// All GitHub labels on this issue, in the order returned by the API.
    pub labels: Vec<String>,
    /// Workflow phase derived from labels (e.g. `"in-progress"`, `"blocked"`).
    /// Always present: `null` when no phase label is set.
    pub phase: Option<&'static str>,
}

/// A single review on a pull request in JSON output.
#[derive(Serialize)]
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
///
/// Includes PR metadata: number, branch, state, review decision, CI checks, conflicts,
/// unresolved review threads, and computed workflow phase.
#[derive(Serialize)]
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
    /// Review decision: "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", etc.
    pub review_decision: Option<String>,
    /// Deprecated: use ci_code_state; retained for one release (issue #218).
    ///
    /// Mirrors `ci_code_state` only — a code-green gate-blocked PR serializes as
    /// `checksState: "passing"` so legacy consumers that filter on `checksState ==
    /// "failing"` are not broken by the gate-blocked case.
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks only: "passing", "failing", "pending", or null.
    ///
    /// Null means the PR has no code CI checks (e.g. docs-only PR).
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: "cleared", "blocked", "pending", or null.
    ///
    /// Null means no gate patterns matched any check on this PR.
    /// "blocked" means a gate check failed — typically waiting on human approval,
    /// not a broken-code signal.
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    ///
    /// Each entry in `code` and `gate` is an object with `"name"` and `"state"` keys.
    /// There is no `ignored` bucket in v1 (reserved for a follow-up issue).
    pub ci_checks: crate::ci_state::CiChecks,
    /// True when the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// All GitHub labels on this PR, in the order returned by the API.
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
    /// Workflow phase derived from labels (e.g. `"pr-ready"`, `"blocked"`).
    /// Always present: `null` when no phase label is set.
    pub phase: Option<&'static str>,
}

/// Session information in JSON output using the EnrichedSession shape.
///
/// Contains tmux session identity (name, host, status), optional Claude enrichment,
/// and the full window → pane hierarchy.
#[derive(Serialize)]
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
    /// Window hierarchy for this session (window → pane structure).
    pub windows: Vec<JsonWindow>,
}

/// Window within a session in JSON output.
///
/// Contains window identity and its panes.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonWindow {
    /// Tmux's stable window index.
    pub index: usize,
    /// Window name from tmux.
    pub name: String,
    /// Whether this is the active window in the session.
    pub is_active: bool,
    /// Tmux layout string, usable with `tmux select-layout` during restore.
    pub layout: String,
    /// Panes belonging to this window.
    pub panes: Vec<JsonPane>,
}

/// Individual pane within a window in JSON output.
///
/// Contains pane identity, running command, title, and Claude detection flag.
#[derive(Serialize)]
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
    /// Working directory at the time of snapshot (from `#{pane_current_path}`).
    pub cwd: String,
    /// Whether this pane is the focused/active pane in its window.
    pub is_active: bool,
    /// Claude session ID for resuming the conversation via `claude --continue`,
    /// populated only when the pane was running Claude.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub claude_session_id: Option<String>,
}

/// Claude enrichment data in JSON output.
///
/// `sessionAgeSec` is computed at serialization time from `session_start_ts`
/// so it always reflects real elapsed time, not the time of the last hook write.
/// All fields except `status` are optional and omitted when absent.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonClaudeInfo {
    /// Claude state as a string: "working", "idle", "input", or "none".
    pub status: String,
    /// Model name (e.g., `"claude-opus-4-6"`), if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<String>,
    /// Last tool invoked, if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_tool: Option<String>,
    /// First line of the last user prompt (≤80 chars), if available.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub current_task: Option<String>,
    /// Elapsed seconds since the session started, computed at read time.
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
    /// Context window usage percentage from status line telemetry.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub context_window_pct: Option<f64>,
    /// Total cost in USD from status line telemetry.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cost_usd: Option<f64>,
    /// Total session duration in milliseconds from status line telemetry.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total_duration_ms: Option<u64>,
    /// Stop reason from the last Stop event.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stop_reason: Option<String>,
    /// Number of assistant turns in the conversation.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub turn_count: Option<u32>,
    /// Elapsed seconds since the current state was entered, computed at read time.
    ///
    /// Derived from `state_changed_at` in the hook state file. Absent when the
    /// hook version does not write `state_changed_at`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state_elapsed_sec: Option<u64>,
}

/// Host reachability information in JSON output.
///
/// Simple boolean indicating whether an SSH host is reachable.
#[derive(Serialize)]
pub struct JsonHostState {
    /// True when the SSH host responded to the last reachability check.
    pub reachable: bool,
}

// ---------------------------------------------------------------------------
// Serialization helpers
// ---------------------------------------------------------------------------

/// Converts a Unix epoch seconds timestamp to an ISO 8601 string.
///
/// Returns `None` when the input is `None` or the conversion fails.
fn unix_to_iso8601(ts: Option<u64>) -> Option<String> {
    use chrono::{DateTime, Utc};
    let secs = ts?;
    let dt = DateTime::<Utc>::from_timestamp(secs as i64, 0)?;
    Some(dt.to_rfc3339())
}

/// Computes elapsed seconds since `session_start_ts` (unix epoch seconds) at
/// the time of the call. Returns `None` when `session_start_ts` is absent or
/// the clock goes backwards.
fn compute_session_age_sec(session_start_ts: Option<u64>) -> Option<u64> {
    let start = session_start_ts?;
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .ok()?
        .as_secs();
    now.checked_sub(start)
}

fn claude_info_from_enrichment(c: &crate::orchard_state::ClaudeEnrichment) -> JsonClaudeInfo {
    JsonClaudeInfo {
        status: claude_state_str(c.status).to_string(),
        model: c.model.clone(),
        last_tool: c.last_tool.clone(),
        current_task: c.current_task.clone(),
        session_age_sec: compute_session_age_sec(c.session_start_ts),
        input_tokens: c.input_tokens,
        output_tokens: c.output_tokens,
        cache_creation_input_tokens: c.cache_creation_input_tokens,
        cache_read_input_tokens: c.cache_read_input_tokens,
        context_window_pct: c.context_window_pct,
        cost_usd: c.cost_usd,
        total_duration_ms: c.total_duration_ms,
        stop_reason: c.stop_reason.clone(),
        turn_count: c.turn_count,
        state_elapsed_sec: compute_session_age_sec(c.state_changed_at),
    }
}

fn claude_info_from_session(c: &crate::session::ClaudeSessionInfo) -> JsonClaudeInfo {
    JsonClaudeInfo {
        status: claude_state_str(c.status).to_string(),
        model: c.model.clone(),
        last_tool: c.last_tool.clone(),
        current_task: c.current_task.clone(),
        session_age_sec: compute_session_age_sec(c.session_start_ts),
        input_tokens: c.input_tokens,
        output_tokens: c.output_tokens,
        cache_creation_input_tokens: c.cache_creation_input_tokens,
        cache_read_input_tokens: c.cache_read_input_tokens,
        context_window_pct: c.context_window_pct,
        cost_usd: c.cost_usd,
        total_duration_ms: c.total_duration_ms,
        stop_reason: c.stop_reason.clone(),
        turn_count: c.turn_count,
        state_elapsed_sec: compute_session_age_sec(c.state_changed_at),
    }
}

fn display_group_str(g: DisplayGroup) -> &'static str {
    match g {
        DisplayGroup::RepoMain => "repo_main",
        DisplayGroup::Prioritized => "prioritized",
        DisplayGroup::NeedsAttention => "needs_attention",
        DisplayGroup::ClaudeWorking => "claude_working",
        DisplayGroup::ReadyToMerge => "ready_to_merge",
        DisplayGroup::Other => "other",
    }
}

fn claude_state_str(s: ClaudeState) -> &'static str {
    match s {
        ClaudeState::Working => "working",
        ClaudeState::Idle => "idle",
        ClaudeState::Input => "input",
        ClaudeState::None => "none",
    }
}

// ---------------------------------------------------------------------------
// From conversions
// ---------------------------------------------------------------------------

impl From<&IssueInfo> for JsonIssue {
    /// Converts an internal `IssueInfo` to JSON output format.
    fn from(i: &IssueInfo) -> Self {
        Self {
            number: i.number,
            title: i.title.clone(),
            state: i.state.clone(),
            assignees: i.assignees.clone(),
            created_at: i.created_at.clone(),
            blocked_by: i.blocked_by.clone(),
            sub_issues: i
                .sub_issues
                .iter()
                .map(|s| JsonSubIssue {
                    number: s.number,
                    title: s.title.clone(),
                    state: s.state.clone(),
                })
                .collect(),
            parent: i.parent,
            labels: i.labels.clone(),
            phase: phase_from_labels(&i.labels),
        }
    }
}

#[allow(deprecated)] // reads pr.checks_state: retained for one release per issue #218
impl From<&PrState> for JsonPr {
    /// Converts an internal `PrState` to JSON output format.
    ///
    /// The legacy `checks_state` field is populated from `pr.checks_state`, which
    /// the cache layer (slice 2, `cache_sources.rs`) already sets to mirror
    /// `ci_code_state` only — so a code-green gate-blocked PR correctly serializes
    /// as `checksState: "passing"` without any coercion here.
    fn from(pr: &PrState) -> Self {
        Self {
            number: pr.number,
            branch: pr.branch.clone(),
            title: pr.title.clone(),
            is_draft: pr.is_draft,
            author: pr.author.clone(),
            requested_reviewers: pr.requested_reviewers.clone(),
            reviews: pr
                .reviews
                .iter()
                .map(|r| JsonReview {
                    author: r.author.clone(),
                    state: r.state.clone(),
                    submitted_at: r.submitted_at.clone(),
                })
                .collect(),
            state: pr.state.clone(),
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
            phase: phase_from_labels(&pr.labels),
        }
    }
}

impl From<&WindowState> for JsonWindow {
    /// Converts an internal `WindowState` to JSON output format with nested panes.
    fn from(w: &WindowState) -> Self {
        Self {
            index: w.index,
            name: w.name.clone(),
            is_active: w.is_active,
            layout: w.layout.clone(),
            panes: w
                .panes
                .iter()
                .map(|p| JsonPane {
                    index: p.index,
                    tmux_target: p.tmux_target.clone(),
                    command: p.command.clone(),
                    title: p.title.clone(),
                    has_claude: p.has_claude,
                    cwd: p.cwd.clone(),
                    is_active: p.is_active,
                    claude_session_id: p.claude_session_id.clone(),
                })
                .collect(),
        }
    }
}

impl From<&SessionState> for JsonSession {
    /// Converts an internal `SessionState` to JSON v4 format with nested `claude` and `windows` fields.
    fn from(s: &SessionState) -> Self {
        let host = match &s.host {
            Some(h) => h.clone(),
            None => "local".to_string(),
        };
        let claude = s.claude.as_ref().map(claude_info_from_enrichment);
        Self {
            name: s.name.clone(),
            host,
            status: "running".to_string(),
            started_at: unix_to_iso8601(s.started_at),
            last_activity_at: unix_to_iso8601(s.last_activity_at),
            claude,
            windows: s.windows.iter().map(Into::into).collect(),
        }
    }
}

impl From<&WorktreeState> for JsonWorktree {
    /// Converts an internal `WorktreeState` to JSON output format, serializing the display group to a string.
    fn from(ws: &WorktreeState) -> Self {
        let ahead_behind = ws
            .ahead_behind
            .map(|(ahead, behind)| JsonAheadBehind { ahead, behind });
        Self {
            path: ws.path.clone(),
            branch: ws.branch.clone(),
            host: ws.host.clone(),
            ahead_behind,
            last_commit_at: ws.last_commit_at.clone(),
            issue: ws.issue.as_ref().map(Into::into),
            pr: ws.pr.as_ref().map(Into::into),
            sessions: ws.sessions.iter().map(Into::into).collect(),
            display_group: display_group_str(ws.display_group).to_string(),
            is_main_worktree: ws.is_main_worktree,
            last_activity_at: ws
                .pr
                .as_ref()
                .and_then(|pr| pr.last_commit_pushed_at.clone())
                .or_else(|| ws.last_commit_at.clone()),
        }
    }
}

impl From<&RepoState> for JsonRepo {
    /// Converts an internal `RepoState` to JSON output format.
    fn from(r: &RepoState) -> Self {
        Self {
            slug: r.slug.clone(),
            default_branch: r.default_branch.clone(),
            main_ci_state: r.main_ci_state.clone(),
            worktrees: r.worktrees.iter().map(Into::into).collect(),
        }
    }
}

impl From<&StandaloneSessionRow> for JsonSession {
    fn from(row: &StandaloneSessionRow) -> Self {
        let host = match &row.session.tmux.host {
            crate::session::Host::Local => "local".to_string(),
            crate::session::Host::Remote(h) => h.clone(),
        };
        let status = match &row.session.tmux.status {
            crate::session::SessionStatus::Running { .. } => "running",
            crate::session::SessionStatus::Dead => "dead",
        };
        let claude = row.session.claude.as_ref().map(claude_info_from_session);
        let windows = row
            .session
            .windows
            .iter()
            .map(|w| JsonWindow {
                index: w.index,
                name: w.name.clone(),
                is_active: w.is_active,
                layout: w.layout.clone(),
                panes: w
                    .panes
                    .iter()
                    .map(|p| JsonPane {
                        index: p.index,
                        tmux_target: p.tmux_target.clone(),
                        command: p.command.clone(),
                        title: p.title.clone(),
                        has_claude: p.has_claude,
                        cwd: p.cwd.clone(),
                        is_active: p.is_active,
                        claude_session_id: p.claude_session_id.clone(),
                    })
                    .collect(),
            })
            .collect();
        Self {
            name: row.session.tmux.name.clone(),
            host,
            status: status.to_string(),
            started_at: unix_to_iso8601(row.session.started_at),
            last_activity_at: unix_to_iso8601(row.session.last_activity_at),
            claude,
            windows,
        }
    }
}

impl From<&OrchardState> for JsonOutput {
    /// Converts the unified `OrchardState` to JSON output, setting version to 4.
    fn from(state: &OrchardState) -> Self {
        let hosts = state
            .hosts
            .iter()
            .map(|(host, h)| {
                (
                    host.clone(),
                    JsonHostState {
                        reachable: h.reachable,
                    },
                )
            })
            .collect();

        Self {
            version: 4,
            tmux_sessions: state.standalone_sessions.iter().map(Into::into).collect(),
            repos: state.repos.iter().map(Into::into).collect(),
            hosts,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{ClaudeEnrichment, RepoState, SessionState, WorktreeState};

    fn empty_state() -> OrchardState {
        OrchardState::new()
    }

    fn make_worktree(display_group: DisplayGroup) -> WorktreeState {
        WorktreeState {
            path: "/repos/main".to_string(),
            branch: "main".to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: None,
            sessions: vec![],
            display_group,
            is_main_worktree: false,
            ahead_behind: None,
            last_commit_at: None,
        }
    }

    #[test]
    fn from_orchard_state_produces_version_4() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(output.version, 4);
    }

    #[test]
    fn from_orchard_state_empty_repos_and_hosts() {
        let output = JsonOutput::from(&empty_state());
        assert!(output.repos.is_empty());
        assert!(output.hosts.is_empty());
    }

    #[test]
    fn display_group_repo_main_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::RepoMain);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "repo_main");
    }

    #[test]
    fn display_group_needs_attention_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::NeedsAttention);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "needs_attention");
    }

    #[test]
    fn display_group_claude_working_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::ClaudeWorking);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "claude_working");
    }

    #[test]
    fn display_group_ready_to_merge_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::ReadyToMerge);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "ready_to_merge");
    }

    #[test]
    fn display_group_other_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::Other);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "other");
    }

    #[test]
    fn json_output_has_camelcase_version_field() {
        let output = JsonOutput::from(&empty_state());
        let value = serde_json::to_value(&output).unwrap();
        assert!(value.get("version").is_some(), "expected 'version' key");
    }

    #[test]
    fn json_repo_has_camelcase_slug_field() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };
        let output = JsonOutput::from(&state);
        let value = serde_json::to_value(&output).unwrap();
        let repo = &value["repos"][0];
        assert!(repo.get("slug").is_some(), "expected 'slug' key in repo");
        assert!(
            repo.get("worktrees").is_some(),
            "expected 'worktrees' key in repo"
        );
    }

    #[test]
    fn json_worktree_has_camelcase_is_main_worktree_field() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![make_worktree(DisplayGroup::RepoMain)],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };
        let output = JsonOutput::from(&state);
        let value = serde_json::to_value(&output).unwrap();
        let wt = &value["repos"][0]["worktrees"][0];
        assert!(
            wt.get("isMainWorktree").is_some(),
            "expected camelCase 'isMainWorktree' key"
        );
        assert!(
            wt.get("displayGroup").is_some(),
            "expected camelCase 'displayGroup' key"
        );
    }

    #[test]
    fn json_session_claude_status_serializes_as_string() {
        let session = SessionState {
            name: "repo-claude".to_string(),
            host: None,
            claude: Some(ClaudeEnrichment {
                status: crate::claude_state::ClaudeState::Working,
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
            }),
            windows: vec![],
            started_at: None,
            last_activity_at: None,
        };
        let js = JsonSession::from(&session);
        assert_eq!(js.host, "local");
        assert_eq!(js.status, "running");
        let claude = js.claude.unwrap();
        assert_eq!(claude.status, "working");
    }

    #[test]
    fn json_session_claude_null_when_no_claude() {
        let session = SessionState {
            name: "repo-main".to_string(),
            host: None,
            claude: None,
            windows: vec![],
            started_at: None,
            last_activity_at: None,
        };
        let js = JsonSession::from(&session);
        assert!(js.claude.is_none());
    }

    // -- Window hierarchy tests (section 9) ----------------------------------

    use crate::orchard_state::{PaneState, WindowState};

    fn make_session_with_windows() -> SessionState {
        SessionState {
            name: "test-session".to_string(),
            host: None,
            claude: None,
            started_at: None,
            last_activity_at: None,
            windows: vec![
                WindowState {
                    index: 0,
                    name: "main".to_string(),
                    is_active: true,
                    layout: String::new(),
                    panes: vec![PaneState {
                        index: 0,
                        tmux_target: "0.0".to_string(),
                        command: "bash".to_string(),
                        title: "bash".to_string(),
                        has_claude: false,
                        cwd: String::new(),
                        is_active: false,
                        claude_session_id: None,
                    }],
                },
                WindowState {
                    index: 1,
                    name: "editor".to_string(),
                    is_active: false,
                    layout: String::new(),
                    panes: vec![PaneState {
                        index: 1,
                        tmux_target: "1.0".to_string(),
                        command: "claude".to_string(),
                        title: "claude".to_string(),
                        has_claude: true,
                        cwd: String::new(),
                        is_active: false,
                        claude_session_id: None,
                    }],
                },
            ],
        }
    }

    #[test]
    fn json_version_is_4() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(output.version, 4);
    }

    #[test]
    fn json_session_includes_windows_array() {
        let session = make_session_with_windows();
        let js = JsonSession::from(&session);
        assert_eq!(js.windows.len(), 2);
    }

    #[test]
    fn json_window_has_index_name_is_active_panes() {
        let session = make_session_with_windows();
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let win0 = &value["windows"][0];
        assert!(win0.get("index").is_some(), "expected 'index'");
        assert!(win0.get("name").is_some(), "expected 'name'");
        assert!(
            win0.get("isActive").is_some(),
            "expected camelCase 'isActive'"
        );
        assert!(win0.get("panes").is_some(), "expected 'panes'");
        assert_eq!(win0["index"], 0);
        assert_eq!(win0["name"], "main");
        assert_eq!(win0["isActive"], true);
    }

    #[test]
    fn json_pane_has_expected_camelcase_fields() {
        let session = make_session_with_windows();
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let pane = &value["windows"][1]["panes"][0];
        assert!(pane.get("index").is_some(), "expected 'index'");
        assert!(
            pane.get("tmuxTarget").is_some(),
            "expected camelCase 'tmuxTarget'"
        );
        assert!(pane.get("command").is_some(), "expected 'command'");
        assert!(pane.get("title").is_some(), "expected 'title'");
        assert!(
            pane.get("hasClaude").is_some(),
            "expected camelCase 'hasClaude'"
        );
        assert_eq!(pane["hasClaude"], true);
    }

    #[test]
    fn json_single_window_session_still_has_windows_array() {
        let session = SessionState {
            name: "single-window".to_string(),
            host: None,
            claude: None,
            started_at: None,
            last_activity_at: None,
            windows: vec![WindowState {
                index: 0,
                name: "main".to_string(),
                is_active: true,
                layout: String::new(),
                panes: vec![],
            }],
        };
        let js = JsonSession::from(&session);
        assert_eq!(js.windows.len(), 1);
    }

    // -- phase field tests ---------------------------------------------------

    fn make_issue_info(number: u32, title: &str, state: &str, labels: Vec<&str>) -> IssueInfo {
        IssueInfo {
            number,
            title: title.to_string(),
            state: state.to_string(),
            labels: labels.into_iter().map(|s| s.to_string()).collect(),
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        }
    }

    #[allow(deprecated)]
    fn make_pr_state_with_labels(number: u32, branch: &str, labels: Vec<&str>) -> PrState {
        use crate::ci_state::CiChecks;
        PrState {
            number,
            branch: branch.to_string(),
            state: Some("open".to_string()),
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            review_decision: None,
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            labels: labels.into_iter().map(|s| s.to_string()).collect(),
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
        }
    }

    #[test]
    fn json_issue_phase_null_when_no_phase_label() {
        let issue = make_issue_info(1, "fix bug", "open", vec!["bug"]);
        let ji = JsonIssue::from(&issue);
        assert!(ji.phase.is_none());
        let v = serde_json::to_value(&ji).unwrap();
        assert_eq!(v["phase"], serde_json::Value::Null);
    }

    #[test]
    fn json_issue_phase_serializes_matched_label() {
        let issue = make_issue_info(2, "work item", "open", vec!["in-progress", "bug"]);
        let ji = JsonIssue::from(&issue);
        assert_eq!(ji.phase, Some("in-progress"));
        let v = serde_json::to_value(&ji).unwrap();
        assert_eq!(v["phase"], "in-progress");
    }

    #[test]
    fn json_pr_phase_null_when_no_phase_label() {
        let pr = make_pr_state_with_labels(10, "feat/branch", vec!["enhancement"]);
        let jp = JsonPr::from(&pr);
        assert!(jp.phase.is_none());
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], serde_json::Value::Null);
    }

    #[test]
    fn json_pr_phase_serializes_matched_label() {
        let pr = make_pr_state_with_labels(10, "feat/branch", vec!["pr-ready"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some("pr-ready"));
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], "pr-ready");
    }

    #[test]
    fn json_pr_phase_resolves_multi_label_by_priority() {
        let pr = make_pr_state_with_labels(10, "feat/branch", vec!["in-progress", "blocked"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some("blocked"));
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], "blocked");
    }

    #[test]
    fn json_issue_phase_key_always_present_when_null() {
        let issue = make_issue_info(3, "empty labels", "open", vec![]);
        let v = serde_json::to_value(JsonIssue::from(&issue)).unwrap();
        assert!(v.get("phase").is_some(), "phase key must always be present");
        assert_eq!(v["phase"], serde_json::Value::Null);
    }

    #[test]
    fn json_pr_phase_key_always_present_when_null() {
        let pr = make_pr_state_with_labels(11, "feat/empty", vec![]);
        let v = serde_json::to_value(JsonPr::from(&pr)).unwrap();
        assert!(v.get("phase").is_some(), "phase key must always be present");
        assert_eq!(v["phase"], serde_json::Value::Null);
    }

    #[test]
    fn json_issue_preserves_existing_fields_when_phase_added() {
        let issue = make_issue_info(219, "phase field", "open", vec!["planned"]);
        let v = serde_json::to_value(JsonIssue::from(&issue)).unwrap();
        assert_eq!(v["phase"], "planned");
        assert_eq!(v["number"], 219);
        assert_eq!(v["title"], "phase field");
        assert_eq!(v["state"], "open");
    }

    #[test]
    fn json_pr_preserves_existing_fields_when_phase_added() {
        let pr = make_pr_state_with_labels(220, "issue219/phase-field", vec!["in-ai-review"]);
        let v = serde_json::to_value(JsonPr::from(&pr)).unwrap();
        assert_eq!(v["phase"], "in-ai-review");
        assert_eq!(v["number"], 220);
        assert_eq!(v["branch"], "issue219/phase-field");
        assert!(
            v.get("reviewDecision").is_some(),
            "reviewDecision key must be present"
        );
        assert!(
            v.get("checksState").is_some(),
            "checksState key must be present"
        );
        assert!(
            v.get("hasConflicts").is_some(),
            "hasConflicts key must be present"
        );
        assert!(
            v.get("unresolvedThreads").is_some(),
            "unresolvedThreads key must be present"
        );
    }

    // -- AC1 / AC2: telemetry fields in JSON output --------------------------

    fn make_enriched_session(enrichment: ClaudeEnrichment) -> SessionState {
        SessionState {
            name: "repo-claude".to_string(),
            host: None,
            claude: Some(enrichment),
            windows: vec![],
            started_at: None,
            last_activity_at: None,
        }
    }

    fn full_enrichment() -> ClaudeEnrichment {
        ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Working,
            model: Some("claude-opus-4-6".to_string()),
            last_tool: Some("Bash".to_string()),
            current_task: Some("fix flaky hook test".to_string()),
            session_start_ts: Some(1700000000),
            input_tokens: Some(50000),
            output_tokens: Some(800),
            cache_creation_input_tokens: Some(10000),
            cache_read_input_tokens: Some(40000),
            context_window_pct: None,
            cost_usd: None,
            total_duration_ms: None,
            rate_limits: None,
            stop_reason: None,
            turn_count: None,
            state_changed_at: None,
        }
    }

    /// AC2: ClaudeState values round-trip correctly as strings.
    #[test]
    fn claude_state_values_are_restricted_to_four_strings() {
        use crate::claude_state::ClaudeState;
        assert_eq!(claude_state_str(ClaudeState::Working), "working");
        assert_eq!(claude_state_str(ClaudeState::Idle), "idle");
        assert_eq!(claude_state_str(ClaudeState::Input), "input");
        assert_eq!(claude_state_str(ClaudeState::None), "none");
    }

    /// AC1: all new telemetry fields are present when enrichment is populated.
    #[test]
    fn all_new_telemetry_fields_present_when_populated() {
        let session = make_enriched_session(full_enrichment());
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert_eq!(claude.model.as_deref(), Some("claude-opus-4-6"));
        assert_eq!(claude.last_tool.as_deref(), Some("Bash"));
        assert_eq!(claude.current_task.as_deref(), Some("fix flaky hook test"));
        assert!(
            claude.session_age_sec.is_some(),
            "sessionAgeSec must be present"
        );
        assert_eq!(claude.input_tokens, Some(50000));
        assert_eq!(claude.output_tokens, Some(800));
        assert_eq!(claude.cache_creation_input_tokens, Some(10000));
        assert_eq!(claude.cache_read_input_tokens, Some(40000));
    }

    /// AC1: status field is always present.
    #[test]
    fn status_field_always_present() {
        let session = make_enriched_session(full_enrichment());
        let js = JsonSession::from(&session);
        assert_eq!(js.claude.unwrap().status, "working");
    }

    /// AC1: new fields are absent (not null) when enrichment has no data.
    #[test]
    fn missing_telemetry_fields_are_absent_not_null() {
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Idle,
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
        });
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let claude = &value["claude"];
        assert_eq!(claude["status"], "idle");
        // Optional fields must be absent (not null) when not set.
        assert!(claude.get("model").is_none(), "model must be absent");
        assert!(claude.get("lastTool").is_none(), "lastTool must be absent");
        assert!(
            claude.get("currentTask").is_none(),
            "currentTask must be absent"
        );
        assert!(
            claude.get("sessionAgeSec").is_none(),
            "sessionAgeSec must be absent"
        );
        assert!(
            claude.get("inputTokens").is_none(),
            "inputTokens must be absent"
        );
        assert!(
            claude.get("outputTokens").is_none(),
            "outputTokens must be absent"
        );
        assert!(
            claude.get("cacheCreationInputTokens").is_none(),
            "cacheCreationInputTokens must be absent"
        );
        assert!(
            claude.get("cacheReadInputTokens").is_none(),
            "cacheReadInputTokens must be absent"
        );
    }

    /// AC5: session_age_sec is computed at read time from session_start_ts.
    #[test]
    fn session_age_sec_computed_at_read_time() {
        // Use a fixed start time well in the past (unix epoch 1 — 1970).
        let past_ts: u64 = 1;
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Working,
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: Some(past_ts),
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
        });
        let js = JsonSession::from(&session);
        let age = js.claude.unwrap().session_age_sec.unwrap();
        // The session is very old — age must be very large (> 1 billion seconds since 1970).
        assert!(
            age > 1_000_000_000,
            "session_age_sec must reflect elapsed time: got {age}"
        );
    }

    /// AC5: session_age_sec absent when session_start_ts is None.
    #[test]
    fn session_age_sec_absent_when_no_start_ts() {
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Idle,
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
        });
        let js = JsonSession::from(&session);
        assert!(
            js.claude.unwrap().session_age_sec.is_none(),
            "sessionAgeSec must be absent when session_start_ts is None"
        );
    }

    /// AC5: session_age_sec is computed fresh — not stale from state file.
    #[test]
    fn session_age_sec_stays_fresh() {
        // If session_start_ts is 10 seconds ago and now advances, age grows.
        let now_secs = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let start_ts = now_secs - 60; // 60 seconds ago
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Working,
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: Some(start_ts),
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
        });
        let js = JsonSession::from(&session);
        let age = js.claude.unwrap().session_age_sec.unwrap();
        assert!(
            age >= 60,
            "session_age_sec must be >= 60 when session started 60s ago: got {age}"
        );
    }

    /// AC6: sessions without a Claude process omit the claude object entirely.
    #[test]
    fn no_claude_process_omits_claude_key() {
        let session = SessionState {
            name: "zsh-session".to_string(),
            host: None,
            claude: None,
            windows: vec![],
            started_at: None,
            last_activity_at: None,
        };
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        assert!(
            value.get("claude").is_none() || value["claude"].is_null(),
            "sessions without Claude must not have claude key"
        );
        let js = JsonSession::from(&session);
        assert!(
            js.claude.is_none(),
            "claude must be None for non-Claude sessions"
        );
    }

    /// AC7 (fresh session): only status present; other fields absent.
    #[test]
    fn fresh_session_only_model_and_status_present() {
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Idle,
            model: Some("claude-opus-4-6".to_string()),
            last_tool: None,
            current_task: None,
            session_start_ts: Some(1700000000),
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
        });
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert!(claude.model.is_some(), "model must be present");
        assert!(
            claude.session_age_sec.is_some(),
            "sessionAgeSec must be present"
        );
        assert!(
            claude.last_tool.is_none(),
            "lastTool must be absent for fresh session"
        );
        assert!(
            claude.current_task.is_none(),
            "currentTask must be absent for fresh session"
        );
        assert!(
            claude.input_tokens.is_none(),
            "inputTokens must be absent for fresh session"
        );
    }

    /// AC7 (heavy working): all fields populated.
    #[test]
    fn heavy_working_all_fields_populated() {
        let session = make_enriched_session(full_enrichment());
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert_eq!(claude.status, "working");
        assert!(claude.model.is_some());
        assert!(claude.session_age_sec.is_some());
        assert!(claude.input_tokens.is_some());
        assert!(claude.output_tokens.is_some());
        assert!(claude.cache_read_input_tokens.is_some());
        assert!(claude.cache_creation_input_tokens.is_some());
        assert!(claude.last_tool.is_some());
        assert!(claude.current_task.is_some());
    }

    /// AC7 (idle after Stop): lastTool absent, tokens present, sessionAge present.
    #[test]
    fn idle_after_stop_last_tool_absent_tokens_present() {
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Idle,
            model: Some("claude-opus-4-6".to_string()),
            last_tool: None, // cleared by Stop
            current_task: None,
            session_start_ts: Some(1700000000),
            input_tokens: Some(50000),
            output_tokens: Some(800),
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            context_window_pct: None,
            cost_usd: None,
            total_duration_ms: None,
            rate_limits: None,
            stop_reason: None,
            turn_count: None,
            state_changed_at: None,
        });
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert_eq!(claude.status, "idle");
        assert!(claude.session_age_sec.is_some());
        assert!(
            claude.last_tool.is_none(),
            "lastTool must be absent after Stop"
        );
        assert_eq!(claude.input_tokens, Some(50000));
        assert_eq!(claude.output_tokens, Some(800));
    }

    /// AC9: legacy state files (no new fields) deserialize cleanly.
    #[test]
    fn legacy_state_file_deserializes_cleanly() {
        use crate::claude_state::ClaudeStateFile;
        let json = r#"{
            "state": "idle",
            "session_id": "abc",
            "tmux_session": "repo_47",
            "cwd": "/workspace",
            "event": "Stop",
            "timestamp": "2026-03-25T10:00:00Z"
        }"#;
        let sf: ClaudeStateFile = serde_json::from_str(json).unwrap();
        assert_eq!(sf.state, "idle");
        assert!(sf.model.is_none());
        assert!(sf.last_tool.is_none());
        assert!(sf.current_task.is_none());
        assert!(sf.session_start_ts.is_none());
        assert!(sf.input_tokens.is_none());
        // Derive the session info from a legacy state file
        let enrichment = crate::session::ClaudeSessionInfo::from_state_file(&sf);
        // idle state => Some(ClaudeSessionInfo) with all new fields None
        let info = enrichment.unwrap();
        assert!(matches!(
            info.status,
            crate::claude_state::ClaudeState::Idle
        ));
        assert!(info.model.is_none());
        assert!(info.last_tool.is_none());
        assert!(info.input_tokens.is_none());
    }

    /// `compute_session_age_sec` returns `None` when `session_start_ts` is ahead of now.
    ///
    /// Clock skew (NTP adjustment, VM resume, etc.) can cause the recorded start time
    /// to be slightly in the future relative to the current clock. `checked_sub` handles
    /// this cleanly — we must never return a wrapped negative value as a large u64.
    #[test]
    fn session_age_sec_returns_none_when_start_is_ahead_of_now() {
        let now_secs = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs();
        // Set start 1000 seconds in the future.
        let future_ts = now_secs + 1000;
        let result = compute_session_age_sec(Some(future_ts));
        assert!(
            result.is_none(),
            "session_age_sec must be None when start_ts is ahead of now (clock skew); got {result:?}"
        );
    }

    // -----------------------------------------------------------------------
    // state_elapsed_sec tests (Part 6)
    // -----------------------------------------------------------------------

    /// AC: state_elapsed_sec is computed from state_changed_at at read time.
    #[test]
    fn state_elapsed_sec_computed_from_state_changed_at() {
        let now_secs = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let state_changed_at = now_secs - 120; // 120 seconds ago
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Working,
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
            state_changed_at: Some(state_changed_at),
        });
        let js = JsonSession::from(&session);
        let elapsed = js.claude.unwrap().state_elapsed_sec.unwrap();
        assert!(
            elapsed >= 120,
            "state_elapsed_sec must be >= 120 when state changed 120s ago: got {elapsed}"
        );
    }

    /// AC: state_elapsed_sec absent when state_changed_at is None.
    #[test]
    fn state_elapsed_sec_absent_when_no_state_changed_at() {
        let session = make_enriched_session(ClaudeEnrichment {
            status: crate::claude_state::ClaudeState::Idle,
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
        });
        let js = JsonSession::from(&session);
        assert!(
            js.claude.unwrap().state_elapsed_sec.is_none(),
            "state_elapsed_sec must be absent when state_changed_at is None"
        );
    }

    // -----------------------------------------------------------------------
    // JsonPr split CI state tests (issue #218, slice 3, tasks #11-15)
    // -----------------------------------------------------------------------

    /// Helper: builds a minimal PrState with the given CI fields.
    #[allow(deprecated)]
    fn make_pr_state_with_ci(
        ci_code_state: Option<&str>,
        ci_gate_state: Option<&str>,
        checks_state: Option<&str>,
    ) -> PrState {
        use crate::ci_state::{CheckInfo, CiChecks};
        let ci_checks = if ci_gate_state == Some("blocked") {
            CiChecks {
                code: vec![CheckInfo {
                    name: "test-unit".to_string(),
                    state: "passing".to_string(),
                    details_url: None,
                }],
                gate: vec![CheckInfo {
                    name: "check-approval-or-label".to_string(),
                    state: "failing".to_string(),
                    details_url: None,
                }],
            }
        } else {
            CiChecks::default()
        };
        PrState {
            number: 1,
            branch: "feat/branch".to_string(),
            state: Some("OPEN".to_string()),
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            review_decision: None,
            checks_state: checks_state.map(|s| s.to_string()),
            ci_code_state: ci_code_state.map(|s| s.to_string()),
            ci_gate_state: ci_gate_state.map(|s| s.to_string()),
            ci_checks,
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

    /// Task #11: JsonPr struct declares ciCodeState, ciGateState, ciChecks, checksState with
    /// camelCase serialization. ciChecks has "code" and "gate" keys but NOT "ignored".
    #[test]
    fn json_pr_emits_split_ci_fields_with_camel_case() {
        let pr_state = make_pr_state_with_ci(Some("passing"), Some("blocked"), Some("passing"));
        let json_pr = JsonPr::from(&pr_state);
        let json = serde_json::to_string_pretty(&json_pr).unwrap();

        assert!(
            json.contains("\"ciCodeState\""),
            "expected ciCodeState key in: {}",
            json
        );
        assert!(
            json.contains("\"ciGateState\""),
            "expected ciGateState key in: {}",
            json
        );
        assert!(
            json.contains("\"ciChecks\""),
            "expected ciChecks key in: {}",
            json
        );
        assert!(
            json.contains("\"checksState\""),
            "expected checksState key in: {}",
            json
        );

        // ciChecks must have "code" and "gate" sub-keys
        let value: serde_json::Value = serde_json::from_str(&json).unwrap();
        let ci_checks = &value["ciChecks"];
        assert!(
            ci_checks.get("code").is_some(),
            "ciChecks must have 'code' key"
        );
        assert!(
            ci_checks.get("gate").is_some(),
            "ciChecks must have 'gate' key"
        );

        // Must NOT emit "ignored" (reserved for follow-up issue)
        assert!(
            ci_checks.get("ignored").is_none(),
            "ciChecks must NOT emit 'ignored' field in v1"
        );
    }

    /// Task #12: checksState is "passing" when ciCodeState=passing and ciGateState=cleared.
    #[test]
    fn json_pr_legacy_checks_state_passing_when_code_passing_gate_cleared() {
        let pr_state = make_pr_state_with_ci(Some("passing"), Some("cleared"), Some("passing"));
        let json_pr = JsonPr::from(&pr_state);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert_eq!(
            value["checksState"],
            serde_json::Value::String("passing".to_string())
        );
    }

    /// Task #13: checksState is "failing" when ciCodeState=failing.
    #[test]
    fn json_pr_legacy_checks_state_failing_when_code_failing() {
        let pr_state = make_pr_state_with_ci(Some("failing"), Some("cleared"), Some("failing"));
        let json_pr = JsonPr::from(&pr_state);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert_eq!(
            value["checksState"],
            serde_json::Value::String("failing".to_string())
        );
    }

    /// Task #14: checksState is "passing" (NOT "failing") when only the gate is blocked.
    /// This is the core regression the feature prevents: legacy consumers must not
    /// see a code-green gate-blocked PR as broken.
    #[test]
    fn json_pr_legacy_checks_state_not_failing_when_code_green_gate_blocked() {
        let pr_state = make_pr_state_with_ci(Some("passing"), Some("blocked"), Some("passing"));
        let json_pr = JsonPr::from(&pr_state);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert_eq!(
            value["checksState"],
            serde_json::Value::String("passing".to_string()),
            "checksState must be 'passing' (not 'failing') when only gate is blocked"
        );
        assert_ne!(
            value["checksState"],
            serde_json::Value::String("failing".to_string()),
            "checksState must NOT be 'failing' for code-green gate-blocked PR"
        );
    }

    /// Task #15: checksState is null when the PR has zero checks in either bucket.
    #[test]
    fn json_pr_legacy_checks_state_null_when_no_ci_checks() {
        let pr_state = make_pr_state_with_ci(None, None, None);
        let json_pr = JsonPr::from(&pr_state);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert_eq!(
            value["checksState"],
            serde_json::Value::Null,
            "checksState must be null when PR has no CI checks"
        );
    }

    // -----------------------------------------------------------------------
    // labels field tests (issue #235)
    // -----------------------------------------------------------------------

    #[test]
    fn json_issue_includes_labels() {
        let issue = make_issue_info(1, "labeled issue", "open", vec!["bug", "in-progress"]);
        let ji = JsonIssue::from(&issue);
        assert_eq!(ji.labels, vec!["bug", "in-progress"]);
        let v = serde_json::to_value(&ji).unwrap();
        assert_eq!(v["labels"], serde_json::json!(["bug", "in-progress"]));
    }

    #[test]
    fn json_issue_empty_labels() {
        let issue = make_issue_info(2, "unlabeled issue", "open", vec![]);
        let ji = JsonIssue::from(&issue);
        assert!(ji.labels.is_empty());
        let v = serde_json::to_value(&ji).unwrap();
        assert_eq!(v["labels"], serde_json::json!([]));
    }

    #[test]
    fn json_pr_includes_labels() {
        let pr = make_pr_state_with_labels(10, "feat/branch", vec!["enhancement", "pr-ready"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.labels, vec!["enhancement", "pr-ready"]);
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["labels"], serde_json::json!(["enhancement", "pr-ready"]));
    }

    #[test]
    fn json_pr_empty_labels() {
        let pr = make_pr_state_with_labels(11, "feat/unlabeled", vec![]);
        let jp = JsonPr::from(&pr);
        assert!(jp.labels.is_empty());
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["labels"], serde_json::json!([]));
    }

    #[test]
    fn json_pr_labels_preserve_order() {
        let pr =
            make_pr_state_with_labels(12, "feat/ordered", vec!["z-label", "a-label", "m-label"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.labels, vec!["z-label", "a-label", "m-label"]);
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(
            v["labels"],
            serde_json::json!(["z-label", "a-label", "m-label"])
        );
    }

    // -----------------------------------------------------------------------
    // Task #8: per-pane cwd/is_active/claude_session_id and per-window layout
    // -----------------------------------------------------------------------

    /// Builds a `PaneState` with all new restore-time metadata fields set.
    fn make_pane_state_with_metadata(
        cwd: &str,
        is_active: bool,
        claude_session_id: Option<&str>,
    ) -> PaneState {
        PaneState {
            index: 0,
            tmux_target: "0.0".to_string(),
            command: "bash".to_string(),
            title: "bash".to_string(),
            has_claude: false,
            cwd: cwd.to_string(),
            is_active,
            claude_session_id: claude_session_id.map(|s| s.to_string()),
        }
    }

    /// Builds a `SessionState` wrapping a single window containing the given pane.
    fn make_session_with_pane(pane: PaneState) -> SessionState {
        SessionState {
            name: "restore-test".to_string(),
            host: None,
            claude: None,
            started_at: None,
            last_activity_at: None,
            windows: vec![WindowState {
                index: 0,
                name: "main".to_string(),
                is_active: true,
                layout: "abc1,80x24,0,0".to_string(),
                panes: vec![pane],
            }],
        }
    }

    /// Task #8 test 1: pane DTO includes cwd, is_active, and claude_session_id.
    #[test]
    fn json_pane_includes_cwd_is_active_claude_session_id() {
        let pane = make_pane_state_with_metadata("/tmp/foo", true, Some("sess-1"));
        let session = make_session_with_pane(pane);
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let p = &value["windows"][0]["panes"][0];
        assert_eq!(p["cwd"], "/tmp/foo", "cwd must be present and match");
        assert_eq!(p["isActive"], true, "isActive must be present and true");
        assert_eq!(
            p["claudeSessionId"], "sess-1",
            "claudeSessionId must be present and match"
        );
    }

    /// Task #8 test 2: claude_session_id is omitted from JSON when None.
    #[test]
    fn json_pane_omits_claude_session_id_when_none() {
        let pane = make_pane_state_with_metadata("/tmp/bar", false, None);
        let session = make_session_with_pane(pane);
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let p = &value["windows"][0]["panes"][0];
        assert!(
            p.get("claudeSessionId").is_none(),
            "claudeSessionId must be absent when None (skip_serializing_if)"
        );
    }

    /// Task #8 test 3: window DTO includes layout.
    #[test]
    fn json_window_includes_layout() {
        let pane = make_pane_state_with_metadata("/tmp/baz", false, None);
        let session = make_session_with_pane(pane);
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let win = &value["windows"][0];
        assert_eq!(
            win["layout"], "abc1,80x24,0,0",
            "layout must be present and match"
        );
    }

    #[test]
    fn labels_field_does_not_change_phase() {
        // "blocked" is higher priority than "in-progress"; phase must be "blocked"
        // AND labels must contain both values.
        let pr = make_pr_state_with_labels(13, "feat/both", vec!["blocked", "in-progress"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some("blocked"));
        assert_eq!(jp.labels, vec!["blocked", "in-progress"]);
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], "blocked");
        assert_eq!(v["labels"], serde_json::json!(["blocked", "in-progress"]));
    }

    // -----------------------------------------------------------------------
    // last_activity_at field tests (issue #240)
    // -----------------------------------------------------------------------

    /// Builds a minimal PrState with the given `last_commit_pushed_at` value.
    #[allow(deprecated)]
    fn make_pr_with_last_commit_pushed_at(pushed_at: Option<&str>) -> PrState {
        use crate::ci_state::CiChecks;
        PrState {
            number: 99,
            branch: "feat/activity".to_string(),
            state: Some("OPEN".to_string()),
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            review_decision: None,
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: CiChecks::default(),
            has_conflicts: false,
            unresolved_threads: 0,
            labels: vec![],
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: pushed_at.map(|s| s.to_string()),
        }
    }

    /// `last_activity_at` is taken from `pr.last_commit_pushed_at` when set.
    #[test]
    fn json_worktree_last_activity_at_from_pr() {
        let ts = "2024-06-01T12:00:00Z";
        let mut wt = make_worktree(DisplayGroup::Other);
        wt.pr = Some(make_pr_with_last_commit_pushed_at(Some(ts)));
        wt.last_commit_at = Some("2024-01-01T00:00:00Z".to_string());
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.last_activity_at.as_deref(), Some(ts));
    }

    /// `last_activity_at` is `None` when there is no PR and no `last_commit_at`.
    #[test]
    fn json_worktree_last_activity_at_null_when_no_timestamps() {
        let wt = make_worktree(DisplayGroup::Other);
        let jw = JsonWorktree::from(&wt);
        assert!(jw.last_activity_at.is_none());
    }

    /// `last_activity_at` falls back to `last_commit_at` when no PR is present.
    #[test]
    fn json_worktree_last_activity_at_from_worktree_commit() {
        let ts = "2024-03-15T08:30:00Z";
        let mut wt = make_worktree(DisplayGroup::Other);
        wt.last_commit_at = Some(ts.to_string());
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.last_activity_at.as_deref(), Some(ts));
    }
}
