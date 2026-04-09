//! Versioned JSON output mapping for the `--json` flag.
//!
//! Decouples the public JSON API from internal `OrchardState`, allowing internal refactors
//! without breaking scripts. All output is camelCase, version-numbered, and backed by tests.
//! Consumed directly by external tools and scripts that call `orchard --json`.

use std::collections::HashMap;

use serde::Serialize;

use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;
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
/// Subset of GitHub issue data: number, title, and state (open/closed).
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonIssue {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state: "open", "closed", or "completed".
    pub state: String,
}

/// Pull request information in JSON output.
///
/// Includes PR metadata: number, branch, state, review decision, CI checks, conflicts, and unresolved review threads.
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
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonClaudeInfo {
    /// Claude state as a string: "working", "idle", "input", or "none".
    pub status: String,
    /// Cumulative session cost in USD, if available.
    pub cost_usd: Option<f64>,
    /// Context window usage percentage, if available.
    pub context_window_pct: Option<f64>,
    /// Model name (e.g., "opus", "sonnet"), if available.
    pub model: Option<String>,
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
        let claude = s.claude.as_ref().map(|c| JsonClaudeInfo {
            status: claude_state_str(c.status).to_string(),
            cost_usd: c.cost_usd,
            context_window_pct: c.context_window_pct,
            model: c.model.clone(),
        });
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
        let claude = row.session.claude.as_ref().map(|c| JsonClaudeInfo {
            status: claude_state_str(c.status).to_string(),
            cost_usd: c.cost_usd,
            context_window_pct: c.context_window_pct,
            model: c.model.clone(),
        });
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
                cost_usd: None,
                context_window_pct: None,
                model: None,
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
}
