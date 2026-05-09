//! Unit tests for the store layer (append, history, join/leave, fold).
//!
//! Each test creates an isolated tempdir, sets `ORCHARD_CHAT_DIR`, and runs
//! against fresh state. Tests do NOT shell out to tmux — that's covered by
//! the two_session_chat integration test.
//!
//! `serial: true` is enforced via a global mutex around env-var manipulation
//! because `std::env::set_var` is process-wide. Cargo runs tests in a single
//! process by default; one test setting the env affects another.

use std::sync::{Mutex, OnceLock};

use chat_core::{
    Event, Member, append_message, join, leave, list_members, list_rooms, read_history,
};

fn env_lock() -> &'static Mutex<()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
}

struct ChatDirGuard {
    _td: tempfile::TempDir,
    _guard: std::sync::MutexGuard<'static, ()>,
}

fn isolated_chat_dir() -> ChatDirGuard {
    let guard = env_lock().lock().unwrap_or_else(|e| e.into_inner());
    let td = tempfile::tempdir().expect("tempdir");
    // SAFETY: protected by env_lock() above; only one test mutates env at a time.
    unsafe {
        std::env::set_var("ORCHARD_CHAT_DIR", td.path());
    }
    ChatDirGuard { _td: td, _guard: guard }
}

#[test]
fn append_then_read_round_trip() {
    let _g = isolated_chat_dir();
    let id1 = append_message("general", "@alice", "hello").unwrap();
    let id2 = append_message("general", "@bob", "hi").unwrap();

    let history = read_history("general", 0).unwrap();
    assert_eq!(history.len(), 2);
    assert_eq!(history[0].id, id1);
    assert_eq!(history[0].sender, "@alice");
    assert_eq!(history[0].text, "hello");
    assert_eq!(history[1].id, id2);
    assert_eq!(history[1].sender, "@bob");
    assert!(!history[0].sender_machine.is_empty());
}

#[test]
fn read_history_filters_out_membership_events() {
    let _g = isolated_chat_dir();
    join("general", "@alice", "host", "alice").unwrap();
    append_message("general", "@alice", "msg-1").unwrap();
    leave("general", "@alice").unwrap();
    append_message("general", "@bob", "msg-2").unwrap();

    let history = read_history("general", 0).unwrap();
    assert_eq!(history.len(), 2, "membership events must not count");
    assert_eq!(history[0].text, "msg-1");
    assert_eq!(history[1].text, "msg-2");
}

#[test]
fn read_history_respects_limit() {
    let _g = isolated_chat_dir();
    for i in 0..5 {
        append_message("general", "@alice", &format!("msg-{i}")).unwrap();
    }
    let history = read_history("general", 3).unwrap();
    assert_eq!(history.len(), 3);
    assert_eq!(history[0].text, "msg-2");
    assert_eq!(history[2].text, "msg-4");
}

#[test]
fn list_members_folds_join_then_leave() {
    let _g = isolated_chat_dir();
    join("general", "@alice", "host-a", "alice").unwrap();
    join("general", "@bob", "host-b", "bob").unwrap();
    leave("general", "@alice").unwrap();

    let members = list_members("general").unwrap();
    assert_eq!(members.len(), 1);
    assert_eq!(members[0].handle, "@bob");
    assert_eq!(members[0].machine, "host-b");
    assert_eq!(members[0].tmux_session, "bob");
}

#[test]
fn list_members_handles_rejoin_after_leave() {
    let _g = isolated_chat_dir();
    join("general", "@alice", "host-a", "alice").unwrap();
    leave("general", "@alice").unwrap();
    // Re-join: handle is back, with new tmux_session
    join("general", "@alice", "host-a", "alice-2").unwrap();

    let members = list_members("general").unwrap();
    assert_eq!(members.len(), 1);
    assert_eq!(members[0].handle, "@alice");
    assert_eq!(members[0].tmux_session, "alice-2");
}

#[test]
fn list_members_returns_empty_for_missing_room() {
    let _g = isolated_chat_dir();
    let members = list_members("nonexistent").unwrap();
    assert!(members.is_empty());
}

#[test]
fn list_rooms_returns_only_jsonl_files() {
    let _g = isolated_chat_dir();
    append_message("alpha", "@a", "x").unwrap();
    append_message("beta", "@b", "y").unwrap();
    append_message("gamma", "@c", "z").unwrap();

    let mut rooms = list_rooms().unwrap();
    rooms.sort();
    assert_eq!(rooms, vec!["alpha", "beta", "gamma"]);
}

#[test]
fn jsonl_row_schema_is_typed() {
    let _g = isolated_chat_dir();
    let id = append_message("general", "@alice", "hello").unwrap();
    join("general", "@alice", "host", "alice").unwrap();
    leave("general", "@alice").unwrap();

    let path = chat_core::room_path("general").unwrap();
    let raw = std::fs::read_to_string(&path).unwrap();
    let lines: Vec<&str> = raw.lines().collect();
    assert_eq!(lines.len(), 3);

    // First line: type=message
    let m: Event = serde_json::from_str(lines[0]).unwrap();
    match m {
        Event::Message {
            id: got_id,
            ts,
            sender,
            sender_machine,
            text,
            source,
        } => {
            assert_eq!(got_id, id);
            assert!(!ts.is_empty());
            assert_eq!(sender, "@alice");
            assert!(!sender_machine.is_empty());
            assert_eq!(text, "hello");
            assert_eq!(source, "internal");
        }
        _ => panic!("expected Message"),
    }

    // Second line: type=member.joined
    let j: Event = serde_json::from_str(lines[1]).unwrap();
    match j {
        Event::MemberJoined {
            ts,
            handle,
            machine,
            tmux_session,
        } => {
            assert!(!ts.is_empty());
            assert_eq!(handle, "@alice");
            assert_eq!(machine, "host");
            assert_eq!(tmux_session, "alice");
        }
        _ => panic!("expected MemberJoined"),
    }

    // Third line: type=member.left
    let l: Event = serde_json::from_str(lines[2]).unwrap();
    match l {
        Event::MemberLeft { ts, handle } => {
            assert!(!ts.is_empty());
            assert_eq!(handle, "@alice");
        }
        _ => panic!("expected MemberLeft"),
    }
}

#[test]
fn message_id_is_ulid_shape() {
    let _g = isolated_chat_dir();
    let id = append_message("general", "@alice", "hi").unwrap();
    // ULID is 26 chars, Crockford base32 alphabet (no I/L/O/U).
    assert_eq!(id.len(), 26, "ULID should be 26 chars, got {id:?}");
    for c in id.chars() {
        assert!(
            c.is_ascii_alphanumeric() && !"ILOUilou".contains(c),
            "ULID char must be Crockford base32, got {c} in {id}"
        );
    }
}

#[test]
fn append_does_not_clobber_existing_history() {
    let _g = isolated_chat_dir();
    append_message("general", "@alice", "first").unwrap();
    append_message("general", "@bob", "second").unwrap();
    append_message("general", "@alice", "third").unwrap();

    let history = read_history("general", 0).unwrap();
    let texts: Vec<&str> = history.iter().map(|m| m.text.as_str()).collect();
    assert_eq!(texts, vec!["first", "second", "third"]);
}

#[test]
fn members_sorted_by_handle() {
    let _g = isolated_chat_dir();
    join("room", "@charlie", "h", "charlie").unwrap();
    join("room", "@alice", "h", "alice").unwrap();
    join("room", "@bob", "h", "bob").unwrap();

    let members = list_members("room").unwrap();
    let handles: Vec<&str> = members.iter().map(|m| m.handle.as_str()).collect();
    assert_eq!(handles, vec!["@alice", "@bob", "@charlie"]);
    // Anchor that Member is reachable via the public re-export.
    let _: Member = members.into_iter().next().unwrap();
}
