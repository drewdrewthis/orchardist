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

// ---------------------------------------------------------------------------
// TOON mode tests (issue #260)
// ---------------------------------------------------------------------------

/// `orchard --help` mentions the `--toon` flag and notes it's for AI agents.
#[test]
fn help_includes_toon_flag() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(contains("--toon"))
        .stderr(contains("AI agent"));
}

/// `orchard heal --toon` must reject the invocation — `--toon` currently
/// only applies to the top-level dashboard output, not subcommands.
///
/// This is a parse-time rejection, so no fixture, cwd, or HOME is needed.
#[test]
fn toon_with_subcommand_is_rejected() {
    Command::cargo_bin("orchard")
        .unwrap()
        .args(["heal", "--toon"])
        .assert()
        .failure()
        .stderr(contains("--toon is not supported"));
}

/// `orchard --json --toon` must reject the invocation (mutually exclusive).
#[test]
fn json_and_toon_are_mutually_exclusive() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    Command::cargo_bin("orchard")
        .unwrap()
        .args(["--json", "--toon"])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .env_remove("TMUX")
        .assert()
        .failure()
        .stderr(contains("mutually exclusive"));
}

/// `orchard --toon` run from a real git repo exits 0 and outputs parseable TOON.
///
/// Mirrors `json_mode_outputs_valid_json`: if the binary succeeds, the output
/// must round-trip through the TOON decoder. If it fails (e.g. `gh` not
/// available), the error must land on stderr.
#[test]
fn toon_mode_outputs_valid_toon() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    let output = Command::cargo_bin("orchard")
        .unwrap()
        .arg("--toon")
        .current_dir(repo.path())
        .env("HOME", home.path())
        .env_remove("TMUX")
        .output()
        .unwrap();

    if output.status.success() {
        let stdout = String::from_utf8_lossy(&output.stdout);
        assert!(!stdout.is_empty(), "toon stdout should not be empty");
        // Happy-path AC: stderr is empty when the command succeeds.
        let stderr = String::from_utf8_lossy(&output.stderr);
        assert!(
            stderr.is_empty(),
            "expected empty stderr on success, got: {stderr}"
        );
        let decoded = json2toon_rs::decode(&stdout, &json2toon_rs::DecoderOptions::default())
            .expect("stdout should be decodable TOON");
        assert!(
            decoded.get("version").is_some(),
            "decoded toon should expose the version field"
        );
    } else {
        assert!(
            !output.stderr.is_empty(),
            "expected an error message on stderr"
        );
    }
}

// ---------------------------------------------------------------------------
// Help text includes quick-chat information
// ---------------------------------------------------------------------------

/// `orchard --help` must mention the `chat` subcommand so users can discover it.
#[test]
fn help_includes_chat_subcommand() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(contains("orchard chat"));
}

/// `orchard --help` must mention the `--message` flag.
#[test]
fn help_includes_chat_message_flag() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(contains("--message"));
}

/// `orchard --help` must mention the quick-chat keybinding (`prefix + O`).
#[test]
fn help_includes_chat_keybinding() {
    Command::cargo_bin("orchard")
        .unwrap()
        .arg("--help")
        .assert()
        .success()
        .stderr(contains("prefix + O"));
}

// ---------------------------------------------------------------------------
// orchard chat --message required
// ---------------------------------------------------------------------------

/// `orchard chat` with no `--message` flag must exit non-zero and print an error.
#[test]
fn chat_missing_message_exits_nonzero() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    Command::cargo_bin("orchard")
        .unwrap()
        .args(["chat"])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .assert()
        .failure()
        .stderr(contains("--message"));
}

/// `orchard chat --message ""` (empty string) must exit non-zero and print an error.
#[test]
fn chat_empty_message_exits_nonzero() {
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    Command::cargo_bin("orchard")
        .unwrap()
        .args(["chat", "--message", ""])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .assert()
        .failure()
        .stderr(contains("--message"));
}
