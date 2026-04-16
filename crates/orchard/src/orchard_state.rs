//! Unified data model for Orchard state.
//!
//! `OrchardState` is the single source of truth consumed by both the TUI and `--json` output.
//! It contains all repos, worktrees, sessions, and host reachability data.
//! Built by `build_state()` from multiple per-source caches; see `docs/architecture.md` for the full data flow.

use std::collections::HashMap;

use crate::ci_state::CiChecks;
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
    /// display_group then by smart criteria (claude input, recency, PR presence,
    /// issue number, branch name).
    pub fn all_worktrees(&self) -> Vec<&WorktreeState> {
        let mut all: Vec<&WorktreeState> =
            self.repos.iter().flat_map(|r| r.worktrees.iter()).collect();

        all.sort_by(|a, b| a.sort_key().cmp(&b.sort_key()));

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
    /// Default branch name (e.g. `main`), from repo meta cache.
    pub default_branch: Option<String>,
    /// Rollup CI state of the default branch.
    pub main_ci_state: Option<String>,
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
    /// Commit distance from upstream (ahead, behind), if available.
    pub ahead_behind: Option<(u32, u32)>,
    /// ISO 8601 timestamp of the most recent commit in this worktree.
    pub last_commit_at: Option<String>,
    /// Physical layout of this worktree — `Bare` (bare repo + linked
    /// worktrees) or `Flat` (single non-bare clone per BoxdFork VM).
    pub layout: crate::cache::WorktreeLayout,
}

impl WorktreeState {
    /// Builds a sort key for multi-criteria ordering of worktree rows.
    pub fn sort_key(&self) -> crate::derive::WorktreeSortKey<'_> {
        let has_claude_input = self.sessions.iter().any(|s| {
            s.claude
                .as_ref()
                .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Input)
        });
        let best_timestamp = self
            .pr
            .as_ref()
            .and_then(|pr| pr.last_commit_pushed_at.as_deref())
            .or(self.last_commit_at.as_deref());
        crate::derive::WorktreeSortKey {
            display_group: self.display_group,
            has_claude_input,
            best_timestamp,
            has_pr: self.pr.is_some(),
            issue_number: self.issue.as_ref().map(|i| i.number),
            branch: &self.branch,
        }
    }
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
    /// Assignees of this issue.
    pub assignees: Vec<String>,
    /// ISO 8601 timestamp when the issue was created.
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the issue was last updated (issue #251 SINCE).
    pub updated_at: Option<String>,
    /// Issue numbers blocking this issue.
    pub blocked_by: Vec<u32>,
    /// Child issues under this issue.
    pub sub_issues: Vec<crate::cache::CachedSubIssue>,
    /// Parent issue number if this is a sub-issue.
    pub parent: Option<u32>,
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
    /// PR title.
    pub title: Option<String>,
    /// Whether the PR is a draft.
    pub is_draft: Option<bool>,
    /// GitHub login of the PR author.
    pub author: Option<String>,
    /// Logins of requested reviewers.
    pub requested_reviewers: Vec<String>,
    /// Reviews submitted on this PR.
    pub reviews: Vec<crate::cache::CachedReview>,
    /// Review decision: "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED", etc.
    pub review_decision: Option<String>,
    /// Aggregate CI checks state: "SUCCESS", "FAILURE", "PENDING", etc.
    ///
    /// Deprecated in favour of [`PrState::ci_code_state`]. Retained for one release
    /// so downstream consumers are not broken. Will be removed in a future version.
    #[deprecated(note = "Use ci_code_state; this field is retained for one release")]
    pub checks_state: Option<String>,
    /// Rollup state for code CI checks only: "passing", "failing", "pending", or None.
    ///
    /// None means the PR has no code CI checks.
    pub ci_code_state: Option<String>,
    /// Rollup state for gate/policy checks: "cleared", "blocked", "pending", or None.
    ///
    /// None means the PR has no gate checks configured.
    pub ci_gate_state: Option<String>,
    /// Per-check breakdown classified into code and gate buckets.
    pub ci_checks: CiChecks,
    /// True when the PR has merge conflicts.
    pub has_conflicts: bool,
    /// Number of unresolved review threads on the PR.
    pub unresolved_threads: u32,
    /// Labels applied to the PR.
    pub labels: Vec<String>,
    /// Number of lines added.
    pub additions: Option<u32>,
    /// Number of lines deleted.
    pub deletions: Option<u32>,
    /// ISO 8601 timestamp when the PR was created.
    pub created_at: Option<String>,
    /// ISO 8601 timestamp when the PR was last updated.
    pub updated_at: Option<String>,
    /// ISO 8601 timestamp of when the last commit was pushed to the PR branch.
    pub last_commit_pushed_at: Option<String>,
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
    /// Unix timestamp when the tmux session was created.
    pub started_at: Option<u64>,
    /// Unix timestamp of the last activity in this session.
    pub last_activity_at: Option<u64>,
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
    /// Tmux layout string for this window (from `#{window_layout}`).
    ///
    /// Applied via `tmux select-layout` during session restore.
    pub layout: String,
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
    /// Working directory of this pane at snapshot time (from `#{pane_current_path}`).
    pub cwd: String,
    /// Whether this pane is the active (focused) pane in its window.
    pub is_active: bool,
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
    /// Context window usage percentage from status line telemetry.
    pub context_window_pct: Option<f64>,
    /// Total cost in USD from status line telemetry.
    pub cost_usd: Option<f64>,
    /// Total session duration in milliseconds from status line telemetry.
    pub total_duration_ms: Option<u64>,
    /// Rate limit data from status line telemetry.
    pub rate_limits: Option<crate::session::ClaudeRateLimits>,
    /// Stop reason from the last Stop event.
    pub stop_reason: Option<String>,
    /// Number of assistant turns in the conversation.
    pub turn_count: Option<u32>,
    /// Unix epoch seconds when the state last transitioned, if available.
    pub state_changed_at: Option<u64>,
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
    #[allow(deprecated)]
    fn from(pr: &crate::derive::PrInfo) -> Self {
        Self {
            number: pr.number,
            branch: pr.branch.clone(),
            state: pr.state.clone(),
            title: pr.title.clone(),
            is_draft: pr.is_draft,
            author: pr.author.clone(),
            requested_reviewers: pr.requested_reviewers.clone(),
            reviews: pr.reviews.clone(),
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
            context_window_pct: c.context_window_pct,
            cost_usd: c.cost_usd,
            total_duration_ms: c.total_duration_ms,
            rate_limits: c.rate_limits.clone(),
            stop_reason: c.stop_reason.clone(),
            turn_count: c.turn_count,
            state_changed_at: c.state_changed_at,
        });
        let windows = s
            .windows
            .iter()
            .map(|w| WindowState {
                index: w.index,
                name: w.name.clone(),
                is_active: w.is_active,
                layout: w.layout.clone(),
                panes: w
                    .panes
                    .iter()
                    .map(|p| PaneState {
                        index: p.index,
                        tmux_target: p.tmux_target.clone(),
                        command: p.command.clone(),
                        title: p.title.clone(),
                        has_claude: p.has_claude,
                        cwd: p.cwd.clone(),
                        is_active: p.is_active,
                    })
                    .collect(),
            })
            .collect();
        Self {
            name: s.tmux.name.clone(),
            host,
            claude,
            windows,
            started_at: s.started_at,
            last_activity_at: s.last_activity_at,
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
            assignees: row.issue_assignees.clone(),
            created_at: row.issue_created_at.clone(),
            updated_at: row.issue_updated_at.clone(),
            blocked_by: row.issue_blocked_by.clone(),
            sub_issues: row.issue_sub_issues.clone(),
            parent: row.issue_parent,
        });

        let ahead_behind = match (row.worktree_ahead, row.worktree_behind) {
            (Some(a), Some(b)) => Some((a, b)),
            (Some(a), None) => Some((a, 0)),
            (None, Some(b)) => Some((0, b)),
            (None, None) => None,
        };

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
            ahead_behind,
            last_commit_at: row.worktree_last_commit_at.clone(),
            layout: row.layout,
        }
    }
}

#[cfg(test)]
#[allow(deprecated)] // PrInfo.checks_state — fixtures still populate the legacy field for now
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
            issue_assignees: vec![],
            issue_created_at: None,
            issue_updated_at: None,
            issue_blocked_by: vec![],
            issue_sub_issues: vec![],
            issue_parent: None,
            pr: None,
            sessions: vec![],
            display_group,
            is_main_worktree: false,
            worktree_ahead: None,
            worktree_behind: None,
            worktree_last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
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
            .map(|(slug, worktrees)| RepoState {
                slug,
                worktrees,
                default_branch: None,
                main_ci_state: None,
            })
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
            ..PrInfo::default()
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
            ..PrInfo::default()
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
            started_at: None,
            last_activity_at: None,
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
                layout: String::new(),
            },
            WindowInfo {
                index: 1,
                name: "editor".to_string(),
                is_active: false,
                panes: vec![PaneInfo::new(2, "1.0", "claude", "claude")],
                layout: String::new(),
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
