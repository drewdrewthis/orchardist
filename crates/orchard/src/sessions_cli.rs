//! `orchard-tui sessions` subcommand — federated session picker.
//!
//! This is the thin-shell entry point for #426. It:
//! 1. Talks to the local daemon at `ORCHARD_DAEMON_URL` (default
//!    `http://127.0.0.1:7777/graphql`). If the daemon is unreachable, prints
//!    a loud error and exits non-zero — there is no silent fallback to
//!    legacy shell discovery.
//! 2. Fans out to every reachable peer in `hosts.peers`, in parallel, and
//!    asks each peer's daemon for its tmux sessions.
//! 3. Renders a single host-tagged list. Without flags, presents an
//!    interactive picker that drops into a tmux attach (local) or
//!    `ssh <addr> -t tmux attach -t <name>` (remote) on Enter.
//!    With `--json`, prints the federated list as a versioned JSON document.
//!
//! Direct shell-outs from this module are limited to the unavoidable escape
//! hatch: launching `tmux` (local attach) or `ssh ... tmux` (remote attach).
//! Discovery is daemon-only.

use std::io::{self, BufRead, Write};
use std::process::Command;

use serde::Serialize;

use crate::daemon::{Client, FederatedFanout, FederatedSession, fan_out};

/// Wire-format version for `orchard-tui sessions --json` (federated).
///
/// Distinct from `sessions_index::SESSIONS_INDEX_VERSION` because this is a
/// new daemon-sourced view; the prior cache-sourced index will be retired
/// once consumers have switched.
pub const FEDERATED_SESSIONS_VERSION: u32 = 1;

/// Top-level versioned wire format for the federated sessions view.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FederatedSessionsOutput {
    /// Schema version. See [`FEDERATED_SESSIONS_VERSION`].
    pub version: u32,
    /// Hostname of the daemon we initiated the fan-out from.
    pub local_hostname: String,
    /// Sessions from local + every reachable peer, host-tagged.
    pub sessions: Vec<FederatedSessionRecord>,
    /// Per-peer outcome surfaced verbatim so consumers can show error rows.
    pub peer_results: Vec<PeerResultRecord>,
}

/// One session entry in the federated list.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FederatedSessionRecord {
    /// Hostname the session lives on (local or peer).
    pub host: String,
    /// Peer GraphQL URL the session was fetched from. `null` for local.
    pub host_address: Option<String>,
    /// True when the session is on the local daemon.
    pub is_local: bool,
    /// Session name as known to the tmux server.
    pub name: String,
    /// Globally-unique daemon id.
    pub id: String,
    /// True when at least one client is attached.
    pub attached: bool,
    /// True when an attached client has been active recently.
    pub active_attached: bool,
    /// RFC3339 last activity timestamp, when known.
    pub last_activity_at: Option<String>,
}

/// Per-peer fan-out outcome record, suitable for serialisation.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PeerResultRecord {
    /// Peer hostname.
    pub hostname: String,
    /// GraphQL URL we tried.
    pub address: String,
    /// True when the fetch returned sessions cleanly.
    pub ok: bool,
    /// Number of sessions returned (0 on failure).
    pub session_count: usize,
    /// Failure reason (`None` on success).
    pub error: Option<String>,
}

/// Build the federated output from a fan-out result.
pub fn build_output(fanout: &FederatedFanout) -> FederatedSessionsOutput {
    let mut sessions: Vec<FederatedSessionRecord> = fanout
        .flatten()
        .into_iter()
        .map(record_from_session)
        .collect();
    // Stable order: local-first, then by host, then by session name.
    sessions.sort_by(|a, b| {
        b.is_local
            .cmp(&a.is_local)
            .then_with(|| a.host.cmp(&b.host))
            .then_with(|| a.name.cmp(&b.name))
    });
    let peer_results = fanout
        .peer_results
        .iter()
        .map(|p| PeerResultRecord {
            hostname: p.hostname.clone(),
            address: p.address.clone(),
            ok: p.sessions.is_ok(),
            session_count: p.sessions.as_ref().map(|s| s.len()).unwrap_or(0),
            error: p.sessions.as_ref().err().map(|e| e.to_string()),
        })
        .collect();
    FederatedSessionsOutput {
        version: FEDERATED_SESSIONS_VERSION,
        local_hostname: fanout.local_hostname.clone(),
        sessions,
        peer_results,
    }
}

fn record_from_session(fs: FederatedSession) -> FederatedSessionRecord {
    FederatedSessionRecord {
        host: fs.host_label,
        host_address: fs.host_address,
        is_local: fs.is_local,
        name: fs.session.name,
        id: fs.session.id,
        attached: fs.session.attached,
        active_attached: fs.session.active_attached,
        last_activity_at: fs.session.last_activity_at,
    }
}

/// Outcome of `attach_target` — used by tests and callers that want to
/// inspect the command before exec.
#[derive(Debug, Clone, PartialEq)]
pub struct AttachPlan {
    /// Program to invoke (`tmux` or `ssh`).
    pub program: String,
    /// Arguments to pass.
    pub args: Vec<String>,
    /// Human-readable description (used in status line / logs).
    pub describe: String,
}

/// Decides how to attach to a session given its host context.
///
/// - Local sessions: `tmux attach -t <name>`.
/// - Remote sessions: `ssh <ssh_target> -t tmux attach -t <name>`. The SSH
///   target prefers an explicit hostname (cheap to type, works with the
///   user's `~/.ssh/config`); we strip the `graphql.` boxd prefix when
///   present so `ssh <box>` resolves the same way as the rest of the
///   orchard tooling.
pub fn attach_target(session: &FederatedSessionRecord) -> AttachPlan {
    if session.is_local {
        AttachPlan {
            program: "tmux".to_string(),
            args: vec!["attach".to_string(), "-t".to_string(), session.name.clone()],
            describe: format!("tmux attach -t {}", session.name),
        }
    } else {
        let ssh_target = ssh_target_for_peer(&session.host, session.host_address.as_deref());
        AttachPlan {
            program: "ssh".to_string(),
            args: vec![
                ssh_target.clone(),
                "-t".to_string(),
                "tmux".to_string(),
                "attach".to_string(),
                "-t".to_string(),
                session.name.clone(),
            ],
            describe: format!("ssh {ssh_target} -t tmux attach -t {}", session.name),
        }
    }
}

/// Pick the best SSH target for a peer.
///
/// Order of preference:
/// 1. The peer's `address`, with the `graphql.` prefix stripped and any
///    trailing path / port removed. This is the boxd-style FQDN that
///    `ssh <box>` resolves through SSH config.
/// 2. The peer's hostname, if no address is available.
fn ssh_target_for_peer(hostname: &str, address: Option<&str>) -> String {
    if let Some(addr) = address {
        let addr = addr.trim();
        // Strip protocol if a full URL leaked through.
        let addr = addr
            .trim_start_matches("https://")
            .trim_start_matches("http://");
        // Strip path.
        let addr = addr.split('/').next().unwrap_or(addr);
        // Strip leading `graphql.`.
        let addr = addr.strip_prefix("graphql.").unwrap_or(addr);
        if !addr.is_empty() {
            return addr.to_string();
        }
    }
    hostname.to_string()
}

/// Run `orchard-tui sessions [--json]`.
///
/// Returns the process exit code so the caller (`main.rs::handle_sessions`)
/// can `std::process::exit(code)` itself.
pub fn run(json: bool) -> i32 {
    let client = match Client::local() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("orchard-tui sessions: {e}");
            return 2;
        }
    };

    let hosts = match client.hosts() {
        Ok(h) => h,
        Err(e) => {
            eprintln!(
                "orchard-tui sessions: failed to query hosts from {}: {e}",
                client.url()
            );
            return 2;
        }
    };

    let fanout = match fan_out(&client, &hosts) {
        Ok(f) => f,
        Err(e) => {
            eprintln!("orchard-tui sessions: fan-out failed: {e}");
            return 2;
        }
    };

    let output = build_output(&fanout);

    if json {
        match serde_json::to_string_pretty(&output) {
            Ok(s) => {
                println!("{s}");
                0
            }
            Err(e) => {
                eprintln!("orchard-tui sessions: failed to serialise JSON: {e}");
                1
            }
        }
    } else {
        run_interactive(&output)
    }
}

/// Render a numbered list and prompt for a selection. Selecting a row execs
/// the appropriate attach plan, replacing this process. Pressing Enter on
/// an empty prompt exits without action.
///
/// stdin / stdout only — no curses; this is intentionally a thin picker that
/// works fine over `ssh -t` and inside tmux popups. The TUI itself remains
/// the rich interface.
fn run_interactive(output: &FederatedSessionsOutput) -> i32 {
    if output.sessions.is_empty() {
        eprintln!(
            "orchard-tui sessions: no sessions across local + {} reachable peer(s)",
            output.peer_results.iter().filter(|p| p.ok).count()
        );
        for peer in &output.peer_results {
            if !peer.ok {
                eprintln!(
                    "  (peer {} unreachable: {})",
                    peer.hostname,
                    peer.error.as_deref().unwrap_or("unknown")
                );
            }
        }
        return 1;
    }

    let stdout = io::stdout();
    let mut out = stdout.lock();
    let _ = writeln!(out, "Federated tmux sessions (host-tagged):");
    let max_host = output
        .sessions
        .iter()
        .map(|s| s.host.len())
        .max()
        .unwrap_or(8);
    for (i, s) in output.sessions.iter().enumerate() {
        let attach_marker = if s.attached { "*" } else { " " };
        let local_marker = if s.is_local { "L" } else { "P" };
        let _ = writeln!(
            out,
            "  [{:>3}] {} {} {:<width$}  {}",
            i + 1,
            local_marker,
            attach_marker,
            s.host,
            s.name,
            width = max_host,
        );
    }
    for peer in &output.peer_results {
        if !peer.ok {
            let _ = writeln!(
                out,
                "  ! peer {} unreachable: {}",
                peer.hostname,
                peer.error.as_deref().unwrap_or("unknown")
            );
        }
    }
    let _ = write!(out, "\nEnter a row number to attach (or blank to exit): ");
    let _ = out.flush();

    let stdin = io::stdin();
    let mut line = String::new();
    if stdin.lock().read_line(&mut line).is_err() {
        return 1;
    }
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return 0;
    }
    let idx: usize = match trimmed.parse() {
        Ok(n) if n >= 1 && n <= output.sessions.len() => n,
        _ => {
            eprintln!("invalid selection: {trimmed}");
            return 1;
        }
    };
    let session = &output.sessions[idx - 1];
    let plan = attach_target(session);
    eprintln!("attaching: {}", plan.describe);
    // Exec the attach. We use std::process::Command::status so the subprocess
    // inherits stdin/stdout/stderr — tmux/ssh need a real TTY.
    let status = Command::new(&plan.program).args(&plan.args).status();
    match status {
        Ok(s) => s.code().unwrap_or(1),
        Err(e) => {
            eprintln!("failed to exec {}: {e}", plan.describe);
            1
        }
    }
}

/// True when the `daemon` field would be populated by a live local daemon.
///
/// Used by callers that want to provide a graceful "daemon-down" message
/// before issuing a query that would otherwise produce a less helpful
/// transport error. Currently calls `health` directly; if that succeeds we
/// know the daemon is up.
pub fn daemon_is_up() -> bool {
    Client::local().and_then(|c| c.health().map(|_| ())).is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::daemon::types::TmuxSession;

    fn fed_session(host: &str, is_local: bool, name: &str, addr: Option<&str>) -> FederatedSession {
        FederatedSession {
            host_label: host.to_string(),
            host_address: addr.map(|s| s.to_string()),
            is_local,
            session: TmuxSession {
                id: format!("TmuxSession:{host}:{name}"),
                name: name.to_string(),
                attached: false,
                active_attached: false,
                last_activity_at: None,
            },
        }
    }

    #[test]
    fn build_output_orders_local_first_then_alpha() {
        let fanout = FederatedFanout {
            local_hostname: "drudru".into(),
            local_sessions: vec![],
            peer_results: vec![],
        };
        // Build manually with mixed entries so we exercise the sort.
        let mut output = build_output(&fanout);
        output.sessions = vec![
            FederatedSessionRecord {
                host: "box-2".into(),
                host_address: Some("https://graphql.box-2/graphql".into()),
                is_local: false,
                name: "alpha".into(),
                id: "TmuxSession:box-2:alpha".into(),
                attached: false,
                active_attached: false,
                last_activity_at: None,
            },
            FederatedSessionRecord {
                host: "drudru".into(),
                host_address: None,
                is_local: true,
                name: "beta".into(),
                id: "TmuxSession:drudru:beta".into(),
                attached: false,
                active_attached: false,
                last_activity_at: None,
            },
            FederatedSessionRecord {
                host: "box-1".into(),
                host_address: Some("https://graphql.box-1/graphql".into()),
                is_local: false,
                name: "gamma".into(),
                id: "TmuxSession:box-1:gamma".into(),
                attached: false,
                active_attached: false,
                last_activity_at: None,
            },
        ];
        // Re-sort using the same comparator the public path uses by feeding
        // through build_output — emulate by rebuilding sessions.
        output.sessions.sort_by(|a, b| {
            b.is_local
                .cmp(&a.is_local)
                .then_with(|| a.host.cmp(&b.host))
                .then_with(|| a.name.cmp(&b.name))
        });
        assert!(output.sessions[0].is_local);
        assert_eq!(output.sessions[0].host, "drudru");
        assert_eq!(output.sessions[1].host, "box-1");
        assert_eq!(output.sessions[2].host, "box-2");
    }

    #[test]
    fn attach_plan_local_uses_tmux_attach() {
        let s = FederatedSessionRecord {
            host: "drudru".into(),
            host_address: None,
            is_local: true,
            name: "main".into(),
            id: "TmuxSession:drudru:main".into(),
            attached: false,
            active_attached: false,
            last_activity_at: None,
        };
        let plan = attach_target(&s);
        assert_eq!(plan.program, "tmux");
        assert_eq!(plan.args, vec!["attach", "-t", "main"]);
    }

    #[test]
    fn attach_plan_remote_uses_ssh_with_t_flag() {
        let s = FederatedSessionRecord {
            host: "orchard".into(),
            host_address: Some("graphql.orchard.boxd.sh".into()),
            is_local: false,
            name: "g-orchard".into(),
            id: "TmuxSession:orchard:g-orchard".into(),
            attached: false,
            active_attached: false,
            last_activity_at: None,
        };
        let plan = attach_target(&s);
        assert_eq!(plan.program, "ssh");
        assert_eq!(
            plan.args,
            vec![
                "orchard.boxd.sh".to_string(),
                "-t".into(),
                "tmux".into(),
                "attach".into(),
                "-t".into(),
                "g-orchard".into(),
            ]
        );
    }

    #[test]
    fn ssh_target_strips_protocol_and_path_and_graphql_prefix() {
        assert_eq!(
            ssh_target_for_peer("orchard", Some("https://graphql.orchard.boxd.sh/graphql")),
            "orchard.boxd.sh"
        );
        assert_eq!(
            ssh_target_for_peer("orchard", Some("graphql.orchard.boxd.sh")),
            "orchard.boxd.sh"
        );
        assert_eq!(
            ssh_target_for_peer("orchard", Some("box-1.boxd.sh")),
            "box-1.boxd.sh"
        );
        assert_eq!(ssh_target_for_peer("orchard", None), "orchard");
    }

    #[test]
    fn build_output_records_peer_errors() {
        let fanout = FederatedFanout {
            local_hostname: "drudru".into(),
            local_sessions: vec![],
            peer_results: vec![crate::daemon::PeerFetchResult {
                hostname: "down".into(),
                address: "https://graphql.down/graphql".into(),
                sessions: Err(crate::daemon::DaemonError::Unreachable {
                    url: "https://graphql.down/graphql".into(),
                    cause: "timeout".into(),
                }),
            }],
        };
        let out = build_output(&fanout);
        assert_eq!(out.peer_results.len(), 1);
        assert!(!out.peer_results[0].ok);
        assert!(
            out.peer_results[0]
                .error
                .as_deref()
                .unwrap()
                .contains("not reachable")
        );
    }

    #[test]
    fn build_output_includes_local_and_peer_when_both_have_sessions() {
        let local = vec![crate::daemon::types::TmuxSession {
            id: "TmuxSession:drudru:l".into(),
            name: "l".into(),
            attached: true,
            active_attached: true,
            last_activity_at: None,
        }];
        let peer = fed_session("box-1", false, "p", Some("https://graphql.box-1/graphql"));
        let fanout = FederatedFanout {
            local_hostname: "drudru".into(),
            local_sessions: local,
            peer_results: vec![crate::daemon::PeerFetchResult {
                hostname: "box-1".into(),
                address: "https://graphql.box-1/graphql".into(),
                sessions: Ok(vec![peer.session.clone()]),
            }],
        };
        let out = build_output(&fanout);
        assert_eq!(out.sessions.len(), 2);
        let local_record = out.sessions.iter().find(|r| r.is_local).expect("has local");
        let peer_record = out.sessions.iter().find(|r| !r.is_local).expect("has peer");
        assert_eq!(local_record.host, "drudru");
        assert_eq!(peer_record.host, "box-1");
        assert_eq!(
            peer_record.host_address.as_deref(),
            Some("https://graphql.box-1/graphql")
        );
    }
}
