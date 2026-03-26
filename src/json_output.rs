use std::collections::HashMap;

use serde::Serialize;

use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;
use crate::orchard_state::{IssueInfo, OrchardState, PrState, RepoState, SessionState, WorktreeState};

// ---------------------------------------------------------------------------
// JSON output types (versioned, camelCase)
// ---------------------------------------------------------------------------

/// Top-level versioned JSON output for `orchard --json`.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonOutput {
    pub version: u32,
    pub repos: Vec<JsonRepo>,
    pub hosts: HashMap<String, JsonHostState>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonRepo {
    pub slug: String,
    pub worktrees: Vec<JsonWorktree>,
}

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

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct JsonIssue {
    pub number: u32,
    pub title: String,
    pub state: String,
}

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
    fn from(i: &IssueInfo) -> Self {
        Self {
            number: i.number,
            title: i.title.clone(),
            state: i.state.clone(),
        }
    }
}

impl From<&PrState> for JsonPr {
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
    fn from(r: &RepoState) -> Self {
        Self {
            slug: r.slug.clone(),
            worktrees: r.worktrees.iter().map(Into::into).collect(),
        }
    }
}

impl From<&OrchardState> for JsonOutput {
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
