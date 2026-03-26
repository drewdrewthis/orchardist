use std::collections::HashMap;

use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;

// ---------------------------------------------------------------------------
// Top-level state
// ---------------------------------------------------------------------------

/// The unified state model for Orchard. Contains all repos and host reachability.
#[derive(Debug, Clone)]
pub struct OrchardState {
    pub repos: Vec<RepoState>,
    pub hosts: HashMap<String, HostState>,
}

impl OrchardState {
    /// Creates an empty OrchardState.
    pub fn new() -> Self {
        Self {
            repos: Vec::new(),
            hosts: HashMap::new(),
        }
    }

    /// Returns a flat list of all worktrees across all repos, sorted by
    /// display_group then by issue number (worktrees without issues sort last
    /// within their group, then by branch name).
    pub fn all_worktrees(&self) -> Vec<&WorktreeState> {
        let mut all: Vec<&WorktreeState> =
            self.repos.iter().flat_map(|r| r.worktrees.iter()).collect();

        all.sort_by(|a, b| {
            a.display_group.cmp(&b.display_group).then_with(|| {
                let a_num = a.issue.as_ref().map(|i| i.number);
                let b_num = b.issue.as_ref().map(|i| i.number);
                match (a_num, b_num) {
                    (Some(an), Some(bn)) => an.cmp(&bn),
                    (Some(_), None) => std::cmp::Ordering::Less,
                    (None, Some(_)) => std::cmp::Ordering::Greater,
                    (None, None) => a.branch.cmp(&b.branch),
                }
            })
        });

        all
    }
}

impl Default for OrchardState {
    fn default() -> Self {
        Self::new()
    }
}

// ---------------------------------------------------------------------------
// Repo / worktree
// ---------------------------------------------------------------------------

/// State for a single repository.
#[derive(Debug, Clone)]
pub struct RepoState {
    pub slug: String,
    pub worktrees: Vec<WorktreeState>,
}

/// State for a single worktree, enriched with issue/PR/session metadata.
#[derive(Debug, Clone)]
pub struct WorktreeState {
    pub path: String,
    pub branch: String,
    pub is_bare: bool,
    pub host: Option<String>,
    pub issue: Option<IssueInfo>,
    pub pr: Option<PrState>,
    pub sessions: Vec<SessionState>,
    pub display_group: DisplayGroup,
    pub is_shepherd: bool,
}

/// Lightweight issue summary attached to a worktree.
#[derive(Debug, Clone)]
pub struct IssueInfo {
    pub number: u32,
    pub title: String,
    pub state: String,
}

/// Lightweight PR summary attached to a worktree.
#[derive(Debug, Clone)]
pub struct PrState {
    pub number: u32,
    pub branch: String,
    pub state: Option<String>,
    pub review_decision: Option<String>,
    pub checks_state: Option<String>,
    pub has_conflicts: bool,
    pub unresolved_threads: u32,
}

/// Lightweight tmux session summary attached to a worktree.
#[derive(Debug, Clone)]
pub struct SessionState {
    pub name: String,
    pub host: Option<String>,
    pub has_claude_active: bool,
    pub claude_is_working: bool,
    pub claude_needs_input: bool,
    pub claude_state: ClaudeState,
    pub context_window_pct: Option<f64>,
    pub cost_usd: Option<f64>,
    pub model: Option<String>,
}

/// Reachability state for a remote host.
#[derive(Debug, Clone)]
pub struct HostState {
    pub reachable: bool,
}

// ---------------------------------------------------------------------------
// From conversions from derive types
// ---------------------------------------------------------------------------

impl From<&crate::derive::PrInfo> for PrState {
    fn from(pr: &crate::derive::PrInfo) -> Self {
        Self {
            number: pr.number,
            branch: pr.branch.clone(),
            // derive::PrInfo does not carry the PR state string; default to None.
            state: None,
            review_decision: pr.review_decision.clone(),
            checks_state: pr.checks_state.clone(),
            has_conflicts: pr.has_conflicts,
            unresolved_threads: pr.unresolved_threads,
        }
    }
}

impl From<&crate::derive::SessionInfo> for SessionState {
    fn from(s: &crate::derive::SessionInfo) -> Self {
        Self {
            name: s.name.clone(),
            host: s.host.clone(),
            has_claude_active: s.has_claude_active,
            claude_is_working: s.claude_is_working,
            claude_needs_input: s.claude_needs_input,
            claude_state: s.claude_state,
            context_window_pct: s.context_window_pct,
            cost_usd: s.cost_usd,
            model: s.model.clone(),
        }
    }
}

impl From<&crate::derive::WorktreeRow> for WorktreeState {
    fn from(row: &crate::derive::WorktreeRow) -> Self {
        let issue = row.issue_number.map(|num| IssueInfo {
            number: num,
            title: row.issue_title.clone().unwrap_or_default(),
            // issue state is not stored in WorktreeRow; default to "open"
            state: "open".to_string(),
        });

        Self {
            path: row.worktree_path.clone(),
            branch: row.branch.clone(),
            is_bare: false, // derive only produces non-bare rows
            host: row.worktree_host.clone(),
            issue,
            pr: row.pr.as_ref().map(Into::into),
            sessions: row.sessions.iter().map(Into::into).collect(),
            display_group: row.display_group,
            is_shepherd: row.is_shepherd,
        }
    }
}
