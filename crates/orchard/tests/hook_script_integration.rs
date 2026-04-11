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
    fs::write(&fake_tmux, "#!/usr/bin/env bash\necho 'test_session'\n").unwrap();
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
    let inflight_path = tmpdir
        .path()
        .join("orchard-claude-test_session.inflight.json");
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
    let inflight_path = tmpdir
        .path()
        .join("orchard-claude-test_session.inflight.json");
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
    let inflight_path = tmpdir
        .path()
        .join("orchard-claude-test_session.inflight.json");
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
    let inflight_path = tmpdir
        .path()
        .join("orchard-claude-test_session.inflight.json");
    fs::write(&inflight_path, r#"["stale-id"]"#).unwrap();

    let payload = r#"{"hook_event_name":"SessionStart","session_id":"s1","cwd":"/workspace"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "idle");

    let inflight =
        read_inflight(tmpdir.path()).expect("inflight file must exist after SessionStart");
    assert_eq!(
        inflight.as_array().unwrap().len(),
        0,
        "inflight array must be empty after SessionStart"
    );
}

// ---------------------------------------------------------------------------
// Helpers for enrichment tests
// ---------------------------------------------------------------------------

/// Creates a test environment with both a fake `tmux` and a fake `orchard`
/// binary that supports `hook-enrich --transcript <path>`.
///
/// The fake `orchard` script reads the transcript path and returns a
/// hard-coded enrichment JSON so tests are independent of the real binary.
fn setup_test_env_with_orchard(
    enrichment_json: &str,
) -> (tempfile::TempDir, std::path::PathBuf) {
    let (tmpdir, bin_dir) = setup_test_env();

    let ej = enrichment_json.to_string();
    let fake_orchard_script = format!(
        "#!/usr/bin/env bash\n\
        # Fake orchard for integration tests\n\
        if [ \"$1\" = \"hook-enrich\" ]; then\n\
            echo '{ej}'\n\
        fi\n"
    );
    let fake_orchard = bin_dir.join("orchard");
    fs::write(&fake_orchard, fake_orchard_script).unwrap();
    let mut perms = fs::metadata(&fake_orchard).unwrap().permissions();
    perms.set_mode(0o755);
    fs::set_permissions(&fake_orchard, perms).unwrap();

    (tmpdir, bin_dir)
}

/// Creates a JSONL transcript file with one assistant message in the given dir.
fn write_mock_transcript(dir: &Path) -> std::path::PathBuf {
    let path = dir.join("transcript.jsonl");
    let line = r#"{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":1234,"output_tokens":56,"cache_creation_input_tokens":100,"cache_read_input_tokens":200}}}"#;
    fs::write(&path, format!("{line}\n")).unwrap();
    path
}

// ---------------------------------------------------------------------------
// AC3: Hook records last_tool on PreToolUse
// ---------------------------------------------------------------------------

/// AC3: Hook records `last_tool` field on PreToolUse event.
#[test]
fn pre_tool_use_records_last_tool() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Edit","tool_use_id":"tool-edit-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(
        state["last_tool"], "Edit",
        "last_tool must be 'Edit' after PreToolUse with tool_name=Edit"
    );
}

// ---------------------------------------------------------------------------
// AC3: Hook records current_task on UserPromptSubmit
// ---------------------------------------------------------------------------

/// AC3: Hook records `current_task` on UserPromptSubmit.
#[test]
fn user_prompt_submit_records_current_task() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "UserPromptSubmit",
        "session_id": "s1",
        "cwd": "/workspace",
        "prompt": "refactor the cache module to support tagging"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(
        state["current_task"],
        "refactor the cache module to support tagging",
        "current_task must match prompt"
    );
}

/// AC3: `current_task` is truncated to 80 characters.
#[test]
fn user_prompt_submit_truncates_current_task_to_80_chars() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let long_prompt = "a".repeat(200);
    let payload = serde_json::json!({
        "hook_event_name": "UserPromptSubmit",
        "session_id": "s1",
        "cwd": "/workspace",
        "prompt": long_prompt
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    let task = state["current_task"].as_str().unwrap();
    assert_eq!(task.len(), 80, "current_task must be exactly 80 chars: got {}", task.len());
}

/// AC3: `current_task` keeps only the first line of a multiline prompt.
#[test]
fn user_prompt_submit_keeps_first_line_only() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "UserPromptSubmit",
        "session_id": "s1",
        "cwd": "/workspace",
        "prompt": "fix the bug\n\nbackground: the hook swallows errors"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(
        state["current_task"],
        "fix the bug",
        "current_task must be only the first line"
    );
}

// ---------------------------------------------------------------------------
// AC3: Hook records session_start_ts and model on SessionStart
// ---------------------------------------------------------------------------

/// AC3: Hook records `session_start_ts` and `model` on SessionStart.
#[test]
fn session_start_records_start_ts_and_model() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "SessionStart",
        "session_id": "s1",
        "cwd": "/workspace",
        "model": "claude-opus-4-6"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert!(
        state["session_start_ts"].is_number(),
        "session_start_ts must be a number"
    );
    assert_eq!(
        state["model"],
        "claude-opus-4-6",
        "model must be 'claude-opus-4-6'"
    );
}

/// AC3: `session_start_ts` is preserved across subsequent events.
#[test]
fn session_start_ts_preserved_across_subsequent_events() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    // First: SessionStart
    let start_payload = serde_json::json!({
        "hook_event_name": "SessionStart",
        "session_id": "s1",
        "cwd": "/workspace",
        "model": "claude-opus-4-6"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &start_payload);

    let state_after_start = read_state_file(tmpdir.path());
    let original_ts = state_after_start["session_start_ts"]
        .as_u64()
        .expect("session_start_ts must be present after SessionStart");

    // Second: PreToolUse
    let tool_payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, tool_payload);

    let state_after_tool = read_state_file(tmpdir.path());
    let preserved_ts = state_after_tool["session_start_ts"]
        .as_u64()
        .expect("session_start_ts must still be present after PreToolUse");

    assert_eq!(
        original_ts,
        preserved_ts,
        "session_start_ts must not change across events"
    );
}

/// AC3: `last_tool` is cleared on Stop (non-tool_use stop_reason).
#[test]
fn stop_clears_last_tool() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    // Seed a state file with last_tool set.
    let state_path = tmpdir.path().join("orchard-claude-test_session.json");
    fs::write(
        &state_path,
        r#"{"state":"working","session_id":"s1","tmux_session":"test_session","cwd":"/workspace","event":"PreToolUse","timestamp":"2026-01-01T00:00:00Z","inflight_tool_count":0,"last_tool":"Bash"}"#,
    )
    .unwrap();

    let payload = r#"{"hook_event_name":"Stop","session_id":"s1","cwd":"/workspace","stop_reason":"end_turn"}"#;
    run_hook(tmpdir.path(), &bin_dir, payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "idle", "Stop(end_turn) must write idle");
    assert!(
        state.get("last_tool").is_none() || state["last_tool"].is_null(),
        "last_tool must be absent after Stop(end_turn)"
    );
}

// ---------------------------------------------------------------------------
// AC8: Hook integration writes new fields end-to-end
// ---------------------------------------------------------------------------

/// AC8: Hook writes all enrichment fields when transcript is present.
#[test]
fn hook_integration_writes_all_enrichment_fields() {
    // Fake orchard returns enrichment with all token fields.
    let enrichment = r#"{"model":"claude-opus-4-6","inputTokens":1234,"outputTokens":56,"cacheCreationInputTokens":100,"cacheReadInputTokens":200}"#;
    let (tmpdir, bin_dir) = setup_test_env_with_orchard(enrichment);

    let transcript = write_mock_transcript(tmpdir.path());
    let payload = serde_json::json!({
        "hook_event_name": "PreToolUse",
        "session_id": "s1",
        "cwd": "/workspace",
        "tool_name": "Bash",
        "tool_use_id": "tool-abc",
        "transcript_path": transcript.to_string_lossy()
    })
    .to_string();

    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "working");
    assert!(state["last_tool"].is_string(), "last_tool must be present");
    assert_eq!(state["model"], "claude-opus-4-6");
    assert_eq!(state["input_tokens"], 1234);
    assert_eq!(state["output_tokens"], 56);
    assert_eq!(state["cache_read_input_tokens"], 200);
    assert_eq!(state["cache_creation_input_tokens"], 100);
}

/// AC8: UserPromptSubmit integration populates current_task.
#[test]
fn hook_integration_user_prompt_populates_current_task() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "UserPromptSubmit",
        "session_id": "s1",
        "cwd": "/workspace",
        "prompt": "draft the release notes"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["current_task"], "draft the release notes");
}

/// AC8: SessionStart integration populates session_start_ts and model.
#[test]
fn hook_integration_session_start_populates_ts_and_model() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "SessionStart",
        "session_id": "s1",
        "cwd": "/workspace",
        "model": "claude-opus-4-6"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    let ts = state["session_start_ts"].as_u64().expect("session_start_ts must be present");
    assert!(ts > 1_700_000_000, "session_start_ts must be a recent unix timestamp");
    assert_eq!(state["model"], "claude-opus-4-6");
}

// ---------------------------------------------------------------------------
// Fix #1 regression tests — last_tool and current_task survive PostToolUse
// ---------------------------------------------------------------------------

/// Regression: `last_tool` set by PreToolUse must survive the PostToolUse that follows.
///
/// Before the fix, write_state on PostToolUse rebuilt the object without preserving
/// last_tool, so the field disappeared immediately after the tool completed.
#[test]
fn pre_tool_use_followed_by_post_tool_use_preserves_last_tool() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    // SessionStart sets session_start_ts and model.
    let start_payload = serde_json::json!({
        "hook_event_name": "SessionStart",
        "session_id": "s1",
        "cwd": "/workspace",
        "model": "claude-opus-4-6"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &start_payload);

    // PreToolUse sets last_tool = "Bash".
    let pre_payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-bash-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, pre_payload);

    let after_pre = read_state_file(tmpdir.path());
    assert_eq!(after_pre["last_tool"], "Bash", "last_tool must be set after PreToolUse");

    // PostToolUse — no extra fields, must preserve last_tool.
    let post_payload = r#"{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Bash","tool_use_id":"tool-bash-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, post_payload);

    let after_post = read_state_file(tmpdir.path());
    assert_eq!(
        after_post["last_tool"], "Bash",
        "last_tool must survive PostToolUse — was dropped before fix"
    );
}

/// Regression: `current_task` set by UserPromptSubmit must survive a subsequent PreToolUse.
///
/// Before the fix, write_state on PreToolUse did not preserve current_task, so the
/// task description disappeared as soon as Claude called its first tool.
#[test]
fn user_prompt_submit_followed_by_pre_tool_use_preserves_current_task() {
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    // UserPromptSubmit sets current_task = "foo".
    let prompt_payload = serde_json::json!({
        "hook_event_name": "UserPromptSubmit",
        "session_id": "s1",
        "cwd": "/workspace",
        "prompt": "foo"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &prompt_payload);

    let after_prompt = read_state_file(tmpdir.path());
    assert_eq!(after_prompt["current_task"], "foo", "current_task must be set after UserPromptSubmit");

    // PreToolUse — sets last_tool but must preserve current_task.
    let pre_payload = r#"{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/workspace","tool_name":"Read","tool_use_id":"tool-read-1"}"#;
    run_hook(tmpdir.path(), &bin_dir, pre_payload);

    let after_pre = read_state_file(tmpdir.path());
    assert_eq!(
        after_pre["current_task"], "foo",
        "current_task must survive PreToolUse — was dropped before fix"
    );
}

/// AC8: Hook still writes state when transcript_path points at a missing file.
#[test]
fn hook_integration_missing_transcript_still_writes_state() {
    // Fake orchard returns {} for missing file (same as real binary).
    let (tmpdir, bin_dir) = setup_test_env_with_orchard("{}");

    let payload = serde_json::json!({
        "hook_event_name": "PreToolUse",
        "session_id": "s1",
        "cwd": "/workspace",
        "tool_name": "Bash",
        "tool_use_id": "tool-abc",
        "transcript_path": "/nonexistent/path/transcript.jsonl"
    })
    .to_string();
    run_hook(tmpdir.path(), &bin_dir, &payload);

    let state = read_state_file(tmpdir.path());
    assert_eq!(state["state"], "working", "state must be written even with missing transcript");
    assert!(state["last_tool"].is_string(), "last_tool must still be written");
    // Token fields must not be present (no enrichment).
    assert!(
        state.get("input_tokens").is_none() || state["input_tokens"].is_null(),
        "input_tokens must not be present when transcript is missing"
    );
}
