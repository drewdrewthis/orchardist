//! End-to-end integration tests for `orchard chat`.
//!
//! These tests require a running tmux server and are gated with `#[ignore]`.
//! Run explicitly with:
//!
//!   cargo test --test chat_integration -- --ignored
//!
//! Each test creates an isolated throwaway tmux session so they do not
//! interfere with any existing sessions.
mod common;

use assert_cmd::Command;

// ---------------------------------------------------------------------------
// Helper: skip if tmux is not on PATH
// ---------------------------------------------------------------------------

fn tmux_available() -> bool {
    std::process::Command::new("tmux")
        .arg("-V")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Polls `tmux has-session -t <name>` until it succeeds or a 2-second timeout
/// elapses. Returns `true` if the session is ready, `false` if it timed out.
fn wait_for_tmux_session(name: &str) -> bool {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
    while std::time::Instant::now() < deadline {
        let ready = std::process::Command::new("tmux")
            .args(["has-session", "-t", name])
            .status()
            .map(|s| s.success())
            .unwrap_or(false);
        if ready {
            return true;
        }
        std::thread::sleep(std::time::Duration::from_millis(20));
    }
    false
}

/// Generates a unique session name to avoid collisions between parallel test runs.
fn unique_session(base: &str) -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let ts = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.subsec_nanos())
        .unwrap_or(0);
    format!("{base}-{ts}")
}

// ---------------------------------------------------------------------------
// orchard chat sends message to real tmux pane
// ---------------------------------------------------------------------------

/// `orchard chat --target <session> --message <text>` delivers the text to a
/// live tmux pane running `cat`.  We verify by capturing the pane output.
///
/// Requires: tmux on PATH and a running tmux server (`$TMUX` set).
#[test]
#[ignore]
fn chat_delivers_message_to_tmux_pane() {
    if !tmux_available() {
        return;
    }

    let session = unique_session("orchardist-test");
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    // Create a throwaway session running `cat` (reads stdin, echoes to pane).
    let created = std::process::Command::new("tmux")
        .args(["new-session", "-d", "-s", &session, "cat"])
        .status()
        .expect("tmux new-session");
    assert!(created.success(), "failed to create test tmux session");

    // Wait for the session to be ready before sending keys.
    assert!(
        wait_for_tmux_session(&session),
        "tmux session did not become ready within 2s"
    );

    // Run `orchard chat --target <session> --message "hello"`.
    let result = Command::cargo_bin("orchard-tui")
        .unwrap()
        .args(["chat", "--target", &session, "--message", "hello"])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .output()
        .unwrap();

    // Capture pane output.
    let pane_output = std::process::Command::new("tmux")
        .args(["capture-pane", "-p", "-t", &format!("{session}:0.0")])
        .output()
        .expect("tmux capture-pane");
    let pane_text = String::from_utf8_lossy(&pane_output.stdout);

    // Clean up.
    let _ = std::process::Command::new("tmux")
        .args(["kill-session", "-t", &session])
        .status();

    assert!(
        result.status.success(),
        "orchard chat must exit 0; stderr: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(
        pane_text.contains("hello"),
        "pane output should contain 'hello'; got: {pane_text}"
    );
}

// ---------------------------------------------------------------------------
// orchard chat exits non-zero when session does not exist
// ---------------------------------------------------------------------------

/// `orchard chat --target <nonexistent> --message <text>` must exit non-zero
/// and print an error to stderr.
#[test]
#[ignore]
fn chat_exits_nonzero_for_missing_session() {
    if !tmux_available() {
        return;
    }

    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    let result = Command::cargo_bin("orchard-tui")
        .unwrap()
        .args([
            "chat",
            "--target",
            "definitely-does-not-exist-9999",
            "--message",
            "hi",
        ])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .output()
        .unwrap();

    assert!(
        !result.status.success(),
        "orchard chat must exit non-zero for a missing session"
    );
    assert!(
        !result.stderr.is_empty(),
        "stderr must contain an error message"
    );
}

// ---------------------------------------------------------------------------
// orchard chat no-ops on empty message
// ---------------------------------------------------------------------------

/// `orchard chat --target <session>` with no `--message` (or empty message)
/// must exit 0 without sending anything.
#[test]
#[ignore]
fn chat_noop_on_empty_message() {
    if !tmux_available() {
        return;
    }

    let session = unique_session("orchardist-noop");
    let repo = common::TestRepo::new();
    let home = tempfile::TempDir::new().unwrap();

    let created = std::process::Command::new("tmux")
        .args(["new-session", "-d", "-s", &session, "cat"])
        .status()
        .expect("tmux new-session");
    assert!(created.success());

    // Wait for the session to be ready before capturing.
    assert!(
        wait_for_tmux_session(&session),
        "tmux session did not become ready within 2s"
    );

    // Capture pane before.
    let before = std::process::Command::new("tmux")
        .args(["capture-pane", "-p", "-t", &format!("{session}:0.0")])
        .output()
        .expect("capture before");

    let result = Command::cargo_bin("orchard-tui")
        .unwrap()
        .args(["chat", "--target", &session])
        .current_dir(repo.path())
        .env("HOME", home.path())
        .output()
        .unwrap();

    // Capture pane after.
    let after = std::process::Command::new("tmux")
        .args(["capture-pane", "-p", "-t", &format!("{session}:0.0")])
        .output()
        .expect("capture after");

    let _ = std::process::Command::new("tmux")
        .args(["kill-session", "-t", &session])
        .status();

    assert!(
        result.status.success(),
        "orchard chat with no message must exit 0"
    );
    // Pane content should not have changed.
    assert_eq!(
        before.stdout, after.stdout,
        "pane must not change when no message is provided"
    );
}
