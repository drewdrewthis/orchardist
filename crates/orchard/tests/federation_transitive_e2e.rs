//! End-to-end regression tests for transitive federation (issue #363, AC5 + AC9).
//!
//! Exercises the full walk → merge pipeline using [`FakeSshExec`] canned
//! responses.  No real SSH is involved.
//!
//! Scenarios:
//! - AC5: Write-path chaining — `kill_remote_tmux_session` with a depth-2
//!   `discovery_path` resolves to the correct jump host and nested SSH command.
//! - AC9: Two-hop (A → B → C): C's worktrees carry `discovery_path = ["local","B","C"]`
//! - AC9: Three-hop (A → B → C → D): D's snapshot appears; tagged with 4-element path
//! - AC9: Cycle (A → B → A): walk terminates, each host fetched once, no duplicates

use std::collections::HashMap;
use std::sync::Arc;

use orchard::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
use orchard::json_output::{JsonOutput, JsonRepo, JsonWorktree};
use orchard::merge_remote::merge_remote_snapshot_with_path;
use orchard::remote_adapter::{FakeSshExec, RemoteKind, SshOutput};
use orchard::transitive_walker::{WalkerConfig, walk};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn ok(stdout: &str) -> SshOutput {
    SshOutput {
        stdout: stdout.to_string(),
        stderr: String::new(),
        exit_code: 0,
    }
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

fn make_snapshot(branch: &str) -> JsonOutput {
    JsonOutput {
        version: 6,
        tmux_sessions: vec![],
        repos: vec![JsonRepo {
            slug: "owner/repo".to_string(),
            default_branch: None,
            main_ci_state: None,
            worktrees: vec![JsonWorktree {
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

fn make_config(root_host: &str) -> GlobalConfig {
    GlobalConfig {
        repos: vec![RepoConfig {
            slug: "owner/repo".to_string(),
            path: "/local/repo".to_string(),
            remotes: vec![RemoteConfig {
                name: "root".to_string(),
                host: root_host.to_string(),
                path: "/remote/repo".to_string(),
                shell: "ssh".to_string(),
                kind: RemoteKind::OrchardProxy,
                allow_transitive: true,
            }],
        }],
        ..GlobalConfig::default()
    }
}

/// Merges all walker snapshots into a fresh state and returns it.
fn merge_all(
    config: &GlobalConfig,
    walker_snapshots: &[(Vec<String>, std::sync::Arc<JsonOutput>)],
) -> orchard::orchard_state::OrchardState {
    let mut state = orchard::build_state::build_state_with_hosts(config, &HashMap::new());
    for (discovery_path, snapshot) in walker_snapshots {
        let host = discovery_path.last().unwrap().clone();
        let snap: JsonOutput = (*snapshot.clone()).clone();
        merge_remote_snapshot_with_path(&mut state, snap, host, Some(discovery_path.clone()));
    }
    state
}

// ---------------------------------------------------------------------------
// AC9 — Two-hop graph A (local) → B → C
// ---------------------------------------------------------------------------

/// AC9: two-hop graph terminates; C's worktrees carry discovery_path == ["local","B","C"].
#[test]
fn ac9_two_hop_graph_c_worktree_has_discovery_path() {
    let snap_b = make_snapshot("issue1/b");
    let snap_c = make_snapshot("issue2/c");

    let mut fake = FakeSshExec::new();
    fake.insert("B", "orchard-tui --json", ok(&ser(&snap_b)));
    fake.insert(
        "B",
        "orchard list-remotes --json",
        // B advertises C with allow_transitive:true so the walker fetches C.
        // C's own list-remotes returns empty (leaf).
        ok(&list_remotes_json(&[("C", true)])),
    );
    fake.insert("C", "orchard-tui --json", ok(&ser(&snap_c)));
    fake.insert(
        "C",
        "orchard list-remotes --json",
        ok(&list_remotes_empty()),
    );

    let cfg = WalkerConfig::new(Arc::new(fake) as Arc<dyn orchard::remote_adapter::SshExec>);
    let result = walk(&[("B", true)], &cfg);

    assert!(
        result.errors.is_empty(),
        "no errors expected for 2-hop graph; got: {:?}",
        result.errors
    );

    // Both B and C must appear in walker snapshots.
    let b_snap = result
        .snapshots
        .iter()
        .find(|(p, _)| p.last().map(String::as_str) == Some("B"))
        .expect("B must appear in walker snapshots");
    let c_snap = result
        .snapshots
        .iter()
        .find(|(p, _)| p.last().map(String::as_str) == Some("C"))
        .expect("C must appear in walker snapshots");

    assert_eq!(
        b_snap.0,
        vec!["local".to_string(), "B".to_string()],
        "B discovery_path must be [local, B]"
    );
    assert_eq!(
        c_snap.0,
        vec!["local".to_string(), "B".to_string(), "C".to_string()],
        "C discovery_path must be [local, B, C]"
    );

    // Merge into state and verify discovery_path on WorktreeState.
    let config = make_config("B");
    let state = merge_all(&config, &result.snapshots);

    let c_wt = state
        .repos
        .iter()
        .flat_map(|r| r.worktrees.iter())
        .find(|w| w.branch == "issue2/c")
        .expect("C's worktree must be present in merged state");

    assert_eq!(
        c_wt.discovery_path.as_deref(),
        Some(["local".to_string(), "B".to_string(), "C".to_string()].as_slice()),
        "C's WorktreeState must carry discovery_path [local, B, C]"
    );

    let b_wt = state
        .repos
        .iter()
        .flat_map(|r| r.worktrees.iter())
        .find(|w| w.branch == "issue1/b")
        .expect("B's worktree must be present in merged state");

    assert_eq!(
        b_wt.discovery_path.as_deref(),
        Some(["local".to_string(), "B".to_string()].as_slice()),
        "B's WorktreeState must carry discovery_path [local, B]"
    );
}

// ---------------------------------------------------------------------------
// AC9 — Three-hop graph A → B → C → D
// ---------------------------------------------------------------------------

/// AC9: three-hop graph terminates; D's worktrees carry discovery_path == ["local","B","C","D"].
#[test]
fn ac9_three_hop_graph_d_worktree_has_discovery_path() {
    let snap_b = make_snapshot("issue10/b");
    let snap_c = make_snapshot("issue11/c");
    let snap_d = make_snapshot("issue12/d");

    let mut fake = FakeSshExec::new();
    fake.insert("B", "orchard-tui --json", ok(&ser(&snap_b)));
    fake.insert(
        "B",
        "orchard list-remotes --json",
        ok(&list_remotes_json(&[("C", true)])),
    );
    fake.insert("C", "orchard-tui --json", ok(&ser(&snap_c)));
    fake.insert(
        "C",
        "orchard list-remotes --json",
        // C advertises D with allow_transitive:true.
        ok(&list_remotes_json(&[("D", true)])),
    );
    fake.insert("D", "orchard-tui --json", ok(&ser(&snap_d)));
    fake.insert(
        "D",
        "orchard list-remotes --json",
        ok(&list_remotes_empty()),
    );

    let cfg = WalkerConfig::new(Arc::new(fake) as Arc<dyn orchard::remote_adapter::SshExec>);
    let result = walk(&[("B", true)], &cfg);

    assert!(
        result.errors.is_empty(),
        "no errors expected for 3-hop graph; got: {:?}",
        result.errors
    );

    let d_entry = result
        .snapshots
        .iter()
        .find(|(p, _)| p.last().map(String::as_str) == Some("D"))
        .expect("D must appear in walker snapshots");

    assert_eq!(
        d_entry.0,
        vec![
            "local".to_string(),
            "B".to_string(),
            "C".to_string(),
            "D".to_string(),
        ],
        "D discovery_path must be [local, B, C, D]"
    );

    // Merge and verify on WorktreeState.
    let config = make_config("B");
    let state = merge_all(&config, &result.snapshots);

    let d_wt = state
        .repos
        .iter()
        .flat_map(|r| r.worktrees.iter())
        .find(|w| w.branch == "issue12/d")
        .expect("D's worktree must be present in merged state");

    assert_eq!(
        d_wt.discovery_path.as_deref(),
        Some(
            [
                "local".to_string(),
                "B".to_string(),
                "C".to_string(),
                "D".to_string(),
            ]
            .as_slice()
        ),
        "D's WorktreeState must carry discovery_path [local, B, C, D]"
    );
}

// ---------------------------------------------------------------------------
// AC9 — Cycle graph A → B → A
// ---------------------------------------------------------------------------

/// AC9: cycle graph terminates; each host fetched once; no duplicate worktrees.
///
/// B advertises A as a transitive remote. A is in the roots' seen-set, so B's
/// advertisement of A is skipped. Walk terminates without error.
#[test]
fn ac9_cycle_graph_terminates_no_duplicates() {
    let snap_a = make_snapshot("issue21/a");
    let snap_b = make_snapshot("issue22/b");

    let mut fake = FakeSshExec::new();
    // A is a root with allow_transitive — it advertises B.
    fake.insert("A", "orchard-tui --json", ok(&ser(&snap_a)));
    fake.insert(
        "A",
        "orchard list-remotes --json",
        ok(&list_remotes_json(&[("B", true)])),
    );
    // B is discovered via A and advertises A back (cycle).
    fake.insert("B", "orchard-tui --json", ok(&ser(&snap_b)));
    fake.insert(
        "B",
        "orchard list-remotes --json",
        ok(&list_remotes_json(&[("A", true)])),
    );

    let cfg = WalkerConfig::new(Arc::new(fake) as Arc<dyn orchard::remote_adapter::SshExec>)
        .with_max_depth(100);
    let result = walk(&[("A", true)], &cfg);

    // Walk must terminate without error.
    assert!(
        result.errors.is_empty(),
        "cycle must not produce errors; got: {:?}",
        result.errors
    );

    // Each host appears exactly once.
    let a_count = result
        .snapshots
        .iter()
        .filter(|(p, _)| p.last().map(String::as_str) == Some("A"))
        .count();
    let b_count = result
        .snapshots
        .iter()
        .filter(|(p, _)| p.last().map(String::as_str) == Some("B"))
        .count();
    assert_eq!(
        a_count, 1,
        "A must appear exactly once; seen-set prevents re-fetch"
    );
    assert_eq!(b_count, 1, "B must appear exactly once");

    // Merge into state — both worktrees present, neither duplicated.
    let config = make_config("A");
    let state = merge_all(&config, &result.snapshots);

    let all_branches: Vec<&str> = state
        .repos
        .iter()
        .flat_map(|r| r.worktrees.iter())
        .map(|w| w.branch.as_str())
        .collect();

    assert!(
        all_branches.contains(&"issue21/a"),
        "A's worktree must be present; branches: {all_branches:?}"
    );
    assert!(
        all_branches.contains(&"issue22/b"),
        "B's worktree must be present; branches: {all_branches:?}"
    );

    let a_dups = all_branches.iter().filter(|&&b| b == "issue21/a").count();
    let b_dups = all_branches.iter().filter(|&&b| b == "issue22/b").count();
    assert_eq!(a_dups, 1, "A's worktree must not be duplicated");
    assert_eq!(b_dups, 1, "B's worktree must not be duplicated");
}

// ---------------------------------------------------------------------------
// AC5 — Write-path chaining for transitive nodes
// ---------------------------------------------------------------------------

/// AC5: Given a WorktreeRow with discovery_path ["local","B","C"] (depth-2
/// transitive topology), `chain_cmd` resolves the SSH target to "B" and the
/// resolved command contains the nested `ssh 'C' '...'` form.
///
/// This proves that `kill_remote_tmux_session`, `remove_remote_worktree`, and
/// the other write-path callers route through `build_ssh_chain` for depth-2
/// hosts without performing a real SSH round-trip.
#[test]
fn ac5_write_path_depth2_routes_through_jump_host() {
    // Topology: local → B → C (depth-2).
    let discovery_path: Vec<String> = vec!["local".to_string(), "B".to_string(), "C".to_string()];

    // Simulate what `kill_remote_tmux_session("C", "my-session", Some(&dp))` does
    // internally: call chain_cmd to get the SSH target and chained command.
    let inner_cmd = "tmux kill-session -t my-session";
    let (ssh_target, chained_cmd) =
        orchard::remote::chain_cmd("C", Some(&discovery_path), inner_cmd);

    // The SSH target must be the jump host (B), not the leaf (C).
    assert_eq!(
        ssh_target, "B",
        "write-path must target jump host B, not leaf host C"
    );

    // The chained command must contain a nested ssh to C.
    assert!(
        chained_cmd.contains("ssh") && chained_cmd.contains("C"),
        "chained command must contain nested ssh to C; got: {chained_cmd:?}"
    );

    // Full form check: `ssh B ssh 'C' 'tmux kill-session -t my-session'`
    let expected = orchard::federation::build_ssh_chain(&discovery_path, inner_cmd);
    assert_eq!(
        chained_cmd, expected,
        "chain_cmd result must match build_ssh_chain directly; got: {chained_cmd:?}"
    );
}

/// AC5: Depth-1 direct remote is bit-identical to before — no nesting, no regression.
#[test]
fn ac5_write_path_depth1_unchanged() {
    let discovery_path: Vec<String> = vec!["local".to_string(), "B".to_string()];

    let inner_cmd = "tmux kill-session -t my-session";
    let (ssh_target, chained_cmd) =
        orchard::remote::chain_cmd("B", Some(&discovery_path), inner_cmd);

    // Depth-1: target is unchanged.
    assert_eq!(ssh_target, "B", "depth-1 must target B directly");

    // Command is passed through unchanged.
    assert_eq!(
        chained_cmd, inner_cmd,
        "depth-1 command must be unchanged; got: {chained_cmd:?}"
    );
}

/// AC5: No discovery_path (legacy / Remmy-type remotes) — completely unchanged.
#[test]
fn ac5_write_path_no_discovery_path_unchanged() {
    let inner_cmd = "tmux kill-session -t my-session";
    let (ssh_target, chained_cmd) = orchard::remote::chain_cmd("legacyhost", None, inner_cmd);

    assert_eq!(
        ssh_target, "legacyhost",
        "no-discovery-path must target legacyhost"
    );
    assert_eq!(
        chained_cmd, inner_cmd,
        "no-discovery-path command must be unchanged; got: {chained_cmd:?}"
    );
}

/// AC5: Three-hop chain (local → B → C → D) produces ssh B ssh 'C' ssh 'D' '...'
/// and targets B as the jump host.
#[test]
fn ac5_write_path_depth3_triple_hop() {
    let discovery_path: Vec<String> = vec![
        "local".to_string(),
        "B".to_string(),
        "C".to_string(),
        "D".to_string(),
    ];

    let inner_cmd = "tmux kill-session -t sess";
    let (ssh_target, chained_cmd) =
        orchard::remote::chain_cmd("D", Some(&discovery_path), inner_cmd);

    // Jump host is B (the first non-local hop).
    assert_eq!(ssh_target, "B", "3-hop chain must target B as jump host");

    // Full expected form from build_ssh_chain.
    let expected = orchard::federation::build_ssh_chain(&discovery_path, inner_cmd);
    assert_eq!(
        chained_cmd, expected,
        "3-hop chain_cmd must match build_ssh_chain; got: {chained_cmd:?}"
    );

    // The chained command must contain both C and D.
    assert!(
        chained_cmd.contains("C") && chained_cmd.contains("D"),
        "chained command must reference both intermediate and leaf host; got: {chained_cmd:?}"
    );
}
