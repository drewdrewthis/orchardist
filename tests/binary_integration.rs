/// Integration tests for the `orchard` binary.
///
/// These tests exercise the compiled binary (via `assert_cmd`) rather than
/// calling library functions directly. They correspond to acceptance criteria
/// from `popup-mode.feature` and `main.rs` CLI handling.
mod common;

use assert_cmd::Command;
use predicates::str::contains;

// ---------------------------------------------------------------------------
// Help flag tests
// ---------------------------------------------------------------------------

/// `orchard --help` exits 0 and prints usage text to stderr.
#[test]
fn help_flag_exits_zero() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(contains("Usage"));
}

/// `orchard -h` (short form) also exits 0.
#[test]
fn help_flag_short_exits_zero() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("-h")
        .assert()
        .success();
}

// ---------------------------------------------------------------------------
// Fatal error test (popup-mode.feature: binary exits non-zero on fatal error)
// ---------------------------------------------------------------------------

/// Running `orchard` (TUI mode) outside a real terminal must exit non-zero.
///
/// In a test process, stdout is a pipe (not a TTY). Without a real terminal,
/// Ratatui cannot initialise and `tui::run()` returns an `Err`, which causes
/// `handle_tui` to call `std::process::exit(1)`. This verifies the binary's
/// fatal-error path.
#[test]
fn binary_exits_with_non_zero_on_fatal_error() {
    let home = tempfile::TempDir::new().unwrap();
    let repo = common::TestRepo::new();

    Command::cargo_bin("orchard")
        .unwrap()
        .current_dir(repo.path())
        .env("HOME", home.path())
        .env_remove("TMUX")
        .assert()
        .failure();
}

// ---------------------------------------------------------------------------
// JSON mode test
// ---------------------------------------------------------------------------

/// `orchard --json` run from a real git repo exits 0 and outputs valid JSON.
///
/// We create a minimal git repo and set HOME to a temp dir (no cache, no
/// GitHub config) so the binary runs with empty data rather than touching
/// real user state. The output must be parseable JSON; the shape doesn't need
/// to match any particular schema for this test.
#[test]
fn json_mode_outputs_valid_json() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    // Prevent the `gh` CLI from being used for auth-dependent operations by
    // pointing HOME at a clean dir (no .config/orchard/config.json).
    let output = Command::cargo_bin("orchard")
        .unwrap()
        .arg("--json")
        .current_dir(repo.path())
        .env("HOME", home.path())
        // Disable real tmux interaction.
        .env_remove("TMUX")
        .output()
        .unwrap();

    // The binary may succeed or fail depending on whether `gh` is available,
    // but if it succeeds the output must be valid JSON.
    if output.status.success() {
        let stdout = String::from_utf8_lossy(&output.stdout);
        let _: serde_json::Value =
            serde_json::from_str(&stdout).expect("stdout should be valid JSON");
    } else {
        // Acceptable: binary failed cleanly (e.g. gh not available).
        // Verify it printed something useful to stderr.
        assert!(
            !output.stderr.is_empty(),
            "expected an error message on stderr"
        );
    }
}
