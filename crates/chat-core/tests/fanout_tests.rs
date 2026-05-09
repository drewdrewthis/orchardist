//! Unit tests for `tmux_fanout`'s skip logic.
//!
//! Tests that don't require a live tmux session — just the predicate paths
//! (skip-sender, skip-empty, format-paste). Real send-keys/capture-pane
//! verification is in `two_session_chat.rs`.

use chat_core::{FanoutOutcome, tmux_fanout};

#[test]
fn skips_sender_self() {
    let outcomes = tmux_fanout(&["alice".to_string()], "@alice", "hello");
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Skipped { recipient, reason } => {
            assert_eq!(recipient, "alice");
            assert_eq!(reason, "sender");
        }
        other => panic!("expected Skipped::sender, got {other:?}"),
    }
}

#[test]
fn skips_sender_when_at_prefix_matches() {
    // `@alice` and `alice` are the same handle — sigil is decoration.
    let outcomes = tmux_fanout(&["@alice".to_string()], "alice", "hello");
    assert_eq!(outcomes.len(), 1);
    assert!(matches!(&outcomes[0], FanoutOutcome::Skipped { reason, .. } if reason == "sender"));
}

#[test]
fn skips_empty_handle() {
    let outcomes = tmux_fanout(&["@".to_string()], "@bob", "hello");
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Skipped { reason, .. } => assert_eq!(reason, "empty handle"),
        other => panic!("expected Skipped::empty, got {other:?}"),
    }
}

#[test]
fn fails_offline_recipient() {
    // Pick a session name unlikely to exist.
    let bogus = "this-session-must-not-exist-1234567890";
    let outcomes = tmux_fanout(&[bogus.to_string()], "@alice", "hi");
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Failed { error, .. } => {
            assert_eq!(error, "no such tmux session");
        }
        other => panic!("expected Failed for offline session, got {other:?}"),
    }
}
