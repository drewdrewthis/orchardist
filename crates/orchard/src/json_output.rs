//! Versioned JSON output mapping for the `--json` flag.
//!
//! Decouples the public JSON API from internal `OrchardState`, allowing internal refactors
//! without breaking scripts. All output is camelCase, version-numbered, and backed by tests.
//! Consumed directly by external tools and scripts that call `orchard --json`.
//!
//! All `Json*` struct definitions live in `json_output_types.rs` — a single source of truth
//! shared by this module (via `include!`) and `build.rs` (for schema generation). The two
//! always describe the same wire format by construction.

use std::collections::HashMap;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::claude_state::ClaudeState;
use crate::derive::{DisplayGroup, phase_from_labels};
use crate::orchard_state::{
    IssueInfo, OrchardState, PrState, RepoState, SessionState, WindowState, WorktreeState,
};
use crate::session::StandaloneSessionRow;
use crate::signal;

// ---------------------------------------------------------------------------
// Single source of truth: Json* struct definitions (shared with build.rs)
// ---------------------------------------------------------------------------
//
// `json_output_types.rs` is the authoritative file.  `std::collections::HashMap`
// is already in scope above (needed for the `JsonOutput::hosts` field).
// `serde::Serialize` and `schemars::JsonSchema` are resolved via absolute crate
// paths by their proc-macros and do not require a `use` import.
// The file also defines local `CiChecks`, `CheckInfo`, and `WorkflowPhase` types —
// the mapping code below converts from the crate-internal equivalents at the boundary.
include!("json_output_types.rs");

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

fn layout_str(l: crate::cache::WorktreeLayout) -> &'static str {
    match l {
        crate::cache::WorktreeLayout::Bare => "bare",
        crate::cache::WorktreeLayout::Flat => "flat",
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

/// Converts a `crate::derive::WorkflowPhase` to the local wire-format `WorkflowPhase`.
///
/// Both types share identical variants and kebab-case serialization; the conversion is
/// a boundary adapter so the crate-internal type does not leak into the JSON wire type.
fn crate_phase_to_local(p: crate::derive::WorkflowPhase) -> WorkflowPhase {
    match p {
        crate::derive::WorkflowPhase::Blocked => WorkflowPhase::Blocked,
        crate::derive::WorkflowPhase::InAiReview => WorkflowPhase::InAiReview,
        crate::derive::WorkflowPhase::PrReady => WorkflowPhase::PrReady,
        crate::derive::WorkflowPhase::InProgress => WorkflowPhase::InProgress,
        crate::derive::WorkflowPhase::NeedsRepro => WorkflowPhase::NeedsRepro,
        crate::derive::WorkflowPhase::NeedsPlan => WorkflowPhase::NeedsPlan,
        crate::derive::WorkflowPhase::Investigating => WorkflowPhase::Investigating,
        crate::derive::WorkflowPhase::Planned => WorkflowPhase::Planned,
    }
}

/// Converts a `crate::ci_state::CheckInfo` to the local wire-format `CheckInfo`.
fn crate_check_info_to_local(c: &crate::ci_state::CheckInfo) -> CheckInfo {
    CheckInfo {
        name: c.name.clone(),
        state: c.state.clone(),
        details_url: c.details_url.clone(),
    }
}

/// Converts a `crate::ci_state::CiChecks` to the local wire-format `CiChecks`.
///
/// Ensures the schema describes exactly what `--json` emits: same field names, same
/// serialization attributes, same nesting.
fn crate_ci_checks_to_local(c: &crate::ci_state::CiChecks) -> CiChecks {
    CiChecks {
        code: c.code.iter().map(crate_check_info_to_local).collect(),
        gate: c.gate.iter().map(crate_check_info_to_local).collect(),
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
            phase: phase_from_labels(&i.labels).map(crate_phase_to_local),
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
        let last = crate::review_comment::last_review_comment(&pr.reviews);
        let last_review_comment_at = last.as_ref().map(|l| l.at.clone());
        let last_review_comment_author = last.as_ref().map(|l| l.author.clone());
        let has_unaddressed_author_comment = crate::review_comment::has_unaddressed_author_comment(
            pr.state.as_deref(),
            pr.author.as_deref(),
            last_review_comment_at.as_deref(),
            last_review_comment_author.as_deref(),
            pr.last_commit_pushed_at.as_deref(),
        );
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
            ci_checks: crate_ci_checks_to_local(&pr.ci_checks),
            has_conflicts: pr.has_conflicts,
            unresolved_threads: pr.unresolved_threads,
            // `unresolvedReviewThreads` is the forward name (matches issue #252).
            // `unresolvedThreads` is retained as an alias until in-repo script
            // consumers migrate. Both fields MUST stay equal — see the
            // `json_pr_retains_legacy_unresolved_threads_alias` test.
            unresolved_review_threads: pr.unresolved_threads,
            last_review_comment_at,
            last_review_comment_author,
            has_unaddressed_author_comment,
            labels: pr.labels.clone(),
            additions: pr.additions,
            deletions: pr.deletions,
            created_at: pr.created_at.clone(),
            updated_at: pr.updated_at.clone(),
            last_commit_pushed_at: pr.last_commit_pushed_at.clone(),
            phase: phase_from_labels(&pr.labels).map(crate_phase_to_local),
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
            discovery_path: s.discovery_path.clone(),
        }
    }
}

impl From<&WorktreeState> for JsonWorktree {
    /// Converts an internal `WorktreeState` to JSON output format.
    ///
    /// `layout` reflects the source worktree's physical layout (`"bare"` for
    /// Remmy/BoxdShared bare-repo+worktrees, `"flat"` for BoxdFork single
    /// clones), not a hardcoded default.
    fn from(ws: &WorktreeState) -> Self {
        let ahead_behind = ws
            .ahead_behind
            .map(|(ahead, behind)| JsonAheadBehind { ahead, behind });
        let pipeline_status = signal::resolve_status(ws);
        Self {
            path: ws.path.clone(),
            branch: ws.branch.clone(),
            host: ws.host.clone(),
            layout: layout_str(ws.layout).to_string(),
            ahead_behind,
            last_commit_at: ws.last_commit_at.clone(),
            issue: ws.issue.as_ref().map(Into::into),
            pr: ws.pr.as_ref().map(Into::into),
            sessions: ws.sessions.iter().map(Into::into).collect(),
            display_group: display_group_str(ws.display_group).to_string(),
            status: pipeline_status.name().to_string(),
            status_glyph: pipeline_status.glyph().to_string(),
            is_main_worktree: ws.is_main_worktree,
            last_activity_at: ws
                .pr
                .as_ref()
                .and_then(|pr| pr.last_commit_pushed_at.clone())
                .or_else(|| ws.last_commit_at.clone()),
            discovery_path: ws.discovery_path.clone(),
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
            discovery_path: None, // standalone sessions don't carry discovery_path
        }
    }
}

impl From<&OrchardState> for JsonOutput {
    /// Converts the unified `OrchardState` to JSON output, setting version to 6.
    ///
    /// Schema v6 adds the `status` and `statusGlyph` fields to each worktree entry.
    /// `status` is a stable snake_case `PipelineStatus` name (e.g. `"unresolved_threads"`).
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

        let errors: Vec<JsonTransitiveError> = state
            .transitive_errors
            .iter()
            .map(|e| JsonTransitiveError {
                dedup_key: e.dedup_key.clone(),
                discovery_path: e.discovery_path.clone(),
                // root is derived from discovery_path[1] rather than a stored field.
                root: e.root().unwrap_or_default().to_string(),
                reason: e.reason.clone(),
                phase: e.phase.clone(),
            })
            .collect();

        Self {
            version: 6,
            tmux_sessions: state.standalone_sessions.iter().map(Into::into).collect(),
            repos: state.repos.iter().map(Into::into).collect(),
            hosts,
            errors,
        }
    }
}

// ---------------------------------------------------------------------------
// Federation ingestion: version-check helper (AC6, Phase 1)
// ---------------------------------------------------------------------------

/// Version numbers this build can ingest from remote `orchard --json` output.
///
/// Only versions explicitly listed here are accepted. A remote running an older
/// or newer schema triggers `AdapterError::ParseFailure` so the caller can fall
/// back to the legacy shell-discovery path rather than silently misinterpreting
/// the payload.
///
/// When a new version is released, add it here *and* bump the emitted version
/// in `From<&OrchardState> for JsonOutput` to match.
pub const SUPPORTED_JSON_OUTPUT_VERSIONS: &[u32] = &[6];

/// Validates that `version` is in [`SUPPORTED_JSON_OUTPUT_VERSIONS`].
///
/// Returns `Ok(())` on success.  Returns
/// `Err(AdapterError::ParseFailure { raw: … })` when the version is unknown so
/// the caller can record a "version skew" diagnostic and fall back to the
/// legacy adapter path (AC6).
///
/// # Examples
///
/// ```
/// use orchard::json_output::check_json_output_version;
/// assert!(check_json_output_version(6).is_ok());
/// assert!(check_json_output_version(0).is_err());
/// assert!(check_json_output_version(99).is_err());
/// ```
pub fn check_json_output_version(version: u32) -> Result<(), crate::remote_adapter::AdapterError> {
    if SUPPORTED_JSON_OUTPUT_VERSIONS.contains(&version) {
        Ok(())
    } else {
        Err(crate::remote_adapter::AdapterError::ParseFailure {
            raw: format!(
                "version skew: remote version {version} is not in supported list {SUPPORTED_JSON_OUTPUT_VERSIONS:?}"
            ),
        })
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
            layout: crate::cache::WorktreeLayout::Bare,
            discovery_path: None,
        }
    }

    #[test]
    fn from_orchard_state_produces_version_6() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(output.version, 6);
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
            transitive_errors: vec![],
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
            transitive_errors: vec![],
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
            discovery_path: None,
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
            discovery_path: None,
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
            discovery_path: None,
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
                    }],
                },
            ],
        }
    }

    #[test]
    fn json_version_is_6() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(output.version, 6);
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
            discovery_path: None,
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
            unresolved_thread_comment_timestamps: vec![],
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
        assert_eq!(ji.phase, Some(WorkflowPhase::InProgress));
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
        assert_eq!(jp.phase, Some(WorkflowPhase::PrReady));
        let v = serde_json::to_value(&jp).unwrap();
        assert_eq!(v["phase"], "pr-ready");
    }

    #[test]
    fn json_pr_phase_resolves_multi_label_by_priority() {
        let pr = make_pr_state_with_labels(10, "feat/branch", vec!["in-progress", "blocked"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some(WorkflowPhase::Blocked));
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
            discovery_path: None,
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
            discovery_path: None,
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
            unresolved_thread_comment_timestamps: vec![],
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

    // -----------------------------------------------------------------------
    // Phase 1 — federation ingestion (AC6 + AC10, #329)
    // -----------------------------------------------------------------------

    /// AC10: schemars schema is byte-identical after adding Deserialize derives.
    ///
    /// Regenerates the schema at test time and compares it byte-for-byte against
    /// the committed `schema.json`.  A divergence means the Deserialize derive
    /// changed the schemars output, which is structurally forbidden by AC10.
    #[test]
    fn schema_byte_identical_after_deserialize_derives() {
        let schema = schemars::schema_for!(JsonOutput);
        let generated = serde_json::to_string_pretty(&schema).expect("schema serialization failed");

        let manifest_dir = env!("CARGO_MANIFEST_DIR");
        let snapshot_path = std::path::Path::new(manifest_dir).join("schema.json");
        let committed =
            std::fs::read_to_string(&snapshot_path).expect("schema.json must exist in crate root");

        assert_eq!(
            generated, committed,
            "schemars schema changed after adding Deserialize derives — \
             this violates AC10 (schema invariance). \
             If the schema legitimately changed, regenerate schema.json with `cargo build`."
        );
    }

    /// AC6 (parse-side): `check_json_output_version(0)` returns `AdapterError::ParseFailure`.
    #[test]
    fn version_zero_is_rejected_with_parse_failure() {
        let result = check_json_output_version(0);
        assert!(result.is_err(), "version 0 must be rejected but got Ok");
        match result.unwrap_err() {
            crate::remote_adapter::AdapterError::ParseFailure { raw } => {
                assert!(
                    raw.contains("version skew"),
                    "error message must mention 'version skew', got: {raw}"
                );
                assert!(
                    raw.contains('0'),
                    "error message must include the unexpected version number, got: {raw}"
                );
            }
            other => panic!("expected ParseFailure, got: {other}"),
        }
    }

    /// AC6 (parse-side): `check_json_output_version(99)` returns `AdapterError::ParseFailure`.
    #[test]
    fn version_ninety_nine_is_rejected_with_parse_failure() {
        let result = check_json_output_version(99);
        assert!(result.is_err(), "version 99 must be rejected but got Ok");
        match result.unwrap_err() {
            crate::remote_adapter::AdapterError::ParseFailure { raw } => {
                assert!(
                    raw.contains("version skew"),
                    "error message must mention 'version skew', got: {raw}"
                );
                assert!(
                    raw.contains("99"),
                    "error message must include the unexpected version number, got: {raw}"
                );
            }
            other => panic!("expected ParseFailure, got: {other}"),
        }
    }

    /// AC6 (parse-side): the current supported version (6) is accepted.
    #[test]
    fn version_six_is_accepted() {
        assert!(
            check_json_output_version(6).is_ok(),
            "version 6 must be accepted"
        );
    }

    /// AC6 round-trip: serialize a `JsonOutput` fixture, deserialize it back,
    /// and verify the deserialized value is deep-equal to the original.
    #[test]
    fn json_output_round_trips_serialize_deserialize() {
        // Construct a realistic JsonOutput fixture via the From<&OrchardState> mapping.
        let state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![make_worktree(DisplayGroup::Other)],
                default_branch: Some("main".to_string()),
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
            transitive_errors: vec![],
        };
        let original = JsonOutput::from(&state);

        // Serialize → JSON string → deserialize back.
        let json_str = serde_json::to_string(&original).expect("serialize must not fail");
        let roundtripped: JsonOutput =
            serde_json::from_str(&json_str).expect("deserialize must not fail");

        // Deep-equal via re-serialization (both objects serialize identically).
        let original_json = serde_json::to_value(&original).expect("to_value original");
        let roundtripped_json = serde_json::to_value(&roundtripped).expect("to_value roundtripped");
        assert_eq!(
            original_json, roundtripped_json,
            "round-tripped JsonOutput must be deep-equal to the original"
        );
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
    // Per-pane cwd/is_active and per-window layout (restore-visible fields)
    // -----------------------------------------------------------------------

    fn make_pane_state_with_metadata(cwd: &str, is_active: bool) -> PaneState {
        PaneState {
            index: 0,
            tmux_target: "0.0".to_string(),
            command: "bash".to_string(),
            title: "bash".to_string(),
            has_claude: false,
            cwd: cwd.to_string(),
            is_active,
        }
    }

    fn make_session_with_pane(pane: PaneState) -> SessionState {
        SessionState {
            name: "restore-test".to_string(),
            host: None,
            claude: None,
            started_at: None,
            last_activity_at: None,
            discovery_path: None,
            windows: vec![WindowState {
                index: 0,
                name: "main".to_string(),
                is_active: true,
                layout: "abc1,80x24,0,0".to_string(),
                panes: vec![pane],
            }],
        }
    }

    #[test]
    fn json_pane_includes_cwd_and_is_active() {
        let pane = make_pane_state_with_metadata("/tmp/foo", true);
        let session = make_session_with_pane(pane);
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let p = &value["windows"][0]["panes"][0];
        assert_eq!(p["cwd"], "/tmp/foo");
        assert_eq!(p["isActive"], true);
    }

    #[test]
    fn json_window_includes_layout() {
        let pane = make_pane_state_with_metadata("/tmp/baz", false);
        let session = make_session_with_pane(pane);
        let value = serde_json::to_value(JsonSession::from(&session)).unwrap();
        let win = &value["windows"][0];
        assert_eq!(win["layout"], "abc1,80x24,0,0");
    }

    #[test]
    fn labels_field_does_not_change_phase() {
        // "blocked" is higher priority than "in-progress"; phase must be "blocked"
        // AND labels must contain both values.
        let pr = make_pr_state_with_labels(13, "feat/both", vec!["blocked", "in-progress"]);
        let jp = JsonPr::from(&pr);
        assert_eq!(jp.phase, Some(WorkflowPhase::Blocked));
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
            unresolved_thread_comment_timestamps: vec![],
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

    // -----------------------------------------------------------------------
    // review_comment field tests
    // -----------------------------------------------------------------------

    /// Builds a minimal PrState with no reviews for testing new review-comment fields.
    #[allow(deprecated)]
    fn make_pr_with_reviews(
        author: Option<&str>,
        state: Option<&str>,
        reviews: Vec<crate::cache::CachedReview>,
        last_commit_pushed_at: Option<&str>,
    ) -> PrState {
        use crate::ci_state::CiChecks;
        PrState {
            number: 1,
            branch: "feat/test".to_string(),
            state: state.map(|s| s.to_string()),
            title: None,
            is_draft: None,
            author: author.map(|s| s.to_string()),
            requested_reviewers: vec![],
            reviews,
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
            last_commit_pushed_at: last_commit_pushed_at.map(|s| s.to_string()),
            unresolved_thread_comment_timestamps: vec![],
        }
    }

    #[test]
    fn json_pr_emits_all_four_new_fields_even_when_reviews_empty() {
        let pr = make_pr_with_reviews(Some("alice"), Some("OPEN"), vec![], None);
        let json_pr = JsonPr::from(&pr);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert!(
            value.get("unresolvedReviewThreads").is_some(),
            "expected 'unresolvedReviewThreads' key"
        );
        assert!(
            value.get("lastReviewCommentAt").is_some(),
            "expected 'lastReviewCommentAt' key (should be null)"
        );
        assert!(
            value.get("lastReviewCommentAuthor").is_some(),
            "expected 'lastReviewCommentAuthor' key (should be null)"
        );
        assert!(
            value.get("hasUnaddressedAuthorComment").is_some(),
            "expected 'hasUnaddressedAuthorComment' key"
        );
        assert_eq!(value["lastReviewCommentAt"], serde_json::Value::Null);
        assert_eq!(value["lastReviewCommentAuthor"], serde_json::Value::Null);
        assert_eq!(value["hasUnaddressedAuthorComment"], false);
    }

    #[test]
    fn json_pr_retains_legacy_unresolved_threads_alias() {
        let mut pr = make_pr_with_reviews(None, Some("OPEN"), vec![], None);
        pr.unresolved_threads = 3;
        let json_pr = JsonPr::from(&pr);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert!(
            value.get("unresolvedThreads").is_some(),
            "expected legacy 'unresolvedThreads' key"
        );
        assert!(
            value.get("unresolvedReviewThreads").is_some(),
            "expected 'unresolvedReviewThreads' key"
        );
        assert_eq!(value["unresolvedThreads"], 3);
        assert_eq!(value["unresolvedReviewThreads"], 3);
    }

    #[test]
    fn json_pr_populates_last_review_comment_fields_from_reviews() {
        let reviews = vec![
            crate::cache::CachedReview {
                author: "charlie".to_string(),
                state: "COMMENTED".to_string(),
                submitted_at: Some("2024-01-01T08:00:00Z".to_string()),
            },
            crate::cache::CachedReview {
                author: "bob".to_string(),
                state: "CHANGES_REQUESTED".to_string(),
                submitted_at: Some("2024-01-03T12:00:00Z".to_string()),
            },
        ];
        let pr = make_pr_with_reviews(
            Some("alice"),
            Some("OPEN"),
            reviews,
            Some("2024-01-02T00:00:00Z"),
        );
        let json_pr = JsonPr::from(&pr);
        let value = serde_json::to_value(&json_pr).unwrap();
        assert_eq!(
            value["lastReviewCommentAt"], "2024-01-03T12:00:00Z",
            "expected max submittedAt"
        );
        assert_eq!(
            value["lastReviewCommentAuthor"], "bob",
            "expected author of max review"
        );
        assert_eq!(
            value["hasUnaddressedAuthorComment"], true,
            "bob's review is after last push"
        );
    }

    // -----------------------------------------------------------------------
    // status / statusGlyph fields on JsonWorktree (issue #320, AC #9)
    // -----------------------------------------------------------------------

    /// Builds a WorktreeState with a PR that has unresolved threads.
    #[allow(deprecated)]
    fn make_worktree_with_unresolved_threads(unresolved_threads: u32) -> WorktreeState {
        use crate::ci_state::CiChecks;
        WorktreeState {
            path: "/repos/feat".to_string(),
            branch: "issue3298/fix-bug".to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: Some(PrState {
                number: 3298,
                branch: "issue3298/fix-bug".to_string(),
                state: Some("open".to_string()),
                title: None,
                is_draft: None,
                author: None,
                requested_reviewers: vec![],
                reviews: vec![],
                review_decision: Some("APPROVED".to_string()),
                checks_state: None,
                ci_code_state: Some("passing".to_string()),
                ci_gate_state: None,
                ci_checks: CiChecks::default(),
                has_conflicts: false,
                unresolved_threads,
                labels: vec![],
                additions: None,
                deletions: None,
                created_at: None,
                updated_at: None,
                last_commit_pushed_at: None,
                unresolved_thread_comment_timestamps: vec![],
            }),
            sessions: vec![],
            display_group: crate::derive::DisplayGroup::NeedsAttention,
            is_main_worktree: false,
            ahead_behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
            discovery_path: None,
        }
    }

    /// AC #9: worktree with approved + CI passing + unresolved_threads=1 serializes
    /// `status == "unresolved_threads"` and `statusGlyph == "💬"`.
    #[test]
    fn json_worktree_status_is_unresolved_threads_when_pr_has_unresolved_threads() {
        let wt = make_worktree_with_unresolved_threads(1);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.status, "unresolved_threads");
        assert_eq!(jw.status_glyph, "\u{1F4AC}"); // 💬

        // Verify via JSON serialization that field names are camelCase.
        let value = serde_json::to_value(&jw).unwrap();
        assert_eq!(value["status"], "unresolved_threads");
        assert_eq!(value["statusGlyph"], "💬");
    }

    /// AC #9: smoke test — worktree with no blockers serializes `status == "ready"`.
    #[test]
    fn json_worktree_status_is_ready_when_no_blockers() {
        let wt = make_worktree_with_unresolved_threads(0);
        let jw = JsonWorktree::from(&wt);
        assert_eq!(jw.status, "ready");
        assert_eq!(jw.status_glyph, "\u{1F7E2}"); // 🟢

        let value = serde_json::to_value(&jw).unwrap();
        assert_eq!(value["status"], "ready");
        assert_eq!(value["statusGlyph"], "🟢");
    }

    /// AC #9: version field has been bumped to reflect the new schema additions.
    #[test]
    fn json_output_version_reflects_status_field_addition() {
        let output = JsonOutput::from(&empty_state());
        assert_eq!(
            output.version, 6,
            "version must be 6 after status/statusGlyph addition"
        );
    }
}
