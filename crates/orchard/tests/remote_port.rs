/// Tests for slice 1 of issue #267 — hexagonal RemoteWorktreeService port.
///
/// All scenarios are @unit: no live SSH, no filesystem I/O beyond what the
/// fake executor provides. Tests fail red until production code fills in
/// the `unimplemented!()` stubs in `remote_adapter.rs` and migrates
/// `global_config::RemoteConfig` to carry the required `kind` field.
///
/// Feature file: specs/features/boxd-first-class-backend.feature
use orchard::remote_adapter::{
    BoxdForkAdapter, FakeSshExec, RemoteAdapter, RemoteConfigTyped, RemoteKind, RemmyAdapter,
    SshExec, SshOutput,
};

// ---------------------------------------------------------------------------
// Scenario: feature.feature:22
// RemoteWorktreeService port defines a minimal stable surface
// ---------------------------------------------------------------------------

/// The port exposes `list_worktrees`, `list_sessions`, and `probe` on all
/// three adapter variants, each returning a typed `Result`.
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
    assert!(result.is_ok(), "list_worktrees must return Ok; got: {result:?}");
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
    assert!(result.is_ok(), "list_sessions must return Ok; got: {result:?}");
}

#[test]
fn port_probe_returns_ok_for_remmy_variant() {
    // feature.feature:22
    let adapter = RemoteAdapter::Remmy(RemmyAdapter {
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
        ssh: Box::new(FakeSshExec::new()),
    });
    let result = adapter.probe();
    assert!(result.is_ok(), "probe must return Ok; got: {result:?}");
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
    assert!(result.is_ok(), "list_worktrees must return Ok; got: {result:?}");
}

#[test]
fn port_list_worktrees_returns_ok_for_boxd_fork_variant() {
    // feature.feature:22
    let adapter = RemoteAdapter::BoxdFork(BoxdForkAdapter {
        golden_host: "boxd.sh".to_string(),
        ssh: Box::new(FakeSshExec::new()),
    });
    let result = adapter.list_worktrees();
    assert!(result.is_ok(), "list_worktrees must return Ok; got: {result:?}");
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
    let cfg = RemoteConfigTyped {
        name: "my-remote".to_string(),
        host: "ubuntu@10.0.3.56".to_string(),
        path: "~/repo".to_string(),
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
    let cfg = RemoteConfigTyped {
        name: "my-boxd".to_string(),
        host: "boxd@orchard-rs.boxd.sh".to_string(),
        path: "~/git-orchard-rs".to_string(),
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
    let cfg = RemoteConfigTyped {
        name: "boxd-fork-langwatch".to_string(),
        host: "boxd.sh".to_string(),
        path: "~/langwatch".to_string(),
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

/// Deserializing a `RemoteConfigTyped` with `"type": "gcp-vm"` must fail,
/// and the serde error must mention the unknown variant name.
///
/// This collapses scenario 4 into the serde layer: once `RemoteKind` is an
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
    let result: Result<RemoteConfigTyped, _> = serde_json::from_str(json);
    assert!(result.is_err(), "unknown type 'gcp-vm' must fail deserialization");

    let err = result.unwrap_err().to_string();
    // The error must name the offending value so the operator knows what to fix.
    assert!(
        err.contains("gcp-vm") || err.contains("unknown variant") || err.contains("expected one of"),
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
    use orchard::global_config::{GlobalConfig, RepoConfig, RemoteConfig};

    // Build a GlobalConfig with a remote that has kind=Remmy.
    // Until RemoteConfig has `kind`, accessing it is a compile error.
    // The test is written so it compiles (no direct .kind access) but
    // fails at runtime: serializing RemoteConfig must include "type".
    let cfg: GlobalConfig = serde_json::from_str(r#"{
        "repos": [{
            "slug": "owner/repo",
            "path": "/workspace/repo",
            "remotes": [
                { "name": "r", "host": "h", "path": "/p", "type": "remmy" }
            ]
        }]
    }"#)
    .expect("GlobalConfig must parse");

    let remote = cfg.repos[0].remotes.first().expect("remote must be present");

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
        remotes_loaded,
        0,
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
    let cmd = format!(
        "git -C {path} worktree list --porcelain"
    );
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
    assert!(
        !wt.is_bare,
        "returned worktree must not be bare"
    );
}
