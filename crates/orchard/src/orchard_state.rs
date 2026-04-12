//! Unified data model for Orchard state.
//!
//! `OrchardState` is the single source of truth consumed by both the TUI and `--json` output.
//! It contains all repos, worktrees, sessions, and host reachability data.
//! Built by `build_state()` from multiple per-source caches; see `docs/architecture.md` for the full data flow.

use std::collections::HashMap;

use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;
use crate::session::{EnrichedSession, Host, StandaloneSessionRow};

// ---------------------------------------------------------------------------
// Top-level state
// ---------------------------------------------------------------------------

/// The unified state model for Orchard. Contains all repos, standalone sessions, and host reachability.
#[derive(Debug, Clone)]
pub struct OrchardState {
    /// All repositories known to Orchard.
    pub repos: Vec<RepoState>,
    /// Standalone tmux sessions not tied to any worktree.
    pub standalone_sessions: Vec<StandaloneSessionRow>,
    /// Reachability state keyed by SSH host name.
    pub hosts: HashMap<String, HostState>,
}

impl OrchardState {
    /// Creates an empty OrchardState.
    pub fn new() -> Self {
        Self {
            repos: Vec::new(),
            standalone_sessions: Vec::new(),
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
    /// Repository slug in `owner/repo` format.
    pub slug: String,
    /// All worktrees belonging to this repository.
    pub worktrees: Vec<WorktreeState>,
}

/// State for a single worktree, enriched with issue/PR/session metadata.
#[derive(Debug, Clone)]
pub struct WorktreeState {
    /// Absolute path to the worktree on disk.
    pub path: String,
    /// Git branch checked out in this worktree.
    pub branch: String,
    /// True when this is a bare worktree (no working tree).
    pub is_bare: bool,
    /// Remote SSH host this worktree lives on, or `None` for local.
    pub host: Option<String>,
    /// Linked GitHub issue, if any.
    pub issue: Option<IssueInfo>,
    /// Linked pull request, if any.
    pub pr: Option<PrState>,
    /// Active tmux sessions associated with this worktree.
    pub sessions: Vec<SessionState>,
    /// Display group controlling sort order and TUI section.
    pub display_group: DisplayGroup,
    /// True when this is the repo's main worktree.
    pub is_main_worktree: bool,
}

/// Lightweight issue summary attached to a worktree.
#[derive(Debug, Clone)]
pub struct IssueInfo {
    /// GitHub issue number.
    pub number: u32,
    /// Issue title.
    pub title: String,
    /// Issue state: "open", "closed", or "completed".
    pub state: String,
    /// Labels applied to the issue.
    pub labels: Vec<String>,
}

/// Lightweight PR summary attached to a worktree.
#[derive(Debug, Clone)]
pub struct PrState {
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
    /// Labels applied to the PR.
    pub labels: Vec<String>,
}

/// Lightweight tmux session summary attached to a worktree.
///
/// Mirrors the `EnrichedSession` domain type from `session.rs`.
/// The `claude` field is `None` when no Claude process is detected.
/// The `windows` field contains the full session → window → pane hierarchy.
#[derive(Debug, Clone)]
pub struct SessionState {
    /// tmux session name.
    pub name: String,
    /// Remote SSH host this session runs on, or `None` for local.
    pub host: Option<String>,
    /// Claude enrichment data, if a Claude process is active.
    pub claude: Option<ClaudeEnrichment>,
    /// Window hierarchy for this session (window → pane structure).
    pub windows: Vec<WindowState>,
}

/// Window within a session, with nested panes.
///
/// Mirrors `WindowInfo` from `session.rs` for the state layer.
#[derive(Debug, Clone)]
pub struct WindowState {
    /// Tmux's stable window index.
    pub index: usize,
    /// Window name from tmux.
    pub name: String,
    /// Whether this is the active window in the session.
    pub is_active: bool,
    /// Panes belonging to this window.
    pub panes: Vec<PaneState>,
}

/// Individual pane within a window.
///
/// Mirrors `PaneInfo` from `session.rs` for the state layer.
#[derive(Debug, Clone)]
pub struct PaneState {
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

/// Claude enrichment data within a `SessionState`.
///
/// Mirrors `ClaudeSessionInfo` from `session.rs` for the state layer.
#[derive(Debug, Clone)]
pub struct ClaudeEnrichment {
    /// Structured Claude state (working, idle, input, none).
    pub status: ClaudeState,
    /// Model name (e.g., `"claude-opus-4-6"`), if available.
    pub model: Option<String>,
    /// Last tool invoked, if available.
    pub last_tool: Option<String>,
    /// First line of the last user prompt (≤80 chars), if available.
    pub current_task: Option<String>,
    /// Unix epoch seconds when the session started, if available.
    pub session_start_ts: Option<u64>,
    /// Total input tokens from the most recent assistant message.
    pub input_tokens: Option<u64>,
    /// Total output tokens from the most recent assistant message.
    pub output_tokens: Option<u64>,
    /// Cache creation input tokens from the most recent assistant message.
    pub cache_creation_input_tokens: Option<u64>,
    /// Cache read input tokens from the most recent assistant message.
    pub cache_read_input_tokens: Option<u64>,
}

/// Reachability state for a remote host.
#[derive(Debug, Clone)]
pub struct HostState {
    /// True when the SSH host responded to the last reachability check.
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
            labels: pr.labels.clone(),
        }
    }
}

impl From<&EnrichedSession> for SessionState {
    fn from(s: &EnrichedSession) -> Self {
        let host = match &s.tmux.host {
            Host::Local => None,
            Host::Remote(h) => Some(h.clone()),
        };
        let claude = s.claude.as_ref().map(|c| ClaudeEnrichment {
            status: c.status,
            model: c.model.clone(),
            last_tool: c.last_tool.clone(),
            current_task: c.current_task.clone(),
            session_start_ts: c.session_start_ts,
            input_tokens: c.input_tokens,
            output_tokens: c.output_tokens,
            cache_creation_input_tokens: c.cache_creation_input_tokens,
            cache_read_input_tokens: c.cache_read_input_tokens,
        });
        let windows = s
            .windows
            .iter()
            .map(|w| WindowState {
                index: w.index,
                name: w.name.clone(),
                is_active: w.is_active,
                panes: w
                    .panes
                    .iter()
                    .map(|p| PaneState {
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
            name: s.tmux.name.clone(),
            host,
            claude,
            windows,
        }
    }
}

impl From<&crate::derive::WorktreeRow> for WorktreeState {
    fn from(row: &crate::derive::WorktreeRow) -> Self {
        let issue = row.issue_number.map(|num| IssueInfo {
            number: num,
            title: row.issue_title.clone().unwrap_or_default(),
            state: row
                .issue_state
                .clone()
                .unwrap_or_else(|| "open".to_string()),
            labels: row.issue_labels.clone(),
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
            is_main_worktree: row.is_main_worktree,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};

    fn make_row(
        repo_slug: &str,
        branch: &str,
        issue_number: Option<u32>,
        issue_state: Option<&str>,
        display_group: DisplayGroup,
    ) -> WorktreeRow {
        WorktreeRow {
            repo_slug: repo_slug.to_string(),
            worktree_path: format!("/repos/{}/{}", repo_slug, branch),
            branch: branch.to_string(),
            worktree_host: None,
            issue_number,
            issue_title: issue_number.map(|n| format!("Issue {}", n)),
            issue_state: issue_state.map(|s| s.to_string()),
            issue_labels: vec![],
            pr: None,
            sessions: vec![],
            display_group,
            is_main_worktree: false,
        }
    }

    fn make_state_with_rows(rows: Vec<WorktreeRow>) -> OrchardState {
        let mut repo_map: std::collections::HashMap<String, Vec<WorktreeState>> =
            std::collections::HashMap::new();
        for row in &rows {
            repo_map
                .entry(row.repo_slug.clone())
                .or_default()
                .push(WorktreeState::from(row));
        }
        let repos = repo_map
            .into_iter()
            .map(|(slug, worktrees)| RepoState { slug, worktrees })
            .collect();
        OrchardState {
            repos,
            standalone_sessions: Vec::new(),
            hosts: std::collections::HashMap::new(),
        }
    }

    #[test]
    fn all_worktrees_returns_sorted_by_display_group_then_issue_number() {
        let rows = vec![
            make_row(
                "owner/repo",
                "feat/issue-5",
                Some(5),
                None,
                DisplayGroup::Other,
            ),
            make_row(
                "owner/repo",
                "feat/issue-2",
                Some(2),
                None,
                DisplayGroup::NeedsAttention,
            ),
            make_row(
                "owner/repo",
                "feat/issue-1",
                Some(1),
                None,
                DisplayGroup::NeedsAttention,
            ),
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
            make_row("owner/repo-a", "main", None, None, DisplayGroup::RepoMain),
            make_row("owner/repo-b", "main", None, None, DisplayGroup::RepoMain),
        ];
        let state = make_state_with_rows(rows);
        assert_eq!(state.all_worktrees().len(), 2);
    }

    #[test]
    fn from_worktree_row_maps_issue_state_open() {
        let row = make_row(
            "owner/repo",
            "feat/issue-10",
            Some(10),
            Some("open"),
            DisplayGroup::Other,
        );
        let ws = WorktreeState::from(&row);
        assert_eq!(ws.issue.unwrap().state, "open");
    }

    #[test]
    fn from_worktree_row_maps_issue_state_closed() {
        let row = make_row(
            "owner/repo",
            "feat/issue-10",
            Some(10),
            Some("closed"),
            DisplayGroup::Other,
        );
        let ws = WorktreeState::from(&row);
        assert_eq!(ws.issue.unwrap().state, "closed");
    }

    #[test]
    fn from_worktree_row_defaults_issue_state_to_open_when_none() {
        let row = make_row(
            "owner/repo",
            "feat/issue-10",
            Some(10),
            None,
            DisplayGroup::Other,
        );
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
            labels: vec![],
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
            labels: vec![],
        };
        let pr_state = PrState::from(&pr_info);
        assert!(pr_state.state.is_none());
    }

    // -- From<&EnrichedSession> for SessionState tests ----------------------

    use crate::session::{
        EnrichedSession, Host, PaneInfo, SessionStatus, TmuxSessionInfo, WindowInfo,
    };

    fn make_enriched_session(windows: Vec<WindowInfo>) -> EnrichedSession {
        let panes = windows.iter().flat_map(|w| w.panes.clone()).collect();
        EnrichedSession {
            tmux: TmuxSessionInfo {
                host: Host::Local,
                name: "test-session".to_string(),
                status: SessionStatus::Running { attached: false },
            },
            claude: None,
            windows,
            panes,
        }
    }

    #[test]
    fn from_enriched_session_converts_windows_with_panes() {
        let windows = vec![
            WindowInfo {
                index: 0,
                name: "main".to_string(),
                is_active: true,
                panes: vec![
                    PaneInfo::new(0, "0.0", "bash", "bash"),
                    PaneInfo::new(1, "0.1", "nvim", "nvim"),
                ],
            },
            WindowInfo {
                index: 1,
                name: "editor".to_string(),
                is_active: false,
                panes: vec![PaneInfo::new(2, "1.0", "claude", "claude")],
            },
        ];
        let enriched = make_enriched_session(windows);
        let state = SessionState::from(&enriched);

        assert_eq!(state.name, "test-session");
        assert!(state.host.is_none());
        assert_eq!(state.windows.len(), 2);
        assert_eq!(state.windows[0].index, 0);
        assert_eq!(state.windows[0].name, "main");
        assert!(state.windows[0].is_active);
        assert_eq!(state.windows[0].panes.len(), 2);
        assert_eq!(state.windows[0].panes[0].tmux_target, "0.0");
        assert_eq!(state.windows[0].panes[1].tmux_target, "0.1");
        assert_eq!(state.windows[1].index, 1);
        assert_eq!(state.windows[1].name, "editor");
        assert!(!state.windows[1].is_active);
        assert_eq!(state.windows[1].panes.len(), 1);
        assert!(state.windows[1].panes[0].has_claude);
    }

    #[test]
    fn from_enriched_session_empty_windows_produces_empty_windows() {
        let enriched = make_enriched_session(vec![]);
        let state = SessionState::from(&enriched);
        assert!(state.windows.is_empty());
    }
}
