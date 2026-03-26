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
            state: pr.state.clone(),
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
            state: row.issue_state.clone().unwrap_or_else(|| "open".to_string()),
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};

    fn make_row(repo_slug: &str, branch: &str, issue_number: Option<u32>, issue_state: Option<&str>, display_group: DisplayGroup) -> WorktreeRow {
        WorktreeRow {
            repo_slug: repo_slug.to_string(),
            worktree_path: format!("/repos/{}/{}", repo_slug, branch),
            branch: branch.to_string(),
            worktree_host: None,
            issue_number,
            issue_title: issue_number.map(|n| format!("Issue {}", n)),
            issue_state: issue_state.map(|s| s.to_string()),
            pr: None,
            sessions: vec![],
            display_group,
            is_shepherd: false,
        }
    }

    fn make_state_with_rows(rows: Vec<WorktreeRow>) -> OrchardState {
        let mut repo_map: std::collections::HashMap<String, Vec<WorktreeState>> = std::collections::HashMap::new();
        for row in &rows {
            repo_map.entry(row.repo_slug.clone()).or_default().push(WorktreeState::from(row));
        }
        let repos = repo_map.into_iter().map(|(slug, worktrees)| RepoState { slug, worktrees }).collect();
        OrchardState { repos, hosts: std::collections::HashMap::new() }
    }

    #[test]
    fn all_worktrees_returns_sorted_by_display_group_then_issue_number() {
        let rows = vec![
            make_row("owner/repo", "feat/issue-5", Some(5), None, DisplayGroup::Other),
            make_row("owner/repo", "feat/issue-2", Some(2), None, DisplayGroup::NeedsAttention),
            make_row("owner/repo", "feat/issue-1", Some(1), None, DisplayGroup::NeedsAttention),
        ];
        let state = make_state_with_rows(rows);
        let all = state.all_worktrees();
        assert_eq!(all.len(), 3);
        // NeedsAttention sorts before Other
        assert_eq!(all[0].display_group, DisplayGroup::NeedsAttention);
        assert_eq!(all[1].display_group, DisplayGroup::NeedsAttention);
        assert_eq!(all[2].display_group, DisplayGroup::Other);
        // Within NeedsAttention, issue 1 sorts before issue 2
        assert_eq!(all[0].issue.as_ref().unwrap().number, 1);
        assert_eq!(all[1].issue.as_ref().unwrap().number, 2);
    }

    #[test]
    fn all_worktrees_from_multiple_repos_are_included() {
        let rows = vec![
            make_row("owner/repo-a", "main", None, None, DisplayGroup::Shepherd),
            make_row("owner/repo-b", "main", None, None, DisplayGroup::Shepherd),
        ];
        let state = make_state_with_rows(rows);
        assert_eq!(state.all_worktrees().len(), 2);
    }

    #[test]
    fn from_worktree_row_maps_issue_state_open() {
        let row = make_row("owner/repo", "feat/issue-10", Some(10), Some("open"), DisplayGroup::Other);
        let ws = WorktreeState::from(&row);
        assert_eq!(ws.issue.unwrap().state, "open");
    }

    #[test]
    fn from_worktree_row_maps_issue_state_closed() {
        let row = make_row("owner/repo", "feat/issue-10", Some(10), Some("closed"), DisplayGroup::Other);
        let ws = WorktreeState::from(&row);
        assert_eq!(ws.issue.unwrap().state, "closed");
    }

    #[test]
    fn from_worktree_row_defaults_issue_state_to_open_when_none() {
        let row = make_row("owner/repo", "feat/issue-10", Some(10), None, DisplayGroup::Other);
        let ws = WorktreeState::from(&row);
        assert_eq!(ws.issue.unwrap().state, "open");
    }

    #[test]
    fn from_pr_info_maps_state_field() {
        let pr_info = PrInfo {
            number: 42,
            branch: "feat/branch".to_string(),
            state: Some("open".to_string()),
            review_decision: None,
            checks_state: None,
            has_conflicts: false,
            unresolved_threads: 0,
        };
        let pr_state = PrState::from(&pr_info);
        assert_eq!(pr_state.state, Some("open".to_string()));
    }

    #[test]
    fn from_pr_info_state_none_when_not_set() {
        let pr_info = PrInfo {
            number: 42,
            branch: "feat/branch".to_string(),
            state: None,
            review_decision: None,
            checks_state: None,
            has_conflicts: false,
            unresolved_threads: 0,
        };
        let pr_state = PrState::from(&pr_info);
        assert!(pr_state.state.is_none());
    }
}
