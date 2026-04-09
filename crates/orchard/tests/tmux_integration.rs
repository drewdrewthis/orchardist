/// Integration tests for tmux-dependent behaviour.
///
/// All tests in this file are gated with `#[ignore]` because they require a
/// running tmux server. Run them explicitly with:
///
///   cargo test --test tmux_integration -- --ignored
///
/// The `tmux` module is not public, so these tests exercise tmux behaviour
/// through the binary rather than calling library functions directly.
mod common;

use assert_cmd::Command;

// ---------------------------------------------------------------------------
// json mode does not create a new tmux session
// ---------------------------------------------------------------------------

/// Running `orchard --json` inside an existing tmux session must not create
/// any new tmux sessions. This guards against regressions where `--json` mode
/// triggers the main-session bootstrap code path.
///
/// Requires: a running tmux server (`$TMUX` is set in the environment).
#[test]
#[ignore]
fn json_mode_does_not_create_tmux_session() {
    // Count sessions before.
    let before_output = std::process::Command::new("tmux")
        .args(["list-sessions", "-F", "#{session_name}"])
        .output()
        .expect("tmux list-sessions");
    let before_count = String::from_utf8_lossy(&before_output.stdout)
        .lines()
        .count();

    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    // Run orchard --json inside the current tmux (TMUX env inherited).
    let _ = Command::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(repo.path())
        .env("HOME", home.path())
        .output()
        .unwrap();

    // Count sessions after.
    let after_output = std::process::Command::new("tmux")
        .args(["list-sessions", "-F", "#{session_name}"])
        .output()
        .expect("tmux list-sessions");
    let after_count = String::from_utf8_lossy(&after_output.stdout)
        .lines()
        .count();

    assert_eq!(
        before_count, after_count,
        "orchard --json must not create new tmux sessions"
    );
}

// ---------------------------------------------------------------------------
// binary outside tmux does not crash
// ---------------------------------------------------------------------------

/// Running `orchard --json` outside a tmux environment (no `$TMUX` env var)
/// must not crash and must not attempt to launch a tmux popup.
///
/// Requires: a running tmux server to confirm no sessions were created; the
/// binary itself must be run with TMUX unset.
#[test]
#[ignore]
fn binary_outside_tmux_runs_without_crashing() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    let before_output = std::process::Command::new("tmux")
        .args(["list-sessions", "-F", "#{session_name}"])
        .output()
        .expect("tmux list-sessions");
    let before_count = String::from_utf8_lossy(&before_output.stdout)
        .lines()
        .count();

    // Explicitly remove TMUX so the binary sees no tmux context.
    let result = Command::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(repo.path())
        .env("HOME", home.path())
        .env_remove("TMUX")
        .output()
        .unwrap();

    // The binary should either succeed (valid JSON) or fail with an error
    // message, but must not hang or panic.
    assert!(
        result.status.success() || !result.stderr.is_empty(),
        "binary must exit cleanly outside tmux"
    );

    let after_output = std::process::Command::new("tmux")
        .args(["list-sessions", "-F", "#{session_name}"])
        .output()
        .expect("tmux list-sessions");
    let after_count = String::from_utf8_lossy(&after_output.stdout)
        .lines()
        .count();

    assert_eq!(
        before_count, after_count,
        "no new tmux sessions should be created"
    );
}
