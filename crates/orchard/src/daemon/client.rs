//! Blocking HTTP GraphQL client. The TUI is synchronous; we keep this
//! synchronous too and let `tokio` handle parallel fan-out where it pays off
//! (peer fetches in particular).
//!
//! Why blocking + reqwest: existing TUI code is sync, and `reqwest::blocking`
//! sets up an internal tokio runtime per call without polluting the rest of
//! the codebase. For the federated peer fan-out we use `std::thread::scope`
//! to issue requests in parallel — sufficient for a handful of peers and
//! keeps the dep surface tight.

use std::time::Duration;

use serde::Serialize;

use super::types::{
    GraphQlResponse, HealthPayload, HostsPayload, TmuxSession, TmuxSessionsPayload,
    WorkViewPayload, WorkViewSnapshot, WorktreesCleanupPayload, WorktreesCleanupResult,
};
use super::{DaemonError, resolve_daemon_url};

/// Default per-request timeout against any single daemon (local or peer).
pub const DEFAULT_TIMEOUT: Duration = Duration::from_secs(5);

/// Blocking GraphQL client. Cheap to construct; reuse one per logical
/// operation when fanning out to peers.
pub struct Client {
    http: reqwest::blocking::Client,
    url: String,
}

impl Client {
    /// Builds a client targeting the daemon URL resolved from
    /// `ORCHARD_DAEMON_URL` (or the hardcoded local default).
    pub fn local() -> Result<Self, DaemonError> {
        Self::for_url(resolve_daemon_url())
    }

    /// Builds a client targeting an explicit GraphQL endpoint URL.
    ///
    /// `url` should be the full `/graphql` path. For peers, the convention is
    /// `https://graphql.<peer-address>/graphql` (the daemon exposes its
    /// federated surface there); see `peer_url`.
    pub fn for_url(url: impl Into<String>) -> Result<Self, DaemonError> {
        let http = reqwest::blocking::Client::builder()
            .timeout(DEFAULT_TIMEOUT)
            .build()
            .map_err(|e| DaemonError::Transport(format!("build http client: {e}")))?;
        Ok(Self {
            http,
            url: url.into(),
        })
    }

    /// Returns the URL this client targets.
    pub fn url(&self) -> &str {
        &self.url
    }

    /// Generic GraphQL POST. Returns the parsed `data` payload, or maps every
    /// failure mode to a [`DaemonError`].
    pub fn query<T>(&self, body: &GraphQlRequest<'_>) -> Result<T, DaemonError>
    where
        T: serde::de::DeserializeOwned,
    {
        let resp = self.http.post(&self.url).json(body).send().map_err(|e| {
            if e.is_connect() || e.is_timeout() {
                DaemonError::Unreachable {
                    url: self.url.clone(),
                    cause: e.to_string(),
                }
            } else {
                DaemonError::Transport(e.to_string())
            }
        })?;

        let status = resp.status();
        if !status.is_success() {
            let body = resp.text().unwrap_or_default();
            let truncated: String = body.chars().take(2000).collect();
            return Err(DaemonError::HttpStatus {
                status: status.as_u16(),
                body: truncated,
            });
        }

        let env: GraphQlResponse<T> = resp
            .json()
            .map_err(|e| DaemonError::Parse(format!("decode JSON: {e}")))?;

        if !env.errors.is_empty() {
            return Err(DaemonError::GraphQl(
                env.errors.into_iter().map(|e| e.message).collect(),
            ));
        }

        env.data
            .ok_or_else(|| DaemonError::Parse("response had neither data nor errors".to_string()))
    }

    /// Probe `Query.health`. Used as a connectivity smoke check.
    pub fn health(&self) -> Result<crate::daemon::types::Health, DaemonError> {
        let req = GraphQlRequest::new("{ health { status uptimeS } }");
        let payload: HealthPayload = self.query(&req)?;
        Ok(payload.health)
    }

    /// Lists tmux sessions on the daemon's host. Maps to `Query.tmuxSessions`.
    pub fn tmux_sessions(&self) -> Result<Vec<TmuxSession>, DaemonError> {
        const Q: &str = r#"
            {
              tmuxSessions {
                id
                name
                attached
                activeAttached
                lastActivityAt
              }
            }
        "#;
        let payload: TmuxSessionsPayload = self.query(&GraphQlRequest::new(Q))?;
        Ok(payload.tmux_sessions)
    }

    /// Lists every host the daemon knows about (local + federated peers).
    /// Two-level peers are NOT walked — only direct peers of each host. The
    /// daemon's `Host.peers` is one hop; deeper transitive walks are not
    /// supported by the schema today.
    pub fn hosts(&self) -> Result<Vec<crate::daemon::types::HostNode>, DaemonError> {
        const Q: &str = r#"
            {
              hosts {
                id
                hostname
                address
                reachable
                peers {
                  id
                  hostname
                  address
                  reachable
                }
              }
            }
        "#;
        let payload: HostsPayload = self.query(&GraphQlRequest::new(Q))?;
        Ok(payload.hosts)
    }

    /// Invokes the `Mutation.worktreesCleanup` operation — the **first mutation
    /// method on this client** (ADR-018: all state mutations go through the
    /// daemon; the TUI never execs local destruction directly).
    ///
    /// Sends a batch cleanup request for the given worktree IDs. Each ID must
    /// be in `<repo_slug>:<branch>` format (e.g. `"owner/repo:feat/my-branch"`).
    ///
    /// `active_session` and `active_cwd` implement the AC-G1 data-loss guard:
    /// the worktree matching either value is excluded from all destruction and
    /// reported as skipped with reason `"hosts-active-session"`. These **must**
    /// be captured in the TUI process (where `$TMUX` is valid) and passed here;
    /// the daemon must not read its own `$TMUX`.
    ///
    /// # Errors
    ///
    /// Returns [`DaemonError::Unreachable`] when the daemon is not reachable.
    /// Returns [`DaemonError::GraphQl`] when the daemon returns GraphQL errors.
    pub fn worktrees_cleanup(
        &self,
        worktree_ids: &[String],
        active_session: Option<&str>,
        active_cwd: Option<&str>,
    ) -> Result<WorktreesCleanupResult, DaemonError> {
        const MUTATION: &str = r#"
            mutation WorktreesCleanup($input: WorktreesCleanupInput!) {
              worktreesCleanup(input: $input) {
                ok
                errCode
                errMsg
                entries {
                  worktreeId
                  ok
                  stage
                  message
                  alreadyRemoved
                  warnings
                }
              }
            }
        "#;

        let mut input = serde_json::json!({
            "worktreeIds": worktree_ids,
        });
        if let Some(sess) = active_session {
            input["activeSession"] = serde_json::Value::String(sess.to_string());
        }
        if let Some(cwd) = active_cwd {
            input["activeCwd"] = serde_json::Value::String(cwd.to_string());
        }

        let req = GraphQlRequest::with_variables(MUTATION, serde_json::json!({ "input": input }));
        let payload: WorktreesCleanupPayload = self.query(&req)?;
        Ok(payload.worktrees_cleanup)
    }

    /// Fetches a complete local-data snapshot via `Query.workView`.
    ///
    /// Returns a [`WorkViewSnapshot`] containing all repos (with their
    /// worktrees, pre-joined with open PR and issue data), all local tmux
    /// sessions, and all local Claude instances. This is the primary read path
    /// for TUI dashboard refresh (Phase 1: local data only).
    ///
    /// # Errors
    ///
    /// Returns [`DaemonError::Unreachable`] when the daemon is not reachable.
    /// Returns [`DaemonError::Parse`] when the response cannot be decoded.
    /// Returns [`DaemonError::GraphQl`] when the daemon returns GraphQL errors.
    ///
    /// # Notes
    ///
    /// In daemon v1 all worktrees carry `host == "local"`. Remote worktrees
    /// continue to be sourced from `cache_sources::refresh_remote_worktrees`
    /// until daemon Workstream F populates per-peer worktrees in `WorkView`.
    pub fn work_view(&self) -> Result<WorkViewSnapshot, DaemonError> {
        const Q: &str = r#"
            {
              workView {
                repos {
                  slug
                  path
                  worktrees {
                    path
                    branch
                    head
                    bare
                    host
                    repo
                    ahead
                    behind
                    pr {
                      number
                      state
                      title
                      statusCheckRollup
                      reviewDecision
                      mergeStateStatus
                      mergeable
                      draft
                      labels
                    }
                    issue {
                      number
                      state
                      title
                      labels
                    }
                  }
                }
                tmuxSessions {
                  id
                  name
                  attached
                  activeAttached
                  lastActivityAt
                  attachedClients
                  windows
                  currentWindow
                }
                claudeInstances {
                  id
                  pane
                  process
                  state
                  sessionUuid
                  rcEnabled
                  lastActivityAt
                  model
                  inflightToolCount
                }
              }
            }
        "#;
        let payload: WorkViewPayload = self.query(&GraphQlRequest::new(Q))?;
        Ok(payload.work_view)
    }
}

/// GraphQL request envelope — carries a query/mutation document plus optional
/// variables. Callers that need no variables use [`GraphQlRequest::new`];
/// mutations with input objects use [`GraphQlRequest::with_variables`].
///
/// `variables` is serialised as-is when present. When absent (`None`) the
/// field is omitted from the JSON body so the server receives a clean
/// query-only request — unchanged behaviour for all existing query callers.
#[derive(Debug, Serialize)]
pub struct GraphQlRequest<'a> {
    /// The query or mutation document.
    pub query: &'a str,
    /// Optional variables object sent alongside the document.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub variables: Option<serde_json::Value>,
}

impl<'a> GraphQlRequest<'a> {
    /// New request with no variables (backwards-compatible for all existing query callers).
    pub fn new(query: &'a str) -> Self {
        Self {
            query,
            variables: None,
        }
    }

    /// New request carrying a variables object.  Used for mutations with input types.
    pub fn with_variables(query: &'a str, variables: serde_json::Value) -> Self {
        Self {
            query,
            variables: Some(variables),
        }
    }
}

/// Builds the GraphQL endpoint URL for a peer host given its `address`.
///
/// Convention (matches PR #413's peerproxy deployment):
/// - If `address` is a full `http(s)://...` URL, return it as-is.
/// - If `address` already starts with `graphql.` (the boxd DNS shape — the
///   daemon registers its peers as `graphql.<box>.boxd.sh`), prefix `https://`
///   and append `/graphql`.
/// - Otherwise (a bare hostname like `box-1.boxd.sh`), insert the `graphql.`
///   subdomain: `https://graphql.<address>/graphql`.
///
/// Empty input yields an empty string so callers can detect "no useable
/// address" before attempting a fetch.
pub fn peer_url(address: &str) -> String {
    let trimmed = address.trim();
    if trimmed.is_empty() {
        return String::new();
    }
    if trimmed.starts_with("http://") || trimmed.starts_with("https://") {
        return trimmed.to_string();
    }
    if trimmed.starts_with("graphql.") {
        return format!("https://{trimmed}/graphql");
    }
    format!("https://graphql.{trimmed}/graphql")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn peer_url_prefixes_graphql() {
        assert_eq!(
            peer_url("box-1.boxd.sh"),
            "https://graphql.box-1.boxd.sh/graphql"
        );
    }

    #[test]
    fn peer_url_preserves_existing_graphql_prefix() {
        // The daemon may surface peers as `graphql.<box>.boxd.sh` already.
        assert_eq!(
            peer_url("graphql.orchard.boxd.sh"),
            "https://graphql.orchard.boxd.sh/graphql"
        );
    }

    #[test]
    fn peer_url_passthrough_when_full_url() {
        assert_eq!(
            peer_url("https://graphql.box-1.boxd.sh/graphql"),
            "https://graphql.box-1.boxd.sh/graphql"
        );
    }

    #[test]
    fn peer_url_trims_whitespace() {
        assert_eq!(
            peer_url("  box-2.boxd.sh\n"),
            "https://graphql.box-2.boxd.sh/graphql"
        );
    }

    #[test]
    fn peer_url_empty_yields_empty() {
        assert_eq!(peer_url(""), "");
    }
}
