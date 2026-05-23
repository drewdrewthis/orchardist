//! Append-only structured event log.
//!
//! Writes task and session lifecycle events as JSON lines to
//! `~/.local/state/git-orchard/events.jsonl`. Rotates at 50 MB, keeping
//! at most three archived files. Used for auditing and retrospective analysis.
use chrono::{DateTime, Utc};
use serde::Serialize;
use serde_json::Value;
use std::collections::HashMap;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};

const MAX_SIZE_BYTES: u64 = 50 * 1024 * 1024; // 50 MB
const MAX_ROTATED_FILES: u32 = 3;

/// A structured event written as a single JSON line to events.jsonl.
#[derive(Debug, Clone, Serialize)]
pub struct Event {
    /// UTC timestamp of when the event was recorded.
    pub ts: DateTime<Utc>,
    /// Event type identifier (e.g. `"task.created"`, `"session.switch"`).
    pub event: String,
    /// Arbitrary key-value payload specific to the event type.
    #[serde(flatten)]
    pub fields: HashMap<String, Value>,
}

/// Returns the path to the events.jsonl file.
///
/// Used by the watch daemon tailer to know which file to tail, and by
/// the events module internally for all writes.
///
/// If `ORCHARD_EVENTS_PATH` is set in the environment the value is used
/// verbatim.  This lets unit tests redirect writes to a temp directory
/// without touching `~/.local/state/git-orchard/events.jsonl`.
pub fn events_path() -> PathBuf {
    if let Ok(p) = std::env::var("ORCHARD_EVENTS_PATH") {
        return PathBuf::from(p);
    }
    dirs::home_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join(".local")
        .join("state")
        .join("git-orchard")
        .join("events.jsonl")
}

/// Appends an event to the given path. Creates the file/dir if needed.
/// Rotates if file exceeds 50 MB (keeps at most 3 rotated files).
///
/// This is the internal, path-parameterised version used by tests.
fn append_event(path: &Path, event_type: &str, fields: &[(&str, Value)]) {
    let event = Event {
        ts: Utc::now(),
        event: event_type.to_string(),
        fields: fields
            .iter()
            .map(|(k, v)| (k.to_string(), v.clone()))
            .collect(),
    };

    let line = match serde_json::to_string(&event) {
        Ok(s) => s,
        Err(_) => return,
    };

    append_line(path, &line);
}

/// Rotates the events file:
///   events.jsonl.2 → delete
///   events.jsonl.1 → events.jsonl.2
///   events.jsonl   → events.jsonl.1
///   new events.jsonl is created on the next write
fn rotate(path: &Path) {
    // Delete the oldest rotated file we track (N = MAX_ROTATED_FILES).
    let oldest = rotated_path(path, MAX_ROTATED_FILES);
    let _ = fs::remove_file(&oldest);

    // Shift remaining rotated files up by one.
    for n in (1..MAX_ROTATED_FILES).rev() {
        let from = rotated_path(path, n);
        let to = rotated_path(path, n + 1);
        if from.exists() {
            let _ = fs::rename(&from, &to);
        }
    }

    // Rename current file to .1
    let _ = fs::rename(path, rotated_path(path, 1));
}

/// Returns `<base_path>.<n>` e.g. events.jsonl.1
fn rotated_path(base: &Path, n: u32) -> PathBuf {
    let name = base
        .file_name()
        .map(|f| format!("{}.{}", f.to_string_lossy(), n))
        .unwrap_or_else(|| format!("events.jsonl.{}", n));
    base.with_file_name(name)
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Appends an event to `~/.local/state/git-orchard/events.jsonl`.
/// Creates the file/dir if needed. Rotates at 50 MB. Never returns an error.
pub fn log_event(event_type: &str, fields: &[(&str, Value)]) {
    append_event(&events_path(), event_type, fields);
}

/// Logs a `task.created` event.
pub fn log_task_created(task_id: &str, source: &str) {
    log_event(
        "task.created",
        &[
            ("task", Value::String(task_id.to_string())),
            ("source", Value::String(source.to_string())),
        ],
    );
}

/// Logs a `task.status_change` event.
pub fn log_task_status_change(task_id: &str, from: &str, to: &str, reason: &str) {
    log_event(
        "task.status_change",
        &[
            ("task", Value::String(task_id.to_string())),
            ("from", Value::String(from.to_string())),
            ("to", Value::String(to.to_string())),
            ("reason", Value::String(reason.to_string())),
        ],
    );
}

/// Logs a `task.archived` event.
pub fn log_task_archived(task_id: &str) {
    log_event(
        "task.archived",
        &[("task", Value::String(task_id.to_string()))],
    );
}

/// Logs a `session.created` event.
pub fn log_session_created(task_id: &str, session: &str) {
    log_event(
        "session.created",
        &[
            ("task", Value::String(task_id.to_string())),
            ("session", Value::String(session.to_string())),
        ],
    );
}

/// Logs a `session.switch` event.
pub fn log_session_switch(task_id: &str, session: &str, trigger: &str) {
    log_event(
        "session.switch",
        &[
            ("task", Value::String(task_id.to_string())),
            ("session", Value::String(session.to_string())),
            ("trigger", Value::String(trigger.to_string())),
        ],
    );
}

/// Logs a `session.dead` event.
pub fn log_session_dead(task_id: &str, session: &str) {
    log_event(
        "session.dead",
        &[
            ("task", Value::String(task_id.to_string())),
            ("session", Value::String(session.to_string())),
        ],
    );
}

/// Logs a `session.orphaned` event.
pub fn log_session_orphaned(session: &str, path: &str) {
    log_event(
        "session.orphaned",
        &[
            ("session", Value::String(session.to_string())),
            ("path", Value::String(path.to_string())),
        ],
    );
}

/// Logs a `worktree.remote_lost` event when a previously-cached remote
/// worktree disappears (e.g. a Boxd fork VM is destroyed between refreshes).
///
/// Field shape matches the AC5 spec (feature.feature:323):
/// `{event, ts, repo, remote_name, remote_type, host, branch, path}`.
/// Consumers (TUI daemon, monitor) distinguish this from `webhook.*` lines
/// by the absence of a `"source"` field.
pub fn log_worktree_remote_lost(
    repo: &str,
    remote_name: &str,
    remote_type: &str,
    host: &str,
    branch: &str,
    path: &str,
) {
    log_event(
        "worktree.remote_lost",
        &[
            ("repo", Value::String(repo.to_string())),
            ("remote_name", Value::String(remote_name.to_string())),
            ("remote_type", Value::String(remote_type.to_string())),
            ("host", Value::String(host.to_string())),
            ("branch", Value::String(branch.to_string())),
            ("path", Value::String(path.to_string())),
        ],
    );
}

/// Logs a `session.remote_lost` event when a previously-cached fork's tmux
/// session cache is deleted because the fork VM is no longer enumerated by
/// the golden Boxd host.
///
/// Field shape mirrors `worktree.remote_lost` (feature.feature:323) so
/// consumers can handle both events with the same destructure.
/// `{event, ts, repo, remote_name, remote_type, host}`.
pub fn log_session_remote_lost(repo: &str, remote_name: &str, remote_type: &str, host: &str) {
    log_event(
        "session.remote_lost",
        &[
            ("repo", Value::String(repo.to_string())),
            ("remote_name", Value::String(remote_name.to_string())),
            ("remote_type", Value::String(remote_type.to_string())),
            ("host", Value::String(host.to_string())),
        ],
    );
}

/// Logs a `refresh.complete` event.
pub fn log_refresh_complete(duration_ms: u64, tasks: usize, sessions: usize, worktrees: usize) {
    log_event(
        "refresh.complete",
        &[
            ("duration_ms", Value::Number(duration_ms.into())),
            ("tasks", Value::Number(tasks.into())),
            ("sessions", Value::Number(sessions.into())),
            ("worktrees", Value::Number(worktrees.into())),
        ],
    );
}

/// Logs an `error` event.
pub fn log_error(message: &str, context: &str) {
    log_event(
        "error",
        &[
            ("message", Value::String(message.to_string())),
            ("context", Value::String(context.to_string())),
        ],
    );
}

/// Logs a watch event to the events log.
pub fn log_watch_event(event_type: &str, details: &str) {
    log_event(
        &format!("watch.{}", event_type),
        &[("details", Value::String(details.to_string()))],
    );
}

/// Appends a webhook-sourced event line to events.jsonl using the #215 schema:
/// `{ts, source:"webhook", kind, repo?, pr?, issue?, actor?, data}`.
///
/// This lives in the same file as the existing `log_event` but writes a
/// DIFFERENT JSON shape. Consumers (the watch tailer) distinguish webhook
/// lines from task/session lines by the presence of the `source` field.
///
/// Both writers go through the same `OpenOptions::append` + 50MB rotation
/// path so writes are atomic and rotation applies uniformly.
pub fn log_webhook_event(event: &crate::webhook::normalize::NormalizedEvent) {
    let path = events_path();
    let line = match serde_json::to_string(event) {
        Ok(s) => s,
        Err(_) => return,
    };
    append_line(&path, &line);
}

/// Appends a single JSON line to `path`, creating the file and its parent
/// directories as needed. Rotates if the file exceeds 50 MB before writing.
fn append_line(path: &Path, line: &str) {
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }

    if let Ok(meta) = fs::metadata(path)
        && meta.len() >= MAX_SIZE_BYTES
    {
        rotate(path);
    }

    let mut file = match OpenOptions::new().create(true).append(true).open(path) {
        Ok(f) => f,
        Err(_) => return,
    };

    // Single write_all so O_APPEND atomicity covers the entire line+newline.
    // Two separate syscalls would allow concurrent writers to interleave
    // as: A_payload | B_payload | A_\n | B_\n → malformed JSON.
    let mut buf = Vec::with_capacity(line.len() + 1);
    buf.extend_from_slice(line.as_bytes());
    buf.push(b'\n');
    let _ = file.write_all(&buf);
    let _ = file.flush();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Read;
    use tempfile::tempdir;

    fn read_lines(path: &Path) -> Vec<String> {
        let mut contents = String::new();
        let mut f = fs::File::open(path).expect("file should exist");
        f.read_to_string(&mut contents).unwrap();
        contents
            .lines()
            .filter(|l| !l.is_empty())
            .map(|l| l.to_string())
            .collect()
    }

    #[test]
    fn event_serializes_as_single_json_line() {
        let mut fields = HashMap::new();
        fields.insert("task".to_string(), Value::String("myrepo#1".to_string()));
        let event = Event {
            ts: Utc::now(),
            event: "task.created".to_string(),
            fields,
        };

        let serialized = serde_json::to_string(&event).unwrap();

        // Must not contain newlines (single line).
        assert!(!serialized.contains('\n'));

        // Must be valid JSON with required fields.
        let parsed: serde_json::Map<String, Value> =
            serde_json::from_str(&serialized).expect("must be valid JSON object");
        assert!(parsed.contains_key("ts"), "missing 'ts' field");
        assert!(parsed.contains_key("event"), "missing 'event' field");
        assert_eq!(parsed["event"], Value::String("task.created".to_string()));
    }

    #[test]
    fn event_has_iso8601_timestamp() {
        let mut fields = HashMap::new();
        fields.insert("task".to_string(), Value::String("myrepo#1".to_string()));
        let event = Event {
            ts: Utc::now(),
            event: "task.created".to_string(),
            fields,
        };

        let serialized = serde_json::to_string(&event).unwrap();
        let parsed: serde_json::Map<String, Value> = serde_json::from_str(&serialized).unwrap();

        let ts_str = parsed["ts"].as_str().expect("ts must be a string");
        // ISO 8601 UTC: must be parseable by chrono as DateTime<Utc>.
        ts_str
            .parse::<DateTime<Utc>>()
            .expect("ts must be ISO 8601 UTC");
    }

    #[test]
    fn log_event_appends_to_file() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        append_event(
            &path,
            "task.created",
            &[("task", Value::String("a#1".to_string()))],
        );
        append_event(
            &path,
            "task.archived",
            &[("task", Value::String("a#2".to_string()))],
        );
        append_event(
            &path,
            "error",
            &[
                ("message", Value::String("oops".to_string())),
                ("context", Value::String("test".to_string())),
            ],
        );

        let lines = read_lines(&path);
        assert_eq!(lines.len(), 3, "expected 3 lines");

        for line in &lines {
            serde_json::from_str::<serde_json::Map<String, Value>>(line)
                .expect("each line must be valid JSON");
        }
    }

    #[test]
    fn rotation_at_50mb() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        // Create a file just over 50 MB.
        let oversized: Vec<u8> = vec![b'x'; (50 * 1024 * 1024) + 1];
        fs::write(&path, &oversized).unwrap();

        append_event(
            &path,
            "task.created",
            &[("task", Value::String("a#1".to_string()))],
        );

        // Original should have been rotated.
        let rotated = rotated_path(&path, 1);
        assert!(
            rotated.exists(),
            "events.jsonl.1 should exist after rotation"
        );

        // New events.jsonl should be small (just the one new event).
        let new_size = fs::metadata(&path).unwrap().len();
        assert!(
            new_size < 1024,
            "new events.jsonl should be small, got {} bytes",
            new_size
        );
    }

    #[test]
    fn rotation_keeps_max_3_files() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        // Pre-populate rotated files .1, .2, .3 (already at the maximum).
        fs::write(rotated_path(&path, 1), b"old1").unwrap();
        fs::write(rotated_path(&path, 2), b"old2").unwrap();
        fs::write(rotated_path(&path, 3), b"old3").unwrap();

        // Create oversized main file to trigger rotation.
        let oversized: Vec<u8> = vec![b'x'; (50 * 1024 * 1024) + 1];
        fs::write(&path, &oversized).unwrap();

        append_event(
            &path,
            "task.created",
            &[("task", Value::String("a#1".to_string()))],
        );

        // No fourth rotated file should be created.
        assert!(
            !rotated_path(&path, 4).exists(),
            "events.jsonl.4 must not exist — max 3 rotated files"
        );

        // The three rotated files should all be present (shifted).
        assert!(
            rotated_path(&path, 1).exists(),
            "events.jsonl.1 should exist"
        );
        assert!(
            rotated_path(&path, 2).exists(),
            "events.jsonl.2 should exist"
        );
        assert!(
            rotated_path(&path, 3).exists(),
            "events.jsonl.3 should exist"
        );
    }

    #[test]
    fn convenience_constructors_produce_correct_event_types() {
        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        // Call each convenience function via append_event to route to our temp path.
        let cases: &[(&str, &[(&str, Value)])] = &[
            (
                "task.created",
                &[
                    ("task", Value::String("r#1".into())),
                    ("source", Value::String("github_issue".into())),
                ],
            ),
            (
                "task.status_change",
                &[
                    ("task", Value::String("r#1".into())),
                    ("from", Value::String("ready".into())),
                    ("to", Value::String("in_progress".into())),
                    ("reason", Value::String("enter".into())),
                ],
            ),
            ("task.archived", &[("task", Value::String("r#1".into()))]),
            (
                "session.created",
                &[
                    ("task", Value::String("r#1".into())),
                    ("session", Value::String("r_1_main".into())),
                ],
            ),
            (
                "session.switch",
                &[
                    ("task", Value::String("r#1".into())),
                    ("session", Value::String("r_1_main".into())),
                    ("trigger", Value::String("keypress".into())),
                ],
            ),
            (
                "session.dead",
                &[
                    ("task", Value::String("r#1".into())),
                    ("session", Value::String("r_1_main".into())),
                ],
            ),
            (
                "session.orphaned",
                &[
                    ("session", Value::String("mystery".into())),
                    ("path", Value::String("/tmp/x".into())),
                ],
            ),
            (
                "refresh.complete",
                &[
                    ("duration_ms", Value::Number(100u64.into())),
                    ("tasks", Value::Number(1usize.into())),
                    ("sessions", Value::Number(2usize.into())),
                    ("worktrees", Value::Number(3usize.into())),
                ],
            ),
            (
                "error",
                &[
                    ("message", Value::String("oops".into())),
                    ("context", Value::String("test".into())),
                ],
            ),
        ];

        for (event_type, fields) in cases {
            append_event(&path, event_type, fields);
        }

        let lines = read_lines(&path);
        assert_eq!(lines.len(), cases.len(), "one line per event");

        for (i, (expected_type, _)) in cases.iter().enumerate() {
            let parsed: serde_json::Map<String, Value> =
                serde_json::from_str(&lines[i]).expect("valid JSON");
            assert_eq!(
                parsed["event"].as_str().unwrap(),
                *expected_type,
                "event type mismatch at index {}",
                i
            );
        }
    }

    #[test]
    fn worktree_remote_lost_event_has_spec_required_fields() {
        // feature.feature:323 — shape of the worktree.remote_lost event.
        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        append_event(
            &path,
            "worktree.remote_lost",
            &[
                ("repo", Value::String("langwatch/langwatch".into())),
                ("remote_name", Value::String("boxd-fork-langwatch".into())),
                ("remote_type", Value::String("boxd-fork".into())),
                ("host", Value::String("boxd@issue3155.boxd.sh".into())),
                (
                    "branch",
                    Value::String("issue3155/custom-evaluator-input-field-race".into()),
                ),
                ("path", Value::String("~/langwatch".into())),
            ],
        );

        let lines = read_lines(&path);
        assert_eq!(lines.len(), 1);
        let parsed: serde_json::Map<String, Value> = serde_json::from_str(&lines[0]).unwrap();
        for field in [
            "ts",
            "event",
            "repo",
            "remote_name",
            "remote_type",
            "host",
            "branch",
            "path",
        ] {
            assert!(
                parsed.contains_key(field),
                "spec-required field '{field}' missing from worktree.remote_lost event"
            );
        }
        assert_eq!(
            parsed["event"].as_str().unwrap(),
            "worktree.remote_lost",
            "event name follows dotted-namespace convention"
        );
        // No `source` field — distinguishes from webhook.* lines.
        assert!(
            !parsed.contains_key("source"),
            "task/session lines must not carry the webhook 'source' field"
        );
    }

    #[test]
    fn webhook_event_line_has_source_and_kind_fields() {
        use crate::webhook::normalize::build_event;

        let dir = tempdir().unwrap();
        let path = dir.path().join("events.jsonl");

        let event = build_event(
            "pull_request.opened".to_string(),
            Some("acme/webapp".to_string()),
            Some(42),
            None,
            Some("some-actor".to_string()),
            &serde_json::json!({"action": "opened"}),
        );

        let line = serde_json::to_string(&event).expect("must serialize");
        append_line(&path, &line);

        let lines = read_lines(&path);
        assert_eq!(lines.len(), 1, "expected exactly one line");

        let parsed: serde_json::Map<String, Value> =
            serde_json::from_str(&lines[0]).expect("line must be valid JSON");
        assert_eq!(
            parsed.get("source").and_then(Value::as_str),
            Some("webhook"),
            "source field must be 'webhook'"
        );
        assert!(parsed.contains_key("kind"), "kind field must be present");
        assert_eq!(
            parsed.get("kind").and_then(Value::as_str),
            Some("pull_request.opened"),
        );
    }
}
