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
    /// All worktrees belonging to this repository.
    pub worktrees: Vec<JsonWorktree>,
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
    /// Workflow phase derived from labels (e.g. `"in-progress"`, `"blocked"`).
    /// Always present: `null` when no phase label is set.
    pub phase: Option<&'static str>,
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

/// Constructs a `JsonClaudeInfo` from a `ClaudeEnrichment`.
///
/// `session_age_sec` is computed at call time so it always reflects real elapsed
/// time rather than the time of the last hook write.
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
            phase: phase_from_labels(&i.labels),
        }
    }
}

impl From<&PrState> for JsonPr {
    /// Converts an internal `PrState` to JSON output format.
    fn from(pr: &PrState) -> Self {
        Self {
            number: pr.number,
            branch: pr.branch.clone(),
            state: pr.state.clone(),
            review_decision: pr.review_decision.clone(),
            checks_state: pr.checks_state.clone(),
            has_conflicts: pr.has_conflicts,
            unresolved_threads: pr.unresolved_threads,
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
            panes: w
                .panes
                .iter()
                .map(|p| JsonPane {
                    index: p.index,
                    tmux_target: p.tmux_target.clone(),
                    command: p.command.clone(),
                    title: p.title.clone(),
                    has_claude: p.has_claude,
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
            claude,
            windows: s.windows.iter().map(Into::into).collect(),
        }
    }
}

impl From<&WorktreeState> for JsonWorktree {
    /// Converts an internal `WorktreeState` to JSON output format, serializing the display group to a string.
    fn from(ws: &WorktreeState) -> Self {
        Self {
            path: ws.path.clone(),
            branch: ws.branch.clone(),
            host: ws.host.clone(),
            issue: ws.issue.as_ref().map(Into::into),
            pr: ws.pr.as_ref().map(Into::into),
            sessions: ws.sessions.iter().map(Into::into).collect(),
            display_group: display_group_str(ws.display_group).to_string(),
            is_main_worktree: ws.is_main_worktree,
        }
    }
}

impl From<&RepoState> for JsonRepo {
    /// Converts an internal `RepoState` to JSON output format.
    fn from(r: &RepoState) -> Self {
        Self {
            slug: r.slug.clone(),
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
        let claude = row.session.claude.as_ref().map(claude_info_from_enrichment);
        let windows = row
            .session
            .windows
            .iter()
            .map(|w| JsonWindow {
                index: w.index,
                name: w.name.clone(),
                is_active: w.is_active,
                panes: w
                    .panes
                    .iter()
                    .map(|p| JsonPane {
                        index: p.index,
                        tmux_target: p.tmux_target.clone(),
                        command: p.command.clone(),
                        title: p.title.clone(),
                        has_claude: p.has_claude,
                    })
                    .collect(),
            })
            .collect();
        Self {
            name: row.session.tmux.name.clone(),
            host,
            status: status.to_string(),
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
            }),
            windows: vec![],
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
            windows: vec![
                WindowState {
                    index: 0,
                    name: "main".to_string(),
                    is_active: true,
                    panes: vec![PaneState {
                        index: 0,
                        tmux_target: "0.0".to_string(),
                        command: "bash".to_string(),
                        title: "bash".to_string(),
                        has_claude: false,
                    }],
                },
                WindowState {
                    index: 1,
                    name: "editor".to_string(),
                    is_active: false,
                    panes: vec![PaneState {
                        index: 1,
                        tmux_target: "1.0".to_string(),
                        command: "claude".to_string(),
                        title: "claude".to_string(),
                        has_claude: true,
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
            windows: vec![WindowState {
                index: 0,
                name: "main".to_string(),
                is_active: true,
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
        }
    }

    fn make_pr_state(number: u32, branch: &str, labels: Vec<&str>) -> PrState {
        PrState {
            number,
            branch: branch.to_string(),
            state: Some("open".to_string()),
            review_decision: None,
            checks_state: None,
            has_conflicts: false,
            unresolved_threads: 0,
            labels: labels.into_iter().map(|s| s.to_string()).collect(),
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
        let pr = make_pr_state(10, "feat/branch", vec!["enhancement"]);
        let jp = JsonPr::from(&pr);
        assert!(jp.phase.is_none());
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], serde_json::Value::Null);
    }

    #[test]
    fn json_pr_phase_serializes_matched_label() {
        let pr = make_pr_state(10, "feat/branch", vec!["pr-ready"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some("pr-ready"));
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], "pr-ready");
    }

    #[test]
    fn json_pr_phase_resolves_multi_label_by_priority() {
        let pr = make_pr_state(10, "feat/branch", vec!["in-progress", "blocked"]);
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
        let pr = make_pr_state(11, "feat/empty", vec![]);
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
        let pr = make_pr_state(220, "issue219/phase-field", vec!["in-ai-review"]);
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
        assert!(claude.session_age_sec.is_some(), "sessionAgeSec must be present");
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
        });
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let claude = &value["claude"];
        assert_eq!(claude["status"], "idle");
        // Optional fields must be absent (not null) when not set.
        assert!(claude.get("model").is_none(), "model must be absent");
        assert!(claude.get("lastTool").is_none(), "lastTool must be absent");
        assert!(claude.get("currentTask").is_none(), "currentTask must be absent");
        assert!(claude.get("sessionAgeSec").is_none(), "sessionAgeSec must be absent");
        assert!(claude.get("inputTokens").is_none(), "inputTokens must be absent");
        assert!(claude.get("outputTokens").is_none(), "outputTokens must be absent");
        assert!(claude.get("cacheCreationInputTokens").is_none(), "cacheCreationInputTokens must be absent");
        assert!(claude.get("cacheReadInputTokens").is_none(), "cacheReadInputTokens must be absent");
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
        });
        let js = JsonSession::from(&session);
        let age = js.claude.unwrap().session_age_sec.unwrap();
        // The session is very old — age must be very large (> 1 billion seconds since 1970).
        assert!(age > 1_000_000_000, "session_age_sec must reflect elapsed time: got {age}");
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
        });
        let js = JsonSession::from(&session);
        let age = js.claude.unwrap().session_age_sec.unwrap();
        assert!(age >= 60, "session_age_sec must be >= 60 when session started 60s ago: got {age}");
    }

    /// AC6: sessions without a Claude process omit the claude object entirely.
    #[test]
    fn no_claude_process_omits_claude_key() {
        let session = SessionState {
            name: "zsh-session".to_string(),
            host: None,
            claude: None,
            windows: vec![],
        };
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        assert!(value.get("claude").is_none() || value["claude"].is_null(),
            "sessions without Claude must not have claude key");
        let js = JsonSession::from(&session);
        assert!(js.claude.is_none(), "claude must be None for non-Claude sessions");
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
        });
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert!(claude.model.is_some(), "model must be present");
        assert!(claude.session_age_sec.is_some(), "sessionAgeSec must be present");
        assert!(claude.last_tool.is_none(), "lastTool must be absent for fresh session");
        assert!(claude.current_task.is_none(), "currentTask must be absent for fresh session");
        assert!(claude.input_tokens.is_none(), "inputTokens must be absent for fresh session");
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
        });
        let js = JsonSession::from(&session);
        let claude = js.claude.unwrap();
        assert_eq!(claude.status, "idle");
        assert!(claude.session_age_sec.is_some());
        assert!(claude.last_tool.is_none(), "lastTool must be absent after Stop");
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
        assert!(matches!(info.status, crate::claude_state::ClaudeState::Idle));
        assert!(info.model.is_none());
        assert!(info.last_tool.is_none());
        assert!(info.input_tokens.is_none());
    }
}
