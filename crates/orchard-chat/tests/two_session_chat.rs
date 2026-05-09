//! End-to-end integration test (#495 AC-18): two real tmux sessions,
//! one sends, the other receives — full Level 2 receipt verification.
//!
//! Skips itself with a clear stderr message if `tmux` is unavailable so CI
//! environments without tmux installed don't false-fail.
//!
//! This test invokes the compiled `orchard-chat` binary (resolved via
//! `CARGO_BIN_EXE_orchard-chat` set by Cargo). The chat dir is rooted in a
//! tempdir via `ORCHARD_CHAT_DIR` so it does not pollute `~/.orchard/`.

use std::path::PathBuf;
use std::process::Command;
use std::time::{Duration, Instant};

fn cli_path() -> PathBuf {
    PathBuf::from(env!("CARGO_BIN_EXE_orchard-chat"))
}

fn tmux_available() -> bool {
    Command::new("tmux")
        .arg("-V")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

struct TmuxServerGuard {
    socket: String,
    sessions: Vec<String>,
}

impl TmuxServerGuard {
    fn new() -> Self {
        // Use a unique socket per test run to isolate from any user tmux server.
        let socket = format!(
            "orchard-chat-test-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_nanos())
                .unwrap_or(0)
        );
        Self {
            socket,
            sessions: Vec::new(),
        }
    }

    fn create_session(&mut self, name: &str) {
        let out = Command::new("tmux")
            .args([
                "-L",
                &self.socket,
                "new-session",
                "-d",
                "-s",
                name,
                "-x",
                "120",
                "-y",
                "40",
            ])
            .output()
            .expect("tmux new-session");
        if !out.status.success() {
            panic!(
                "tmux new-session failed: {}",
                String::from_utf8_lossy(&out.stderr)
            );
        }
        self.sessions.push(name.to_string());
    }

    fn capture_pane(&self, session: &str) -> String {
        let out = Command::new("tmux")
            .args(["-L", &self.socket, "capture-pane", "-p", "-t", session])
            .output()
            .expect("tmux capture-pane");
        String::from_utf8_lossy(&out.stdout).into_owned()
    }
}

impl Drop for TmuxServerGuard {
    fn drop(&mut self) {
        // kill-server is the cleanest teardown — drops every session on this
        // socket and the tmux daemon along with them.
        let _ = Command::new("tmux")
            .args(["-L", &self.socket, "kill-server"])
            .output();
    }
}

/// Run the orchard-chat CLI with `ORCHARD_CHAT_DIR` set to `chat_dir` and
/// the tmux socket env-shadowed so child tmux invocations connect to our
/// isolated server. Returns (stdout, stderr, exit code).
fn run_cli(
    cli: &PathBuf,
    chat_dir: &std::path::Path,
    tmux_socket: &str,
    args: &[&str],
) -> (String, String, i32) {
    // Prepend a tmux wrapper script so the CLI's tmux invocations also use
    // the test socket. Simpler approach: alias TMUX_TMPDIR doesn't help; the
    // -L flag is the only reliable switch. Since the CLI's chat-core uses
    // the bare `tmux` binary without -L, we install a wrapper on PATH.
    let wrapper_dir = tempfile::tempdir().expect("wrapper tempdir");
    let real_tmux = which_tmux();
    let wrapper_path = wrapper_dir.path().join("tmux");
    let script = format!(
        "#!/bin/sh\nexec {} -L {} \"$@\"\n",
        shell_escape(&real_tmux),
        shell_escape(tmux_socket)
    );
    std::fs::write(&wrapper_path, script).expect("write wrapper");
    use std::os::unix::fs::PermissionsExt;
    std::fs::set_permissions(&wrapper_path, std::fs::Permissions::from_mode(0o755))
        .expect("chmod wrapper");

    let path_env = format!(
        "{}:{}",
        wrapper_dir.path().display(),
        std::env::var("PATH").unwrap_or_default()
    );

    let out = Command::new(cli)
        .args(args)
        .env("ORCHARD_CHAT_DIR", chat_dir)
        .env("PATH", &path_env)
        // Unset TMUX so sender::resolve falls back; we always pass --as.
        .env_remove("TMUX")
        .output()
        .expect("run orchard-chat");

    drop(wrapper_dir); // After the child exits, the wrapper isn't needed.

    (
        String::from_utf8_lossy(&out.stdout).into_owned(),
        String::from_utf8_lossy(&out.stderr).into_owned(),
        out.status.code().unwrap_or(-1),
    )
}

fn which_tmux() -> String {
    let out = Command::new("which")
        .arg("tmux")
        .output()
        .expect("which tmux");
    String::from_utf8_lossy(&out.stdout).trim().to_string()
}

fn shell_escape(s: &str) -> String {
    if s.chars().all(|c| c.is_ascii_alphanumeric() || "/_.-".contains(c)) {
        s.to_string()
    } else {
        format!("'{}'", s.replace('\'', "'\\''"))
    }
}

#[test]
fn two_session_chat_end_to_end() {
    if !tmux_available() {
        eprintln!("skipping: tmux not installed");
        return;
    }

    let cli = cli_path();
    let chat_td = tempfile::tempdir().expect("chat tempdir");
    let chat_dir = chat_td.path();

    let mut server = TmuxServerGuard::new();
    server.create_session("alice");
    server.create_session("bob");

    // Both sessions join #test
    let (_, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["join", "#test", "--as", "@alice", "--session", "alice"],
    );
    assert_eq!(code, 0, "alice join: {stderr}");
    let (_, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["join", "#test", "--as", "@bob", "--session", "bob"],
    );
    assert_eq!(code, 0, "bob join: {stderr}");

    // Alice sends to #test
    let (stdout, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["send", "#test", "hello-bob", "--as", "@alice", "--json"],
    );
    assert_eq!(code, 0, "alice send (stdout: {stdout}, stderr: {stderr})");

    // (a) JSONL contains the line with type:"message"
    let jsonl_path = chat_dir.join("test.jsonl");
    let raw = std::fs::read_to_string(&jsonl_path).expect("read jsonl");
    let mut found_msg = false;
    let mut alice_joined = false;
    let mut bob_joined = false;
    for line in raw.lines() {
        let v: serde_json::Value =
            serde_json::from_str(line).expect("parse jsonl line");
        match v["type"].as_str() {
            Some("message") => {
                if v["text"].as_str() == Some("hello-bob")
                    && v["sender"].as_str() == Some("@alice")
                {
                    found_msg = true;
                }
            }
            Some("member.joined") => match v["handle"].as_str() {
                Some("@alice") => alice_joined = true,
                Some("@bob") => bob_joined = true,
                _ => {}
            },
            _ => {}
        }
    }
    assert!(found_msg, "JSONL missing the message line: {raw}");
    assert!(alice_joined, "JSONL missing alice join: {raw}");
    assert!(bob_joined, "JSONL missing bob join: {raw}");

    // (b) tmux capture-pane on bob shows the prefixed paste — retry up to ~1s.
    //
    // We've already returned 0 from the CLI so the scrollback verify
    // succeeded. But re-confirm here from the test's perspective so the
    // assertion is independent of the CLI's verify path.
    let deadline = Instant::now() + Duration::from_secs(1);
    let mut bob_capture = String::new();
    while Instant::now() < deadline {
        bob_capture = server.capture_pane("bob");
        if bob_capture.contains("hello-bob") && bob_capture.contains("@alice") {
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    assert!(
        bob_capture.contains("hello-bob") && bob_capture.contains("@alice"),
        "bob's pane should show the prefixed paste; got:\n{bob_capture}"
    );

    // (c) alice's pane should NOT show the paste.
    let alice_capture = server.capture_pane("alice");
    assert!(
        !alice_capture.contains("hello-bob"),
        "alice's pane should NOT echo the message; got:\n{alice_capture}"
    );

    // (d) members #test returns both handles
    let (stdout, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["members", "#test", "--json"],
    );
    assert_eq!(code, 0, "members (stderr: {stderr})");
    assert!(
        stdout.contains("@alice") && stdout.contains("@bob"),
        "members should include both; got:\n{stdout}"
    );

    // (e) the --json SendOutcome contains a Delivered outcome with verified_at
    //     for bob. Re-send with --json so we can parse it.
    let (stdout, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["send", "#test", "second-message", "--as", "@alice", "--json"],
    );
    assert_eq!(code, 0, "second send (stderr: {stderr})");
    let v: serde_json::Value =
        serde_json::from_str(stdout.trim()).expect("parse SendOutcome json");
    let fanout = v["fanout"].as_array().expect("fanout array");
    let bob_outcome = fanout
        .iter()
        .find(|o| o["recipient"] == "@bob")
        .expect("bob in fanout");
    assert_eq!(
        bob_outcome["kind"], "delivered",
        "bob outcome should be delivered: {bob_outcome}"
    );
    assert!(
        bob_outcome["verified_at"]
            .as_str()
            .map(|s| !s.is_empty())
            .unwrap_or(false),
        "bob outcome should have verified_at: {bob_outcome}"
    );
    // bob is a bash session in this test, so transcript-verify won't find a
    // Claude transcript dir and will fall back to scrollback. The
    // verified_via field discriminates the proof.
    assert!(
        bob_outcome["verified_via"] == "transcript"
            || bob_outcome["verified_via"] == "scrollback",
        "verified_via should be transcript or scrollback: {bob_outcome}"
    );
}

#[test]
fn direct_send_routes_to_handle_session() {
    if !tmux_available() {
        eprintln!("skipping: tmux not installed");
        return;
    }
    let cli = cli_path();
    let chat_td = tempfile::tempdir().expect("chat tempdir");
    let chat_dir = chat_td.path();

    let mut server = TmuxServerGuard::new();
    server.create_session("alice");
    server.create_session("bob");

    // Direct send `@bob` — no join needed, handle resolves directly to a
    // tmux session of the same name.
    let (stdout, stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["send", "@bob", "direct-ping", "--as", "@alice", "--json"],
    );
    assert_eq!(
        code, 0,
        "alice direct send (stdout: {stdout}, stderr: {stderr})"
    );

    // Verify bob's pane received it.
    let deadline = Instant::now() + Duration::from_secs(1);
    let mut got = false;
    while Instant::now() < deadline {
        if server.capture_pane("bob").contains("direct-ping") {
            got = true;
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    assert!(got, "bob's pane should show 'direct-ping' from direct send");

    // Direct sends store under `@bob.jsonl` so history is queryable.
    assert!(
        chat_dir.join("@bob.jsonl").exists(),
        "@bob.jsonl should exist after direct send"
    );
}

#[test]
fn unknown_handle_fails_gracefully() {
    if !tmux_available() {
        eprintln!("skipping: tmux not installed");
        return;
    }
    let cli = cli_path();
    let chat_td = tempfile::tempdir().expect("chat tempdir");
    let chat_dir = chat_td.path();

    let mut server = TmuxServerGuard::new();
    server.create_session("alice");
    // Note: no `nobody` session exists.

    let (stdout, _stderr, code) = run_cli(
        &cli,
        chat_dir,
        &server.socket,
        &["send", "@nobody", "ping", "--as", "@alice", "--json"],
    );
    // Exit code 1: message landed in JSONL but fanout failed.
    assert_eq!(code, 1, "expected partial-failure exit, got {code}");

    // The JSONL has the message, but the fanout outcome is Failed.
    let v: serde_json::Value =
        serde_json::from_str(stdout.trim()).expect("parse outcome json");
    let fanout = v["fanout"].as_array().expect("fanout array");
    assert_eq!(fanout.len(), 1);
    assert_eq!(fanout[0]["kind"], "failed");
    let err = fanout[0]["error"]
        .as_str()
        .expect("error should be a string");
    assert!(
        err.starts_with("no such tmux session"),
        "expected 'no such tmux session…', got: {err}"
    );
}

#[test]
fn missing_target_sigil_returns_usage_error() {
    if !tmux_available() {
        eprintln!("skipping: tmux not installed");
        return;
    }
    let cli = cli_path();
    let chat_td = tempfile::tempdir().expect("chat tempdir");
    let server = TmuxServerGuard::new();

    let (_stdout, stderr, code) = run_cli(
        &cli,
        chat_td.path(),
        &server.socket,
        &["send", "general", "no-sigil", "--as", "@alice"],
    );
    assert_eq!(code, 3, "expected usage error, stderr: {stderr}");
    assert!(stderr.contains("'#'") || stderr.contains("'@'"));
}
