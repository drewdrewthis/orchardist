//! Wire types for the daemon GraphQL responses the TUI consumes.
//!
//! These are deliberately narrow — only the fields the TUI reads. They mirror
//! `schema.graphql` (see ADR-011) but ignore everything else. Adding a field
//! here is cheap; just ask for it in the query string and bump the struct.

use serde::Deserialize;

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
}
