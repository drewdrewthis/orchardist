//! Versioned JSON output mapping for the `--json` flag.
//!
//! Decouples the public JSON API from internal `OrchardState`, allowing internal refactors
//! without breaking scripts. All output is camelCase, version-numbered, and backed by tests.
//! Consumed directly by external tools and scripts that call `orchard --json`.

use std::collections::HashMap;

use serde::Serialize;

use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;
use crate::orchard_state::{IssueInfo, OrchardState, PrState, RepoState, SessionState, WorktreeState};

// ---------------------------------------------------------------------------
// JSON output types (versioned, camelCase)
// ---------------------------------------------------------------------------

/// Top-level versioned JSON output for `orchard --json`.
///
/// Contains a version number (for forward compatibility) and collections of repos and hosts.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonOutput {
    pub version: u32,
    pub repos: Vec<JsonRepo>,
    pub hosts: HashMap<String, JsonHostState>,
}

/// A single repository in JSON output.
///
/// Contains the repo slug and all worktrees within it.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonRepo {
    pub slug: String,
    pub worktrees: Vec<JsonWorktree>,
}

/// A single worktree in JSON output.
///
/// Represents a git worktree with its path, branch, host, linked issue/PR, active sessions, and display group.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonWorktree {
    pub path: String,
    pub branch: String,
    pub host: Option<String>,
    pub issue: Option<JsonIssue>,
    pub pr: Option<JsonPr>,
    pub sessions: Vec<JsonSession>,
    pub display_group: String,
    pub is_shepherd: bool,
}

/// Issue information in JSON output.
///
/// Subset of GitHub issue data: number, title, and state (open/closed).
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonIssue {
    pub number: u32,
    pub title: String,
    pub state: String,
}

/// Pull request information in JSON output.
///
/// Includes PR metadata: number, branch, state, review decision, CI checks, conflicts, and unresolved review threads.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonPr {
    pub number: u32,
    pub branch: String,
    pub state: Option<String>,
    pub review_decision: Option<String>,
    pub checks_state: Option<String>,
    pub has_conflicts: bool,
    pub unresolved_threads: u32,
}

/// Claude session information in JSON output.
///
/// Represents an active Claude Code session: name, host, state (working/idle/input/none),
/// and optional context window and cost metrics.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonSession {
    pub name: String,
    pub host: Option<String>,
    pub claude_state: String,
    pub context_window_pct: Option<f64>,
    pub cost_usd: Option<f64>,
    pub model: Option<String>,
}

/// Host reachability information in JSON output.
///
/// Simple boolean indicating whether an SSH host is reachable.
#[derive(Serialize)]
pub struct JsonHostState {
    pub reachable: bool,
}

// ---------------------------------------------------------------------------
// Serialization helpers
// ---------------------------------------------------------------------------

fn display_group_str(g: DisplayGroup) -> &'static str {
    match g {
        DisplayGroup::Shepherd => "shepherd",
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

impl From<&SessionState> for JsonSession {
    /// Converts an internal `SessionState` to JSON output format, serializing Claude state to a string.
    fn from(s: &SessionState) -> Self {
        Self {
            name: s.name.clone(),
            host: s.host.clone(),
            claude_state: claude_state_str(s.claude_state).to_string(),
            context_window_pct: s.context_window_pct,
            cost_usd: s.cost_usd,
            model: s.model.clone(),
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
            is_shepherd: ws.is_shepherd,
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

impl From<&OrchardState> for JsonOutput {
    /// Converts the unified `OrchardState` to JSON output, setting version to 2.
    fn from(state: &OrchardState) -> Self {
        let hosts = state
            .hosts
            .iter()
            .map(|(host, h)| (host.clone(), JsonHostState { reachable: h.reachable }))
            .collect();

        Self {
            version: 2,
            repos: state.repos.iter().map(Into::into).collect(),
            hosts,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::claude_state::ClaudeState;
    use crate::derive::DisplayGroup;
    use crate::orchard_state::{RepoState, SessionState, WorktreeState};

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
            is_shepherd: false,
        }
    }

    #[test]
    fn from_orchard_state_produces_version_2() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(output.version, 2);
    }

    #[test]
    fn from_orchard_state_empty_repos_and_hosts() {
        let output = JsonOutput::from(&empty_state());
        assert!(output.repos.is_empty());
        assert!(output.hosts.is_empty());
    }

    #[test]
    fn display_group_shepherd_serializes_to_snake_case() {
        let wt = make_worktree(DisplayGroup::Shepherd);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.display_group, "shepherd");
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
            repos: vec![RepoState { slug: "owner/repo".to_string(), worktrees: vec![] }],
            hosts: HashMap::new(),
        };
        let output = JsonOutput::from(&state);
        let value = serde_json::to_value(&output).unwrap();
        let repo = &value["repos"][0];
        assert!(repo.get("slug").is_some(), "expected 'slug' key in repo");
        assert!(repo.get("worktrees").is_some(), "expected 'worktrees' key in repo");
    }

    #[test]
    fn json_worktree_has_camelcase_is_shepherd_field() {
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![make_worktree(DisplayGroup::Shepherd)],
            }],
            hosts: HashMap::new(),
        };
        let output = JsonOutput::from(&state);
        let value = serde_json::to_value(&output).unwrap();
        let wt = &value["repos"][0]["worktrees"][0];
        assert!(wt.get("isShepherd").is_some(), "expected camelCase 'isShepherd' key");
        assert!(wt.get("displayGroup").is_some(), "expected camelCase 'displayGroup' key");
    }

    #[test]
    fn json_session_claude_state_serializes_as_string() {
        let session = SessionState {
            name: "repo-claude".to_string(),
            host: None,
            has_claude_active: true,
            claude_is_working: true,
            claude_needs_input: false,
            claude_state: ClaudeState::Working,
            context_window_pct: None,
            cost_usd: None,
            model: None,
        };
        let js = JsonSession::from(&session);
        assert_eq!(js.claude_state, "working");
    }
}
