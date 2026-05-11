//! Integration test for the GUI's chat command bridges.
//!
//! Exercises the Rust-side path the Tauri commands expose:
//!   - chat-core append → JSONL on disk
//!   - chat-core read → list_rooms / read_history / list_members
//! against an isolated $ORCHARD_CHAT_DIR. The full Tauri runtime is
//! not booted (Tauri command tests would need a webview); we test the
//! library calls the commands wrap, which is where the actual logic
//! lives.

use std::sync::Mutex;

use chat_core::types::Target;
use chat_core::{append_message, list_members, list_rooms, read_history, tmux_fanout};

// chat_dir() reads $ORCHARD_CHAT_DIR; we serialize tests to keep the
// env var stable across them.
static ENV_LOCK: Mutex<()> = Mutex::new(());

fn with_chat_dir<F: FnOnce()>(f: F) {
    let _guard = ENV_LOCK.lock().unwrap();
    let temp = tempfile::tempdir().expect("temp dir");
    // Set both ORCHARD_CHAT_DIR and HOME so neither path can leak to
    // the user's real ~/.orchard/chat.
    std::env::set_var("ORCHARD_CHAT_DIR", temp.path());
    std::env::set_var("HOME", temp.path());
    f();
}

#[test]
fn chat_send_appends_jsonl_and_history_readback() {
    with_chat_dir(|| {
        let target = Target::parse("#alpha").unwrap();
        let room = target.room_name();
        let id = append_message(&room, "@gui-tester", "hello from gui").expect("append");
        assert!(!id.is_empty(), "message id should be non-empty");

        let history = read_history(&room, 0).expect("history");
        assert_eq!(history.len(), 1);
        assert_eq!(history[0].text, "hello from gui");
        assert_eq!(history[0].sender, "@gui-tester");
        assert_eq!(history[0].source, "internal");
    });
}

#[test]
fn chat_list_rooms_after_two_appends() {
    with_chat_dir(|| {
        append_message("alpha", "@a", "hi").unwrap();
        append_message("beta", "@b", "yo").unwrap();
        let mut rooms = list_rooms().unwrap();
        rooms.sort();
        assert_eq!(rooms, vec!["alpha".to_string(), "beta".to_string()]);
    });
}

#[test]
fn chat_room_with_no_members_returns_empty() {
    with_chat_dir(|| {
        append_message("solo", "@a", "talking to myself").unwrap();
        let members = list_members("solo").unwrap();
        assert!(members.is_empty(), "no member.joined yet → empty members");
    });
}

#[test]
fn chat_fanout_to_unknown_recipient_reports_failure_not_panic() {
    with_chat_dir(|| {
        let recipients = vec!["@nonexistent-session-12345".to_string()];
        let outcomes = tmux_fanout(&recipients, "@sender", "test text");
        assert_eq!(outcomes.len(), 1);
        // Either Failed (no such session) or ByteOnly (verify timed out).
        // Both are acceptable — a panic would not be.
        match &outcomes[0] {
            chat_core::FanoutOutcome::Failed { recipient, .. }
            | chat_core::FanoutOutcome::ByteOnly { recipient, .. }
            | chat_core::FanoutOutcome::Skipped { recipient, .. } => {
                assert_eq!(recipient, "@nonexistent-session-12345");
            }
            chat_core::FanoutOutcome::Delivered { .. } => {
                panic!("unexpected delivery to nonexistent session");
            }
        }
    });
}
