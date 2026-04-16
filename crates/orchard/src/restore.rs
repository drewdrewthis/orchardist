//! Session restore: reconstruct dead tmux sessions from cache.
//!
//! Separates the pure classification (which cached sessions need restoring)
//! from the imperative shell (running `tmux` commands to rebuild them).
//! This follows the functional-core-imperative-shell pattern used elsewhere
//! in orchard — see docs/architecture.md.

use std::path::Path;

use crate::cache::CachedTmuxSession;

// ---------------------------------------------------------------------------
// Outcome types
// ---------------------------------------------------------------------------

/// Per-session outcome of a restore attempt.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SessionRestoreOutcome {
    /// The session was recreated in tmux.
    Restored {
        /// Number of windows created.
        windows: usize,
        /// Number of panes created (including the first pane of each window).
        panes: usize,
        /// Number of panes where `claude --continue` was sent and a `claude`
        /// process was subsequently observed within the detection timeout.
        claude_resumed: usize,
    },
    /// The session did not need restoring.
    Skipped(SkipReason),
    /// A tmux command failed partway through restore. The session may be in
    /// a partial state; restore does not attempt to clean up.
    Failed {
        /// Which step of the restore algorithm failed.
        step: RestoreStep,
        /// Error message from the failed command.
        error: String,
    },
}

/// Why a cached session was skipped for restore.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SkipReason {
    /// A live tmux session with the same name already exists.
    AlreadyRunning,
    /// The worktree path no longer exists on disk.
    WorktreeGone,
    /// The cached session is on a remote host; restore v1 skips these.
    RemoteNotSupported,
}

/// Which step of the restore algorithm produced an error.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RestoreStep {
    /// `tmux new-session` failed.
    NewSession,
    /// `tmux new-window` failed.
    NewWindow,
    /// `tmux split-window` failed.
    SplitWindow,
    /// `tmux select-layout` failed.
    SelectLayout,
    /// `tmux send-keys` for a `cd` command failed.
    SendCd,
    /// `tmux send-keys` for `claude --continue` failed, OR the detection
    /// timeout expired without the pane's command becoming `claude`.
    ResumeClaude,
    /// `tmux select-window` or `tmux select-pane` failed.
    SelectActive,
}

/// Aggregated report across all cached sessions.
#[derive(Debug, Clone, Default)]
pub struct RestoreReport {
    /// (session_name, outcome) pairs, in the order they were attempted.
    pub sessions: Vec<(String, SessionRestoreOutcome)>,
}

/// A plan for restoring a single session, produced by [`restore`].
#[derive(Debug, Clone)]
pub struct RestorePlan<'a> {
    /// The cached session to restore.
    pub session: &'a CachedTmuxSession,
}

// ---------------------------------------------------------------------------
// Pure classifier
// ---------------------------------------------------------------------------

/// Classifies each cached session: produce a [`RestorePlan`] or a
/// [`SkipReason`] for sessions that should be skipped.
///
/// Pure: all IO (checking live sessions, checking worktree existence) is
/// performed by the caller and passed in as parameters.
///
/// - `live_session_names`: names returned by `tmux list-sessions`. Any
///   cached session with a matching name is skipped as `AlreadyRunning`.
/// - `worktree_exists`: closure returning true if the cached session's
///   `path` exists on disk. Cached sessions whose path does not exist are
///   skipped as `WorktreeGone`.
/// - `cached`: sessions read from the tmux cache file.
///
/// Returns `(plans, skipped)` — plans to execute and the pre-computed skip
/// outcomes the caller can fold into [`RestoreReport`].
pub fn restore<'a, F>(
    live_session_names: &[String],
    worktree_exists: F,
    cached: &'a [CachedTmuxSession],
) -> (Vec<RestorePlan<'a>>, Vec<(String, SessionRestoreOutcome)>)
where
    F: Fn(&str) -> bool,
{
    let mut plans = Vec::new();
    let mut skipped = Vec::new();

    for session in cached {
        if session.host.is_some() {
            skipped.push((
                session.name.clone(),
                SessionRestoreOutcome::Skipped(SkipReason::RemoteNotSupported),
            ));
            continue;
        }
        if live_session_names.iter().any(|n| n == &session.name) {
            skipped.push((
                session.name.clone(),
                SessionRestoreOutcome::Skipped(SkipReason::AlreadyRunning),
            ));
            continue;
        }
        if !worktree_exists(&session.path) {
            skipped.push((
                session.name.clone(),
                SessionRestoreOutcome::Skipped(SkipReason::WorktreeGone),
            ));
            continue;
        }
        plans.push(RestorePlan { session });
    }

    (plans, skipped)
}

// ---------------------------------------------------------------------------
// Imperative shell (stub — Task #6 fills in the tmux orchestration)
// ---------------------------------------------------------------------------

/// Runs the tmux commands to reconstruct a single session from cache.
///
/// Task #6 fills in the full orchestration (new-session → new-window →
/// split-window → select-layout → send-keys cd → claude --continue →
/// select-window/pane). For now this stub returns a `Failed` outcome so
/// downstream wiring (Task #7) has a concrete API to target.
pub fn restore_session(plan: &RestorePlan<'_>) -> SessionRestoreOutcome {
    // TODO(issue #190, task #6): implement tmux orchestration.
    let _ = plan;
    SessionRestoreOutcome::Failed {
        step: RestoreStep::NewSession,
        error: "restore_session not yet implemented (task #6)".to_string(),
    }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Default worktree existence check: `Path::new(path).exists()`.
pub fn worktree_exists_default(path: &str) -> bool {
    Path::new(path).exists()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;

    fn make_cached(name: &str, path: &str, host: Option<&str>) -> CachedTmuxSession {
        CachedTmuxSession {
            name: name.to_string(),
            path: path.to_string(),
            host: host.map(String::from),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            claude_session_ids: HashMap::new(),
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    #[test]
    fn restore_skips_sessions_already_running() {
        let cached = vec![make_cached("foo", "/some/path", None)];
        let live = vec!["foo".to_string()];

        let (plans, skipped) = restore(&live, |_| true, &cached);

        assert!(plans.is_empty());
        assert_eq!(skipped.len(), 1);
        assert_eq!(skipped[0].0, "foo");
        assert_eq!(
            skipped[0].1,
            SessionRestoreOutcome::Skipped(SkipReason::AlreadyRunning)
        );
    }

    #[test]
    fn restore_skips_remote_sessions() {
        let cached = vec![make_cached("remote-session", "/some/path", Some("remote"))];
        // live_session_names is deliberately non-empty to prove it's ignored
        let live = vec!["remote-session".to_string()];

        let (plans, skipped) = restore(&live, |_| true, &cached);

        assert!(plans.is_empty());
        assert_eq!(skipped.len(), 1);
        assert_eq!(
            skipped[0].1,
            SessionRestoreOutcome::Skipped(SkipReason::RemoteNotSupported)
        );
    }

    #[test]
    fn restore_skips_when_worktree_path_gone() {
        let cached = vec![make_cached("dead-session", "/nonexistent/path", None)];

        let (plans, skipped) = restore(&[], |_| false, &cached);

        assert!(plans.is_empty());
        assert_eq!(skipped.len(), 1);
        assert_eq!(
            skipped[0].1,
            SessionRestoreOutcome::Skipped(SkipReason::WorktreeGone)
        );
    }

    #[test]
    fn restore_produces_plan_when_session_dead_and_worktree_exists() {
        let cached = vec![make_cached("my-session", "/existing/path", None)];

        let (plans, skipped) = restore(&[], |_| true, &cached);

        assert_eq!(plans.len(), 1);
        assert!(skipped.is_empty());
        assert_eq!(plans[0].session.name, "my-session");
    }

    #[test]
    fn restore_partitions_multiple_sessions() {
        let cached = vec![
            make_cached("alive", "/path/a", None),
            make_cached("remote", "/path/b", Some("box1")),
            make_cached("restorable", "/path/c", None),
        ];
        let live = vec!["alive".to_string()];

        let (plans, skipped) = restore(&live, |_| true, &cached);

        assert_eq!(plans.len(), 1);
        assert_eq!(plans[0].session.name, "restorable");

        assert_eq!(skipped.len(), 2);
        let skip_map: HashMap<&str, &SessionRestoreOutcome> =
            skipped.iter().map(|(n, o)| (n.as_str(), o)).collect();
        assert_eq!(
            skip_map["alive"],
            &SessionRestoreOutcome::Skipped(SkipReason::AlreadyRunning)
        );
        assert_eq!(
            skip_map["remote"],
            &SessionRestoreOutcome::Skipped(SkipReason::RemoteNotSupported)
        );
    }
}
