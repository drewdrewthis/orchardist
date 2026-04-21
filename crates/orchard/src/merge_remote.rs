//! Pure merge step: folds remote `JsonOutput` snapshots into a local `OrchardState`.
//!
//! The remote orchard has already performed all join/enrichment (PR, issue, claude,
//! CI check state, `display_group`). This module trusts those pre-computed fields
//! and maps them directly into native `OrchardState` types without re-running the
//! local derive/join pipeline. Remote-sourced worktrees are tagged with the given
//! `host` and deduped against any existing entries by `(host, path)`.
//!
//! # Design invariant
//!
//! `derive_all_repos` and the local PR/issue/claude enrichment functions are never
//! called here. Doing so would overwrite remote-computed enrichment with locally-
//! missing data (e.g. a PR that exists on the remote but has not been fetched locally).

use std::collections::HashMap;
use std::path::Path;

use crate::ci_state::{CheckInfo, CiChecks};
use crate::claude_state::ClaudeState;
use crate::derive::DisplayGroup;
use crate::global_config::GlobalConfig;
use crate::json_output::{
    JsonClaudeInfo, JsonIssue, JsonOutput, JsonPr, JsonSession, JsonWorktree,
};
use crate::orchard_state::{
    ClaudeEnrichment, HostState, IssueInfo, OrchardState, PaneState, PrState, RepoState,
    SessionState, WindowState, WorktreeState,
};
use crate::session::{
    ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, StandaloneConfig,
    StandaloneSessionRow, TmuxSessionInfo, WindowInfo,
};

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Folds a remote `JsonOutput` snapshot into an already-built `OrchardState`.
///
/// The remote orchard has already joined and enriched its own data (PR, issue,
/// claude, CI check, `display_group`). This function trusts those fields and
/// does **not** re-run local join/enrichment over them. Remote-sourced worktrees
/// are tagged with the given `host` and deduped against any local or prior remote
/// entries by `(host, path)` with preference for the snapshot.
///
/// Standalone sessions (top-level `tmux_sessions` in `JsonOutput`) are merged
/// into `state.standalone_sessions`. Same `(name, host)` pair → one row. Same
/// name on different hosts → both rows are kept.
pub fn merge_remote_snapshot(state: &mut OrchardState, snapshot: JsonOutput, host: String) {
    // --- Repos and worktrees --------------------------------------------------
    for json_repo in snapshot.repos {
        // Find or create the matching RepoState.
        let repo_idx = if let Some(idx) = state.repos.iter().position(|r| r.slug == json_repo.slug)
        {
            idx
        } else {
            state.repos.push(RepoState {
                slug: json_repo.slug.clone(),
                worktrees: Vec::new(),
                default_branch: json_repo.default_branch.clone(),
                main_ci_state: json_repo.main_ci_state.clone(),
            });
            state.repos.len() - 1
        };

        for json_wt in json_repo.worktrees {
            // Determine the effective host for this worktree.
            // If the JsonWorktree has an explicit host field set, use that.
            // Otherwise tag it with the snapshot's host.
            let wt_host = json_wt.host.clone().unwrap_or_else(|| host.clone());
            let path = json_wt.path.clone();

            // Dedup: remove any prior entry with same (host, path). Snapshot wins.
            state.repos[repo_idx].worktrees.retain(|w| {
                let w_host = w.host.as_deref().unwrap_or("");
                !(w_host == wt_host && w.path == path)
            });

            let wt_state = worktree_state_from_json(json_wt, wt_host);
            state.repos[repo_idx].worktrees.push(wt_state);
        }
    }

    // --- Standalone sessions --------------------------------------------------
    for json_session in snapshot.tmux_sessions {
        // A remote machine's "local" sessions are remote from the local perspective.
        // Override with the snapshot's host so the entry is correctly tagged.
        let effective_host = if json_session.host == "local" {
            Some(host.clone())
        } else {
            Some(json_session.host.clone())
        };

        let name = json_session.name.clone();

        // Dedup: same (name, host) → one row. Snapshot wins.
        let eff_host_str = effective_host.as_deref().unwrap_or("");
        state.standalone_sessions.retain(|row| {
            let row_host = match &row.session.tmux.host {
                Host::Local => "",
                Host::Remote(h) => h.as_str(),
            };
            !(row.session.tmux.name == name && row_host == eff_host_str)
        });

        let standalone = standalone_row_from_json(json_session, effective_host);
        state.standalone_sessions.push(standalone);
    }
}

/// Like `build_state_with_hosts`, but also folds in pre-fetched remote snapshots.
///
/// Calls the existing builder then merges each `(host, JsonOutput)` pair in order.
/// The local derive pipeline runs only for local/legacy-sourced worktrees; remote
/// snapshots bypass it entirely (see [`merge_remote_snapshot`]).
pub fn build_state_with_snapshots(
    config: &GlobalConfig,
    hosts: &HashMap<String, HostState>,
    snapshots: Vec<(String, JsonOutput)>,
) -> OrchardState {
    let mut state = crate::build_state::build_state_with_hosts(config, hosts);
    for (host, snapshot) in snapshots {
        merge_remote_snapshot(&mut state, snapshot, host);
    }
    state
}

/// Loads cached snapshots from disk and folds them into a freshly-built `OrchardState`.
///
/// This is the **production entry point** for TUI cold start and watch-daemon refresh.
/// It reads every `OrchardProxy` remote's snapshot from `~/.cache/orchard/` and merges
/// the results into the state produced by [`crate::build_state::build_state_with_hosts`],
/// ensuring federated remote enrichment (PR, issue, claude, CI) reaches callers without
/// re-running the local derive/join pipeline over remote data.
///
/// Use [`build_state_with_cached_snapshots_from`] in tests where you need to redirect
/// the cache directory.
pub fn build_state_with_cached_snapshots(
    config: &GlobalConfig,
    hosts: &HashMap<String, HostState>,
) -> OrchardState {
    let snapshots = crate::orchard_snapshot::load_cached_snapshots(config);
    build_state_with_snapshots(config, hosts, snapshots)
}

/// Like [`build_state_with_cached_snapshots`] but reads snapshots from `cache_dir`.
///
/// Intended for tests that redirect cache writes to a [`tempfile::TempDir`].
pub fn build_state_with_cached_snapshots_from(
    config: &GlobalConfig,
    hosts: &HashMap<String, HostState>,
    cache_dir: &Path,
) -> OrchardState {
    let snapshots = crate::orchard_snapshot::load_cached_snapshots_from(config, cache_dir);
    build_state_with_snapshots(config, hosts, snapshots)
}


// ---------------------------------------------------------------------------
// Private converters: Json* → OrchardState types (no derive/join)
// ---------------------------------------------------------------------------

fn worktree_state_from_json(json_wt: JsonWorktree, host: String) -> WorktreeState {
    let issue = json_wt.issue.map(issue_info_from_json);
    let pr = json_wt.pr.map(pr_state_from_json);
    let sessions = json_wt
        .sessions
        .iter()
        .map(|s| session_state_from_json(s, Some(host.clone())))
        .collect();

    let display_group = parse_display_group(&json_wt.display_group);
    let ahead_behind = json_wt.ahead_behind.map(|ab| (ab.ahead, ab.behind));
    let layout = if json_wt.layout == "flat" {
        crate::cache::WorktreeLayout::Flat
    } else {
        crate::cache::WorktreeLayout::Bare
    };

    WorktreeState {
        path: json_wt.path,
        branch: json_wt.branch,
        is_bare: false,
        host: Some(host),
        issue,
        pr,
        sessions,
        display_group,
        is_main_worktree: json_wt.is_main_worktree,
        ahead_behind,
        last_commit_at: json_wt.last_commit_at,
        layout,
    }
}

fn issue_info_from_json(json_issue: JsonIssue) -> IssueInfo {
    IssueInfo {
        number: json_issue.number,
        title: json_issue.title,
        state: json_issue.state,
        labels: json_issue.labels,
        assignees: json_issue.assignees,
        created_at: json_issue.created_at,
        updated_at: None,
        blocked_by: json_issue.blocked_by,
        sub_issues: json_issue
            .sub_issues
            .into_iter()
            .map(|s| crate::cache::CachedSubIssue {
                number: s.number,
                title: s.title,
                state: s.state,
            })
            .collect(),
        parent: json_issue.parent,
    }
}

#[allow(deprecated)] // PrState.checks_state retained for one release
fn pr_state_from_json(json_pr: JsonPr) -> PrState {
    let reviews: Vec<crate::cache::CachedReview> = json_pr
        .reviews
        .into_iter()
        .map(|r| crate::cache::CachedReview {
            author: r.author,
            state: r.state,
            submitted_at: r.submitted_at,
        })
        .collect();

    let ci_checks = ci_checks_from_json(&json_pr.ci_checks);

    PrState {
        number: json_pr.number,
        branch: json_pr.branch,
        state: json_pr.state,
        title: json_pr.title,
        is_draft: json_pr.is_draft,
        author: json_pr.author,
        requested_reviewers: json_pr.requested_reviewers,
        reviews,
        review_decision: json_pr.review_decision,
        checks_state: json_pr.checks_state,
        ci_code_state: json_pr.ci_code_state,
        ci_gate_state: json_pr.ci_gate_state,
        ci_checks,
        has_conflicts: json_pr.has_conflicts,
        unresolved_threads: json_pr.unresolved_threads,
        labels: json_pr.labels,
        additions: json_pr.additions,
        deletions: json_pr.deletions,
        created_at: json_pr.created_at,
        updated_at: json_pr.updated_at,
        last_commit_pushed_at: json_pr.last_commit_pushed_at,
        unresolved_thread_comment_timestamps: vec![],
    }
}

fn ci_checks_from_json(json_ci: &crate::json_output::CiChecks) -> CiChecks {
    CiChecks {
        code: json_ci
            .code
            .iter()
            .map(|c| CheckInfo {
                name: c.name.clone(),
                state: c.state.clone(),
                details_url: c.details_url.clone(),
            })
            .collect(),
        gate: json_ci
            .gate
            .iter()
            .map(|c| CheckInfo {
                name: c.name.clone(),
                state: c.state.clone(),
                details_url: c.details_url.clone(),
            })
            .collect(),
    }
}

/// Converts a `JsonSession` to a `SessionState` suitable for attaching to a `WorktreeState`.
fn session_state_from_json(
    json_session: &JsonSession,
    host_override: Option<String>,
) -> SessionState {
    let host = host_override.or_else(|| {
        if json_session.host == "local" {
            None
        } else {
            Some(json_session.host.clone())
        }
    });

    let claude = json_session
        .claude
        .as_ref()
        .and_then(claude_enrichment_from_json);

    let windows: Vec<WindowState> = json_session
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

    SessionState {
        name: json_session.name.clone(),
        host,
        claude,
        windows,
        started_at: None,
        last_activity_at: None,
    }
}

/// Converts `JsonClaudeInfo` to `ClaudeEnrichment` for use in `SessionState`.
///
/// Returns `None` when the parsed status is `ClaudeState::None`, matching
/// the convention used by the local enrichment path.
fn claude_enrichment_from_json(json_claude: &JsonClaudeInfo) -> Option<ClaudeEnrichment> {
    let status: ClaudeState = json_claude.status.parse().unwrap_or(ClaudeState::None);
    if status == ClaudeState::None {
        return None;
    }
    Some(ClaudeEnrichment {
        status,
        model: json_claude.model.clone(),
        last_tool: json_claude.last_tool.clone(),
        current_task: json_claude.current_task.clone(),
        session_start_ts: None,
        input_tokens: json_claude.input_tokens,
        output_tokens: json_claude.output_tokens,
        cache_creation_input_tokens: json_claude.cache_creation_input_tokens,
        cache_read_input_tokens: json_claude.cache_read_input_tokens,
        context_window_pct: json_claude.context_window_pct,
        cost_usd: json_claude.cost_usd,
        total_duration_ms: json_claude.total_duration_ms,
        rate_limits: None,
        stop_reason: json_claude.stop_reason.clone(),
        turn_count: json_claude.turn_count,
        state_changed_at: None,
    })
}

/// Converts `JsonClaudeInfo` to `ClaudeSessionInfo` for use in `EnrichedSession`.
///
/// Returns `None` when the parsed status is `ClaudeState::None`, matching
/// the convention used by `ClaudeSessionInfo::from_state_file`.
fn claude_session_info_from_json(json_claude: &JsonClaudeInfo) -> Option<ClaudeSessionInfo> {
    let status: ClaudeState = json_claude.status.parse().unwrap_or(ClaudeState::None);
    if status == ClaudeState::None {
        return None;
    }
    Some(ClaudeSessionInfo {
        status,
        model: json_claude.model.clone(),
        last_tool: json_claude.last_tool.clone(),
        current_task: json_claude.current_task.clone(),
        session_start_ts: None,
        input_tokens: json_claude.input_tokens,
        output_tokens: json_claude.output_tokens,
        cache_creation_input_tokens: json_claude.cache_creation_input_tokens,
        cache_read_input_tokens: json_claude.cache_read_input_tokens,
        context_window_pct: json_claude.context_window_pct,
        cost_usd: json_claude.cost_usd,
        total_duration_ms: json_claude.total_duration_ms,
        rate_limits: None,
        stop_reason: json_claude.stop_reason.clone(),
        turn_count: json_claude.turn_count,
        state_changed_at: None,
    })
}

/// Converts a `JsonSession` to a `StandaloneSessionRow`.
///
/// `host` is the effective host (`Some(remote_host)` for remote sessions,
/// `None` for genuinely local ones — which should not appear in remote snapshots).
fn standalone_row_from_json(
    json_session: JsonSession,
    host: Option<String>,
) -> StandaloneSessionRow {
    let tmux_host = match &host {
        Some(h) => Host::Remote(h.clone()),
        None => Host::Local,
    };

    let status = if json_session.status == "running" {
        SessionStatus::Running { attached: false }
    } else {
        SessionStatus::Dead
    };

    let claude = json_session
        .claude
        .as_ref()
        .and_then(claude_session_info_from_json);

    let windows: Vec<WindowInfo> = json_session
        .windows
        .iter()
        .map(|w| WindowInfo {
            index: w.index,
            name: w.name.clone(),
            is_active: w.is_active,
            layout: w.layout.clone(),
            panes: w
                .panes
                .iter()
                .map(|p| {
                    crate::session::PaneInfo::new_with_metadata(
                        p.index,
                        &p.tmux_target,
                        &p.command,
                        &p.title,
                        p.cwd.clone(),
                        p.is_active,
                    )
                })
                .collect(),
        })
        .collect();

    let panes = windows.iter().flat_map(|w| w.panes.clone()).collect();
    let name = json_session.name.clone();

    StandaloneSessionRow {
        session: EnrichedSession {
            tmux: TmuxSessionInfo {
                host: tmux_host,
                name: name.clone(),
                status,
            },
            claude,
            windows,
            panes,
            started_at: None,
            last_activity_at: None,
        },
        config: StandaloneConfig {
            name,
            command: String::new(),
            cwd: String::new(),
            start_on_launch: false,
        },
    }
}

/// Parses a `display_group` snake_case string back to a `DisplayGroup`.
///
/// Unrecognised strings fall back to `DisplayGroup::Other` so unknown future
/// variants do not cause a panic.
fn parse_display_group(s: &str) -> DisplayGroup {
    // Re-use serde's snake_case deserialization via a JSON string.
    let quoted = format!("\"{}\"", s);
    serde_json::from_str::<DisplayGroup>(&quoted).unwrap_or(DisplayGroup::Other)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::json_output::{
        CheckInfo as JsonCheckInfo, CiChecks as JsonCiChecks, JsonClaudeInfo, JsonIssue,
        JsonOutput, JsonPr, JsonRepo, JsonSession, JsonWorktree,
    };

    // ---- helpers -----------------------------------------------------------

    fn make_json_output_with_worktree(wt: JsonWorktree) -> JsonOutput {
        JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![wt],
            }],
            hosts: HashMap::new(),
        }
    }

    fn make_minimal_worktree(path: &str, branch: &str) -> JsonWorktree {
        JsonWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            host: None,
            layout: "bare".to_string(),
            ahead_behind: None,
            last_commit_at: None,
            issue: None,
            pr: None,
            sessions: vec![],
            display_group: "other".to_string(),
            status: "ready".to_string(),
            status_glyph: "\u{1f7e2}".to_string(),
            is_main_worktree: false,
            last_activity_at: None,
        }
    }

    fn make_empty_state() -> OrchardState {
        OrchardState::new()
    }

    fn make_empty_pr(number: u32, branch: &str, state: &str) -> JsonPr {
        JsonPr {
            number,
            branch: branch.to_string(),
            title: None,
            is_draft: None,
            author: None,
            requested_reviewers: vec![],
            reviews: vec![],
            state: Some(state.to_string()),
            review_decision: None,
            checks_state: None,
            ci_code_state: None,
            ci_gate_state: None,
            ci_checks: JsonCiChecks {
                code: vec![],
                gate: vec![],
            },
            has_conflicts: false,
            unresolved_threads: 0,
            unresolved_review_threads: 0,
            last_review_comment_at: None,
            last_review_comment_author: None,
            has_unaddressed_author_comment: false,
            labels: vec![],
            additions: None,
            deletions: None,
            created_at: None,
            updated_at: None,
            last_commit_pushed_at: None,
            phase: None,
        }
    }

    fn make_empty_issue(number: u32, state: &str) -> JsonIssue {
        JsonIssue {
            number,
            title: format!("Issue #{number}"),
            state: state.to_string(),
            assignees: vec![],
            created_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
            labels: vec![],
            phase: None,
        }
    }

    // ---- AC4: PR enrichment preservation -----------------------------------

    /// AC4 — Remote JsonOutput carries PR enrichment that is preserved locally.
    ///
    /// The merge MUST NOT re-derive or overwrite the pr.number / pr.state from
    /// local cache files. The remote snapshot values must survive intact.
    #[test]
    fn ac4_pr_enrichment_preserved() {
        let mut wt = make_minimal_worktree("/remote/path", "issue329/federated");
        wt.pr = Some(make_empty_pr(335, "issue329/federated", "open"));

        let snapshot = make_json_output_with_worktree(wt);
        let mut state = make_empty_state();
        merge_remote_snapshot(&mut state, snapshot, "boxd@vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        let wt_state = repo
            .worktrees
            .iter()
            .find(|w| w.branch == "issue329/federated")
            .unwrap();

        let pr = wt_state.pr.as_ref().expect("pr must be preserved");
        assert_eq!(pr.number, 335, "pr.number must equal 335");
        assert_eq!(
            pr.state,
            Some("open".to_string()),
            "pr.state must equal 'open'"
        );
    }

    /// AC4 — Remote JsonOutput carries issue enrichment that is preserved locally.
    #[test]
    fn ac4_issue_enrichment_preserved() {
        let mut wt = make_minimal_worktree("/remote/path", "issue329/federated");
        wt.issue = Some(make_empty_issue(329, "open"));

        let snapshot = make_json_output_with_worktree(wt);
        let mut state = make_empty_state();
        merge_remote_snapshot(&mut state, snapshot, "boxd@vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        let wt_state = repo
            .worktrees
            .iter()
            .find(|w| w.branch == "issue329/federated")
            .unwrap();

        let issue = wt_state.issue.as_ref().expect("issue must be preserved");
        assert_eq!(issue.number, 329, "issue.number must equal 329");
        assert_eq!(issue.state, "open", "issue.state must equal 'open'");
    }

    /// AC4 — Remote JsonOutput carries claude and check-state enrichment.
    ///
    /// Claude state "working", CI checks "passing", and display_group "claude_working"
    /// must all survive the merge without recomputation.
    #[test]
    fn ac4_claude_and_ci_enrichment_preserved() {
        let mut wt = make_minimal_worktree("/remote/path", "issue329/federated");
        wt.display_group = "claude_working".to_string();
        wt.sessions = vec![JsonSession {
            name: "or_issue329".to_string(),
            host: "local".to_string(),
            status: "running".to_string(),
            started_at: None,
            last_activity_at: None,
            claude: Some(JsonClaudeInfo {
                status: "working".to_string(),
                model: Some("claude-opus-4-6".to_string()),
                last_tool: None,
                current_task: None,
                session_age_sec: None,
                input_tokens: None,
                output_tokens: None,
                cache_creation_input_tokens: None,
                cache_read_input_tokens: None,
                context_window_pct: None,
                cost_usd: None,
                total_duration_ms: None,
                stop_reason: None,
                turn_count: None,
                state_elapsed_sec: None,
            }),
            windows: vec![],
        }];
        let mut pr = make_empty_pr(335, "issue329/federated", "open");
        pr.ci_code_state = Some("passing".to_string());
        pr.ci_checks = JsonCiChecks {
            code: vec![JsonCheckInfo {
                name: "ci".to_string(),
                state: "passing".to_string(),
                details_url: None,
            }],
            gate: vec![],
        };
        wt.pr = Some(pr);

        let snapshot = make_json_output_with_worktree(wt);
        let mut state = make_empty_state();
        merge_remote_snapshot(&mut state, snapshot, "boxd@vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        let wt_state = repo
            .worktrees
            .iter()
            .find(|w| w.branch == "issue329/federated")
            .unwrap();

        // display_group preserved — not recomputed
        assert_eq!(
            wt_state.display_group,
            DisplayGroup::ClaudeWorking,
            "display_group must be ClaudeWorking, not recomputed"
        );

        // CI check state preserved
        let pr = wt_state.pr.as_ref().expect("pr must exist");
        assert_eq!(pr.ci_code_state, Some("passing".to_string()));
        assert_eq!(pr.ci_checks.code.len(), 1);
        assert_eq!(pr.ci_checks.code[0].state, "passing");

        // Claude state preserved
        let session = wt_state.sessions.first().expect("session must exist");
        let claude = session.claude.as_ref().expect("claude must exist");
        assert_eq!(
            claude.status,
            ClaudeState::Working,
            "claude status must be Working"
        );
    }

    /// AC4 — build_state skips join/enrichment for remote-sourced worktrees.
    ///
    /// A worktree with PR #9999 that does not exist in any local cache must still
    /// appear with pr.number == 9999 after merge. If local join logic ran, it would
    /// return None.
    #[test]
    fn ac4_remote_worktree_bypasses_local_join() {
        let mut wt = make_minimal_worktree("/remote/nonexistent", "issue9999/branch");
        wt.pr = Some({
            let mut p = make_empty_pr(9999, "issue9999/branch", "open");
            p.title = Some("Imaginary PR".to_string());
            p
        });
        wt.issue = Some(make_empty_issue(9999, "open"));

        let snapshot = make_json_output_with_worktree(wt);
        let mut state = make_empty_state();
        // If local join ran, PR #9999 would not exist in local caches → pr would be None.
        merge_remote_snapshot(&mut state, snapshot, "boxd@vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        let wt_state = repo
            .worktrees
            .iter()
            .find(|w| w.branch == "issue9999/branch")
            .unwrap();

        let pr = wt_state
            .pr
            .as_ref()
            .expect("pr must survive without local join");
        assert_eq!(pr.number, 9999);

        let issue = wt_state
            .issue
            .as_ref()
            .expect("issue must survive without local join");
        assert_eq!(issue.number, 9999);
    }

    // ---- AC5: standalone sessions ------------------------------------------

    /// AC5 — Remote standalone sessions are merged with host set.
    ///
    /// A `tmux_sessions` entry from the remote snapshot must appear in
    /// `OrchardState.standalone_sessions` with the remote host populated.
    #[test]
    fn ac5_remote_standalone_session_host_set() {
        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![JsonSession {
                name: "shepherd".to_string(),
                host: "local".to_string(), // remote's "local" → remote from our perspective
                status: "running".to_string(),
                started_at: None,
                last_activity_at: None,
                claude: None,
                windows: vec![],
            }],
            repos: vec![],
            hosts: HashMap::new(),
        };

        let mut state = make_empty_state();
        merge_remote_snapshot(&mut state, snapshot, "boxd@vm.boxd.sh".to_string());

        assert_eq!(state.standalone_sessions.len(), 1);
        let row = &state.standalone_sessions[0];
        assert_eq!(row.session.tmux.name, "shepherd");

        // Host must be the remote host, not local.
        match &row.session.tmux.host {
            Host::Remote(h) => assert_eq!(h, "boxd@vm.boxd.sh"),
            Host::Local => panic!("standalone session host must be Remote, not Local"),
        }
    }

    /// AC5 — Local and remote standalone sessions coexist.
    ///
    /// A local (`Host::Local`) session and a remote session with different names
    /// must both appear in `standalone_sessions`.
    #[test]
    fn ac5_local_and_remote_standalone_sessions_coexist() {
        // Seed a local standalone session directly.
        let local_row = StandaloneSessionRow {
            session: EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "global".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            },
            config: StandaloneConfig {
                name: "global".to_string(),
                command: String::new(),
                cwd: String::new(),
                start_on_launch: false,
            },
        };

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![JsonSession {
                name: "shepherd".to_string(),
                host: "local".to_string(),
                status: "running".to_string(),
                started_at: None,
                last_activity_at: None,
                claude: None,
                windows: vec![],
            }],
            repos: vec![],
            hosts: HashMap::new(),
        };

        let mut state = make_empty_state();
        state.standalone_sessions.push(local_row);
        merge_remote_snapshot(&mut state, snapshot, "remote-host".to_string());

        assert_eq!(
            state.standalone_sessions.len(),
            2,
            "both local and remote standalone sessions must coexist"
        );

        let global = state
            .standalone_sessions
            .iter()
            .find(|r| r.session.tmux.name == "global")
            .unwrap();
        assert!(
            matches!(global.session.tmux.host, Host::Local),
            "global must be Local"
        );

        let shepherd = state
            .standalone_sessions
            .iter()
            .find(|r| r.session.tmux.name == "shepherd")
            .unwrap();
        match &shepherd.session.tmux.host {
            Host::Remote(h) => assert_eq!(h, "remote-host"),
            Host::Local => panic!("shepherd must be Remote"),
        }
    }

    // ---- AC10: union + dedup ------------------------------------------------

    /// AC10 — `orchard --json` unions local and remote repos/worktrees/sessions.
    ///
    /// 1 repo, 2 local worktrees + 3 remote worktrees = 5 entries on the same slug.
    /// 3 remote entries have non-null host; 2 local have null host.
    #[test]
    fn ac10_union_local_and_remote_worktrees() {
        let local_wt1 = WorktreeState {
            path: "/local/repo-main".to_string(),
            branch: "main".to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::RepoMain,
            is_main_worktree: true,
            ahead_behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
        };
        let local_wt2 = WorktreeState {
            path: "/local/repo-feat".to_string(),
            branch: "feat/local".to_string(),
            is_bare: false,
            host: None,
            issue: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
            ahead_behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
        };

        let mut state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![local_wt1, local_wt2],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![
                    make_minimal_worktree("/remote/branch-a", "issue100/branch-a"),
                    make_minimal_worktree("/remote/branch-b", "issue101/branch-b"),
                    make_minimal_worktree("/remote/branch-c", "issue102/branch-c"),
                ],
            }],
            hosts: HashMap::new(),
        };

        merge_remote_snapshot(&mut state, snapshot, "vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        assert_eq!(repo.worktrees.len(), 5, "2 local + 3 remote = 5 worktrees");

        let remote_count = repo.worktrees.iter().filter(|w| w.host.is_some()).count();
        let local_count = repo.worktrees.iter().filter(|w| w.host.is_none()).count();
        assert_eq!(remote_count, 3, "3 remote entries must have non-null host");
        assert_eq!(local_count, 2, "2 local entries must have null host");
    }

    /// AC10 — `(host, path)` deduplication keeps remote over local on conflict.
    ///
    /// When the remote snapshot has an entry with the same (host, path) as an
    /// existing entry, the snapshot (proxy) wins over the legacy entry.
    #[test]
    fn ac10_dedup_keeps_remote_over_local_on_conflict() {
        // Simulate a "legacy" entry at the same path + host.
        let legacy_wt = WorktreeState {
            path: "/remote/some-path".to_string(),
            branch: "old-branch".to_string(),
            is_bare: false,
            host: Some("vm.boxd.sh".to_string()),
            issue: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
            ahead_behind: None,
            last_commit_at: None,
            layout: crate::cache::WorktreeLayout::Bare,
        };

        let mut state = OrchardState {
            repos: vec![RepoState {
                slug: "owner/repo".to_string(),
                worktrees: vec![legacy_wt],
                default_branch: None,
                main_ci_state: None,
            }],
            standalone_sessions: vec![],
            hosts: HashMap::new(),
        };

        // Snapshot entry for the same (host, path) but with enriched PR data.
        let mut remote_wt = make_minimal_worktree("/remote/some-path", "new-branch-with-pr");
        remote_wt.host = Some("vm.boxd.sh".to_string());
        remote_wt.pr = Some({
            let mut p = make_empty_pr(42, "new-branch-with-pr", "open");
            p.title = Some("Enriched by remote".to_string());
            p
        });

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![remote_wt],
            }],
            hosts: HashMap::new(),
        };

        merge_remote_snapshot(&mut state, snapshot, "vm.boxd.sh".to_string());

        let repo = state.repos.iter().find(|r| r.slug == "owner/repo").unwrap();
        assert_eq!(
            repo.worktrees.len(),
            1,
            "dedup must produce exactly 1 WorktreeState for this (host, path) tuple"
        );

        let wt = &repo.worktrees[0];
        let pr = wt
            .pr
            .as_ref()
            .expect("remote (proxy) entry with PR must win");
        assert_eq!(pr.number, 42, "remote PR number must survive dedup");
    }

    /// AC10 — Standalone sessions are unioned without duplication.
    ///
    /// Same session name on different hosts → both rows appear (distinguished by host).
    #[test]
    fn ac10_standalone_sessions_unioned_by_name_and_host() {
        // Pre-seed a local standalone session named "shepherd".
        let local_shepherd = StandaloneSessionRow {
            session: EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "shepherd".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            },
            config: StandaloneConfig {
                name: "shepherd".to_string(),
                command: String::new(),
                cwd: String::new(),
                start_on_launch: false,
            },
        };

        let mut state = make_empty_state();
        state.standalone_sessions.push(local_shepherd);

        // Remote snapshot also has "shepherd" but on a different host.
        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![JsonSession {
                name: "shepherd".to_string(),
                host: "local".to_string(),
                status: "running".to_string(),
                started_at: None,
                last_activity_at: None,
                claude: None,
                windows: vec![],
            }],
            repos: vec![],
            hosts: HashMap::new(),
        };

        merge_remote_snapshot(&mut state, snapshot, "remote-host".to_string());

        // Both must appear — same name but different hosts.
        assert_eq!(
            state.standalone_sessions.len(),
            2,
            "same name on different hosts must produce 2 rows"
        );

        let local = state.standalone_sessions.iter().find(|r| {
            r.session.tmux.name == "shepherd" && matches!(r.session.tmux.host, Host::Local)
        });
        assert!(local.is_some(), "local shepherd must still be present");

        let remote = state.standalone_sessions.iter().find(|r| {
            r.session.tmux.name == "shepherd"
                && matches!(&r.session.tmux.host, Host::Remote(h) if h == "remote-host")
        });
        assert!(remote.is_some(), "remote shepherd must be present");
    }

    // ---- build_state_with_snapshots ----------------------------------------

    /// `build_state_with_snapshots` with no snapshots behaves like `build_state_with_hosts`.
    ///
    /// Compares the two outputs for equivalence rather than asserting emptiness —
    /// the test host's filesystem may carry cached tmux sessions that legitimately
    /// surface as standalone rows; what matters is that no snapshots means no
    /// divergence from the pre-federation builder.
    #[test]
    fn build_state_with_snapshots_no_snapshots_is_identical_to_base() {
        let config = GlobalConfig::default();
        let hosts = HashMap::new();
        let with_snapshots = build_state_with_snapshots(&config, &hosts, vec![]);
        let baseline = crate::build_state::build_state_with_hosts(&config, &hosts);
        assert_eq!(with_snapshots.repos.len(), baseline.repos.len());
        assert_eq!(
            with_snapshots.standalone_sessions.len(),
            baseline.standalone_sessions.len()
        );
        assert_eq!(with_snapshots.hosts.len(), baseline.hosts.len());
    }

    /// `build_state_with_snapshots` merges remote repos from the snapshot.
    #[test]
    fn build_state_with_snapshots_merges_remote_repos() {
        let config = GlobalConfig::default();
        let hosts = HashMap::new();

        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "remote-owner/remote-repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![make_minimal_worktree("/remote/main", "main")],
            }],
            hosts: HashMap::new(),
        };

        let state = build_state_with_snapshots(
            &config,
            &hosts,
            vec![("remote-host".to_string(), snapshot)],
        );

        assert_eq!(state.repos.len(), 1);
        assert_eq!(state.repos[0].slug, "remote-owner/remote-repo");
        assert_eq!(state.repos[0].worktrees.len(), 1);
        assert_eq!(
            state.repos[0].worktrees[0].host,
            Some("remote-host".to_string())
        );
    }

    // ---- parse_display_group ------------------------------------------------

    #[test]
    fn parse_display_group_known_variants() {
        assert_eq!(parse_display_group("other"), DisplayGroup::Other);
        assert_eq!(
            parse_display_group("claude_working"),
            DisplayGroup::ClaudeWorking
        );
        assert_eq!(
            parse_display_group("needs_attention"),
            DisplayGroup::NeedsAttention
        );
        assert_eq!(
            parse_display_group("ready_to_merge"),
            DisplayGroup::ReadyToMerge
        );
        assert_eq!(parse_display_group("repo_main"), DisplayGroup::RepoMain);
        assert_eq!(
            parse_display_group("prioritized"),
            DisplayGroup::Prioritized
        );
    }

    #[test]
    fn parse_display_group_unknown_falls_back_to_other() {
        assert_eq!(
            parse_display_group("unknown_future_variant"),
            DisplayGroup::Other
        );
        assert_eq!(parse_display_group(""), DisplayGroup::Other);
    }

    // ---- ci_checks_from_json ------------------------------------------------

    #[test]
    fn ci_checks_from_json_maps_code_and_gate() {
        let json_ci = JsonCiChecks {
            code: vec![JsonCheckInfo {
                name: "tests".to_string(),
                state: "passing".to_string(),
                details_url: Some("https://ci.example.com/1".to_string()),
            }],
            gate: vec![JsonCheckInfo {
                name: "license-check".to_string(),
                state: "failing".to_string(),
                details_url: None,
            }],
        };
        let ci = ci_checks_from_json(&json_ci);
        assert_eq!(ci.code.len(), 1);
        assert_eq!(ci.code[0].name, "tests");
        assert_eq!(ci.code[0].state, "passing");
        assert_eq!(
            ci.code[0].details_url,
            Some("https://ci.example.com/1".to_string())
        );
        assert_eq!(ci.gate.len(), 1);
        assert_eq!(ci.gate[0].name, "license-check");
        assert_eq!(ci.gate[0].state, "failing");
    }
}
