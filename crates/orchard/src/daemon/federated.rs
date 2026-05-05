//! Federated session fan-out.
//!
//! Today the daemon's `Query.tmuxSessions` only returns sessions running on
//! the queried host. To answer "every session orchard knows about", we have
//! to fan out from the TUI: query the local daemon for sessions, ask the
//! local daemon for `hosts.peers`, then query each reachable peer's daemon
//! for its sessions. Issue #425 will eventually flatten this so the local
//! daemon aggregates peers itself; until that lands, this module owns the
//! fan-out.
//!
//! Concurrency: we fan out to peers with [`std::thread::scope`]. Each peer
//! response is independent; one slow / unreachable peer cannot delay the
//! others past the per-client timeout. Errors are kept per-peer so the TUI
//! can render the federated list with explicit "<host> unreachable" rows
//! instead of failing the whole call.

use super::client::{Client, peer_url};
use super::types::{HostNode, TmuxSession};
use super::DaemonError;

/// One row in the federated session view: a session plus the host it lives on.
#[derive(Debug, Clone)]
pub struct FederatedSession {
    /// `local` for the daemon we initiated the fan-out from, otherwise the
    /// peer's hostname.
    pub host_label: String,

    /// Peer's reachable address (e.g. `graphql.orchard.boxd.sh`). `None` for
    /// the local host.
    pub host_address: Option<String>,

    /// True when this session is on the local daemon's host.
    pub is_local: bool,

    /// The session itself.
    pub session: TmuxSession,
}

/// Per-peer fan-out outcome. Successful fetches contribute sessions; failures
/// surface so the TUI can show a status line.
#[derive(Debug)]
pub struct PeerFetchResult {
    /// Hostname of the peer we tried to reach.
    pub hostname: String,
    /// Address used for the fetch (the resolved GraphQL URL).
    pub address: String,
    /// Sessions returned, when the fetch succeeded.
    pub sessions: Result<Vec<TmuxSession>, DaemonError>,
}

/// Result of a full federated fan-out.
#[derive(Debug)]
pub struct FederatedFanout {
    /// Local host's hostname (label used on local rows).
    pub local_hostname: String,
    /// Local sessions, host-tagged.
    pub local_sessions: Vec<TmuxSession>,
    /// Per-peer results. Errors are not fatal — the TUI surfaces them inline.
    pub peer_results: Vec<PeerFetchResult>,
}

impl FederatedFanout {
    /// Flatten into one host-tagged list. Failed peer fetches yield no rows
    /// (the TUI reads `peer_results` separately to render error indicators).
    pub fn flatten(&self) -> Vec<FederatedSession> {
        let mut out: Vec<FederatedSession> =
            Vec::with_capacity(self.local_sessions.len() + self.peer_results.len());
        for s in &self.local_sessions {
            out.push(FederatedSession {
                host_label: self.local_hostname.clone(),
                host_address: None,
                is_local: true,
                session: s.clone(),
            });
        }
        for peer in &self.peer_results {
            if let Ok(sessions) = &peer.sessions {
                for s in sessions {
                    out.push(FederatedSession {
                        host_label: peer.hostname.clone(),
                        host_address: Some(peer.address.clone()),
                        is_local: false,
                        session: s.clone(),
                    });
                }
            }
        }
        out
    }
}

/// Fans out from `local` to every reachable peer in `hosts` and collects the
/// merged result. The local daemon's sessions are read with the supplied
/// client so that an `ORCHARD_DAEMON_URL` override flows through.
///
/// `hosts` should come from `local.hosts()`. We pick the first host as
/// "local" (v1 of the daemon only ever returns one host with peers nested
/// under it).
pub fn fan_out(
    local: &Client,
    hosts: &[HostNode],
) -> Result<FederatedFanout, DaemonError> {
    let local_host = hosts
        .first()
        .ok_or_else(|| DaemonError::Parse("hosts query returned 0 hosts".to_string()))?;

    let local_sessions = local.tmux_sessions()?;

    // Resolve unique reachable peers across all returned hosts. v1 only has
    // one host so this iterates `local_host.peers`, but be robust to schema
    // evolution (hosts could surface peers at the top level later).
    let mut peers: Vec<&HostNode> = Vec::new();
    let mut seen_addrs: std::collections::HashSet<String> = std::collections::HashSet::new();
    for host in hosts {
        for peer in &host.peers {
            if !peer.reachable {
                continue;
            }
            let addr = match peer.address.as_deref() {
                Some(a) if !a.is_empty() => a.to_string(),
                _ => continue,
            };
            if seen_addrs.insert(addr) {
                peers.push(peer);
            }
        }
    }

    let peer_results: Vec<PeerFetchResult> = std::thread::scope(|scope| {
        let handles: Vec<_> = peers
            .iter()
            .map(|peer| {
                let hostname = peer.hostname.clone();
                let address = peer.address.clone().unwrap_or_default();
                scope.spawn(move || fetch_peer(&hostname, &address))
            })
            .collect();
        handles
            .into_iter()
            .map(|h| h.join().unwrap_or_else(|_| PeerFetchResult {
                hostname: "<panicked>".to_string(),
                address: String::new(),
                sessions: Err(DaemonError::Transport("peer fetch thread panicked".into())),
            }))
            .collect()
    });

    Ok(FederatedFanout {
        local_hostname: local_host.hostname.clone(),
        local_sessions,
        peer_results,
    })
}

fn fetch_peer(hostname: &str, address: &str) -> PeerFetchResult {
    let url = peer_url(address);
    if url.is_empty() {
        return PeerFetchResult {
            hostname: hostname.to_string(),
            address: address.to_string(),
            sessions: Err(DaemonError::Transport(
                "peer has empty address".to_string(),
            )),
        };
    }
    let client = match Client::for_url(&url) {
        Ok(c) => c,
        Err(e) => {
            return PeerFetchResult {
                hostname: hostname.to_string(),
                address: url,
                sessions: Err(e),
            };
        }
    };
    let sessions = client.tmux_sessions();
    PeerFetchResult {
        hostname: hostname.to_string(),
        address: url,
        sessions,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn host(name: &str, addr: Option<&str>, reachable: bool, peers: Vec<HostNode>) -> HostNode {
        HostNode {
            id: format!("Host:{name}"),
            hostname: name.to_string(),
            address: addr.map(|s| s.to_string()),
            reachable,
            peers,
        }
    }

    fn session(name: &str) -> TmuxSession {
        TmuxSession {
            id: format!("TmuxSession:H:{name}"),
            name: name.to_string(),
            attached: false,
            active_attached: false,
            last_activity_at: None,
        }
    }

    #[test]
    fn flatten_tags_local_and_peer_sessions() {
        let fanout = FederatedFanout {
            local_hostname: "drudru".to_string(),
            local_sessions: vec![session("alpha"), session("beta")],
            peer_results: vec![
                PeerFetchResult {
                    hostname: "box1".to_string(),
                    address: "https://graphql.box1/graphql".to_string(),
                    sessions: Ok(vec![session("gamma")]),
                },
                PeerFetchResult {
                    hostname: "box2".to_string(),
                    address: "https://graphql.box2/graphql".to_string(),
                    sessions: Err(DaemonError::Transport("nope".into())),
                },
            ],
        };
        let flat = fanout.flatten();
        // Two local + one from box1; box2 contributes nothing because it failed.
        assert_eq!(flat.len(), 3);
        assert_eq!(flat[0].host_label, "drudru");
        assert!(flat[0].is_local);
        assert_eq!(flat[2].host_label, "box1");
        assert!(!flat[2].is_local);
        assert_eq!(flat[2].session.name, "gamma");
    }

    #[test]
    fn flatten_skips_failed_peers_but_keeps_locals() {
        let fanout = FederatedFanout {
            local_hostname: "drudru".to_string(),
            local_sessions: vec![session("alpha")],
            peer_results: vec![PeerFetchResult {
                hostname: "box1".to_string(),
                address: "https://graphql.box1/graphql".to_string(),
                sessions: Err(DaemonError::Unreachable {
                    url: "https://graphql.box1/graphql".to_string(),
                    cause: "timeout".into(),
                }),
            }],
        };
        assert_eq!(fanout.flatten().len(), 1);
    }

    #[test]
    fn fan_out_errors_when_hosts_empty() {
        // Need a real client even though we never make a call (fan_out
        // short-circuits on empty hosts before issuing tmuxSessions).
        // Use a URL guaranteed unreachable so a stray query would surface,
        // but fan_out should never reach that path.
        let _ = host("ignored", None, false, vec![]);
        let local = Client::for_url("http://127.0.0.1:1/graphql").unwrap();
        let err = fan_out(&local, &[]).unwrap_err();
        assert!(matches!(err, DaemonError::Parse(_)));
    }
}
