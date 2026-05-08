//! Wire types for the daemon GraphQL responses the TUI consumes.
//!
//! These are deliberately narrow — only the fields the TUI reads. They mirror
//! `schema.graphql` (see ADR-011) but ignore everything else. Adding a field
//! here is cheap; just ask for it in the query string and bump the struct.
//!
//! # Key types
//!
//! - [`WorkViewSnapshot`] — the primary composite read for TUI refresh. One
//!   round-trip to the daemon yields everything the TUI needs for local data.
//! - [`GraphQlResponse`] / [`GraphQlError`] — generic envelope wrapping every
//!   query response.

use serde::{Deserialize, Serialize};

/// Top-level GraphQL response envelope.
#[derive(Debug, Deserialize)]
pub struct GraphQlResponse<T> {
    /// Resolved data, when the request succeeded.
    pub data: Option<T>,

    /// GraphQL-level errors. May coexist with partial `data`.
    #[serde(default)]
    pub errors: Vec<GraphQlError>,
}

/// One GraphQL error entry.
#[derive(Debug, Deserialize)]
pub struct GraphQlError {
    /// Human-readable error message.
    pub message: String,
}

/// `Query.health` payload — used as a connectivity probe.
#[derive(Debug, Deserialize)]
pub struct HealthPayload {
    /// `health` field result.
    pub health: Health,
}

/// Health node.
#[derive(Debug, Deserialize)]
pub struct Health {
    /// Status string, "ok" when serving.
    pub status: String,
    /// Daemon uptime in seconds.
    #[serde(rename = "uptimeS")]
    pub uptime_s: i64,
}

/// `Query.tmuxSessions` payload — local sessions on the queried daemon.
#[derive(Debug, Deserialize)]
pub struct TmuxSessionsPayload {
    /// `tmuxSessions` field result.
    #[serde(rename = "tmuxSessions")]
    pub tmux_sessions: Vec<TmuxSession>,
}

/// One tmux session as exposed by the daemon. Narrow projection.
#[derive(Debug, Clone, Deserialize)]
pub struct TmuxSession {
    /// Globally-unique node id (`TmuxSession:<host>:<sessionName>`).
    pub id: String,

    /// Session name as known to the tmux server.
    pub name: String,

    /// True when at least one client is attached.
    #[serde(default)]
    pub attached: bool,

    /// True when an attached client has been active recently.
    #[serde(default, rename = "activeAttached")]
    pub active_attached: bool,

    /// RFC3339 timestamp of most recent activity. Optional in v1.
    #[serde(default, rename = "lastActivityAt")]
    pub last_activity_at: Option<String>,
}

/// `Query.host` payload — local host metadata.
#[derive(Debug, Deserialize)]
pub struct HostPayload {
    /// `host` field result.
    pub host: HostNode,
}

/// `Query.hosts` payload — every host the daemon knows about (local + peers).
#[derive(Debug, Deserialize)]
pub struct HostsPayload {
    /// `hosts` field result.
    pub hosts: Vec<HostNode>,
}

/// One host as exposed by the daemon. Narrow projection.
#[derive(Debug, Clone, Deserialize)]
pub struct HostNode {
    /// Globally-unique node id (`Host:<machineId>`).
    pub id: String,

    /// OS-reported hostname.
    pub hostname: String,

    /// Reachable network address. Null for the local host; populated for peers.
    #[serde(default)]
    pub address: Option<String>,

    /// True when the daemon has reached the host recently.
    #[serde(default)]
    pub reachable: bool,

    /// Peer hosts this daemon federates with.
    #[serde(default)]
    pub peers: Vec<HostNode>,
}

// ============================================================
//  WorkView — composite read for TUI local-data refresh
// ============================================================

/// Top-level `Query.workView` payload — one round-trip supplies everything
/// the TUI needs for local worktrees, tmux sessions, and Claude instances.
#[derive(Debug, Deserialize)]
pub struct WorkViewPayload {
    /// `workView` field result.
    #[serde(rename = "workView")]
    pub work_view: WorkViewSnapshot,
}

/// Complete snapshot of local orchard state as delivered by the daemon's
/// `workView` query. This is the primary input for Phase 1 TUI refresh.
///
/// All three top-level collections are always present (they may be empty
/// vectors when there is nothing to report).
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewSnapshot {
    /// One entry per git project root the daemon knows about.
    pub projects: Vec<WorkViewProject>,

    /// All tmux sessions on the local host.
    #[serde(rename = "tmuxSessions")]
    pub tmux_sessions: Vec<WorkViewTmuxSession>,

    /// All Claude Code instances on the local host.
    #[serde(rename = "claudeInstances")]
    pub claude_instances: Vec<ClaudeInstance>,
}

/// One git project (repository root) as exposed by `workView`.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewProject {
    /// Project name (usually the repository basename).
    pub name: String,

    /// Absolute path to the project root / bare repository.
    pub directory: String,

    /// Worktrees belonging to this project.
    pub worktrees: Vec<WorkViewWorktree>,
}

/// One git worktree as exposed by `workView`. PR and issue joins are
/// performed **daemon-side** and delivered pre-enriched.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewWorktree {
    /// Absolute path to the worktree on disk.
    pub path: String,

    /// Active branch name (`refs/heads/…` stripped).
    pub branch: String,

    /// HEAD commit SHA.
    pub head: String,

    /// True for bare worktrees (the linked-worktree root).
    pub bare: bool,

    /// Host identifier. In daemon v1 this is always `"local"`.
    pub host: String,

    /// Repository slug (`owner/repo`).
    pub repo: String,

    /// Open PR whose head branch matches this worktree's branch. `None` when
    /// there is no open matching PR. Note: closed/merged PRs are **not**
    /// included in v1 (see research/035 for the schema limitation).
    pub pr: Option<WorkViewPr>,

    /// Issue linked to this worktree (via PR closing keywords or branch
    /// convention). `None` when no issue is detected.
    pub issue: Option<WorkViewIssue>,
}

/// Pull request fields carried in a `workView` worktree. Narrow projection —
/// only the badges the TUI renders.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewPr {
    /// GitHub PR number.
    pub number: u64,

    /// PR state string: `"OPEN"`, `"CLOSED"`, or `"MERGED"`.
    pub state: String,

    /// PR title.
    pub title: String,

    /// Aggregated CI status: `"SUCCESS"`, `"FAILURE"`, `"PENDING"`, etc.
    #[serde(default, rename = "statusCheckRollup")]
    pub status_check_rollup: Option<String>,

    /// Latest review decision: `"APPROVED"`, `"CHANGES_REQUESTED"`, etc.
    #[serde(default, rename = "reviewDecision")]
    pub review_decision: Option<String>,

    /// GitHub merge-state status: `"CLEAN"`, `"BLOCKED"`, `"DIRTY"`, etc.
    #[serde(default, rename = "mergeStateStatus")]
    pub merge_state_status: Option<String>,

    /// Whether the PR is a draft.
    #[serde(default)]
    pub draft: bool,

    /// Label names applied to the PR.
    #[serde(default)]
    pub labels: Vec<String>,
}

/// Issue fields carried in a `workView` worktree. Narrow projection.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewIssue {
    /// GitHub issue number.
    pub number: u64,

    /// Issue state string: `"OPEN"` or `"CLOSED"`.
    pub state: String,

    /// Issue title.
    pub title: String,
}

/// One tmux session as exposed by `workView.tmuxSessions`. Extends the
/// existing [`TmuxSession`] with window metadata needed for session display.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct WorkViewTmuxSession {
    /// Globally-unique node id (`TmuxSession:<host>:<sessionName>`).
    pub id: String,

    /// Session name as known to the tmux server.
    pub name: String,

    /// True when at least one client is attached.
    #[serde(default)]
    pub attached: bool,

    /// True when an attached client has been active recently.
    #[serde(default, rename = "activeAttached")]
    pub active_attached: bool,

    /// RFC3339 timestamp of most recent activity.
    #[serde(default, rename = "lastActivityAt")]
    pub last_activity_at: Option<String>,

    /// Number of clients currently attached.
    #[serde(default, rename = "attachedClients")]
    pub attached_clients: u32,

    /// Number of windows in this session.
    #[serde(default)]
    pub windows: u32,

    /// Name of the currently active window.
    #[serde(default, rename = "currentWindow")]
    pub current_window: Option<String>,

    /// Working directory of the session's active pane. Used by the client-side
    /// adapter to match sessions to worktrees via [`crate::paths::session_belongs_to_worktree`].
    ///
    /// Absent in older daemon versions; falls back to name-based matching when `None`.
    #[serde(default)]
    pub path: Option<String>,
}

/// One Claude Code instance as exposed by `workView.claudeInstances`.
///
/// The sessions↔claude join is performed **client-side** by walking
/// `ClaudeInstance.pane → TmuxPane → TmuxSession`. No daemon-side join field
/// is available for this relationship.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct ClaudeInstance {
    /// Globally-unique node id.
    pub id: String,

    /// Pane reference used to locate the tmux session this instance lives in.
    /// Format: `TmuxPane:<host>:<session>:<window>:<pane>`.
    pub pane: String,

    /// Process identifier (PID as string or process name).
    pub process: String,

    /// Current activity state: `"idle"`, `"working"`, `"waiting"`, etc.
    pub state: String,

    /// Claude session UUID for resuming sessions.
    #[serde(default, rename = "sessionUuid")]
    pub session_uuid: Option<String>,

    /// Whether RC file integration is enabled for this instance.
    #[serde(default, rename = "rcEnabled")]
    pub rc_enabled: bool,

    /// RFC3339 timestamp of most recent activity.
    #[serde(default, rename = "lastActivityAt")]
    pub last_activity_at: Option<String>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_health_envelope() {
        let raw = r#"{"data":{"health":{"status":"ok","uptimeS":42}}}"#;
        let env: GraphQlResponse<HealthPayload> = serde_json::from_str(raw).unwrap();
        let h = env.data.unwrap().health;
        assert_eq!(h.status, "ok");
        assert_eq!(h.uptime_s, 42);
    }

    #[test]
    fn parses_tmux_sessions() {
        let raw = r#"{
            "data": {
                "tmuxSessions": [
                    {"id":"TmuxSession:H:a","name":"a","attached":true,"activeAttached":true,"lastActivityAt":"2026-05-05T12:00:00Z"},
                    {"id":"TmuxSession:H:b","name":"b","attached":false,"activeAttached":false}
                ]
            }
        }"#;
        let env: GraphQlResponse<TmuxSessionsPayload> = serde_json::from_str(raw).unwrap();
        let sessions = env.data.unwrap().tmux_sessions;
        assert_eq!(sessions.len(), 2);
        assert_eq!(sessions[0].name, "a");
        assert!(sessions[0].attached);
        assert!(!sessions[1].active_attached);
        assert!(sessions[1].last_activity_at.is_none());
    }

    #[test]
    fn parses_hosts_with_peers() {
        let raw = r#"{
            "data": {
                "hosts": [
                    {"id":"Host:local","hostname":"local","address":null,"reachable":true,
                     "peers":[
                        {"id":"Host:p1","hostname":"box-1","address":"box-1.boxd.sh","reachable":true,"peers":[]}
                     ]
                    }
                ]
            }
        }"#;
        let env: GraphQlResponse<HostsPayload> = serde_json::from_str(raw).unwrap();
        let hosts = env.data.unwrap().hosts;
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts[0].hostname, "local");
        assert!(hosts[0].address.is_none());
        assert_eq!(hosts[0].peers.len(), 1);
        assert_eq!(hosts[0].peers[0].address.as_deref(), Some("box-1.boxd.sh"));
    }

    #[test]
    fn surfaces_graphql_errors() {
        let raw = r#"{"errors":[{"message":"introspection disabled"}],"data":null}"#;
        let env: GraphQlResponse<HealthPayload> = serde_json::from_str(raw).unwrap();
        assert!(env.data.is_none());
        assert_eq!(env.errors.len(), 1);
        assert_eq!(env.errors[0].message, "introspection disabled");
    }

    // -----------------------------------------------------------------------
    //  WorkViewSnapshot parser tests
    // -----------------------------------------------------------------------

    /// Fixture representing a minimal valid `workView` GraphQL response. Covers
    /// all fields required by the Phase 1 implementation: one project with one
    /// worktree carrying a PR+issue join, one tmux session with window metadata,
    /// and one Claude instance.
    const WORK_VIEW_FIXTURE: &str = r#"{
        "data": {
            "workView": {
                "projects": [
                    {
                        "name": "git-orchard-rs",
                        "directory": "/home/example/workspace/git-orchard-rs",
                        "worktrees": [
                            {
                                "path": "/home/example/workspace/git-orchard-rs/.worktrees/issue429/rip-cache",
                                "branch": "issue429/rip-cache-sources",
                                "head": "abc1234def5678",
                                "bare": false,
                                "host": "local",
                                "repo": "drewdrewthis/git-orchard-rs",
                                "pr": {
                                    "number": 429,
                                    "state": "OPEN",
                                    "title": "Rip cache_sources from TUI",
                                    "statusCheckRollup": "SUCCESS",
                                    "reviewDecision": "APPROVED",
                                    "mergeStateStatus": "CLEAN",
                                    "draft": false,
                                    "labels": ["enhancement", "phase-1"]
                                },
                                "issue": {
                                    "number": 429,
                                    "state": "OPEN",
                                    "title": "Rip cache_sources from TUI dashboard refresh"
                                }
                            },
                            {
                                "path": "/home/example/workspace/git-orchard-rs",
                                "branch": "main",
                                "head": "deadbeef",
                                "bare": false,
                                "host": "local",
                                "repo": "drewdrewthis/git-orchard-rs",
                                "pr": null,
                                "issue": null
                            }
                        ]
                    }
                ],
                "tmuxSessions": [
                    {
                        "id": "TmuxSession:local:issue429",
                        "name": "issue429",
                        "attached": true,
                        "activeAttached": true,
                        "lastActivityAt": "2026-05-08T10:00:00Z",
                        "attachedClients": 1,
                        "windows": 3,
                        "currentWindow": "editor"
                    }
                ],
                "claudeInstances": [
                    {
                        "id": "ClaudeInstance:local:12345",
                        "pane": "TmuxPane:local:issue429:editor:0",
                        "process": "claude",
                        "state": "working",
                        "sessionUuid": "550e8400-e29b-41d4-a716-446655440000",
                        "rcEnabled": true,
                        "lastActivityAt": "2026-05-08T10:01:00Z"
                    }
                ]
            }
        }
    }"#;

    #[test]
    fn parses_work_view_snapshot_envelope() {
        let env: GraphQlResponse<WorkViewPayload> =
            serde_json::from_str(WORK_VIEW_FIXTURE).unwrap();
        let snapshot = env.data.unwrap().work_view;

        // projects
        assert_eq!(snapshot.projects.len(), 1);
        let project = &snapshot.projects[0];
        assert_eq!(project.name, "git-orchard-rs");
        assert_eq!(
            project.directory,
            "/home/example/workspace/git-orchard-rs"
        );

        // worktrees
        assert_eq!(project.worktrees.len(), 2);
        let wt = &project.worktrees[0];
        assert_eq!(wt.branch, "issue429/rip-cache-sources");
        assert_eq!(wt.head, "abc1234def5678");
        assert!(!wt.bare);
        assert_eq!(wt.host, "local");
        assert_eq!(wt.repo, "drewdrewthis/git-orchard-rs");

        // worktree without PR/issue
        assert!(project.worktrees[1].pr.is_none());
        assert!(project.worktrees[1].issue.is_none());
    }

    #[test]
    fn parses_work_view_pr_fields() {
        let env: GraphQlResponse<WorkViewPayload> =
            serde_json::from_str(WORK_VIEW_FIXTURE).unwrap();
        let wt = &env.data.unwrap().work_view.projects[0].worktrees[0];
        let pr = wt.pr.as_ref().unwrap();

        assert_eq!(pr.number, 429);
        assert_eq!(pr.state, "OPEN");
        assert_eq!(pr.title, "Rip cache_sources from TUI");
        assert_eq!(pr.status_check_rollup.as_deref(), Some("SUCCESS"));
        assert_eq!(pr.review_decision.as_deref(), Some("APPROVED"));
        assert_eq!(pr.merge_state_status.as_deref(), Some("CLEAN"));
        assert!(!pr.draft);
        assert_eq!(pr.labels, vec!["enhancement", "phase-1"]);
    }

    #[test]
    fn parses_work_view_issue_fields() {
        let env: GraphQlResponse<WorkViewPayload> =
            serde_json::from_str(WORK_VIEW_FIXTURE).unwrap();
        let wt = &env.data.unwrap().work_view.projects[0].worktrees[0];
        let issue = wt.issue.as_ref().unwrap();

        assert_eq!(issue.number, 429);
        assert_eq!(issue.state, "OPEN");
        assert_eq!(issue.title, "Rip cache_sources from TUI dashboard refresh");
    }

    #[test]
    fn parses_work_view_tmux_session_fields() {
        let env: GraphQlResponse<WorkViewPayload> =
            serde_json::from_str(WORK_VIEW_FIXTURE).unwrap();
        let snapshot = env.data.unwrap().work_view;
        assert_eq!(snapshot.tmux_sessions.len(), 1);
        let s = &snapshot.tmux_sessions[0];

        assert_eq!(s.id, "TmuxSession:local:issue429");
        assert_eq!(s.name, "issue429");
        assert!(s.attached);
        assert!(s.active_attached);
        assert_eq!(s.last_activity_at.as_deref(), Some("2026-05-08T10:00:00Z"));
        assert_eq!(s.attached_clients, 1);
        assert_eq!(s.windows, 3);
        assert_eq!(s.current_window.as_deref(), Some("editor"));
    }

    #[test]
    fn parses_work_view_claude_instance_fields() {
        let env: GraphQlResponse<WorkViewPayload> =
            serde_json::from_str(WORK_VIEW_FIXTURE).unwrap();
        let snapshot = env.data.unwrap().work_view;
        assert_eq!(snapshot.claude_instances.len(), 1);
        let ci = &snapshot.claude_instances[0];

        assert_eq!(ci.id, "ClaudeInstance:local:12345");
        assert_eq!(ci.pane, "TmuxPane:local:issue429:editor:0");
        assert_eq!(ci.process, "claude");
        assert_eq!(ci.state, "working");
        assert_eq!(
            ci.session_uuid.as_deref(),
            Some("550e8400-e29b-41d4-a716-446655440000")
        );
        assert!(ci.rc_enabled);
        assert_eq!(
            ci.last_activity_at.as_deref(),
            Some("2026-05-08T10:01:00Z")
        );
    }

    #[test]
    fn parses_work_view_optional_fields_absent() {
        // Minimal WorkViewSnapshot: empty collections, optional fields absent.
        let raw = r#"{
            "data": {
                "workView": {
                    "projects": [],
                    "tmuxSessions": [],
                    "claudeInstances": []
                }
            }
        }"#;
        let env: GraphQlResponse<WorkViewPayload> = serde_json::from_str(raw).unwrap();
        let snapshot = env.data.unwrap().work_view;
        assert!(snapshot.projects.is_empty());
        assert!(snapshot.tmux_sessions.is_empty());
        assert!(snapshot.claude_instances.is_empty());
    }

    #[test]
    fn parses_work_view_pr_optional_fields_default() {
        // PR with only required fields — optional fields should default cleanly.
        let raw = r#"{
            "data": {
                "workView": {
                    "projects": [{
                        "name": "repo",
                        "directory": "/repo",
                        "worktrees": [{
                            "path": "/repo/wt",
                            "branch": "feat/x",
                            "head": "cafe",
                            "bare": false,
                            "host": "local",
                            "repo": "owner/repo",
                            "pr": {
                                "number": 1,
                                "state": "OPEN",
                                "title": "Fix it"
                            },
                            "issue": null
                        }]
                    }],
                    "tmuxSessions": [],
                    "claudeInstances": []
                }
            }
        }"#;
        let env: GraphQlResponse<WorkViewPayload> = serde_json::from_str(raw).unwrap();
        let wt = &env.data.unwrap().work_view.projects[0].worktrees[0];
        let pr = wt.pr.as_ref().unwrap();
        assert!(pr.status_check_rollup.is_none());
        assert!(pr.review_decision.is_none());
        assert!(pr.merge_state_status.is_none());
        assert!(!pr.draft);
        assert!(pr.labels.is_empty());
    }
}
