/// Tests for slice 1 of issue #267 — hexagonal RemoteWorktreeService port.
///
/// All scenarios are @unit: no live SSH, no filesystem I/O beyond what the
/// fake executor provides. Tests fail red until production code fills in
/// the `unimplemented!()` stubs in `remote_adapter.rs` and migrates
/// `global_config::RemoteConfig` to carry the required `kind` field.
///
/// Feature file: specs/features/boxd-first-class-backend.feature
use orchard::global_config::RemoteConfig;
use orchard::remote_adapter::{
    BoxdForkAdapter, FakeSshExec, RemmyAdapter, RemoteAdapter, RemoteKind, SshExec, SshOutput,
};

// ---------------------------------------------------------------------------
// Scenario: feature.feature:22
// RemoteWorktreeService port defines a minimal stable surface
// ---------------------------------------------------------------------------

/// The port exposes `list_worktrees` and `list_sessions` on all three adapter
/// variants, each returning a typed `Result`.
///
/// These tests call each method on each variant and assert `Ok`. They fail
/// red (panic from `unimplemented!`) until the production code fills in the
/// stubs. When production code lands, the `unimplemented!` is replaced with
/// real logic and these tests pass.
#[test]
fn port_list_worktrees_returns_ok_for_remmy_variant() {
    // feature.feature:22
    let adapter = RemoteAdapter::Remmy(RemmyAdapter {
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
        ssh: Box::new(FakeSshExec::new()),
    });
    let result = adapter.list_worktrees();
    assert!(
        result.is_ok(),
        "list_worktrees must return Ok; got: {result:?}"
    );
}

#[test]
fn port_list_sessions_returns_ok_for_remmy_variant() {
    // feature.feature:22
    let adapter = RemoteAdapter::Remmy(RemmyAdapter {
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
        ssh: Box::new(FakeSshExec::new()),
    });
    let result = adapter.list_sessions();
    assert!(
        result.is_ok(),
        "list_sessions must return Ok; got: {result:?}"
    );
}

#[test]
fn port_list_worktrees_returns_ok_for_boxd_shared_variant() {
    // feature.feature:22
    use orchard::remote_adapter::BoxdSharedAdapter;
    let adapter = RemoteAdapter::BoxdShared(BoxdSharedAdapter {
        host: "boxd@orchard-rs.boxd.sh".to_string(),
        path: "~/git-orchard-rs".to_string(),
        ssh: Box::new(FakeSshExec::new()),
    });
    let result = adapter.list_worktrees();
    assert!(
        result.is_ok(),
        "list_worktrees must return Ok; got: {result:?}"
    );
}

#[test]
fn port_list_worktrees_returns_ok_for_boxd_fork_variant() {
    // feature.feature:22 — port surface check. BoxdFork now returns
    // Err(FetchFailure) when the golden host is unreachable (so the
    // caller can distinguish outage from "zero forks"); the surface
    // check therefore needs a successful list response stub. An empty
    // JSON array is the simplest valid payload.
    let mut fake = FakeSshExec::new();
    fake.insert(
        "boxd.sh",
        "list --json",
        SshOutput {
            stdout: "[]".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );
    let adapter = RemoteAdapter::BoxdFork(BoxdForkAdapter {
        golden_host: "boxd.sh".to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    });
    let result = adapter.list_worktrees();
    assert!(
        result.is_ok(),
        "list_worktrees must return Ok on a successful empty list; got: {result:?}"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:33
// SSH exec is injectable so adapters are unit-testable without the network
// ---------------------------------------------------------------------------

/// FakeSshExec returns canned responses keyed on (host, cmd).
/// Adapters accept `Box<dyn SshExec>`, so unit tests need no real `ssh`.
#[test]
fn fake_ssh_exec_returns_canned_stdout_without_spawning_a_subprocess() {
    // feature.feature:33
    let mut fake = FakeSshExec::new();
    fake.insert(
        "ubuntu@10.0.3.56",
        "echo hello",
        SshOutput {
            stdout: "hello\n".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let result = fake.exec("ubuntu@10.0.3.56", "echo hello").unwrap();
    assert_eq!(result.stdout, "hello\n");
    assert_eq!(result.exit_code, 0);
    // A missing key must return an error (not a silent empty result).
    assert!(fake.exec("ubuntu@10.0.3.56", "unknown-cmd").is_err());
}

/// RemmyAdapter accepts a `Box<dyn SshExec>` — the seam is in the adapter struct,
/// not bolted onto each test.
#[test]
fn remmy_adapter_accepts_box_dyn_ssh_exec_seam() {
    // feature.feature:33
    let ssh: Box<dyn SshExec> = Box::new(FakeSshExec::new());
    // Constructing the adapter with a fake runner must compile.
    let _adapter = RemmyAdapter {
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
        ssh,
    };
    // If we got here, the seam exists on the struct (not only in tests).
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:40
// Adapter dispatch selects implementation from RemoteConfig.type
// ---------------------------------------------------------------------------

/// `RemoteAdapter::from_config` with kind=Remmy wraps a RemmyAdapter.
#[test]
fn dispatch_remmy_type_returns_remmy_adapter() {
    // feature.feature:40
    let cfg = RemoteConfig {
        name: "my-remote".to_string(),
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
        shell: "ssh".to_string(),
        kind: RemoteKind::Remmy,
    };
    let ssh: Box<dyn SshExec> = Box::new(FakeSshExec::new());
    let adapter = RemoteAdapter::from_config(&cfg, ssh);
    assert!(
        matches!(adapter, RemoteAdapter::Remmy(_)),
        "from_config with kind=Remmy must return RemoteAdapter::Remmy"
    );
}

/// `RemoteAdapter::from_config` with kind=BoxdShared wraps a BoxdSharedAdapter.
#[test]
fn dispatch_boxd_shared_type_returns_boxd_shared_adapter() {
    // feature.feature:40
    let cfg = RemoteConfig {
        name: "my-boxd".to_string(),
        host: "boxd@orchard-rs.boxd.sh".to_string(),
        path: "~/git-orchard-rs".to_string(),
        shell: "ssh".to_string(),
        kind: RemoteKind::BoxdShared,
    };
    let ssh: Box<dyn SshExec> = Box::new(FakeSshExec::new());
    let adapter = RemoteAdapter::from_config(&cfg, ssh);
    assert!(
        matches!(adapter, RemoteAdapter::BoxdShared(_)),
        "from_config with kind=BoxdShared must return RemoteAdapter::BoxdShared"
    );
}

/// `RemoteAdapter::from_config` with kind=BoxdFork wraps a BoxdForkAdapter.
#[test]
fn dispatch_boxd_fork_type_returns_boxd_fork_adapter() {
    // feature.feature:40
    let cfg = RemoteConfig {
        name: "boxd-fork-langwatch".to_string(),
        host: "boxd.sh".to_string(),
        path: "~/langwatch".to_string(),
        shell: "ssh".to_string(),
        kind: RemoteKind::BoxdFork,
    };
    let ssh: Box<dyn SshExec> = Box::new(FakeSshExec::new());
    let adapter = RemoteAdapter::from_config(&cfg, ssh);
    assert!(
        matches!(adapter, RemoteAdapter::BoxdFork(_)),
        "from_config with kind=BoxdFork must return RemoteAdapter::BoxdFork"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:52
// Unknown remote type fails fast with actionable error
// ---------------------------------------------------------------------------

/// Deserializing a `RemoteConfig` with `"type": "gcp-vm"` must fail, and the
/// serde error must mention the unknown variant name.
///
/// This collapses scenario 4 into the serde layer: because `RemoteKind` is an
/// enum, there is no runtime "unknown type" path — the config is rejected at
/// parse time with an error that lists supported variants.
#[test]
fn unknown_remote_type_rejected_by_serde_with_named_variants() {
    // feature.feature:52
    let json = r#"{
        "name": "gpu",
        "host": "ubuntu@10.0.0.1",
        "path": "/home/ubuntu/repo",
        "type": "gcp-vm"
    }"#;
    let result: Result<RemoteConfig, _> = serde_json::from_str(json);
    assert!(
        result.is_err(),
        "unknown type 'gcp-vm' must fail deserialization"
    );

    let err = result.unwrap_err().to_string();
    // The error must name the offending value so the operator knows what to fix.
    assert!(
        err.contains("gcp-vm")
            || err.contains("unknown variant")
            || err.contains("expected one of"),
        "error must identify the unknown type or list valid variants, got: {err}"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:215
// RemoteConfig schema requires a `type` field
// ---------------------------------------------------------------------------

/// `GlobalConfig` parsed from JSON with a `"type"` field on remote entries must
/// round-trip that field — i.e. `RemoteConfig` must carry it and re-serialize it.
///
/// Fails red until `global_config::RemoteConfig` gains a `kind: RemoteKind`
/// field (serialized as `"type"`) and the public serde impl handles it.
#[test]
fn remote_config_schema_has_required_type_field_with_known_variants() {
    // feature.feature:215
    use orchard::global_config::GlobalConfig;

    // Build a GlobalConfig with a remote that has kind=Remmy.
    // Until RemoteConfig has `kind`, accessing it is a compile error.
    // The test is written so it compiles (no direct .kind access) but
    // fails at runtime: serializing RemoteConfig must include "type".
    let cfg: GlobalConfig = serde_json::from_str(
        r#"{
        "repos": [{
            "slug": "owner/repo",
            "path": "/workspace/repo",
            "remotes": [
                { "name": "r", "host": "h", "path": "/p", "type": "remmy" }
            ]
        }]
    }"#,
    )
    .expect("GlobalConfig must parse");

    let remote = cfg.repos[0]
        .remotes
        .first()
        .expect("remote must be present");

    // Round-trip the remote through serde_json::Value.
    let val = serde_json::to_value(remote).expect("RemoteConfig must serialize");

    // Fails red until RemoteConfig carries and serializes the "type" field.
    assert!(
        val.get("type").is_some(),
        "serialized RemoteConfig must include a 'type' field; got: {val}"
    );

    let type_str = val["type"].as_str().expect("'type' must be a string");
    assert_eq!(
        type_str, "remmy",
        "'type' must serialize as 'remmy', got {type_str:?}"
    );
}

/// `RemoteKind` round-trips through serde as kebab-case strings.
/// Tests the `remote_adapter` type — passes immediately as a shape guard.
#[test]
fn remote_kind_serializes_as_kebab_case() {
    // feature.feature:215
    let cases = [
        (RemoteKind::Remmy, "\"remmy\""),
        (RemoteKind::BoxdShared, "\"boxd-shared\""),
        (RemoteKind::BoxdFork, "\"boxd-fork\""),
    ];
    for (kind, expected_json) in cases {
        let serialized = serde_json::to_string(&kind).unwrap();
        assert_eq!(
            serialized, expected_json,
            "{kind:?} must serialize to {expected_json}"
        );
    }
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:221
// Missing type field produces a clear validation error
// ---------------------------------------------------------------------------

/// A config entry with `name: "gpu"` and `host` but no `"type"` must fail to
/// parse `GlobalConfig` — the missing field must be surfaced as a parse error.
///
/// Fails red until the `GlobalConfig` serde impl (or its raw intermediate)
/// enforces the `"type"` field on remote entries.
#[test]
fn missing_type_field_in_global_config_fails_to_load() {
    // feature.feature:221
    use orchard::global_config::GlobalConfig;

    // Entry with name "gpu" and host, but no "type".
    let json = r#"{
        "repos": [{
            "slug": "owner/repo",
            "path": "/workspace/repo",
            "remotes": [{
                "name": "gpu",
                "host": "ubuntu@10.0.0.1",
                "path": "/home/ubuntu/repo"
            }]
        }]
    }"#;

    // Until the loader enforces "type", this parses successfully with remotes populated.
    // Once enforced, it should either return Err (serde) or Ok with empty remotes.
    let result: Result<GlobalConfig, _> = serde_json::from_str(json);

    let remotes_loaded = result
        .as_ref()
        .ok()
        .and_then(|cfg| cfg.repos.first())
        .map(|r| r.remotes.len())
        .unwrap_or(0);

    // Fails red: currently parses with 1 remote. Must be 0 once enforced.
    assert_eq!(
        remotes_loaded, 0,
        "remote entry without 'type' must be rejected (0 remotes loaded); \
         got {remotes_loaded} remote(s) — loader does not yet enforce 'type'"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:228
// Legacy configs without a type are not silently auto-detected by name
// ---------------------------------------------------------------------------

/// An entry whose `name` contains "boxd" but omits `"type"` must fail the
/// same way as any other entry missing `"type"` — not be promoted to boxd-shared.
///
/// Fails red until the config loader enforces the `type` field.
#[test]
fn name_containing_boxd_without_type_is_not_silently_auto_detected() {
    // feature.feature:228
    use orchard::global_config::GlobalConfig;

    let json = r#"{
        "repos": [{
            "slug": "owner/repo",
            "path": "/workspace/repo",
            "remotes": [{
                "name": "boxd-fork-langwatch",
                "host": "boxd@orchard-rs.boxd.sh",
                "path": "~/git-orchard-rs"
            }]
        }]
    }"#;

    let result: Result<GlobalConfig, _> = serde_json::from_str(json);
    let remotes_loaded = result
        .as_ref()
        .ok()
        .and_then(|cfg| cfg.repos.first())
        .map(|r| r.remotes.len())
        .unwrap_or(0);

    // Fails red: currently parses with 1 remote despite no "type" field.
    // The loader must not infer type from the name substring "boxd".
    assert_eq!(
        remotes_loaded, 0,
        "entry with 'boxd' in name but no 'type' must be rejected (0 remotes); \
         got {remotes_loaded} — loader must not auto-detect from name"
    );
}

/// An entry whose `name` contains "remmy" without `type` is similarly rejected.
#[test]
fn name_containing_remmy_without_type_is_not_silently_auto_detected() {
    // feature.feature:228
    use orchard::global_config::GlobalConfig;

    let json = r#"{
        "repos": [{
            "slug": "owner/repo",
            "path": "/workspace/repo",
            "remotes": [{
                "name": "remmy-gpu",
                "host": "ubuntu@10.0.3.56",
                "path": "~/langwatch-workspace"
            }]
        }]
    }"#;

    let result: Result<GlobalConfig, _> = serde_json::from_str(json);
    let remotes_loaded = result
        .as_ref()
        .ok()
        .and_then(|cfg| cfg.repos.first())
        .map(|r| r.remotes.len())
        .unwrap_or(0);

    assert_eq!(
        remotes_loaded, 0,
        "entry with 'remmy' in name but no 'type' must be rejected (0 remotes); \
         got {remotes_loaded} — loader must not auto-detect from name"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:63
// RemmyAdapter wraps current universal-git-worktree-list-over-SSH behavior
// (seam/shape only — no live SSH)
// ---------------------------------------------------------------------------

/// Given the porcelain output from `git worktree list --porcelain` via a fake
/// SSH runner, `RemmyAdapter::list_worktrees()` returns exactly the non-bare
/// worktrees with the correct branch and host.
#[test]
fn remmy_adapter_list_worktrees_returns_non_bare_entries_with_correct_host_and_branch() {
    // feature.feature:63

    let host = "ubuntu@10.0.3.56";
    let path = "~/langwatch-workspace";
    let cmd = format!("git -C {path} worktree list --porcelain");
    let porcelain = "\
worktree /home/ubuntu/langwatch-workspace\n\
bare\n\
\n\
worktree /home/ubuntu/langwatch-workspace/worktrees/feat-x\n\
HEAD abc123\n\
branch refs/heads/feat-x\n";

    let mut fake = FakeSshExec::new();
    fake.insert(
        host,
        &cmd,
        SshOutput {
            stdout: porcelain.to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = RemmyAdapter {
        host: host.to_string(),
        path: path.to_string(),
        ssh: Box::new(fake),
    };

    let worktrees = adapter
        .list_worktrees()
        .expect("list_worktrees must not error with a valid fake response");

    assert_eq!(
        worktrees.len(),
        1,
        "exactly 1 non-bare worktree expected, got {}",
        worktrees.len()
    );

    let wt = &worktrees[0];
    assert_eq!(
        wt.branch, "feat-x",
        "branch must be 'feat-x', got {:?}",
        wt.branch
    );
    assert_eq!(
        wt.host.as_deref(),
        Some(host),
        "host must be {host:?}, got {:?}",
        wt.host
    );
    assert!(!wt.is_bare, "returned worktree must not be bare");
}

// ===========================================================================
// Slice 2 — feature.feature:80, :130, :140, :149, :185, :194, :202
// ===========================================================================

// ---------------------------------------------------------------------------
// Scenario: feature.feature:80
// BoxdSharedAdapter preserves current single-VM-with-worktrees behavior
// ---------------------------------------------------------------------------

/// BoxdSharedAdapter uses the same porcelain SSH path as RemmyAdapter.
/// It calls `git -C <path> worktree list --porcelain` on the Boxd VM
/// and returns non-bare worktrees tagged with the boxd host.
///
/// Fails red until BoxdSharedAdapter.list_worktrees() is wired up with real
/// porcelain parsing (currently returns Ok(vec![])).
#[test]
fn boxd_shared_adapter_returns_parsed_non_bare_worktrees_with_correct_host_and_branch() {
    // feature.feature:80
    use orchard::remote_adapter::BoxdSharedAdapter;

    let host = "boxd@orchard-rs.boxd.sh";
    let path = "~/git-orchard-rs";
    let cmd = format!("git -C {path} worktree list --porcelain");
    let porcelain = "\
worktree /home/boxd/git-orchard-rs\n\
bare\n\
\n\
worktree /home/boxd/git-orchard-rs/worktrees/issue240\n\
HEAD def456\n\
branch refs/heads/issue240/smart-sorting\n";

    let mut fake = FakeSshExec::new();
    fake.insert(
        host,
        &cmd,
        SshOutput {
            stdout: porcelain.to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = BoxdSharedAdapter {
        host: host.to_string(),
        path: path.to_string(),
        ssh: Box::new(fake),
    };

    let worktrees = adapter
        .list_worktrees()
        .expect("list_worktrees must not error with a valid fake response");

    // Fails red: stub returns Ok(vec![]) — expects 1 parsed worktree.
    assert_eq!(
        worktrees.len(),
        1,
        "exactly 1 non-bare worktree expected, got {}",
        worktrees.len()
    );

    let wt = &worktrees[0];

    assert_eq!(
        wt.branch, "issue240/smart-sorting",
        "branch must be 'issue240/smart-sorting', got {:?}",
        wt.branch
    );

    assert_eq!(
        wt.host.as_deref(),
        Some(host),
        "host must be {host:?}, got {:?}",
        wt.host
    );

    assert!(!wt.is_bare, "returned worktree must not be bare");

    // BoxdShared uses bare-repo model, so layout must be Bare.
    assert_eq!(
        wt.layout,
        orchard::cache::WorktreeLayout::Bare,
        "BoxdSharedAdapter worktrees must carry layout=Bare"
    );
}

/// BoxdSharedAdapter returns an empty list (not an error) when the SSH runner
/// returns non-zero exit code — consistent with RemmyAdapter's degraded behavior.
#[test]
fn boxd_shared_adapter_returns_empty_on_ssh_failure() {
    // feature.feature:80 — degraded path
    use orchard::remote_adapter::BoxdSharedAdapter;

    let host = "boxd@orchard-rs.boxd.sh";
    let path = "~/git-orchard-rs";
    // No canned response → FakeSshExec will return Err.
    let adapter = BoxdSharedAdapter {
        host: host.to_string(),
        path: path.to_string(),
        ssh: Box::new(FakeSshExec::new()),
    };

    // Per the RemmyAdapter contract: SSH failure returns Ok(vec![]) rather than Err.
    let result = adapter.list_worktrees();
    assert!(
        result.is_ok(),
        "SSH failure must not propagate as Err; got {result:?}"
    );
    // Stub currently returns Ok(vec![]) so this passes already — the real test
    // is the non-empty assertion in the success case above.
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:140
// CachedWorktree model carries layout flag
// ---------------------------------------------------------------------------

/// CachedWorktree now has a `layout` field of type WorktreeLayout.
/// Fails red for any assertion that requires WorktreeState / JsonOutput to also
/// carry the field — those are NOT wired yet.
#[test]
fn cached_worktree_has_layout_field_defaulting_to_bare() {
    // feature.feature:140
    use orchard::cache::{CachedWorktree, WorktreeLayout};

    // Construct a CachedWorktree with explicit layout=Flat.
    let wt = CachedWorktree {
        path: "/home/boxd/langwatch".to_string(),
        branch: "issue3155/foo".to_string(),
        is_bare: false,
        is_locked: false,
        host: Some("boxd@issue3155.boxd.sh".to_string()),
        ahead: None,
        behind: None,
        last_commit_at: None,
        layout: WorktreeLayout::Flat,
    };

    assert_eq!(
        wt.layout,
        WorktreeLayout::Flat,
        "CachedWorktree.layout must carry WorktreeLayout::Flat"
    );
}

/// Legacy cache JSON without a `layout` key must deserialize with layout=Bare
/// (serde(default) on the field).
#[test]
fn cached_worktree_layout_defaults_to_bare_when_missing_from_json() {
    // feature.feature:140 — backward compat with on-disk caches
    use orchard::cache::{CachedWorktree, WorktreeLayout};

    let json = r#"{
        "path": "/home/ubuntu/langwatch-workspace/worktrees/feat-x",
        "branch": "feat-x",
        "is_bare": false,
        "is_locked": false
    }"#;

    let wt: CachedWorktree = serde_json::from_str(json)
        .expect("legacy cache JSON (no layout field) must deserialize without error");

    assert_eq!(
        wt.layout,
        WorktreeLayout::Bare,
        "layout must default to Bare when absent from on-disk cache"
    );
}

/// WorktreeLayout round-trips through serde as kebab-case strings.
#[test]
fn worktree_layout_serializes_as_kebab_case() {
    // feature.feature:140
    use orchard::cache::WorktreeLayout;

    let bare = serde_json::to_string(&WorktreeLayout::Bare).unwrap();
    let flat = serde_json::to_string(&WorktreeLayout::Flat).unwrap();

    assert_eq!(bare, "\"bare\"", "Bare must serialize as 'bare'");
    assert_eq!(flat, "\"flat\"", "Flat must serialize as 'flat'");

    // Round-trip.
    let parsed: WorktreeLayout = serde_json::from_str("\"flat\"").unwrap();
    assert_eq!(parsed, WorktreeLayout::Flat);
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:149
// JsonOutput version is bumped and layout field documented
//
// Current version is 6 (bumped from 5 to add status/statusGlyph fields, issue #320).
// ---------------------------------------------------------------------------

/// Constructing a JsonOutput from an OrchardState with a Flat-layout worktree
/// must emit version 6 (current) and include "layout" on each worktree.
#[test]
fn json_output_version_bumped_to_next_and_includes_layout_field() {
    // feature.feature:149
    use std::collections::HashMap;

    use orchard::derive::DisplayGroup;
    use orchard::json_output::{JsonOutput, JsonSource};
    use orchard::orchard_state::{OrchardState, RepoState, WorktreeState};

    // Build a minimal OrchardState with two worktrees — one bare-layout (default),
    // one flat (BoxdFork). Until WorktreeState has a layout field, the test
    // fails to compile or asserts the wrong value.
    let bare_wt = WorktreeState {
        path: "/repos/local/main".to_string(),
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
        layout: orchard::cache::WorktreeLayout::Bare,
        source: JsonSource::Local,
    };

    // Until `WorktreeState.layout` exists, we can still test the version bump.
    let state = OrchardState {
        repos: vec![RepoState {
            slug: "owner/repo".to_string(),
            worktrees: vec![bare_wt],
            default_branch: None,
            main_ci_state: None,
        }],
        standalone_sessions: vec![],
        hosts: HashMap::new(),
    };

    let output = JsonOutput::from(&state);
    let value = serde_json::to_value(&output).unwrap();

    let version = value["version"].as_u64().expect("version must be a number");
    assert_eq!(
        version, 6,
        "JsonOutput version must be 6 (bumped for status/statusGlyph fields, issue #320); got {version}"
    );

    // Fails red: JsonWorktree has no layout field yet.
    let wt_value = &value["repos"][0]["worktrees"][0];
    assert!(
        wt_value.get("layout").is_some(),
        "each worktree entry must include a 'layout' field; got: {wt_value}"
    );

    let layout_str = wt_value["layout"]
        .as_str()
        .expect("layout must be a string");
    assert!(
        layout_str == "bare" || layout_str == "flat",
        "layout must be 'bare' or 'flat', got {layout_str:?}"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:194
// boxd.sh unreachable only affects the BoxdForkAdapter — other adapters run normally
// ---------------------------------------------------------------------------

/// When BoxdForkAdapter fails (golden host unreachable), RemmyAdapter and
/// BoxdSharedAdapter still complete. The failure is contained to the BoxdFork
/// variant — enum dispatch means each adapter call is independent.
///
/// This test verifies the isolation property through enum dispatch:
/// - RemmyAdapter with valid canned response → Ok with worktrees
/// - BoxdSharedAdapter with valid canned response → Ok (currently empty stub, passes trivially)
/// - BoxdForkAdapter with no canned response → Err (or Ok from stub — both acceptable as long
///   as Remmy/BoxdShared are unaffected)
///
/// The key assertion is that calling list_worktrees on the Remmy adapter
/// still returns parsed results regardless of whether BoxdFork would fail.
#[test]
fn boxd_fork_unreachable_does_not_affect_remmy_or_boxd_shared_adapters() {
    // feature.feature:194
    use orchard::remote_adapter::BoxdSharedAdapter;

    // --- Remmy: provide real porcelain data ---
    let remmy_host = "ubuntu@10.0.3.56";
    let remmy_path = "~/langwatch-workspace";
    let remmy_cmd = format!("git -C {remmy_path} worktree list --porcelain");
    let remmy_porcelain = "\
worktree /home/ubuntu/langwatch-workspace\n\
bare\n\
\n\
worktree /home/ubuntu/langwatch-workspace/worktrees/issue-7\n\
HEAD aabbcc\n\
branch refs/heads/issue-7/my-feature\n";

    let mut remmy_fake = FakeSshExec::new();
    remmy_fake.insert(
        remmy_host,
        &remmy_cmd,
        SshOutput {
            stdout: remmy_porcelain.to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let remmy_adapter = RemoteAdapter::Remmy(orchard::remote_adapter::RemmyAdapter {
        host: remmy_host.to_string(),
        path: remmy_path.to_string(),
        ssh: Box::new(remmy_fake),
    });

    // --- BoxdFork: no SSH responses → fails with no-canned-response error ---
    let fork_adapter = RemoteAdapter::BoxdFork(orchard::remote_adapter::BoxdForkAdapter {
        golden_host: "boxd.sh".to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(FakeSshExec::new()), // no canned responses
    });

    // --- BoxdShared: valid canned response ---
    let boxd_host = "boxd@orchard-rs.boxd.sh";
    let boxd_path = "~/git-orchard-rs";
    let boxd_cmd = format!("git -C {boxd_path} worktree list --porcelain");
    let mut boxd_fake = FakeSshExec::new();
    boxd_fake.insert(
        boxd_host,
        &boxd_cmd,
        SshOutput {
            stdout: "\
worktree /home/boxd/git-orchard-rs\n\
bare\n\
\n\
worktree /home/boxd/git-orchard-rs/worktrees/issue267\n\
HEAD 112233\n\
branch refs/heads/issue267/boxd-backend\n"
                .to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );
    let boxd_adapter = RemoteAdapter::BoxdShared(BoxdSharedAdapter {
        host: boxd_host.to_string(),
        path: boxd_path.to_string(),
        ssh: Box::new(boxd_fake),
    });

    // Remmy must return worktrees even when BoxdFork would fail.
    let remmy_result = remmy_adapter.list_worktrees();
    assert!(
        remmy_result.is_ok(),
        "RemmyAdapter must succeed regardless of BoxdFork state; got: {remmy_result:?}"
    );
    // Slice 1 already implements porcelain parsing for Remmy.
    let remmy_wts = remmy_result.unwrap();
    assert_eq!(
        remmy_wts.len(),
        1,
        "Remmy must return 1 worktree; got {}",
        remmy_wts.len()
    );

    // BoxdFork failure (missing SSH canned response) must not prevent other adapters.
    // Stub currently returns Ok(vec![]) — acceptable for this isolation test.
    let fork_result = fork_adapter.list_worktrees();
    // We don't assert Ok/Err — the point is that calling fork_result.is_ok()/is_err()
    // does NOT affect the Remmy or BoxdShared calls above.
    let _ = fork_result;

    // BoxdShared must return its worktrees (fails red until real parsing is wired).
    let boxd_result = boxd_adapter.list_worktrees();
    assert!(
        boxd_result.is_ok(),
        "BoxdSharedAdapter must succeed regardless of BoxdFork state; got: {boxd_result:?}"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:202
// Detached HEAD on flat clone reports commit, not the literal "HEAD"
// ---------------------------------------------------------------------------

/// When `git rev-parse --abbrev-ref HEAD` returns "HEAD" (detached),
/// BoxdForkAdapter must fall back to `git rev-parse --short HEAD` and set
/// the branch to "(detached: <sha>)".
///
/// Fails red until BoxdForkAdapter implements the flat-clone parse path with
/// the detached-HEAD fallback. Currently the stub returns Ok(vec![]).
#[test]
fn boxd_fork_adapter_detached_head_produces_formatted_commit_branch() {
    // feature.feature:202
    let fork_host = "boxd.sh";
    let fork_name = "issue3155";
    let fork_vm_host = "boxd@issue3155.boxd.sh";
    let repo_path = "~/langwatch";

    // ssh boxd.sh list --json returns one fork.
    let list_cmd = "list --json";
    let list_json = format!(
        r#"[{{"name": "{fork_name}", "host": "{fork_name}.boxd.sh", "status": "running"}}]"#
    );

    // First branch probe: returns "HEAD" (detached).
    let branch_cmd = format!("cd {repo_path} && git rev-parse --abbrev-ref HEAD");
    // Second probe: short commit hash.
    let commit_cmd = format!("cd {repo_path} && git rev-parse --short HEAD");

    let mut fake = FakeSshExec::new();
    fake.insert(
        fork_host,
        list_cmd,
        SshOutput {
            stdout: list_json,
            stderr: String::new(),
            exit_code: 0,
        },
    );
    fake.insert(
        fork_vm_host,
        &branch_cmd,
        SshOutput {
            stdout: "HEAD\n".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );
    fake.insert(
        fork_vm_host,
        &commit_cmd,
        SshOutput {
            stdout: "abc1234\n".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: fork_host.to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    };

    let worktrees = adapter
        .list_worktrees()
        .expect("list_worktrees must not error");

    // Fails red: stub returns Ok(vec![]) — expects 1 worktree.
    assert_eq!(
        worktrees.len(),
        1,
        "expected 1 flat-clone worktree, got {}",
        worktrees.len()
    );

    let wt = &worktrees[0];

    // Branch must use the detached-HEAD format, not the literal "HEAD".
    assert!(
        !wt.branch.is_empty() && wt.branch != "HEAD",
        "branch must not be the literal 'HEAD', got {:?}",
        wt.branch
    );
    assert!(
        wt.branch.contains("detached") && wt.branch.contains("abc1234"),
        "branch must contain 'detached' and the commit sha 'abc1234', got {:?}",
        wt.branch
    );
    assert_eq!(
        wt.branch, "(detached: abc1234)",
        "branch must be '(detached: abc1234)', got {:?}",
        wt.branch
    );

    // Must carry flat layout.
    assert_eq!(
        wt.layout,
        orchard::cache::WorktreeLayout::Flat,
        "detached-HEAD flat-clone must have layout=Flat"
    );
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:130
// Flat-clone layout parsed without bare-repo assumption
// ---------------------------------------------------------------------------

/// BoxdForkAdapter retrieves the branch via `git rev-parse --abbrev-ref HEAD`
/// (a single command), NOT via `git worktree list --porcelain`. The resulting
/// CachedWorktree must have layout=Flat and path=repo_path (no subdirectory).
///
/// Fails red: stub returns Ok(vec![]).
#[test]
fn boxd_fork_adapter_flat_clone_produces_single_worktree_at_repo_path() {
    // feature.feature:130
    let fork_host = "boxd.sh";
    let fork_name = "issue3155";
    let fork_vm_host = "boxd@issue3155.boxd.sh";
    let repo_path = "~/langwatch";

    let list_cmd = "list --json";
    let list_json = format!(
        r#"[{{"name": "{fork_name}", "host": "{fork_name}.boxd.sh", "status": "running"}}]"#
    );
    let branch_cmd = format!("cd {repo_path} && git rev-parse --abbrev-ref HEAD");

    let mut fake = FakeSshExec::new();
    fake.insert(
        fork_host,
        list_cmd,
        SshOutput {
            stdout: list_json,
            stderr: String::new(),
            exit_code: 0,
        },
    );
    fake.insert(
        fork_vm_host,
        &branch_cmd,
        SshOutput {
            stdout: "issue3155/foo\n".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: fork_host.to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    };

    let worktrees = adapter
        .list_worktrees()
        .expect("list_worktrees must not error");

    // Fails red: stub returns Ok(vec![]).
    assert_eq!(
        worktrees.len(),
        1,
        "expected 1 flat-clone worktree, got {}",
        worktrees.len()
    );

    let wt = &worktrees[0];

    // path must be the repo root, not a subdirectory.
    assert_eq!(
        wt.path, repo_path,
        "flat-clone path must be the repo root {repo_path:?}, got {:?}",
        wt.path
    );

    assert_eq!(
        wt.branch, "issue3155/foo",
        "branch must be 'issue3155/foo', got {:?}",
        wt.branch
    );

    assert_eq!(
        wt.layout,
        orchard::cache::WorktreeLayout::Flat,
        "flat clone must have layout=Flat, not Bare"
    );

    // host must be the per-fork VM host (not the golden host).
    assert_eq!(
        wt.host.as_deref(),
        Some(fork_vm_host),
        "flat-clone host must be {fork_vm_host:?}, got {:?}",
        wt.host
    );

    // is_bare must be false — flat clones are not bare repos.
    assert!(!wt.is_bare, "flat clone must not be bare");
}

// ---------------------------------------------------------------------------
// Scenario: feature.feature:185
// Malformed JSON from `ssh boxd.sh list --json` degrades gracefully
// ---------------------------------------------------------------------------

/// When `ssh boxd.sh list --json` returns invalid JSON, BoxdForkAdapter must
/// return Err(AdapterError::ParseFailure) — not panic, not silently return
/// empty results.
///
/// Fails red: stub currently returns Ok(vec![]) rather than propagating
/// a ParseFailure error.
#[test]
fn boxd_fork_adapter_malformed_list_json_returns_parse_failure_error() {
    // feature.feature:185
    let fork_host = "boxd.sh";
    let list_cmd = "list --json";
    let malformed = "{ this is not valid json [[[";

    let mut fake = FakeSshExec::new();
    fake.insert(
        fork_host,
        list_cmd,
        SshOutput {
            stdout: malformed.to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: fork_host.to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    };

    let result = adapter.list_worktrees();

    // Fails red: stub returns Ok(vec![]).
    // When production code lands, list_worktrees must return Err with
    // a ParseFailure variant when JSON cannot be decoded.
    assert!(
        result.is_err(),
        "malformed JSON from boxd.sh list must return Err, not Ok(vec![]); \
         stub returns Ok(vec![]) — implement BoxdForkAdapter.list_worktrees() to parse \
         `ssh boxd.sh list --json` and return AdapterError::ParseFailure on invalid JSON"
    );

    let err_str = result.unwrap_err().to_string();
    assert!(
        err_str.to_lowercase().contains("parse") || err_str.to_lowercase().contains("boxd"),
        "error must identify the parse failure; got: {err_str}"
    );
}

/// Truncated-payload variant: a partial JSON array also triggers ParseFailure.
#[test]
fn boxd_fork_adapter_truncated_list_json_returns_parse_failure_error() {
    // feature.feature:185
    let fork_host = "boxd.sh";
    let list_cmd = "list --json";
    let truncated = r#"[{"name": "issue3155", "host":"#; // cut off mid-object

    let mut fake = FakeSshExec::new();
    fake.insert(
        fork_host,
        list_cmd,
        SshOutput {
            stdout: truncated.to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: fork_host.to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    };

    let result = adapter.list_worktrees();

    // Fails red: stub returns Ok(vec![]).
    assert!(
        result.is_err(),
        "truncated JSON from boxd.sh list must return Err; \
         stub returns Ok(vec![]) — implement BoxdForkAdapter.list_worktrees()"
    );
}

/// AC7 + review-fix: a transient SSH failure on the BoxdFork golden host
/// must NOT be reported as `Ok(vec![])` — that would cause
/// `cache_sources::refresh_remote_worktrees` to walk every previously-
/// cached fork and emit a `worktree.remote_lost` event for each. Returning
/// `Err(AdapterError::FetchFailure)` distinguishes "fetch failed" from
/// "fetch succeeded with zero entries" so the caller preserves the cache.
#[test]
fn boxd_fork_adapter_returns_fetch_failure_not_empty_on_golden_host_ssh_failure() {
    use orchard::remote_adapter::AdapterError;

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: "boxd.sh".to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        // FakeSshExec with no canned response — every exec returns Err.
        ssh: Box::new(FakeSshExec::new()),
    };

    let result = adapter.list_worktrees();
    assert!(
        result.is_err(),
        "golden-host SSH failure must propagate as Err, got: {result:?}"
    );

    // Downcast to AdapterError::FetchFailure to confirm the variant.
    let err = result.unwrap_err();
    let downcast = err
        .downcast_ref::<AdapterError>()
        .expect("error must be AdapterError");
    assert!(
        matches!(downcast, AdapterError::FetchFailure { .. }),
        "expected FetchFailure variant, got: {downcast:?}"
    );
}

/// Hostnames containing shell or ssh-option characters are dropped at
/// parse time so a compromised boxd controller cannot inject `-o
/// ProxyCommand=...` into the per-fork SSH argv.
#[test]
fn boxd_fork_adapter_rejects_unsafe_host_strings() {
    let golden_host = "boxd.sh";
    let mut fake = FakeSshExec::new();
    fake.insert(
        golden_host,
        "list --json",
        SshOutput {
            // First entry has a malicious host; second has a clean one.
            stdout: r#"[
                {"name": "evil",  "host": "evil.host -o ProxyCommand=evil"},
                {"name": "clean", "host": "clean.boxd.sh"}
            ]"#
            .to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );
    // Branch resolution for the clean fork: stub the per-fork SSH so it
    // returns a branch (otherwise resolve_fork_branch falls back to the
    // fork name, which still produces a valid CachedWorktree).
    fake.insert(
        "boxd@clean.boxd.sh",
        "cd ~/langwatch && git rev-parse --abbrev-ref HEAD",
        SshOutput {
            stdout: "main\n".to_string(),
            stderr: String::new(),
            exit_code: 0,
        },
    );

    let adapter = orchard::remote_adapter::BoxdForkAdapter {
        golden_host: golden_host.to_string(),
        fork_repo_path: "~/langwatch".to_string(),
        ssh: Box::new(fake),
    };

    let worktrees = adapter
        .list_worktrees()
        .expect("clean entry should still produce a worktree");
    assert_eq!(
        worktrees.len(),
        1,
        "evil entry must be dropped, clean one kept; got: {worktrees:?}"
    );
    assert_eq!(worktrees[0].host.as_deref(), Some("boxd@clean.boxd.sh"));
}
