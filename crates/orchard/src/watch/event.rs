//! Watch event types emitted by the diff engine.
//!
//! `WatchEvent` wraps an `EventKind` with a UTC timestamp. Events are serialized
//! as JSON for delivery to subscribers via tmux `send-keys`.

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// A single watch event with a UTC timestamp.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WatchEvent {
    /// UTC timestamp when this event was created.
    pub ts: DateTime<Utc>,
    /// The kind of state transition or notification.
    pub kind: EventKind,
}

impl WatchEvent {
    /// Creates a new `WatchEvent` timestamped to now.
    pub fn now(kind: EventKind) -> Self {
        WatchEvent {
            ts: Utc::now(),
            kind,
        }
    }
}

/// The kind of event detected by the watch system.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum EventKind {
    /// Claude is paused waiting for user input in a worktree session.
    ClaudeNeedsInput {
        /// Worktree path.
        worktree: String,
        /// tmux session name.
        session: String,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// Claude completed its turn in a worktree session.
    ClaudeFinished {
        /// Worktree path.
        worktree: String,
        /// tmux session name.
        session: String,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// Claude started working in a worktree session.
    ClaudeStarted {
        /// Worktree path.
        worktree: String,
        /// tmux session name.
        session: String,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// CI checks transitioned to failing for a PR.
    CiFailed {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number.
        pr_number: u32,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// CI checks transitioned to passing for a PR.
    CiPassed {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number.
        pr_number: u32,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// New unresolved review comments appeared on a PR.
    ReviewComments {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number.
        pr_number: u32,
        /// Number of unresolved review threads.
        thread_count: u32,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// PR is approved and CI passes — ready to merge.
    PrReadyToMerge {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number.
        pr_number: u32,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// PR was merged.
    PrMerged {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number.
        pr_number: u32,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
    /// A new worktree was detected.
    WorktreeAdded {
        /// Worktree path.
        worktree: String,
        /// Git branch name.
        branch: String,
    },
    /// A worktree was removed.
    WorktreeRemoved {
        /// Worktree path.
        worktree: String,
        /// Git branch name.
        branch: String,
    },
    /// A tmux session appeared.
    SessionStarted {
        /// tmux session name.
        session: String,
        /// Associated worktree path, if any.
        worktree: Option<String>,
    },
    /// A tmux session disappeared.
    SessionDied {
        /// tmux session name.
        session: String,
        /// Associated worktree path, if any.
        worktree: Option<String>,
    },
    /// Periodic heartbeat carrying aggregate counts.
    Heartbeat {
        /// Total number of known worktrees.
        worktree_count: usize,
        /// Total number of active tmux sessions.
        session_count: usize,
    },
    /// A worktree's `PipelineStatus` transitioned from one value to another.
    ///
    /// This event is orthogonal to the existing [`ReviewComments`] count event:
    /// both may fire for the same diff, and they remain distinct — count
    /// transition vs. status transition. The `from`/`to` strings use the stable
    /// snake_case names returned by [`crate::signal::PipelineStatus::name`] so
    /// downstream scripts can parse them reliably without deserializing the enum.
    StatusChanged {
        /// Worktree path.
        worktree: String,
        /// GitHub PR number, if the transition involves a PR.
        pr_number: Option<u32>,
        /// Previous pipeline status (snake_case, from `PipelineStatus::name()`).
        from: String,
        /// New pipeline status (snake_case, from `PipelineStatus::name()`).
        to: String,
        /// Human-readable label (issue title or branch name).
        label: String,
    },
}

impl EventKind {
    /// Notification text for this event kind, if it warrants a desktop notification.
    ///
    /// Returns `(title, message, session_name)` for events that should trigger
    /// a desktop notification. Returns `None` for event kinds that don't.
    pub fn notification(&self) -> Option<(&str, String, Option<&str>)> {
        match self {
            EventKind::ClaudeNeedsInput { session, label, .. } => Some((
                "Claude needs input",
                format!("{} is waiting for you", label),
                Some(session.as_str()),
            )),
            EventKind::ClaudeFinished { session, label, .. } => {
                Some(("Claude finished", label.clone(), Some(session.as_str())))
            }
            EventKind::CiFailed {
                pr_number, label, ..
            } => Some(("CI Failed", format!("#{} {}", pr_number, label), None)),
            EventKind::ReviewComments {
                pr_number,
                thread_count,
                ..
            } => Some((
                "Review comments",
                format!("#{} has {} unresolved thread(s)", pr_number, thread_count),
                None,
            )),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn watch_event_now_sets_timestamp() {
        let event = WatchEvent::now(EventKind::Heartbeat {
            worktree_count: 3,
            session_count: 2,
        });
        // Just verify it's recent (not default/epoch).
        let age = Utc::now().signed_duration_since(event.ts);
        assert!(age.num_seconds() < 5);
    }

    #[test]
    fn event_kind_serializes_with_snake_case_type_tag() {
        let kind = EventKind::ClaudeNeedsInput {
            worktree: "/workspace/repo".to_string(),
            session: "repo_47_claude".to_string(),
            label: "Fix auth bug".to_string(),
        };
        let event = WatchEvent::now(kind);
        let json = serde_json::to_string(&event).unwrap();
        assert!(
            json.contains(r#""type":"claude_needs_input""#),
            "expected snake_case type tag in: {json}"
        );
    }

    #[test]
    fn event_kind_roundtrip_serialization() {
        let kinds = vec![
            EventKind::ClaudeNeedsInput {
                worktree: "/wt/a".to_string(),
                session: "sess_a".to_string(),
                label: "Issue 1".to_string(),
            },
            EventKind::ClaudeFinished {
                worktree: "/wt/b".to_string(),
                session: "sess_b".to_string(),
                label: "Issue 2".to_string(),
            },
            EventKind::CiFailed {
                worktree: "/wt/c".to_string(),
                pr_number: 42,
                label: "Branch".to_string(),
            },
            EventKind::Heartbeat {
                worktree_count: 5,
                session_count: 3,
            },
        ];

        for kind in kinds {
            let event = WatchEvent::now(kind);
            let json = serde_json::to_string(&event).unwrap();
            let decoded: WatchEvent = serde_json::from_str(&json)
                .unwrap_or_else(|e| panic!("failed to decode: {e}\njson: {json}"));
            // Verify the type tag round-trips by re-serializing.
            let json2 = serde_json::to_string(&decoded).unwrap();
            assert_eq!(json, json2, "round-trip mismatch");
        }
    }

    #[test]
    fn notification_returns_some_for_claude_needs_input() {
        let kind = EventKind::ClaudeNeedsInput {
            worktree: "/wt/a".to_string(),
            session: "my_session".to_string(),
            label: "Fix bug".to_string(),
        };
        let notif = kind.notification();
        assert!(notif.is_some());
        let (title, msg, session) = notif.unwrap();
        assert_eq!(title, "Claude needs input");
        assert!(msg.contains("Fix bug"));
        assert_eq!(session, Some("my_session"));
    }

    #[test]
    fn notification_returns_none_for_heartbeat() {
        let kind = EventKind::Heartbeat {
            worktree_count: 1,
            session_count: 1,
        };
        assert!(kind.notification().is_none());
    }
}
