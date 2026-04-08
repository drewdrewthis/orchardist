//! Integration tests for `hooks/orchard-state.sh`.
//!
//! Each test pipes a JSON hook payload into the script via stdin with `TMPDIR`
//! overridden to a fresh tempdir, and asserts the resulting state file and/or
//! inflight sidecar contents.
//!
//! A fake `tmux` shim is injected via a temp `bin/` directory on `PATH` so the
//! script can derive a session name without a live tmux server. The shim outputs
//! a fixed session name "test_session".

use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};

// ---------------------------------------------------------------------------
// Test harness helpers
// ---------------------------------------------------------------------------

const HOOK_SCRIPT: &str = env!("CARGO_MANIFEST_DIR");

/// Creates a temporary directory hierarchy suitable for running the hook script:
///   - `$tmpdir/bin/tmux` — fake tmux shim that echoes "test_session" for `display-message -p '#S'`
///   - Returns (tmpdir, bin_dir, path to hook script)
fn setup_test_env() -> (tempfile::TempDir, std::path::PathBuf) {
    let tmpdir = tempfile::tempdir().expect("create tempdir");

    // Create a fake tmux binary.
    let bin_dir = tmpdir.path().join("bin");
    fs::create_dir_all(&bin_dir).unwrap();
    let fake_tmux = bin_dir.join("tmux");
    fs::write(
        &fake_tmux,
        "#!/usr/bin/env bash\necho 'test_session'\n",
    )
    .unwrap();
    let mut perms = fs::metadata(&fake_tmux).unwrap().permissions();
    perms.set_mode(0o755);
    fs::set_permissions(&fake_tmux, perms).unwrap();

    (tmpdir, bin_dir)
}

/// Runs the hook script with the given JSON payload and environment.
/// Returns the exit status.
fn run_hook(tmpdir: &Path, bin_dir: &Path, json_payload: &str) -> std::process::ExitStatus {
    let script = format!("{}/hooks/orchard-state.sh", HOOK_SCRIPT);
    let original_path = std::env::var("PATH").unwrap_or_default();
    let new_path = format!("{}:{}", bin_dir.display(), original_path);

    let mut child = Command::new("bash")
        .arg(&script)
        .env("TMUX", "fake:0.0") // pretend we're inside tmux
        .env("TMPDIR", tmpdir)
        .env("PATH", &new_path)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("spawn hook script");

    child
        .stdin
        .as_mut()
        .unwrap()
        .write_all(json_payload.as_bytes())
        .unwrap();

    child.wait().expect("wait hook script")
}

/// Reads the state file written by the hook for "test_session".
fn read_state_file(tmpdir: &Path) -> serde_json::Value {
    let path = tmpdir.join("orchard-claude-test_session.json");
    let raw = fs::read_to_string(&path)
        .unwrap_or_else(|_| panic!("state file not found at {}", path.display()));
    serde_json::from_str(&raw).expect("parse state file JSON")
}

/// Reads the inflight sidecar. Returns `None` if the file doesn't exist.
fn read_inflight(tmpdir: &Path) -> Option<serde_json::Value> {
    let path = tmpdir.join("orchard-claude-test_session.inflight.json");
    if path.exists() {
        let raw = fs::read_to_string(&path).unwrap();
        Some(serde_json::from_str(&raw).expect("parse inflight JSON"))
    } else {
        None
    }
}

// ---------------------------------------------------------------------------
// Stop event — stop_reason handling
// ---------------------------------------------------------------------------

/// `Stop(stop_reason=tool_use)` must exit 0 without touching the state file.
#[test]
fn stop_tool_use_is_noop() {
    let (tmpdir, bin_dir) = setup_test_env();

    // Write a pre-existing state file to confirm it is NOT overwritten.
    let state_path = tmpdir.path().join("orchard-claude-test_session.json");
    fs::write(&state_path, r#"{"state":"working","marker":"original"}"#).unwrap();

    let payload = r#"{"hook_event_name":"Stop","session_id":"s1","cwd":"/workspace","stop_reason":"tool_use"}"#;
    let status = run_hook(tmpdir.path(), &bin_dir, payload);
    assert!(status.success(), "hook must exit 0 for Stop(tool_use)");

    // State file must be unchanged.
    let raw = fs::read_to_string(&state_path).unwrap();
    assert!(
        raw.contains("original"),
        "state file must not be overwritten on Stop(tool_use)"
    );
}

/// `Stop(stop_reason=end_turn)` writes idle state.
#[test]
fn stop_end_turn_writes_idle() {
    let (tmpdir, bin_dir) = setup_test_env();

    let payload = r#"{"hook_event_name":"Stop","session_id":"s1","cwd":"/workspace","stop_reason":"end_turn"}"#;
    let status = run_hook(tmpdir.path(), &bin_dir, payload);
    assert!(status.success());

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "idle", "Stop(end_turn) must write idle");
    assert_eq!(
        state["stop_reason"], "end_turn",
        "stop_reason must be present in state file"
    );
}

// ---------------------------------------------------------------------------
// PreToolUse — inflight tracking
// ---------------------------------------------------------------------------

/// `PreToolUse` (non-AskUserQuestion) appends `tool_use_id` to the inflight sidecar
/// and writes `state=working`.
#[test]
fn pre_tool_use_tracks_inflight() {
    let (tmpdir, bin_dir) = setup_test_env();

    let payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-abc-123"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "working");
    assert_eq!(state["inflight_tool_count"], 1);

    let inflight = read_inflight(tmpdir.path()).expect("inflight file must exist");
    assert_eq!(
        inflight.as_array().unwrap().len(),
        1,
        "inflight array must contain one entry"
    );
    assert_eq!(inflight[0], "tool-abc-123");
}

// ---------------------------------------------------------------------------
// PostToolUse — removes from inflight
// ---------------------------------------------------------------------------

/// `PostToolUse` removes the `tool_use_id` from the inflight sidecar.
/// When the sidecar is empty afterwards, `inflight_tool_count` is 0.
#[test]
fn post_tool_use_removes_inflight() {
    let (tmpdir, bin_dir) = setup_test_env();

    // Seed inflight sidecar with one entry.
    let inflight_path = tmpdir.path().join("orchard-claude-test_session.inflight.json");
    fs::write(&inflight_path, r#"["tool-abc-123"]"#).unwrap();

    let payload = r#"{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-abc-123"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "working");
    assert_eq!(state["inflight_tool_count"], 0);

    let inflight = read_inflight(tmpdir.path()).expect("inflight file must exist");
    assert_eq!(
        inflight.as_array().unwrap().len(),
        0,
        "inflight array must be empty after matching PostToolUse"
    );
}

// ---------------------------------------------------------------------------
// PostToolUseFailure — identical inflight tracking to PostToolUse
// ---------------------------------------------------------------------------

/// `PostToolUseFailure` removes the `tool_use_id` from inflight, same as `PostToolUse`.
#[test]
fn post_tool_use_failure_removes_inflight() {
    let (tmpdir, bin_dir) = setup_test_env();

    // Seed inflight sidecar with one entry.
    let inflight_path = tmpdir.path().join("orchard-claude-test_session.inflight.json");
    fs::write(&inflight_path, r#"["tool-fail-999"]"#).unwrap();

    let payload = r#"{"hook_event_name":"PostToolUseFailure","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-fail-999","error":"timeout"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "working");

    let inflight = read_inflight(tmpdir.path()).expect("inflight file must exist");
    assert_eq!(
        inflight.as_array().unwrap().len(),
        0,
        "inflight array must be empty after PostToolUseFailure"
    );
}

// ---------------------------------------------------------------------------
// SessionEnd — cleans up both files
// ---------------------------------------------------------------------------

/// `SessionEnd` deletes both the state file and the inflight sidecar.
#[test]
fn session_end_clears_both_files() {
    let (tmpdir, bin_dir) = setup_test_env();

    // Seed both files.
    let state_path = tmpdir.path().join("orchard-claude-test_session.json");
    let inflight_path = tmpdir.path().join("orchard-claude-test_session.inflight.json");
    fs::write(&state_path, r#"{"state":"working"}"#).unwrap();
    fs::write(&inflight_path, r#"["tool-abc-123"]"#).unwrap();

    let payload = r#"{"hook_event_name":"SessionEnd","session_id":"s1","cwd":"/workspace"}"#;
    let status = run_hook(tmpdir.path(), &bin_dir, payload);
    assert!(status.success());

    assert!(
        !state_path.exists(),
        "state file must be deleted on SessionEnd"
    );
    assert!(
        !inflight_path.exists(),
        "inflight file must be deleted on SessionEnd"
    );
}

// ---------------------------------------------------------------------------
// State file fields — structural assertions
// ---------------------------------------------------------------------------

/// `PreToolUse` state file includes `inflight_tool_count` field.
#[test]
fn pre_tool_use_state_file_includes_inflight_tool_count() {
    let (tmpdir, bin_dir) = setup_test_env();

    let payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Read","tool_use_id":"tool-read-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert!(
        state.get("inflight_tool_count").is_some(),
        "inflight_tool_count field must be present"
    );
}

/// `Stop(end_turn)` state file includes `stop_reason` field.
#[test]
fn stop_state_file_includes_stop_reason() {
    let (tmpdir, bin_dir) = setup_test_env();

    let payload = r#"{"hook_event_name":"Stop","session_id":"s1","cwd":"/workspace","stop_reason":"end_turn"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert!(
        state.get("stop_reason").is_some(),
        "stop_reason field must be present in Stop state file"
    );
}

// ---------------------------------------------------------------------------
// SessionStart — clears inflight
// ---------------------------------------------------------------------------

/// `SessionStart` writes idle state and resets the inflight sidecar to `[]`.
#[test]
fn session_start_clears_inflight() {
    let (tmpdir, bin_dir) = setup_test_env();

    // Seed inflight sidecar with stale entry.
    let inflight_path = tmpdir.path().join("orchard-claude-test_session.inflight.json");
    fs::write(&inflight_path, r#"["stale-id"]"#).unwrap();

    let payload = r#"{"hook_event_name":"SessionStart","session_id":"s1","cwd":"/workspace"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "idle");

    let inflight = read_inflight(tmpdir.path()).expect("inflight file must exist after SessionStart");
    assert_eq!(
        inflight.as_array().unwrap().len(),
        0,
        "inflight array must be empty after SessionStart"
    );
}
