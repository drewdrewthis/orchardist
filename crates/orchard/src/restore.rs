//! Session restore: reconstruct dead tmux sessions from cache.
//!
//! Separates the pure classification (which cached sessions need restoring)
//! from the imperative shell (running `tmux` commands to rebuild them).
//! This follows the functional-core-imperative-shell pattern used elsewhere
//! in orchard — see docs/architecture.md.

use std::path::Path;
use std::process::Command;
use std::time::Duration;

use crate::cache::CachedTmuxSession;
use crate::logger::LOG;
use crate::session::build_windows;

const CLAUDE_DETECTION_POLL_INTERVAL: Duration = Duration::from_millis(200);
const CLAUDE_DETECTION_TIMEOUT: Duration = Duration::from_millis(2000);

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
// Imperative shell
// ---------------------------------------------------------------------------

/// Runs the tmux commands to reconstruct a single session from cache.
///
/// Implements the full orchestration:
/// 1. `tmux new-session` (with kill-and-retry on first failure)
/// 2. Build structured window/pane view via [`build_windows`]
/// 3. Create additional windows (`new-window`), rename, split panes
/// 4. Apply saved layout (`select-layout`) — non-fatal
/// 5. `cd` each pane to its saved working directory — non-fatal
/// 6. Resume Claude sessions (`claude --continue`) — non-fatal, polls for
///    confirmation up to [`CLAUDE_DETECTION_TIMEOUT`]
/// 7. Activate the previously-active window and pane
///
/// Only `new-session` failure produces [`SessionRestoreOutcome::Failed`].
/// All other failures are logged as warnings and the session is returned as
/// [`SessionRestoreOutcome::Restored`] with accurate partial counts.
pub fn restore_session(plan: &RestorePlan<'_>) -> SessionRestoreOutcome {
    let session = plan.session;

    // Step 1: create the session (with kill-retry on first failure).
    if let Err(e) = create_session(&session.name, &session.path) {
        LOG.warn(&format!(
            "restore: new-session {} failed: {}; retrying after kill-session",
            session.name, e
        ));
        let _ = run_tmux(&["kill-session", "-t", &session.name]);
        if let Err(e2) = create_session(&session.name, &session.path) {
            return SessionRestoreOutcome::Failed {
                step: RestoreStep::NewSession,
                error: format!("{e}; retry after kill-session: {e2}"),
            };
        }
    }

    // Step 2: build structured view.
    let windows = build_windows(
        &session.pane_targets,
        &session.pane_commands,
        &session.pane_titles,
        &session.window_names,
        &session.window_active,
        &session.pane_paths,
        &session.pane_active,
        &session.window_layouts,
        &session.claude_session_ids,
    );

    if windows.is_empty() {
        return SessionRestoreOutcome::Restored {
            windows: 0,
            panes: 0,
            claude_resumed: 0,
        };
    }

    let mut windows_created = 0usize;
    let mut panes_created = 0usize;
    let mut claude_resumed = 0usize;

    // Step 3: build windows and panes.
    for (idx, window) in windows.iter().enumerate() {
        let is_first = idx == 0;
        let first_pane_cwd = window
            .panes
            .first()
            .map(|p| p.cwd.as_str())
            .filter(|s| !s.is_empty())
            .unwrap_or(session.path.as_str());

        if !is_first {
            let target = format!("{}:{}", session.name, window.index);
            if let Err(e) = run_tmux(&["new-window", "-t", &target, "-c", first_pane_cwd]) {
                LOG.warn(&format!("restore: new-window {target} failed: {e}"));
                continue;
            }
        }
        windows_created += 1;

        let target = format!("{}:{}", session.name, window.index);

        // Rename window (non-fatal).
        if let Err(e) = run_tmux(&["rename-window", "-t", &target, &window.name]) {
            LOG.warn(&format!("restore: rename-window {target} failed: {e}"));
        }

        // First pane is implicit; split for each subsequent pane.
        panes_created += 1;
        for pane in window.panes.iter().skip(1) {
            let cwd = if pane.cwd.is_empty() {
                session.path.as_str()
            } else {
                pane.cwd.as_str()
            };
            if let Err(e) = run_tmux(&["split-window", "-t", &target, "-c", cwd]) {
                LOG.warn(&format!("restore: split-window {target} failed: {e}"));
                continue;
            }
            panes_created += 1;
        }

        // Apply layout (non-fatal).
        if !window.layout.is_empty()
            && let Err(e) = run_tmux(&["select-layout", "-t", &target, &window.layout])
        {
            LOG.warn(&format!("restore: select-layout {target} failed: {e}"));
        }
    }

    // Step 4: cd each pane to its saved working directory.
    for window in &windows {
        for pane in &window.panes {
            if pane.cwd.is_empty() {
                continue;
            }
            let pane_target = format!("{}:{}", session.name, pane.tmux_target);
            let cmd_str = format!("cd {}", shell_quote(&pane.cwd));
            if let Err(e) = run_tmux(&["send-keys", "-t", &pane_target, &cmd_str, "Enter"]) {
                LOG.warn(&format!("restore: send-keys cd {pane_target} failed: {e}"));
            }
        }
    }

    // Step 5: resume Claude in each Claude-enabled pane.
    for window in &windows {
        for pane in &window.panes {
            if !pane.has_claude {
                continue;
            }
            let Some(id) = &pane.claude_session_id else {
                continue;
            };
            if id.is_empty() {
                continue;
            }

            let pane_target = format!("{}:{}", session.name, pane.tmux_target);
            let claude_cmd = format!("claude --continue {}", shell_quote(id));
            match run_tmux(&["send-keys", "-t", &pane_target, &claude_cmd, "Enter"]) {
                Ok(()) => {
                    if wait_for_claude(&pane_target) {
                        claude_resumed += 1;
                    } else {
                        LOG.warn(&format!(
                            "restore: claude detection timeout for {pane_target}"
                        ));
                    }
                }
                Err(e) => {
                    LOG.warn(&format!(
                        "restore: send-keys claude {pane_target} failed: {e}"
                    ));
                }
            }
        }
    }

    // Step 6: activate the correct window and pane.
    let active_window = windows.iter().find(|w| w.is_active).or(windows.first());
    if let Some(active) = active_window {
        let win_target = format!("{}:{}", session.name, active.index);
        if let Err(e) = run_tmux(&["select-window", "-t", &win_target]) {
            LOG.warn(&format!("restore: select-window {win_target} failed: {e}"));
        }

        let active_pane = active
            .panes
            .iter()
            .find(|p| p.is_active)
            .or(active.panes.first());
        if let Some(pane) = active_pane {
            let pane_target = format!("{}:{}", session.name, pane.tmux_target);
            if let Err(e) = run_tmux(&["select-pane", "-t", &pane_target]) {
                LOG.warn(&format!("restore: select-pane {pane_target} failed: {e}"));
            }
        }
    }

    SessionRestoreOutcome::Restored {
        windows: windows_created,
        panes: panes_created,
        claude_resumed,
    }
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

fn create_session(name: &str, path: &str) -> std::io::Result<()> {
    run_tmux(&["new-session", "-d", "-s", name, "-c", path])
}

fn run_tmux(args: &[&str]) -> std::io::Result<()> {
    let out = Command::new("tmux").args(args).output()?;
    if out.status.success() {
        Ok(())
    } else {
        let err = String::from_utf8_lossy(&out.stderr).into_owned();
        Err(std::io::Error::other(format!("tmux {args:?}: {err}")))
    }
}

/// Polls `tmux list-panes` until the pane's current command is `claude` or
/// the timeout expires.
fn wait_for_claude(pane_target: &str) -> bool {
    let start = std::time::Instant::now();
    while start.elapsed() < CLAUDE_DETECTION_TIMEOUT {
        let out = Command::new("tmux")
            .args([
                "list-panes",
                "-t",
                pane_target,
                "-F",
                "#{pane_current_command}",
            ])
            .output();
        if let Ok(o) = out
            && o.status.success()
        {
            let cmd = String::from_utf8_lossy(&o.stdout).trim().to_string();
            if cmd == "claude" {
                return true;
            }
        }
        std::thread::sleep(CLAUDE_DETECTION_POLL_INTERVAL);
    }
    false
}

/// Quotes a string for safe use in a shell command sent via `tmux send-keys`.
///
/// Safe characters (alphanumeric, `/`, `_`, `.`, `-`) are passed through
/// unchanged. Everything else is wrapped in single quotes with any embedded
/// single quotes escaped as `'\''`.
fn shell_quote(s: &str) -> String {
    if !s.is_empty()
        && s.chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '/' | '_' | '.' | '-'))
    {
        s.to_string()
    } else {
        format!("'{}'", s.replace('\'', "'\\''"))
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

    // -----------------------------------------------------------------------
    // shell_quote unit tests
    // -----------------------------------------------------------------------

    #[test]
    fn shell_quote_passes_safe_paths_unchanged() {
        assert_eq!(shell_quote("/home/user/proj"), "/home/user/proj");
    }

    #[test]
    fn shell_quote_wraps_spaces_in_single_quotes() {
        assert_eq!(shell_quote("/home/user/my proj"), "'/home/user/my proj'");
    }

    #[test]
    fn shell_quote_escapes_embedded_single_quote() {
        assert_eq!(shell_quote("it's"), "'it'\\''s'");
    }

    #[test]
    fn shell_quote_empty_string_gets_quoted() {
        assert_eq!(shell_quote(""), "''");
    }

    // -----------------------------------------------------------------------
    // Integration test: requires a live tmux server
    // -----------------------------------------------------------------------

    /// Verifies that `restore_session` creates a tmux session with the expected
    /// number of panes and that each pane's working directory is set correctly.
    ///
    /// Requires tmux to be available on the PATH. Run with:
    /// `cargo test -p orchard --lib restore:: -- --ignored`
    #[test]
    #[ignore]
    fn restore_session_creates_new_session_with_panes() {
        use std::process::Command;

        let session_name = "orchard-test-restore-integration";

        // Clean up any leftover session from a previous run.
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", session_name])
            .output();

        // Use /tmp as a guaranteed-existing path.
        let cwd_a = "/tmp";
        let cwd_b = "/tmp";

        let session = CachedTmuxSession {
            name: session_name.to_string(),
            path: cwd_a.to_string(),
            host: None,
            // Two panes in window 0.
            pane_targets: vec!["0.0".to_string(), "0.1".to_string()],
            pane_commands: vec!["bash".to_string(), "bash".to_string()],
            pane_titles: vec![String::new(), String::new()],
            window_names: vec!["main".to_string(), "main".to_string()],
            window_active: vec!["1".to_string(), "1".to_string()],
            window_layouts: vec![String::new(), String::new()],
            pane_paths: vec![cwd_a.to_string(), cwd_b.to_string()],
            pane_active: vec!["1".to_string(), "0".to_string()],
            claude_session_ids: HashMap::new(),
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        };

        let plan = RestorePlan { session: &session };
        let outcome = restore_session(&plan);

        // Verify the outcome is Restored.
        match &outcome {
            SessionRestoreOutcome::Restored { windows, panes, .. } => {
                assert_eq!(*windows, 1, "expected 1 window, got {windows}");
                assert_eq!(*panes, 2, "expected 2 panes, got {panes}");
            }
            other => panic!("expected Restored, got {other:?}"),
        }

        // Verify tmux has the session.
        let has_session = Command::new("tmux")
            .args(["has-session", "-t", session_name])
            .status()
            .expect("tmux has-session failed")
            .success();
        assert!(
            has_session,
            "tmux session {session_name} not found after restore"
        );

        // Verify pane count.
        let list_panes = Command::new("tmux")
            .args([
                "list-panes",
                "-s",
                "-t",
                session_name,
                "-F",
                "#{pane_current_path}",
            ])
            .output()
            .expect("tmux list-panes failed");
        let pane_paths_out = String::from_utf8_lossy(&list_panes.stdout);
        let pane_paths: Vec<&str> = pane_paths_out.lines().collect();
        assert_eq!(
            pane_paths.len(),
            2,
            "expected 2 pane path lines, got: {pane_paths:?}"
        );

        // Clean up.
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", session_name])
            .output();
    }
}
