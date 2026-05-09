//! Watcher offset-bookkeeping regression test.
//!
//! Reproduces and prevents the offset-leak bug where the previous
//! `Vec::split` implementation double-counted newline bytes, causing
//! `consumed_to` to drift past the actual file content and silently
//! drop subsequent lines.
//!
//! This is the bug the parent session's interactive smoke test
//! flushed: the second of two consecutive sends would land in the
//! JSONL but never reach the GUI because the watcher's offset already
//! pointed past it. Catching this in a unit test means a future
//! refactor of the line-walking loop can't reintroduce the leak
//! without breaking CI.

use orchard_gui_lib::chat::{parse_new_lines, AppendedPayload};

fn line(json: &str) -> Vec<u8> {
    let mut v = json.as_bytes().to_vec();
    v.push(b'\n');
    v
}

fn cat(parts: &[&[u8]]) -> Vec<u8> {
    let mut out = Vec::new();
    for p in parts {
        out.extend_from_slice(p);
    }
    out
}

#[test]
fn parses_single_complete_line() {
    let bytes = line(
        r#"{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","sender_machine":"m","text":"hi","source":"internal"}"#,
    );
    let (payloads, consumed) = parse_new_lines(&bytes, 0, "general");
    assert_eq!(payloads.len(), 1);
    assert_eq!(consumed, bytes.len() as u64);
    match &payloads[0] {
        AppendedPayload::Message { room, line, .. } => {
            assert_eq!(room, "general");
            assert_eq!(line.text, "hi");
        }
        other => panic!("unexpected payload: {other:?}"),
    }
}

#[test]
fn offset_advances_correctly_across_two_consecutive_lines() {
    // This is the live-smoke regression: two messages back-to-back. The
    // old offset-leak bug would advance `consumed_to` past the second
    // line on the first iteration, so a follow-up tail read at that
    // offset would silently miss the second line.
    let one = line(
        r#"{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","sender_machine":"m","text":"first","source":"internal"}"#,
    );
    let two = line(
        r#"{"type":"message","ts":"2026-05-09T17:00:01Z","id":"01J2","sender":"@a","sender_machine":"m","text":"second","source":"internal"}"#,
    );
    let bytes = cat(&[&one, &two]);

    let (payloads, consumed) = parse_new_lines(&bytes, 0, "r");
    assert_eq!(payloads.len(), 2, "both lines should parse");
    assert_eq!(
        consumed,
        bytes.len() as u64,
        "consumed_to must equal total length, not overshoot"
    );

    // Now simulate a tail read after the first line — second line must
    // still be discovered.
    let (payloads_tail, _consumed_tail) = parse_new_lines(&bytes, one.len() as u64, "r");
    assert_eq!(payloads_tail.len(), 1);
    match &payloads_tail[0] {
        AppendedPayload::Message { line, .. } => assert_eq!(line.text, "second"),
        other => panic!("unexpected payload: {other:?}"),
    }
}

#[test]
fn partial_trailing_line_stays_uncommitted() {
    // First line complete, second line missing its newline. The
    // partial line MUST stay uncommitted so the next watcher tick can
    // re-read it once chat-core finishes the write.
    let complete = line(
        r#"{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","sender_machine":"m","text":"complete","source":"internal"}"#,
    );
    let partial = br#"{"type":"message","ts":"2026-05-09T17:00:01Z","id":"01J2","sender":"@a","sender_machine":"m","text":"incomp"#.to_vec();
    let bytes = cat(&[&complete, &partial]);

    let (payloads, consumed) = parse_new_lines(&bytes, 0, "r");
    assert_eq!(payloads.len(), 1, "only the complete line should parse");
    assert_eq!(
        consumed,
        complete.len() as u64,
        "offset should sit at first line's end, NOT past the partial trailer"
    );
    assert!(consumed < bytes.len() as u64);
}

#[test]
fn malformed_line_is_skipped_no_crash() {
    let bad = b"{not valid json}\n".to_vec();
    let good = line(
        r#"{"type":"message","ts":"2026-05-09T17:00:00Z","id":"01J1","sender":"@a","sender_machine":"m","text":"after-bad","source":"internal"}"#,
    );
    let bytes = cat(&[&bad, &good]);

    let (payloads, consumed) = parse_new_lines(&bytes, 0, "r");
    assert_eq!(payloads.len(), 1, "bad line skipped, good line kept");
    assert_eq!(consumed, bytes.len() as u64);
    match &payloads[0] {
        AppendedPayload::Message { line, .. } => assert_eq!(line.text, "after-bad"),
        other => panic!("unexpected payload: {other:?}"),
    }
}

#[test]
fn member_joined_event_round_trips() {
    let bytes = line(
        r#"{"type":"member.joined","ts":"2026-05-09T17:00:00Z","handle":"@bob","machine":"drew-mac","tmux_session":"card-bob"}"#,
    );
    let (payloads, _) = parse_new_lines(&bytes, 0, "general");
    assert_eq!(payloads.len(), 1);
    match &payloads[0] {
        AppendedPayload::MemberJoined { room, line } => {
            assert_eq!(room, "general");
            assert_eq!(line.handle, "@bob");
            assert_eq!(line.tmux_session, "card-bob");
        }
        other => panic!("unexpected payload: {other:?}"),
    }
}

#[test]
fn empty_chunk_returns_no_payloads_and_unchanged_offset() {
    let (payloads, consumed) = parse_new_lines(b"", 0, "r");
    assert!(payloads.is_empty());
    assert_eq!(consumed, 0);
}

#[test]
fn last_greater_than_bytes_len_returns_safely() {
    // Defensive: should not panic if the watcher gets called with a
    // stale offset (file rotation race). emit_new_lines() catches this
    // separately, but parse_new_lines must not panic either.
    let bytes = b"some content\n".to_vec();
    let (payloads, consumed) = parse_new_lines(&bytes, 9999, "r");
    assert!(payloads.is_empty());
    assert_eq!(consumed, 9999);
}
