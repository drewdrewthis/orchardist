//! Unit tests for the events.jsonl tailer.
//!
//! Covers: cold-start EOF (AC #34), non-webhook/malformed skipping (AC #33),
//! partial-line advance (AC #30), size-growth trigger (AC #31),
//! truncation reset (AC #32), missing-file silence (AC #37).

use super::*;
use std::fs;
use std::io::Write;
use tempfile::NamedTempFile;

fn webhook_line(kind: &str) -> String {
    format!(
        r#"{{"source":"webhook","kind":"{}","ts":"2024-01-01T00:00:00Z","data":{{}}}}"#,
        kind
    )
}

fn task_line() -> String {
    r#"{"ts":"2024-01-01T00:00:00Z","event":"task.created","task":"repo#1"}"#.to_string()
}

/// AC #34: historical lines present at cold start are NOT replayed.
#[test]
fn cold_start_skips_historical_lines() {
    let mut f = NamedTempFile::new().unwrap();
    writeln!(f, "{}", webhook_line("push")).unwrap();
    writeln!(f, "{}", webhook_line("push")).unwrap();
    writeln!(f, "{}", webhook_line("push")).unwrap();
    f.flush().unwrap();

    let mut tailer = Tailer::new(f.path().to_path_buf());

    assert!(tailer.poll().is_empty(), "cold start must return empty");

    writeln!(f, "{}", webhook_line("pull_request.opened")).unwrap();
    f.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 1, "only the newly appended line");
}

/// AC #33: task lines and malformed JSON are silently skipped; only webhook
/// lines are forwarded.
#[test]
fn poll_returns_new_webhook_lines_only() {
    let mut f = NamedTempFile::new().unwrap();
    let mut tailer = Tailer::new(f.path().to_path_buf());

    writeln!(f, "{}", task_line()).unwrap();
    writeln!(f, "{{malformed json").unwrap();
    writeln!(f, "{}", webhook_line("pull_request.opened")).unwrap();
    f.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 1, "only the webhook line");
    assert_eq!(results[0]["kind"].as_str(), Some("pull_request.opened"));
}

/// AC #30: offset advances only past the last complete newline; trailing
/// partial bytes are retried on the next poll.
#[test]
fn poll_advances_offset_only_past_last_newline() {
    let mut f = NamedTempFile::new().unwrap();
    let mut tailer = Tailer::new(f.path().to_path_buf());

    let line1 = webhook_line("push.one");
    let line2 = webhook_line("push.two");
    let partial = webhook_line("push.partial");
    // Write two complete lines and one partial (no trailing newline).
    write!(f, "{}\n{}\n{}", line1, line2, &partial[..partial.len() - 1]).unwrap();
    f.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 2, "only complete lines returned");

    // Complete the partial line.
    let suffix = &partial[partial.len() - 1..];
    writeln!(f, "{}", suffix).unwrap();
    f.flush().unwrap();

    let results2 = tailer.poll();
    assert_eq!(results2.len(), 1, "the completed partial line");
}

/// AC #31: a read is triggered whenever size > stored offset, regardless of
/// whether mtime changed (mtime resolution is 1s on macOS/NFS).
#[test]
fn poll_reads_when_size_grows_even_if_mtime_unchanged() {
    let mut f = NamedTempFile::new().unwrap();
    writeln!(f, "{}", task_line()).unwrap();
    f.flush().unwrap();

    // Create tailer at current EOF.
    let mut tailer = Tailer::new(f.path().to_path_buf());

    // Append a webhook line — size is now > offset.
    writeln!(f, "{}", webhook_line("pull_request.opened")).unwrap();
    f.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 1, "new line returned because size > offset");
}

/// AC #32: when the file is shorter than the stored offset (truncation),
/// the offset resets to 0 and the new content is read from the start.
#[test]
fn truncation_resets_offset_to_zero() {
    let mut f = NamedTempFile::new().unwrap();
    for _ in 0..3 {
        writeln!(f, "{}", task_line()).unwrap();
    }
    f.flush().unwrap();

    let mut tailer = Tailer::new(f.path().to_path_buf());
    assert!(tailer.offset > 20, "offset should be large after cold start");

    // Truncate and write fresh content.
    let path = f.path().to_path_buf();
    let mut fresh = fs::OpenOptions::new()
        .write(true)
        .truncate(true)
        .open(&path)
        .unwrap();
    writeln!(fresh, "{}", webhook_line("pull_request.opened")).unwrap();
    fresh.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 1, "new line after truncation");
    assert!(tailer.offset <= 200, "offset reset and advanced past new content");
}

/// AC #37: polling a path that never existed returns empty silently (no panic,
/// no warning logged for missing file).
#[test]
fn missing_file_is_silent() {
    let path = std::env::temp_dir().join("orchard_tailer_test_missing_file.jsonl");
    let _ = fs::remove_file(&path);

    let mut tailer = Tailer::new(path);
    assert!(tailer.poll().is_empty(), "first poll: empty");
    assert!(tailer.poll().is_empty(), "second poll: empty");
    assert!(tailer.poll().is_empty(), "third poll: empty");
}

/// AC #33 (continued): malformed JSON does not stall the offset; subsequent
/// valid lines are still returned.
#[test]
fn poll_skips_malformed_json_without_losing_progress() {
    let mut f = NamedTempFile::new().unwrap();
    let mut tailer = Tailer::new(f.path().to_path_buf());

    write!(
        f,
        "{{malformed}}\n{}\n",
        webhook_line("pull_request.opened")
    )
    .unwrap();
    f.flush().unwrap();

    let results = tailer.poll();
    assert_eq!(results.len(), 1, "only the valid webhook line");

    assert!(tailer.poll().is_empty(), "no leftover lines");
}
