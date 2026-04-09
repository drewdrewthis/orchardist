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
    /// A monitored metric crossed its configured threshold.
    Threshold {
        /// Worktree path.
        worktree: String,
        /// tmux session name.
        session: String,
        /// Metric name (e.g. "context_window_pct", "cost_usd").
        metric: String,
        /// Current metric value.
        value: f64,
        /// Configured threshold that was exceeded.
        threshold: f64,
    },
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
}
