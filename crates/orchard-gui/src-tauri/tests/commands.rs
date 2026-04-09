//! Integration tests for the `commands` module.
//!
//! These live outside `src/` because a Tauri lib crate with `cdylib` and
//! `staticlib` crate-types does not emit inner unit-test binaries. Driving
//! the module through its public API from a `tests/` file sidesteps that.

use std::cell::RefCell;
use std::fs;
use std::io::Write;

use orchard_gui_lib::commands::{list_sessions_in, read_session_file, send_to_tmux_with, Shell};

/// Records every `run` invocation so tests can assert on how the command was
/// constructed, without spawning a real tmux process.
struct MockShell {
    calls: RefCell<Vec<(String, Vec<String>)>>,
    exit_code: i32,
}

impl MockShell {
    fn new(exit_code: i32) -> Self {
        Self {
            calls: RefCell::new(Vec::new()),
            exit_code,
        }
    }
}

// Tests are single-threaded per binary invocation.
unsafe impl Sync for MockShell {}

impl Shell for MockShell {
    fn run(&self, program: &str, args: &[&str]) -> anyhow::Result<i32> {
        self.calls.borrow_mut().push((
            program.to_string(),
            args.iter().map(|s| s.to_string()).collect(),
        ));
        Ok(self.exit_code)
    }
}

#[test]
fn list_sessions_returns_empty_when_root_missing() {
    let tmp = tempfile::tempdir().unwrap();
    let missing = tmp.path().join("does-not-exist");
    let sessions = list_sessions_in(&missing).unwrap();
    assert!(sessions.is_empty());
}

#[test]
fn list_sessions_finds_jsonl_files_sorted_by_mtime_desc() {
    let tmp = tempfile::tempdir().unwrap();
    let project = tmp.path().join("-Users-someone-repo");
    fs::create_dir_all(&project).unwrap();

    let older = project.join("aaaa.jsonl");
    let newer = project.join("bbbb.jsonl");
    let ignored = project.join("notes.txt");
    fs::write(&older, "{}\n").unwrap();
    fs::write(&ignored, "ignore me").unwrap();
    std::thread::sleep(std::time::Duration::from_millis(20));
    fs::write(&newer, "{}\n").unwrap();

    let sessions = list_sessions_in(tmp.path()).unwrap();
    assert_eq!(sessions.len(), 2);
    assert_eq!(sessions[0].session_id, "bbbb");
    assert_eq!(sessions[1].session_id, "aaaa");
    assert_eq!(sessions[0].project_slug, "-Users-someone-repo");
    assert!(sessions[0].modified_unix >= sessions[1].modified_unix);
}

#[test]
fn read_session_file_parses_each_line_as_json() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("session.jsonl");
    let mut f = fs::File::create(&path).unwrap();
    writeln!(f, r#"{{"type":"user","message":{{"content":"hi"}}}}"#).unwrap();
    writeln!(f).unwrap();
    writeln!(
        f,
        r#"{{"type":"assistant","message":{{"content":"hello"}}}}"#
    )
    .unwrap();

    let events = read_session_file(&path).unwrap();
    assert_eq!(events.len(), 2);
    assert_eq!(events[0]["type"], "user");
    assert_eq!(events[1]["type"], "assistant");
}

#[test]
fn read_session_file_skips_malformed_lines() {
    let tmp = tempfile::tempdir().unwrap();
    let path = tmp.path().join("session.jsonl");
    fs::write(
        &path,
        "{\"type\":\"user\"}\nnot json\n{\"type\":\"assistant\"}\n",
    )
    .unwrap();

    let events = read_session_file(&path).unwrap();
    assert_eq!(events.len(), 2);
    assert_eq!(events[0]["type"], "user");
    assert_eq!(events[1]["type"], "assistant");
}

#[test]
fn send_to_tmux_invokes_send_keys_with_literal_flag() {
    let shell = MockShell::new(0);
    let code = send_to_tmux_with(&shell, "orchardist:0.0", "hello world").unwrap();
    assert_eq!(code, 0);
    let calls = shell.calls.borrow();
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].0, "tmux");
    assert_eq!(
        calls[0].1,
        vec!["send-keys", "-t", "orchardist:0.0", "-l", "hello world"]
    );
}

#[test]
fn send_to_tmux_preserves_multiline_payload() {
    let shell = MockShell::new(0);
    send_to_tmux_with(&shell, "sess:1", "line1\nline2").unwrap();
    let calls = shell.calls.borrow();
    assert_eq!(calls[0].1[4], "line1\nline2");
}

#[test]
fn send_to_tmux_propagates_nonzero_exit_code() {
    let shell = MockShell::new(2);
    let code = send_to_tmux_with(&shell, "sess:1", "hi").unwrap();
    assert_eq!(code, 2);
}
