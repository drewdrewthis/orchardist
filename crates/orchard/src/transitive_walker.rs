//! BFS transitive-federation walker (issue #363, Phase 2).
//!
//! Traverses the orchard graph starting from directly-configured remotes that
//! have `allow_transitive: true`. At each level all nodes are queried in
//! parallel (level-parallel BFS). The walk terminates when:
//!
//! 1. No new nodes are discovered (seen-set fully covers the frontier), OR
//! 2. The `max_depth` cap is reached (belt-and-suspenders against dedup misses).
//!
//! # Protocol
//!
//! Each hop makes TWO calls via [`SshExec`]:
//!
//! - `orchard --json` — fetches the state snapshot for that host.
//! - `orchard list-remotes --json` — discovers children (grandchildren of root).
//!   Exit 127 (command not found) is treated as a **leaf** — the host is a
//!   pre-transitive orchard build. No [`TransitiveError`] is emitted; the
//!   snapshot fetched above is still recorded.
//!
//! # Adapter dedup
//!
//! A per-walk `HashMap<dedup_key → Arc<OnceLock<…>>>` ensures that a diamond
//! topology causes only one SSH round-trip for `orchard --json` on a shared leaf.
//! The snapshot is stored as `Arc<JsonOutput>` so the same value can be
//! shared across discovery paths without cloning.
//!
//! # Seen-set
//!
//! Keyed by [`crate::federation::host_dedup_key`]. If a host's key is already
//! in the set it is skipped silently. The seen-set is the primary
//! cycle-termination mechanism; `max_depth` is the safety net.
//!
//! # Failure handling
//!
//! Any SSH failure, JSON parse error, or version skew during either call records
//! a [`TransitiveError`] and emits a `remote_adapter.proxy_failure` event to
//! `events.jsonl`. The walk continues with other frontier nodes.

use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::Duration;

use crate::federation::{
    ListRemotesOutput, check_list_remotes_version, emit_federation_discovered_host, host_dedup_key,
};
use crate::json_output::JsonOutput;
use crate::remote_adapter::SshExec;

// ---------------------------------------------------------------------------
// Public constants
// ---------------------------------------------------------------------------

/// Default maximum BFS depth. Belt-and-suspenders cap; seen-set is the
/// primary termination mechanism.
pub const DEFAULT_MAX_DEPTH: u32 = 8;

/// Default per-hop timeout. Each hop (both SSH calls combined) is bounded
/// to this duration.
pub const DEFAULT_PER_HOP_TIMEOUT: Duration = Duration::from_secs(10);

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/// Configuration for a single walker run.
pub struct WalkerConfig {
    /// Maximum BFS depth. Traversal stops at this depth regardless of how
    /// many new nodes would be discoverable beyond it.
    pub max_depth: u32,
    /// Wall-clock budget per hop. When exceeded, a [`TransitiveError`]
    /// with `reason` containing `"timeout"` is recorded.
    pub per_hop_timeout: Duration,
    /// SSH executor shared across all threads in this walk.
    pub ssh: Arc<dyn SshExec>,
    /// When `true`, skip `orchard --json` for depth-1 roots.
    ///
    /// Set by `refresh_and_build_with_walker_config` because those roots were
    /// already fetched by `OrchardProxyAdapter` in the pre-walker phase.
    /// Leave `false` (the default) when calling the walker directly in tests
    /// or from contexts that have not pre-fetched depth-1 snapshots.
    pub skip_depth1_snapshot: bool,
}

impl WalkerConfig {
    /// Constructs a `WalkerConfig` with [`DEFAULT_MAX_DEPTH`] and
    /// [`DEFAULT_PER_HOP_TIMEOUT`].
    pub fn new(ssh: Arc<dyn SshExec>) -> Self {
        Self {
            max_depth: DEFAULT_MAX_DEPTH,
            per_hop_timeout: DEFAULT_PER_HOP_TIMEOUT,
            ssh,
            skip_depth1_snapshot: false,
        }
    }

    /// Overrides `max_depth`.
    pub fn with_max_depth(mut self, depth: u32) -> Self {
        self.max_depth = depth;
        self
    }

    /// Overrides `per_hop_timeout`.
    pub fn with_per_hop_timeout(mut self, timeout: Duration) -> Self {
        self.per_hop_timeout = timeout;
        self
    }

    /// Enables skipping `orchard --json` for depth-1 roots.
    ///
    /// Only set this when the caller has already fetched depth-1 snapshots
    /// via `OrchardProxyAdapter` (i.e., from `refresh_and_build_with_walker_config`).
    pub fn with_skip_depth1_snapshot(mut self) -> Self {
        self.skip_depth1_snapshot = true;
        self
    }
}

/// Error recorded when a single hop fails (partially or fully).
///
/// The walk continues after recording a `TransitiveError`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TransitiveError {
    /// `host_dedup_key()` of the failing host.
    pub dedup_key: String,
    /// Full discovery path from `"local"` to the failing node.
    ///
    /// Example: `["local", "boxd", "evals-v3-debug"]`
    pub discovery_path: Vec<String>,
    /// Short, stable reason label matching the vocabulary from
    /// [`crate::remote_adapter::classify_proxy_failure_reason`].
    pub reason: String,
    /// Which call failed: `"list-remotes"`, `"fetch"`, or
    /// `"list_remotes_after_snapshot"`.
    pub phase: String,
}

impl TransitiveError {
    /// Returns the directly-configured root host from which this node was
    /// reached: `discovery_path[1]` (the first element after `"local"`).
    ///
    /// Returns `None` when `discovery_path` has fewer than 2 elements, which
    /// should not occur in practice but is handled defensively.
    pub fn root(&self) -> Option<&str> {
        self.discovery_path.get(1).map(String::as_str)
    }
}

/// Result of a single walker run.
pub struct WalkerResult {
    /// `(discovery_path, snapshot)` — one per successfully-fetched node.
    ///
    /// The first element of every `discovery_path` is `"local"`.
    /// Snapshots are wrapped in `Arc` so a diamond topology can share one
    /// `JsonOutput` across multiple discovery paths without cloning.
    pub snapshots: Vec<(Vec<String>, Arc<JsonOutput>)>,
    /// Errors encountered during the walk. Non-fatal; the walk continued.
    pub errors: Vec<TransitiveError>,
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

/// A node waiting to be visited in the BFS frontier.
#[derive(Clone)]
struct FrontierNode {
    host: String,
    dedup_key: String,
    discovery_path: Vec<String>,
    /// When `true`, skip the `orchard --json` call for this node.
    ///
    /// Set for depth-1 roots whose snapshot was already fetched by
    /// `OrchardProxyAdapter` during the pre-walker refresh phase.
    /// The walker only needs `list-remotes` for these nodes to discover children.
    skip_snapshot: bool,
}

/// The outcome of visiting a single node.
enum HopOutcome {
    /// The snapshot fetch succeeded and `list-remotes` returned children (possibly empty).
    Success {
        snapshot: Arc<JsonOutput>,
        /// Child hosts from `list-remotes` (`allow_transitive == true` + `orchard-proxy` only).
        children: Vec<String>,
    },
    /// `list-remotes` failed after a successful snapshot fetch (or skip).
    ///
    /// The snapshot is still recorded; the error is surfaced so operators can
    /// observe the partial failure.  No children are discovered for this node.
    ListRemotesFailed {
        snapshot: Arc<JsonOutput>,
        err: TransitiveError,
    },
    /// `orchard list-remotes --json` returned exit 127. The node is a leaf;
    /// its snapshot was fetched and is in the cache.
    Leaf,
    /// A hard error occurred (snapshot fetch failed, or timeout).
    Error(TransitiveError),
}

struct LevelResult {
    node: FrontierNode,
    outcome: HopOutcome,
}

/// Shared memoization cache: `dedup_key → Arc<OnceLock<Option<Arc<JsonOutput>>>>`.
///
/// `None` inside the lock means the fetch was attempted and failed.
type SnapshotCache = Arc<Mutex<HashMap<String, Arc<OnceLock<Option<Arc<JsonOutput>>>>>>>;

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

/// Walks the orchard federation graph from the given `roots`.
///
/// `roots` is a slice of `(host, allow_transitive)` pairs representing
/// directly-configured `OrchardProxy` remotes.  Roots with
/// `allow_transitive == false` are not seeded into the walker at all —
/// their snapshots are already fetched by `OrchardProxyAdapter` in the
/// pre-walker refresh phase, and they have no children to discover.
///
/// For `allow_transitive == true` roots (depth-1), the walker skips the
/// `orchard --json` call (already done by `OrchardProxyAdapter`) and only
/// calls `list-remotes --json` to discover transitive children.
///
/// All SSH calls go through `config.ssh`, making the walker fully testable
/// with [`crate::remote_adapter::FakeSshExec`].
pub fn walk(roots: &[(&str, bool)], config: &WalkerConfig) -> WalkerResult {
    let seen: Arc<Mutex<HashSet<String>>> = Arc::new(Mutex::new(HashSet::new()));
    let cache: SnapshotCache = Arc::new(Mutex::new(HashMap::new()));

    let mut all_snapshots: Vec<(Vec<String>, Arc<JsonOutput>)> = Vec::new();
    let mut all_errors: Vec<TransitiveError> = Vec::new();

    // Build a fast lookup: dedup_key → allow_transitive for depth-1 roots.
    let root_allow_transitive: HashMap<String, bool> = roots
        .iter()
        .filter_map(|&(host, allow)| host_dedup_key(host).ok().map(|k| (k, allow)))
        .collect();

    // Seed the frontier.
    // - Roots with allow_transitive=false are excluded: they have no children
    //   to discover and (when skip_depth1_snapshot is set) their snapshots are
    //   already fetched by OrchardProxyAdapter.  Mark them seen so a transitive
    //   child cannot re-introduce them.
    // - Roots with allow_transitive=true are seeded.  When skip_depth1_snapshot
    //   is set, their orchard --json call is also skipped (pre-fetched by
    //   OrchardProxyAdapter); only list-remotes is called to find children.
    let mut frontier: Vec<FrontierNode> = Vec::new();
    for &(host, allow) in roots {
        let key = match host_dedup_key(host) {
            Ok(k) => k,
            Err(_) => continue,
        };
        if !allow {
            // Mark as seen so a transitive child cannot re-introduce this host.
            seen.lock().unwrap().insert(key);
            continue;
        }
        let is_new = {
            let mut s = seen.lock().unwrap();
            s.insert(key.clone())
        };
        if !is_new {
            continue;
        }
        emit_federation_discovered_host(host, &key);
        frontier.push(FrontierNode {
            host: host.to_string(),
            dedup_key: key,
            discovery_path: vec!["local".to_string(), host.to_string()],
            // Skip orchard --json for depth-1 roots when the pre-walker phase
            // already fetched them via OrchardProxyAdapter.
            skip_snapshot: config.skip_depth1_snapshot,
        });
    }

    let mut depth: u32 = 1;

    while !frontier.is_empty() {
        let level_nodes: Vec<FrontierNode> = std::mem::take(&mut frontier);
        let (tx, rx) = std::sync::mpsc::channel::<LevelResult>();

        std::thread::scope(|scope| {
            for node in &level_nodes {
                let tx = tx.clone();
                let ssh = Arc::clone(&config.ssh);
                let timeout = config.per_hop_timeout;
                let cache = Arc::clone(&cache);
                let node = node.clone();

                scope.spawn(move || {
                    let result = visit_with_timeout(node, ssh, timeout, cache);
                    let _ = tx.send(result);
                });
            }
            drop(tx);
        });
        // All spawned threads have joined (scope exited).

        let level_results: Vec<LevelResult> = rx.into_iter().collect();

        for LevelResult { node, outcome } in level_results {
            match outcome {
                HopOutcome::Success { snapshot, children } => {
                    all_snapshots.push((node.discovery_path.clone(), snapshot));

                    let should_expand = if depth == 1 {
                        root_allow_transitive
                            .get(&node.dedup_key)
                            .copied()
                            .unwrap_or(false)
                    } else {
                        true // deeper nodes only appear because a parent advertised them
                    };

                    if should_expand && depth < config.max_depth {
                        for child_host in children {
                            let child_key = match host_dedup_key(&child_host) {
                                Ok(k) => k,
                                Err(_) => continue,
                            };
                            let is_new = {
                                let mut s = seen.lock().unwrap();
                                s.insert(child_key.clone())
                            };
                            if is_new {
                                emit_federation_discovered_host(&child_host, &child_key);
                                let mut child_path = node.discovery_path.clone();
                                child_path.push(child_host.clone());
                                frontier.push(FrontierNode {
                                    host: child_host,
                                    dedup_key: child_key,
                                    discovery_path: child_path,
                                    // Depth 2+: always fetch orchard --json.
                                    skip_snapshot: false,
                                });
                            }
                        }
                    }
                }
                HopOutcome::ListRemotesFailed { snapshot, err } => {
                    // Snapshot was fetched (or skipped for depth-1 roots) but
                    // list-remotes failed.  Record the snapshot so the node's
                    // own state is visible in the dashboard, and surface the
                    // list-remotes failure so operators can observe the partial
                    // failure (topology silently shrinking is worse than a
                    // visible error).
                    all_snapshots.push((node.discovery_path.clone(), snapshot));
                    emit_proxy_failure_event(&err);
                    all_errors.push(err);
                }
                HopOutcome::Leaf => {
                    // Snapshot was fetched before list-remotes was called; pull from cache.
                    if let Some(snap) = take_cached_snapshot(&cache, &node.dedup_key) {
                        all_snapshots.push((node.discovery_path.clone(), snap));
                    }
                }
                HopOutcome::Error(err) => {
                    emit_proxy_failure_event(&err);
                    all_errors.push(err);
                }
            }
        }

        depth += 1;
    }

    WalkerResult {
        snapshots: all_snapshots,
        errors: all_errors,
    }
}

// ---------------------------------------------------------------------------
// Internal: timeout wrapper
// ---------------------------------------------------------------------------

/// Runs [`visit_inner`] in a detached thread and waits for the result up to
/// `timeout`. On timeout returns a `TransitiveError` and lets the inner thread
/// run to completion in the background.
fn visit_with_timeout(
    node: FrontierNode,
    ssh: Arc<dyn SshExec>,
    timeout: Duration,
    cache: SnapshotCache,
) -> LevelResult {
    let (tx, rx) = std::sync::mpsc::channel::<LevelResult>();

    let node_for_thread = node;
    let node_for_result = node_for_thread.clone();

    std::thread::spawn(move || {
        let outcome = visit_inner(&node_for_thread, ssh.as_ref(), &cache);
        let _ = tx.send(LevelResult {
            node: node_for_thread,
            outcome,
        });
    });

    match rx.recv_timeout(timeout) {
        Ok(result) => result,
        Err(_) => {
            let secs = timeout.as_secs();
            let reason = if secs > 0 {
                format!("timeout ({}s)", secs)
            } else {
                format!("timeout ({}ms)", timeout.subsec_millis())
            };
            let err = TransitiveError {
                dedup_key: node_for_result.dedup_key.clone(),
                discovery_path: node_for_result.discovery_path.clone(),
                reason,
                phase: "fetch".to_string(),
            };
            LevelResult {
                node: node_for_result,
                outcome: HopOutcome::Error(err),
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Internal: actual hop logic
// ---------------------------------------------------------------------------

/// Executes SSH calls for a node. No timeout enforcement here.
///
/// When `node.skip_snapshot` is `true` (depth-1 roots), the `orchard --json`
/// call is skipped because the snapshot was already fetched by
/// `OrchardProxyAdapter` in the pre-walker phase.  Only `list-remotes --json`
/// is called to discover transitive children.
fn visit_inner(node: &FrontierNode, ssh: &dyn SshExec, cache: &SnapshotCache) -> HopOutcome {
    // Step 1: fetch orchard --json (unless this node's snapshot is already
    // available from the pre-walker OrchardProxyAdapter phase).
    if !node.skip_snapshot {
        let fetch_ok = fetch_snapshot(node, ssh, cache);
        if !fetch_ok {
            return HopOutcome::Error(TransitiveError {
                dedup_key: node.dedup_key.clone(),
                discovery_path: node.discovery_path.clone(),
                reason: format!("fetch failure for {}", node.host),
                phase: "fetch".to_string(),
            });
        }
    }

    // Step 2: call list-remotes to discover children.
    let empty_snapshot = || {
        Arc::new(JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: std::collections::HashMap::new(),
            errors: vec![],
        })
    };

    match call_list_remotes(node, ssh) {
        ListRemotesResult::Ok(children) => {
            // For skip_snapshot nodes, take_cached_snapshot returns None (nothing
            // was stored).  Use an empty stub — the real snapshot for this host
            // was already merged by the pre-walker OrchardProxyAdapter phase.
            let snapshot =
                take_cached_snapshot(cache, &node.dedup_key).unwrap_or_else(empty_snapshot);
            HopOutcome::Success { snapshot, children }
        }
        ListRemotesResult::Leaf => HopOutcome::Leaf,
        ListRemotesResult::Err(mut err) => {
            // list-remotes failed but snapshot was successfully fetched (or
            // skip_snapshot was set, meaning the pre-walker phase handled it).
            // Use a distinct phase label so the error is distinguishable from
            // a full-hop failure where the snapshot was never fetched.
            err.phase = "list_remotes_after_snapshot".to_string();
            let snapshot =
                take_cached_snapshot(cache, &node.dedup_key).unwrap_or_else(empty_snapshot);
            HopOutcome::ListRemotesFailed { snapshot, err }
        }
    }
}

// ---------------------------------------------------------------------------
// Internal: orchard --json with OnceLock dedup
// ---------------------------------------------------------------------------

/// Fetches `orchard --json` for `node` and stores the result in `cache`.
///
/// Returns `true` on success, `false` on failure. The `OnceLock` ensures
/// only one thread fires the SSH call; others reuse the cached result.
fn fetch_snapshot(node: &FrontierNode, ssh: &dyn SshExec, cache: &SnapshotCache) -> bool {
    let lock = {
        let mut map = cache.lock().unwrap();
        Arc::clone(
            map.entry(node.dedup_key.clone())
                .or_insert_with(|| Arc::new(OnceLock::new())),
        )
    };

    lock.get_or_init(|| {
        let output = match ssh.exec(&node.host, "orchard --json") {
            Ok(o) => o,
            Err(_) => return None,
        };
        if output.exit_code != 0 {
            return None;
        }
        let parsed: JsonOutput = match serde_json::from_str(&output.stdout) {
            Ok(v) => v,
            Err(_) => return None,
        };
        if crate::json_output::check_json_output_version(parsed.version).is_err() {
            return None;
        }
        Some(Arc::new(parsed))
    });

    matches!(lock.get(), Some(Some(_)))
}

// ---------------------------------------------------------------------------
// Internal: list-remotes
// ---------------------------------------------------------------------------

enum ListRemotesResult {
    Ok(Vec<String>),
    Leaf,
    Err(TransitiveError),
}

fn call_list_remotes(node: &FrontierNode, ssh: &dyn SshExec) -> ListRemotesResult {
    let output = match ssh.exec(&node.host, "orchard list-remotes --json") {
        Ok(o) => o,
        Err(e) => {
            let reason = crate::remote_adapter::classify_proxy_failure_reason(&e.to_string());
            return ListRemotesResult::Err(TransitiveError {
                dedup_key: node.dedup_key.clone(),
                discovery_path: node.discovery_path.clone(),
                reason,
                phase: "list-remotes".to_string(),
            });
        }
    };

    if output.exit_code == 127 {
        return ListRemotesResult::Leaf;
    }

    if output.exit_code != 0 {
        let reason = crate::remote_adapter::classify_proxy_failure_reason(&format!(
            "remote orchard failed (exit {})",
            output.exit_code
        ));
        return ListRemotesResult::Err(TransitiveError {
            dedup_key: node.dedup_key.clone(),
            discovery_path: node.discovery_path.clone(),
            reason,
            phase: "list-remotes".to_string(),
        });
    }

    let parsed: ListRemotesOutput = match serde_json::from_str(&output.stdout) {
        Ok(v) => v,
        Err(_) => {
            return ListRemotesResult::Err(TransitiveError {
                dedup_key: node.dedup_key.clone(),
                discovery_path: node.discovery_path.clone(),
                reason: "parse failure".to_string(),
                phase: "list-remotes".to_string(),
            });
        }
    };

    if let Err(e) = check_list_remotes_version(parsed.version) {
        return ListRemotesResult::Err(TransitiveError {
            dedup_key: node.dedup_key.clone(),
            discovery_path: node.discovery_path.clone(),
            reason: e,
            phase: "list-remotes".to_string(),
        });
    }

    let children: Vec<String> = parsed
        .remotes
        .into_iter()
        .filter(|r| r.allow_transitive && r.kind == "orchard-proxy")
        .map(|r| r.host)
        .collect();

    ListRemotesResult::Ok(children)
}

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

fn emit_proxy_failure_event(err: &TransitiveError) {
    crate::events::log_event(
        "remote_adapter.proxy_failure",
        &[
            ("host", serde_json::Value::String(err.dedup_key.clone())),
            ("reason", serde_json::Value::String(err.reason.clone())),
            ("phase", serde_json::Value::String(err.phase.clone())),
            (
                "discovery_path",
                serde_json::Value::Array(
                    err.discovery_path
                        .iter()
                        .map(|s| serde_json::Value::String(s.clone()))
                        .collect(),
                ),
            ),
        ],
    );
}

fn take_cached_snapshot(cache: &SnapshotCache, dedup_key: &str) -> Option<Arc<JsonOutput>> {
    let map = cache.lock().unwrap();
    let lock = map.get(dedup_key)?;
    lock.get()?.as_ref().map(Arc::clone)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::json_output::{JsonOutput, JsonRepo};
    use crate::remote_adapter::{FakeSshExec, SshOutput};
    use std::collections::HashMap;
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};

    // -----------------------------------------------------------------------
    // Helpers
    // -----------------------------------------------------------------------

    fn make_empty_output() -> JsonOutput {
        JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![],
            hosts: HashMap::new(),
            errors: vec![],
        }
    }

    fn make_output_with_branch(branch: &str) -> JsonOutput {
        JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![crate::json_output::JsonWorktree {
                    path: format!("/remote/{branch}"),
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
                    discovery_path: None,
                }],
            }],
            hosts: HashMap::new(),
            errors: vec![],
        }
    }

    fn ser(output: &JsonOutput) -> String {
        serde_json::to_string(output).unwrap()
    }

    fn list_remotes_json(children: &[(&str, bool)]) -> String {
        let remotes: Vec<serde_json::Value> = children
            .iter()
            .map(|(host, allow)| {
                serde_json::json!({
                    "name": host,
                    "host": host,
                    "kind": "orchard-proxy",
                    "path": "/remote",
                    "allowTransitive": allow
                })
            })
            .collect();
        serde_json::json!({ "version": 1, "remotes": remotes }).to_string()
    }

    fn list_remotes_empty() -> String {
        list_remotes_json(&[])
    }

    fn ok(stdout: &str) -> SshOutput {
        SshOutput {
            stdout: stdout.to_string(),
            stderr: String::new(),
            exit_code: 0,
        }
    }

    fn exit_code(code: i32) -> SshOutput {
        SshOutput {
            stdout: String::new(),
            stderr: String::new(),
            exit_code: code,
        }
    }

    fn walker(fake: FakeSshExec) -> WalkerConfig {
        WalkerConfig::new(Arc::new(fake) as Arc<dyn SshExec>)
    }

    // -----------------------------------------------------------------------
    // AC9: Two-hop graph A -> B -> C
    // -----------------------------------------------------------------------

    /// feature:311 — two-hop graph terminates with full merged state
    #[test]
    fn two_hop_graph_terminates() {
        let snap_b = make_output_with_branch("issue1/b");
        let snap_c = make_output_with_branch("issue2/c");

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("C", true)])),
        );
        fake.insert("C", "orchard --json", ok(&ser(&snap_c)));
        fake.insert(
            "C",
            "orchard list-remotes --json",
            ok(&list_remotes_empty()),
        );

        let result = walk(&[("B", true)], &walker(fake));

        assert!(result.errors.is_empty(), "errors: {:?}", result.errors);
        assert_eq!(result.snapshots.len(), 2);

        let b = result
            .snapshots
            .iter()
            .find(|(p, _)| p.as_slice() == ["local", "B"]);
        let c = result
            .snapshots
            .iter()
            .find(|(p, _)| p.as_slice() == ["local", "B", "C"]);
        assert!(b.is_some(), "B must exist");
        assert!(c.is_some(), "C must exist with full discovery path");

        let (_, c_out) = c.unwrap();
        assert_eq!(c_out.repos[0].worktrees[0].branch, "issue2/c");
    }

    // -----------------------------------------------------------------------
    // Three-hop: A -> B -> C -> D
    // -----------------------------------------------------------------------

    /// feature:321 — three-hop terminates with full merged state
    #[test]
    fn three_hop_graph_terminates() {
        let snap_b = make_empty_output();
        let snap_c = make_empty_output();
        let snap_d = make_output_with_branch("issue3/d");

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("C", true)])),
        );
        fake.insert("C", "orchard --json", ok(&ser(&snap_c)));
        fake.insert(
            "C",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("D", true)])),
        );
        fake.insert("D", "orchard --json", ok(&ser(&snap_d)));
        fake.insert(
            "D",
            "orchard list-remotes --json",
            ok(&list_remotes_empty()),
        );

        let result = walk(&[("B", true)], &walker(fake));

        assert!(result.errors.is_empty(), "errors: {:?}", result.errors);
        assert_eq!(result.snapshots.len(), 3);

        let d = result
            .snapshots
            .iter()
            .find(|(p, _)| p.as_slice() == ["local", "B", "C", "D"]);
        assert!(d.is_some(), "D must appear with full discovery path");
        let (_, d_out) = d.unwrap();
        assert_eq!(d_out.repos[0].worktrees[0].branch, "issue3/d");
    }

    // -----------------------------------------------------------------------
    // AC3: Cycle A -> B -> A terminates via seen-set
    // -----------------------------------------------------------------------

    /// feature:78, feature:224 — cycle A → B → A terminates via seen-set, no error emitted
    ///
    /// A is a directly-configured remote root (not the local machine).  B is
    /// A's child.  B's list-remotes advertises A back.  A is already in the
    /// seen-set from seeding, so the walk terminates without re-visiting A.
    #[test]
    fn cycle_terminates_via_seen_set() {
        let snap_a = make_empty_output();
        let snap_b = make_empty_output();

        let mut fake = FakeSshExec::new();
        // A's SSH responses (visited at depth 1).
        fake.insert("A", "orchard --json", ok(&ser(&snap_a)));
        fake.insert(
            "A",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("B", true)])),
        );
        // B's SSH responses (visited at depth 2).
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        // B advertises A back — cycle. A is already in seen-set so A is not re-queued.
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("A", true)])),
        );

        let config = WalkerConfig::new(Arc::new(fake) as Arc<dyn SshExec>).with_max_depth(100); // seen-set (not depth cap) must terminate this
        let result = walk(&[("A", true)], &config);

        assert!(
            result.errors.is_empty(),
            "cycle must terminate cleanly: {:?}",
            result.errors
        );
        // A and B visited once each.
        assert_eq!(result.snapshots.len(), 2, "A and B each visited once");
        assert!(
            result
                .snapshots
                .iter()
                .any(|(p, _)| p.as_slice() == ["local", "A"]),
            "A present"
        );
        assert!(
            result
                .snapshots
                .iter()
                .any(|(p, _)| p.as_slice() == ["local", "A", "B"]),
            "B present"
        );
    }

    // -----------------------------------------------------------------------
    // AC3: Diamond A -> {B, C} -> D; one SSH call for D
    // -----------------------------------------------------------------------

    /// feature:87 — diamond dedup: D's orchard --json fires exactly once
    #[test]
    fn diamond_dedup_single_ssh_for_leaf() {
        let snap_b = make_empty_output();
        let snap_c = make_empty_output();
        let snap_d = make_output_with_branch("issue4/diamond");

        let d_call_count = Arc::new(AtomicUsize::new(0));

        struct CountingSshExec {
            inner: FakeSshExec,
            count: Arc<AtomicUsize>,
        }
        impl SshExec for CountingSshExec {
            fn exec(&self, host: &str, cmd: &str) -> anyhow::Result<SshOutput> {
                if host == "D" && cmd == "orchard --json" {
                    self.count.fetch_add(1, Ordering::SeqCst);
                }
                self.inner.exec(host, cmd)
            }
        }

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("D", true)])),
        );
        fake.insert("C", "orchard --json", ok(&ser(&snap_c)));
        fake.insert(
            "C",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("D", true)])),
        );
        fake.insert("D", "orchard --json", ok(&ser(&snap_d)));
        fake.insert(
            "D",
            "orchard list-remotes --json",
            ok(&list_remotes_empty()),
        );

        let ssh = Arc::new(CountingSshExec {
            inner: fake,
            count: Arc::clone(&d_call_count),
        }) as Arc<dyn SshExec>;

        let result = walk(&[("B", true), ("C", true)], &WalkerConfig::new(ssh));

        assert!(result.errors.is_empty(), "errors: {:?}", result.errors);

        let d_snaps: Vec<_> = result
            .snapshots
            .iter()
            .filter(|(_, s)| {
                !s.repos.is_empty()
                    && s.repos[0].worktrees.first().map(|w| w.branch.as_str())
                        == Some("issue4/diamond")
            })
            .collect();
        assert_eq!(d_snaps.len(), 1, "D must appear exactly once in snapshots");

        assert_eq!(
            d_call_count.load(Ordering::SeqCst),
            1,
            "D's orchard --json must fire exactly once (OnceLock dedup)"
        );
    }

    // -----------------------------------------------------------------------
    // AC7: Middle-hop failure doesn't abort tree
    // -----------------------------------------------------------------------

    /// feature:236 — tree A -> {B, C}, B -> D, C unreachable; B and D fetched
    #[test]
    fn middle_hop_failure_does_not_abort_tree() {
        let snap_b = make_empty_output();
        let snap_d = make_output_with_branch("issue5/d");

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("D", true)])),
        );
        fake.insert("C", "orchard --json", exit_code(255));
        fake.insert("C", "orchard list-remotes --json", exit_code(255));
        fake.insert("D", "orchard --json", ok(&ser(&snap_d)));
        fake.insert(
            "D",
            "orchard list-remotes --json",
            ok(&list_remotes_empty()),
        );

        let result = walk(&[("B", true), ("C", true)], &walker(fake));

        assert!(
            result
                .snapshots
                .iter()
                .any(|(p, _)| p.as_slice() == ["local", "B"]),
            "B must be present"
        );
        assert!(
            result
                .snapshots
                .iter()
                .any(|(p, _)| p.as_slice() == ["local", "B", "D"]),
            "D must be present via B"
        );

        assert_eq!(result.errors.len(), 1, "exactly one error for C");
        let c_key = host_dedup_key("C").unwrap();
        assert_eq!(result.errors[0].dedup_key, c_key);
        // root() is now computed from discovery_path[1] instead of a stored field.
        assert!(
            result.errors[0].root().is_some(),
            "root() must return Some for a well-formed discovery_path"
        );
        assert!(
            !result.errors[0].discovery_path.is_empty(),
            "discovery_path must be set"
        );
        assert!(!result.errors[0].reason.is_empty(), "reason must be set");
        assert!(!result.errors[0].phase.is_empty(), "phase must be set");
    }

    // -----------------------------------------------------------------------
    // Exit 127 from list-remotes = leaf, not error
    // -----------------------------------------------------------------------

    /// feature:262 — exit 127 from list-remotes is a leaf, not a TransitiveError
    #[test]
    fn exit_127_on_list_remotes_is_leaf_not_error() {
        let snap_b = make_output_with_branch("issue6/leaf");

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert("B", "orchard list-remotes --json", exit_code(127));

        let result = walk(&[("B", true)], &walker(fake));

        assert!(
            result.errors.is_empty(),
            "exit 127 must NOT produce a TransitiveError, got: {:?}",
            result.errors
        );
        assert_eq!(result.snapshots.len(), 1, "B's snapshot must be present");
        assert_eq!(result.snapshots[0].0.as_slice(), ["local", "B"]);
    }

    // -----------------------------------------------------------------------
    // --max-depth halts traversal
    // -----------------------------------------------------------------------

    /// feature:205 — max_depth=2 halts at depth 2; D not fetched
    #[test]
    fn max_depth_halts_at_specified_depth() {
        let snap_b = make_empty_output();
        let snap_c = make_empty_output();

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("C", true)])),
        );
        fake.insert("C", "orchard --json", ok(&ser(&snap_c)));
        fake.insert(
            "C",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("D", true)])),
        );
        // D not in FakeSshExec — visiting it would cause an error.

        let config = WalkerConfig::new(Arc::new(fake) as Arc<dyn SshExec>).with_max_depth(2);
        let result = walk(&[("B", true)], &config);

        assert_eq!(
            result.snapshots.len(),
            2,
            "B (depth 1) and C (depth 2) must be fetched"
        );
        assert!(
            !result
                .snapshots
                .iter()
                .any(|(p, _)| p.last() == Some(&"D".to_string())),
            "D must not be fetched (depth 3 > max_depth 2)"
        );
    }

    // -----------------------------------------------------------------------
    // Seen-set alone terminates cycle (no max-depth dependency)
    // -----------------------------------------------------------------------

    /// feature:224 — cycle B -> C -> B terminates via seen-set alone
    #[test]
    fn seen_set_terminates_cycle_without_max_depth() {
        let snap_b = make_empty_output();
        let snap_c = make_empty_output();

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        fake.insert(
            "B",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("C", true)])),
        );
        fake.insert("C", "orchard --json", ok(&ser(&snap_c)));
        fake.insert(
            "C",
            "orchard list-remotes --json",
            ok(&list_remotes_json(&[("B", true)])),
        );

        let config = WalkerConfig::new(Arc::new(fake) as Arc<dyn SshExec>).with_max_depth(1000); // ensure seen-set (not depth) terminates
        let result = walk(&[("B", true)], &config);

        assert!(
            result.errors.is_empty(),
            "cycle terminates cleanly: {:?}",
            result.errors
        );
        assert_eq!(result.snapshots.len(), 2, "B and C each visited once");
    }

    // -----------------------------------------------------------------------
    // Per-hop timeout fires
    // -----------------------------------------------------------------------

    /// feature:284, feature:292 — timeout fires and produces TransitiveError
    #[test]
    fn per_hop_timeout_fires() {
        let timeout = Duration::from_millis(50);

        struct HangingSshExec;
        impl SshExec for HangingSshExec {
            fn exec(&self, _host: &str, _cmd: &str) -> anyhow::Result<SshOutput> {
                std::thread::sleep(Duration::from_secs(60));
                unreachable!()
            }
        }

        let config = WalkerConfig::new(Arc::new(HangingSshExec) as Arc<dyn SshExec>)
            .with_per_hop_timeout(timeout);
        let result = walk(&[("slow-host", true)], &config);

        assert_eq!(result.snapshots.len(), 0, "timeout must prevent snapshot");
        assert_eq!(result.errors.len(), 1, "must have one TransitiveError");
        assert!(
            result.errors[0].reason.contains("timeout"),
            "reason must contain 'timeout', got: {}",
            result.errors[0].reason
        );
    }

    // -----------------------------------------------------------------------
    // Fix 4: allow_transitive=false root produces zero SSH calls from the walker
    // -----------------------------------------------------------------------

    /// Fix 4 — `allow_transitive=false` roots are not seeded into the walker.
    ///
    /// When a root is configured with `allow_transitive=false`, the walker must
    /// make zero SSH calls for it (no `orchard --json`, no `list-remotes`).
    /// The snapshot for such a root is handled by OrchardProxyAdapter, not
    /// the walker.
    #[test]
    fn fix4_allow_transitive_false_root_produces_zero_ssh_calls() {
        use std::sync::atomic::{AtomicUsize, Ordering};

        struct CountingSshExec {
            count: Arc<AtomicUsize>,
        }
        impl SshExec for CountingSshExec {
            fn exec(&self, _host: &str, _cmd: &str) -> anyhow::Result<SshOutput> {
                self.count.fetch_add(1, Ordering::SeqCst);
                // Returning error is fine — the test asserts zero calls.
                Err(anyhow::anyhow!("should not be called"))
            }
        }

        let call_count = Arc::new(AtomicUsize::new(0));
        let ssh = Arc::new(CountingSshExec {
            count: Arc::clone(&call_count),
        }) as Arc<dyn SshExec>;

        let config = WalkerConfig::new(ssh);
        // Pass a root with allow_transitive=false.
        let result = walk(&[("direct-host", false)], &config);

        assert_eq!(
            call_count.load(Ordering::SeqCst),
            0,
            "allow_transitive=false root must produce zero SSH calls from the walker"
        );
        assert!(
            result.snapshots.is_empty(),
            "allow_transitive=false root must produce no walker snapshots (snapshot handled by OrchardProxyAdapter)"
        );
        assert!(
            result.errors.is_empty(),
            "allow_transitive=false root must produce no errors"
        );
    }

    // -----------------------------------------------------------------------
    // Fix 6: list-remotes failure after successful snapshot is surfaced
    // -----------------------------------------------------------------------

    /// Fix 6 — `list-remotes` failure after a successful `orchard --json` fetch
    /// surfaces a `TransitiveError` with `phase: "list_remotes_after_snapshot"`.
    ///
    /// Without the fix, the walker silently returned `Success{children: vec![]}`,
    /// causing the topology to shrink a level with no operator signal.
    #[test]
    fn fix6_list_remotes_failure_after_snapshot_surfaces_error() {
        let snap_b = make_output_with_branch("issue99/b");

        let mut fake = FakeSshExec::new();
        fake.insert("B", "orchard --json", ok(&ser(&snap_b)));
        // list-remotes fails with a non-127 exit code (network blip).
        fake.insert("B", "orchard list-remotes --json", exit_code(1));

        let result = walk(&[("B", true)], &walker(fake));

        // The snapshot for B must still be recorded.
        assert_eq!(result.snapshots.len(), 1, "B's snapshot must be present");
        assert_eq!(result.snapshots[0].0.as_slice(), ["local", "B"]);

        // A TransitiveError must be emitted for the list-remotes failure.
        assert_eq!(
            result.errors.len(),
            1,
            "list-remotes failure must surface a TransitiveError"
        );
        assert_eq!(
            result.errors[0].phase, "list_remotes_after_snapshot",
            "phase must be 'list_remotes_after_snapshot' to distinguish from full-hop failures"
        );
    }
}
