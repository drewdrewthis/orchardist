//! Unit tests for `tmux_fanout`'s skip logic.
//!
//! Tests that don't require a live tmux session — just the predicate paths
//! (skip-sender, skip-empty, format-paste). Real send-keys/capture-pane
//! verification is in `two_session_chat.rs`.

use chat_core::{FanoutOutcome, Recipient, tmux_fanout};

#[test]
fn skips_sender_self() {
    let outcomes = tmux_fanout(&[Recipient::new("@alice", "alice")], "@alice", "hello");
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Skipped { recipient, reason } => {
            assert_eq!(recipient, "@alice");
            assert_eq!(reason, "sender");
        }
        other => panic!("expected Skipped::sender, got {other:?}"),
    }
}

#[test]
fn skips_sender_when_at_prefix_matches() {
    // `@alice` and `alice` are the same handle — sigil is decoration.
    let outcomes = tmux_fanout(&[Recipient::new("@alice", "alice")], "alice", "hello");
    assert_eq!(outcomes.len(), 1);
    assert!(matches!(&outcomes[0], FanoutOutcome::Skipped { reason, .. } if reason == "sender"));
}

#[test]
fn skips_empty_tmux_session() {
    let outcomes = tmux_fanout(&[Recipient::new("@bob", "")], "@alice", "hello");
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Skipped { reason, .. } => assert_eq!(reason, "empty tmux session"),
        other => panic!("expected Skipped::empty, got {other:?}"),
    }
}

#[test]
fn fails_offline_recipient() {
    // Pick a session name unlikely to exist.
    let bogus = "this-session-must-not-exist-1234567890";
    let outcomes = tmux_fanout(
        &[Recipient::new(format!("@{bogus}"), bogus)],
        "@alice",
        "hi",
    );
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Failed { error, .. } => {
            assert!(error.starts_with("no such tmux session"), "got: {error}");
        }
        other => panic!("expected Failed for offline session, got {other:?}"),
    }
}

#[test]
fn handle_and_session_can_diverge() {
    // The bug from live testing: handle `@orchardist_spawn2` (slugified)
    // must still route to actual tmux session `orchardist-spawn2`
    // (dashes preserved). The Recipient struct keeps these distinct.
    let bogus = "this-also-does-not-exist-rs-spawn2";
    let outcomes = tmux_fanout(
        &[Recipient::new("@this_also_does_not_exist_rs_spawn2", bogus)],
        "@alice",
        "hi",
    );
    assert_eq!(outcomes.len(), 1);
    match &outcomes[0] {
        FanoutOutcome::Failed { recipient, error } => {
            // Recipient field carries the human-facing handle…
            assert_eq!(recipient, "@this_also_does_not_exist_rs_spawn2");
            // …but the error message names the actual session that was looked up.
            assert!(error.contains(bogus), "error should name session: {error}");
        }
        other => panic!("expected Failed, got {other:?}"),
    }
}
