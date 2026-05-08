//! AC 4 — Regression test: `refresh_and_build` (and `--json` mode) must NOT
//! invoke `restore_session` for cached dead-but-restorable sessions.
//!
//! Feature file: specs/features/restore-explicit-subcommand.feature
//!   Scenario: "refresh_and_build does not invoke restore_session for cached dead entries"
//!
//! # Why subprocess, not in-process
//!
//! `restore_all_local` is guarded by a process-global `OnceLock<()>` called
//! `RESTORE_RAN`. Once set, it short-circuits all subsequent calls in the same
//! process. Running the assertion in a subprocess gives us:
//!   - A fresh `RESTORE_RAN` state every time.
//!   - No cross-test contamination from the OnceLock.
//!   - A clean filesystem (HOME redirected to a tempdir).
//!
//! After AC 6 removes `RESTORE_RAN`, the subprocess approach is still correct
//! because it exercises the binary exactly as a real user would.
//!
//! # Mechanism
//!
//! 1. Write a `tmux_sessions.json` cache entry for session "ghost" whose
//!    worktree path exists on disk but which is NOT in the live tmux server.
//! 2. Place a fake `tmux` binary first on PATH that:
//!    - Returns an empty session list for `list-sessions` (tmux is up, no sessions).
//!    - Returns empty output for `list-panes` (used by `refresh_local`).
//!    - Writes a marker file and exits 0 for `new-session` (so we can detect
//!      whether `restore_session` was ever called).
//! 3. Run `orchard-tui --json` with the fake tmux on PATH.
//! 4. Assert the marker file was NOT written.
//!    - On current code: `refresh_and_build` calls `restore_all_local()`, which
//!      finds "ghost" dead+restorable and calls `tmux new-session` → marker written
//!      → assertion fails → test fails (the desired FAIL-on-main behaviour).
//!    - After the fix (remove read-path calls): no restore → no marker → test passes.

mod common;

use assert_cmd::Command;
use orchard::cache::{CachedTmuxSession, tmux_cache_path, write_cache};
use std::os::unix::fs::PermissionsExt;
use tempfile::TempDir;

/// Creates a fake `tmux` shell script inside `bin_dir` that:
/// - `list-sessions` (any flags) → exits 0, prints nothing.
/// - `list-panes` (any flags)   → exits 0, prints nothing.
/// - `new-session` (any flags)  → writes `<marker_path>` and exits 0.
/// - anything else              → exits 0, prints nothing.
///
/// The caller must prepend `bin_dir` to PATH before running the binary under test.
fn write_fake_tmux(bin_dir: &std::path::Path, marker_path: &std::path::Path) {
    let script = format!(
        r#"#!/bin/sh
# Fake tmux for AC-4 regression test.
# Detects the first positional subcommand and acts accordingly.
subcommand="$1"
case "$subcommand" in
  new-session)
    # Record that restore_session called us.
    touch '{marker}'
    exit 0
    ;;
  list-sessions|list-panes|list-windows|kill-session|has-session|select-window|select-pane|rename-window|split-window|new-window|send-keys|select-layout|capture-pane)
    # All other session-management commands: succeed silently.
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
"#,
        marker = marker_path.display()
    );

    let tmux_path = bin_dir.join("tmux");
    std::fs::write(&tmux_path, script).expect("write fake tmux script");
    std::fs::set_permissions(&tmux_path, std::fs::Permissions::from_mode(0o755))
        .expect("chmod +x fake tmux");
}

/// Writes a `CachedTmuxSession` for a local (no host) session named "ghost"
/// whose working directory is `worktree_path`.
///
/// This session is "dead but restorable": it is not in `list-sessions` output
/// (because our fake tmux returns nothing) but its path exists on disk.
fn write_ghost_session_cache(cache_dir: &std::path::Path, worktree_path: &std::path::Path) {
    // The cache file lives at the standard path relative to `cache_dir`.
    // `tmux_cache_path(None)` gives us the global filename we need.
    let global = tmux_cache_path(None);
    let filename = global.file_name().expect("tmux cache path has filename");
    let cache_file = cache_dir.join(filename);

    let ghost = CachedTmuxSession {
        name: "ghost".to_string(),
        path: worktree_path.to_string_lossy().into_owned(),
        host: None, // local session — eligible for restore_all_local
        pane_targets: vec!["0.0".to_string()],
        pane_titles: vec![],
        pane_commands: vec!["bash".to_string()],
        window_names: vec!["main".to_string()],
        window_active: vec!["1".to_string()],
        window_layouts: vec![],
        pane_paths: vec![],
        pane_active: vec!["1".to_string()],
        created_at: None,
        last_activity_at: None,
        last_output_lines: vec![],
        claude_state_raw: None,
    };

    write_cache(&cache_file, &[ghost]).expect("write ghost session cache");
}

// ---------------------------------------------------------------------------
// The regression test
// ---------------------------------------------------------------------------

/// AC 4: `orchard-tui --json` (which calls `refresh_and_build`) must NOT
/// invoke `restore_session` for a cached dead-but-restorable local session.
///
/// This test FAILS on main (where `build_state.rs` calls `restore_all_local`)
/// and PASSES once that call is removed (AC 1 fix).
///
/// Design invariants:
/// - Deterministic: no real tmux server needed; fake tmux is deterministic.
/// - No flakes: the marker file is either written (restore happened) or absent.
/// - Subprocess: avoids the process-global `RESTORE_RAN` OnceLock contaminating
///   other tests in this suite.
#[test]
fn refresh_and_build_does_not_invoke_restore_session_for_dead_cached_session() {
    // -----------------------------------------------------------------------
    // Set up temp dirs
    // -----------------------------------------------------------------------
    let tmp = TempDir::new().expect("create root temp dir");

    // HOME for the subprocess — cache lives at <home>/.cache/orchard/.
    let home_dir = tmp.path().join("home");
    std::fs::create_dir_all(&home_dir).expect("create home dir");

    let cache_dir = home_dir.join(".cache").join("orchard");
    std::fs::create_dir_all(&cache_dir).expect("create cache dir");

    // A fake worktree path that EXISTS on disk (so worktree_exists_default returns true).
    let worktree_path = tmp.path().join("ghost-worktree");
    std::fs::create_dir_all(&worktree_path).expect("create ghost worktree dir");

    // Where the fake tmux will drop its marker if new-session is called.
    let marker_path = tmp.path().join("new-session-called.marker");

    // Directory holding the fake tmux binary.
    let fake_bin_dir = tmp.path().join("fake-bin");
    std::fs::create_dir_all(&fake_bin_dir).expect("create fake bin dir");

    // -----------------------------------------------------------------------
    // Write fixtures
    // -----------------------------------------------------------------------

    // 1. Fake tmux binary: records new-session calls via the marker file.
    write_fake_tmux(&fake_bin_dir, &marker_path);

    // 2. tmux session cache: one dead-but-restorable local session "ghost".
    write_ghost_session_cache(&cache_dir, &worktree_path);

    // 3. A minimal git repo so orchard-tui doesn't complain about cwd.
    let repo = common::TestRepo::new();

    // -----------------------------------------------------------------------
    // Build a PATH that puts our fake tmux before the real one
    // -----------------------------------------------------------------------
    let original_path = std::env::var("PATH").unwrap_or_default();
    let patched_path = format!("{}:{original_path}", fake_bin_dir.display());

    // -----------------------------------------------------------------------
    // Run the binary under test
    // -----------------------------------------------------------------------
    // `orchard-tui --json` exercises exactly the `refresh_and_build` path.
    // We redirect HOME so the binary reads from our controlled cache directory.
    // We unset TMUX so the binary does not enter popup/session-attach paths.
    let _output = Command::cargo_bin("orchard-tui")
        .expect("orchard-tui binary must be available")
        .arg("--json")
        .current_dir(repo.path())
        .env("HOME", &home_dir)
        .env("PATH", &patched_path)
        .env_remove("TMUX")
        // Ensure GH CLI calls fail gracefully rather than hanging on auth.
        .env("GH_TOKEN", "test-token-invalid")
        .timeout(std::time::Duration::from_secs(30))
        .output()
        .expect("orchard-tui --json must not hang");

    // -----------------------------------------------------------------------
    // Assert: the fake tmux's `new-session` must NOT have been called
    // -----------------------------------------------------------------------
    // On current main: restore_all_local() is called from refresh_and_build_with_walker_config
    // (build_state.rs:344). It finds "ghost" dead+restorable → calls tmux new-session
    // → the fake tmux writes the marker → this assertion fails (test fails on main ✓).
    //
    // After the AC-1 fix (remove restore_all_local from build_state.rs):
    // No restore is invoked → marker file absent → assertion passes (test passes ✓).
    assert!(
        !marker_path.exists(),
        "restore_session must not be called from the read path (--json / refresh_and_build).\n\
         The fake tmux's `new-session` was invoked, which means restore_all_local() ran \
         from inside refresh_and_build. Remove the restore_all_local() call at \
         crates/orchard/src/build_state.rs and crates/orchard/src/tui/mod.rs \
         (issue #460 AC 1) to fix this."
    );
}
