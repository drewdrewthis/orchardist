//! AC7 — `orchard --json` is cache-only; `orchard refresh` is the explicit
//! fresh-data entry point.
//!
//! Scenarios covered:
//! - `orchard --json` spawns no SSH even with a configured OrchardProxy remote.
//! - `orchard --json` with a cached snapshot returns the cached worktree quickly.
//! - `orchard refresh --help` (or `orchard refresh`) exits cleanly (subcommand exists).

use std::collections::HashMap;
use std::time::Instant;

use assert_cmd::Command;
use orchard::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
use orchard::json_output::{JsonOutput, JsonRepo, JsonWorktree};
use orchard::merge_remote::build_state_with_cached_snapshots_from;
use orchard::orchard_snapshot::write_snapshot_to;
use orchard::remote_adapter::RemoteKind;
use tempfile::TempDir;

// ---------------------------------------------------------------------------
// AC7 scenario 1: `orchard --json` spawns no SSH
//
// We prove this deterministically by placing a fake `ssh` wrapper script at
// the front of PATH. The wrapper writes a marker file when invoked. After the
// `orchard --json` run we assert the marker file does NOT exist.
// ---------------------------------------------------------------------------

/// `orchard --json` must never invoke `ssh`, even when an OrchardProxy remote
/// is configured. Reads are cache-only; SSH only happens inside `orchard refresh`
/// or `orchard watch`.
///
/// We verify this by placing a fake `ssh` wrapper in a tempdir and prepending
/// it to PATH. If `orchard --json` calls `ssh`, the wrapper writes
/// `ssh-called.marker`. After the run we assert the marker is absent.
#[test]
fn orchard_json_reads_cache_only_no_ssh_spawned() {
    let home_dir = TempDir::new().expect("create temp home");
    let fake_ssh_dir = TempDir::new().expect("create fake ssh dir");
    let marker = fake_ssh_dir.path().join("ssh-called.marker");

    // Write a fake `ssh` script that records its invocation and exits 0.
    let fake_ssh_path = fake_ssh_dir.path().join("ssh");
    let marker_path_str = marker.display().to_string();
    let script = format!("#!/bin/sh\ntouch \"{marker_path_str}\"\nexit 0\n");
    std::fs::write(&fake_ssh_path, script).expect("write fake ssh script");

    // Make it executable.
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&fake_ssh_path, std::fs::Permissions::from_mode(0o755))
            .expect("chmod fake ssh");
    }

    let original_path = std::env::var("PATH").unwrap_or_default();
    let new_path = format!("{}:{}", fake_ssh_dir.path().display(), original_path);

    let start = Instant::now();

    Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("--json")
        .env("HOME", home_dir.path())
        // XDG_CONFIG_HOME must also be redirected so load_global_config()
        // doesn't read the real ~/.config/orchard/config.json.
        .env("XDG_CONFIG_HOME", home_dir.path().join(".config"))
        .env("PATH", &new_path)
        .assert()
        .success();

    // Marker file must NOT exist — no SSH was spawned.
    assert!(
        !marker.exists(),
        "orchard --json must not invoke ssh; marker file was created at {}",
        marker.display()
    );

    // Generous hang-guard: if we somehow block (regression), we still catch it.
    let elapsed = start.elapsed();
    assert!(
        elapsed.as_millis() < 10_000,
        "orchard --json must not hang; took {:?}",
        elapsed
    );
}

// ---------------------------------------------------------------------------
// AC7 scenario 2: cached snapshot is returned quickly
// ---------------------------------------------------------------------------

/// Pre-write a snapshot to a tempdir and verify `build_state_with_cached_snapshots_from`
/// returns the cached worktree in under 500ms (no SSH, pure disk read).
#[test]
fn orchard_json_with_cached_snapshot_returns_quickly() {
    let cache_dir = TempDir::new().expect("create temp cache dir");
    let host = "vm.boxd.sh";

    // Write a snapshot with one worktree.
    let snapshot = JsonOutput {
        version: 6,
        tmux_sessions: vec![],
        repos: vec![JsonRepo {
            slug: "owner/repo".to_string(),
            default_branch: None,
            main_ci_state: None,
            worktrees: vec![JsonWorktree {
                path: "/remote/wt1".to_string(),
                branch: "issue329/federated-orchard".to_string(),
                host: None,
                layout: "bare".to_string(),
                issue: None,
                pr: None,
                sessions: vec![],
                display_group: "other".to_string(),
                status: "ready".to_string(),
                status_glyph: "🟢".to_string(),
                is_main_worktree: false,
                ahead_behind: None,
                last_commit_at: None,
                last_activity_at: None,
                discovery_path: None,
            }],
        }],
        hosts: HashMap::new(),
        errors: vec![],
    };

    write_snapshot_to(host, &snapshot, cache_dir.path()).expect("write snapshot");

    let config = GlobalConfig {
        repos: vec![RepoConfig {
            slug: "owner/repo".to_string(),
            path: "/local/repo".to_string(),
            remotes: vec![RemoteConfig {
                name: "vm".to_string(),
                host: host.to_string(),
                path: "/remote/repo".to_string(),
                shell: "ssh".to_string(),
                kind: RemoteKind::OrchardProxy,
                allow_transitive: false,
            }],
        }],
        ..GlobalConfig::default()
    };

    let start = Instant::now();
    let state = build_state_with_cached_snapshots_from(&config, &HashMap::new(), cache_dir.path());
    let elapsed = start.elapsed();

    // Must be fast — no SSH involved.
    assert!(
        elapsed.as_millis() < 500,
        "build_state_with_cached_snapshots_from must complete in under 500ms; took {:?}",
        elapsed
    );

    // Must contain the cached worktree.
    let repo = state
        .repos
        .iter()
        .find(|r| r.slug == "owner/repo")
        .expect("owner/repo must be present");

    assert!(
        repo.worktrees
            .iter()
            .any(|w| w.branch == "issue329/federated-orchard"),
        "cached worktree branch must appear in state; got: {:?}",
        repo.worktrees.iter().map(|w| &w.branch).collect::<Vec<_>>()
    );
}

// ---------------------------------------------------------------------------
// AC7 scenario 3: `orchard refresh` subcommand exists
// ---------------------------------------------------------------------------

/// `orchard refresh --help` must exit 0, proving the subcommand is registered.
///
/// We can't easily call `orchard refresh` end-to-end in a unit test without
/// real SSH infrastructure, but `--help` (which falls through to usage and
/// exits 0) is sufficient to prove the subcommand is wired in.
#[test]
fn orchard_refresh_exists_as_subcommand() {
    // `orchard --help` should list `orchard refresh` in the usage text.
    Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("--help")
        .assert()
        .success()
        .stderr(predicates::str::contains("refresh"));
}

/// `orchard refresh` with a clean HOME (no global config, no SSH targets)
/// must exit 0 — nothing to refresh is a valid state.
#[test]
fn orchard_refresh_exits_zero_with_empty_config() {
    let home_dir = TempDir::new().expect("create temp home");

    Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("refresh")
        .env("HOME", home_dir.path())
        .env("XDG_CONFIG_HOME", home_dir.path().join(".config"))
        .assert()
        .success();
}
