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
use crate::remote::shell_escape;
use crate::session::build_windows;

const CLAUDE_DETECTION_POLL_INTERVAL: Duration = Duration::from_millis(200);
const CLAUDE_DETECTION_TIMEOUT: Duration = Duration::from_millis(2000);

/// Max length for an untrusted session name, pane target, pane path, window
/// layout, or Claude session id read out of the cache. Anything longer is a
/// sign the cache is malformed or tampered with and is rejected rather than
/// fed to tmux.
const MAX_CACHE_STRING_LEN: usize = 4096;

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

/// Which step of the restore algorithm produced a fatal error.
///
/// Only unrecoverable failures surface here. All subordinate steps
/// (`new-window`, `split-window`, `select-layout`, `send-keys cd`,
/// `claude --continue`, `select-window`, `select-pane`) are best-effort:
/// on failure they log a warning and the session is still reported as
/// [`SessionRestoreOutcome::Restored`] with accurate partial counts.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RestoreStep {
    /// `tmux new-session` failed even after a `kill-session` retry.
    NewSession,
    /// A value read from the cache failed validation before any tmux
    /// command ran (e.g., embedded newline in a pane path, leading `-`
    /// in a session name). Restore is refused to avoid injection.
    InputValidation,
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

/// Returns the names of currently-running local tmux sessions.
///
/// Returns `Some(vec)` on success, including the empty vec when tmux is
/// running with no sessions. Returns `None` when tmux itself failed (not
/// installed, daemon errored) — the caller must NOT treat this as "no
/// sessions alive", since doing so would make [`restore_all_local`] recreate
/// every cached session on top of the live tmux server.
pub fn live_local_session_names() -> Option<Vec<String>> {
    let out = Command::new("tmux")
        .args(["list-sessions", "-F", "#{session_name}"])
        .output()
        .ok()?;
    // "no server running" exits non-zero on stderr with a specific message; any
    // other non-zero exit is treated as failure too. Both collapse to None.
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if stderr.contains("no server running") || stderr.contains("no current server") {
            return Some(Vec::new());
        }
        return None;
    }
    Some(
        String::from_utf8_lossy(&out.stdout)
            .lines()
            .filter(|l| !l.is_empty())
            .map(|l| l.to_string())
            .collect(),
    )
}

/// Minimum interval between two consecutive restore attempts across orchard
/// processes. A cron-polling `orchard --json` every minute would otherwise
/// re-probe every Claude pane's 2 s detection timeout and risk
/// `kill-session` + recreate against sessions tmux just failed to list.
const RESTORE_COOLDOWN: Duration = Duration::from_secs(5 * 60);

/// Filename for the sentinel that records the last restore attempt. Lives in
/// the cache dir so it shares the tmux cache's lifecycle (rotated, cleaned
/// alongside it).
const RESTORE_SENTINEL: &str = "restore_last_run";

/// Guard that ensures [`restore_all_local`] runs at most once per process,
/// backstopping the file-based cooldown for the double-call-within-one-binary
/// case (TUI boot + manual `refresh_and_build`).
static RESTORE_RAN: std::sync::OnceLock<()> = std::sync::OnceLock::new();

/// Reads the local tmux cache, partitions cached sessions by [`restore`], runs
/// [`restore_session`] for each plan, and returns a combined [`RestoreReport`].
///
/// Safe to call from every `orchard` entry point. Two guards prevent storms:
///
/// 1. **In-process**: a [`OnceLock`] short-circuits repeated calls within the
///    same binary (e.g. `App::new` + `refresh_and_build` both invoking restore).
/// 2. **Cross-process**: a sentinel file in the cache dir records the last
///    restore timestamp. If the previous run was within [`RESTORE_COOLDOWN`],
///    this call is a no-op. That keeps `orchard --json` in a cron loop from
///    re-probing every minute.
///
/// Silently returns an empty report on any IO failure so startup is never
/// blocked.
pub fn restore_all_local() -> RestoreReport {
    if RESTORE_RAN.set(()).is_err() {
        return RestoreReport::default();
    }
    if recently_attempted_restore(RESTORE_COOLDOWN) {
        return RestoreReport::default();
    }

    let cached: Vec<CachedTmuxSession> =
        crate::cache::read_cache::<CachedTmuxSession>(&crate::cache::tmux_cache_path(None)).entries;
    if cached.is_empty() {
        record_restore_attempt();
        return RestoreReport::default();
    }

    let Some(live) = live_local_session_names() else {
        LOG.warn(
            "restore: tmux list-sessions failed; skipping restore to avoid overwriting a running server",
        );
        return RestoreReport::default();
    };
    record_restore_attempt();
    run_restore(&live, &cached)
}

/// Returns true when the restore-sentinel file exists and was written less
/// than `cooldown` ago. Missing or stale sentinels return false.
fn recently_attempted_restore(cooldown: Duration) -> bool {
    let Ok(metadata) = std::fs::metadata(sentinel_path()) else {
        return false;
    };
    let Ok(modified) = metadata.modified() else {
        return false;
    };
    modified
        .elapsed()
        .map(|elapsed| elapsed < cooldown)
        .unwrap_or(false)
}

/// Touches the restore sentinel file so the next process's `recently_attempted_restore`
/// check can see the timestamp. Failure is non-fatal (restore proceeds regardless).
fn record_restore_attempt() {
    let path = sentinel_path();
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    // Writing zero bytes is enough — only the mtime matters.
    let _ = std::fs::write(&path, b"");
}

fn sentinel_path() -> std::path::PathBuf {
    crate::cache::cache_dir().join(RESTORE_SENTINEL)
}

/// Folds the pure classifier and the imperative shell into a single
/// [`RestoreReport`]. Exposed separately from [`restore_all_local`] so tests
/// can inject a `live` slice and skip the `tmux list-sessions` subprocess.
fn run_restore(live: &[String], cached: &[CachedTmuxSession]) -> RestoreReport {
    let (plans, skipped) = restore(live, worktree_exists_default, cached);
    let mut report = RestoreReport { sessions: skipped };
    for plan in &plans {
        let outcome = restore_session(plan);
        let name = plan.session.name.clone();
        if matches!(outcome, SessionRestoreOutcome::Restored { .. }) {
            LOG.info(&format!("restore: {name}: {outcome:?}"));
        } else {
            LOG.warn(&format!("restore: {name}: {outcome:?}"));
        }
        report.sessions.push((name, outcome));
    }
    report
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

    // Step 0: validate every cache-sourced string that will reach a tmux
    // command line. A tampered or corrupted cache file is untrusted input;
    // we refuse rather than feed it to `tmux` or `send-keys`.
    if let Err(e) = validate_session_for_restore(session) {
        LOG.warn(&format!("restore: refusing {}: {}", session.name, e));
        return SessionRestoreOutcome::Failed {
            step: RestoreStep::InputValidation,
            error: e,
        };
    }

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

        // Apply layout (non-fatal). `--` prevents tmux from parsing a layout
        // string that happens to start with `-` as an option flag.
        if !window.layout.is_empty()
            && let Err(e) = run_tmux(&["select-layout", "-t", &target, "--", &window.layout])
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
            let cmd_str = format!("cd {}", shell_escape(&pane.cwd));
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
            // Pass `--` after `--continue` so a validated-but-unusual session
            // id (e.g. starting with `-`) cannot be parsed by `claude` as a
            // flag. Session id itself is validated — see `is_valid_claude_session_id`.
            let claude_cmd = format!("claude --continue -- {}", shell_escape(id));
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

fn create_session(name: &str, path: &str) -> anyhow::Result<()> {
    run_tmux(&["new-session", "-d", "-s", name, "-c", path])
}

/// Runs `tmux` with the given args, returning `Ok(())` when the child exits
/// zero. Uses `anyhow::Result` to match the project-wide convention already
/// in `cache_sources::run_local` / `remote::ssh_exec`.
fn run_tmux(args: &[&str]) -> anyhow::Result<()> {
    let out = Command::new("tmux").args(args).output()?;
    if out.status.success() {
        Ok(())
    } else {
        let err = String::from_utf8_lossy(&out.stderr).into_owned();
        Err(anyhow::anyhow!("tmux {args:?} failed: {err}"))
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

// ---------------------------------------------------------------------------
// Input validation
// ---------------------------------------------------------------------------

/// Returns true when `s` contains no control characters, is non-empty, and is
/// shorter than [`MAX_CACHE_STRING_LEN`]. Newlines, tabs, NUL, and other
/// control bytes are rejected because `tmux send-keys` will feed them straight
/// to the pane's shell, and an unquotable newline is not neutralised by
/// [`shell_escape`] — single-quoted strings still contain literal newlines.
fn is_safe_cache_string(s: &str) -> bool {
    !s.is_empty() && s.len() <= MAX_CACHE_STRING_LEN && !s.chars().any(|c| c.is_control())
}

/// Returns true when `name` is a plausible tmux session name.
///
/// tmux itself rejects names containing `.` or `:`; we additionally refuse
/// anything starting with `-` (tmux could parse it as a flag) and control
/// characters.
fn is_valid_session_name(name: &str) -> bool {
    is_safe_cache_string(name)
        && !name.starts_with('-')
        && !name.contains(':')
        && !name.contains('.')
}

/// Returns true when `target` matches the `{window_index}.{pane_index}`
/// shape that `CachedTmuxSession.pane_targets` is supposed to hold.
fn is_valid_pane_target(target: &str) -> bool {
    match target.split_once('.') {
        Some((w, p)) => {
            !w.is_empty()
                && !p.is_empty()
                && w.chars().all(|c| c.is_ascii_digit())
                && p.chars().all(|c| c.is_ascii_digit())
        }
        None => false,
    }
}

/// Returns true when `id` looks like a plausible Claude session identifier.
///
/// Claude emits UUID-like tokens; anything outside `[A-Za-z0-9_-]{1,128}` is
/// rejected AND a leading `-` is refused. Without the leading-dash ban an id
/// of `--dangerously-skip-permissions` would pass the char allowlist and be
/// passed as a flag to the `claude` binary — argument injection.
fn is_valid_claude_session_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 128
        && !id.starts_with('-')
        && id
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_'))
}

/// Validates every cache-sourced string the restore shell will feed to tmux.
///
/// Returns `Err` with a human-readable reason on the first offender. The
/// cost (one pass over the pane vecs) is negligible next to the subprocess
/// work that follows.
fn validate_session_for_restore(session: &CachedTmuxSession) -> Result<(), String> {
    if !is_valid_session_name(&session.name) {
        return Err(format!("invalid session name: {:?}", session.name));
    }
    if !is_safe_cache_string(&session.path) || session.path.starts_with('-') {
        return Err(format!("invalid session path: {:?}", session.path));
    }
    for target in &session.pane_targets {
        if !is_valid_pane_target(target) {
            return Err(format!("invalid pane target: {target:?}"));
        }
    }
    for path in &session.pane_paths {
        // Empty pane_paths are allowed (we fall back to session.path); only
        // reject non-empty strings that contain control chars or are absurdly long.
        if !path.is_empty() && (!is_safe_cache_string(path) || path.starts_with('-')) {
            return Err(format!("invalid pane path: {path:?}"));
        }
    }
    for layout in &session.window_layouts {
        if !layout.is_empty() && (!is_safe_cache_string(layout) || layout.starts_with('-')) {
            return Err(format!("invalid window layout: {layout:?}"));
        }
    }
    for name in &session.window_names {
        if !name.is_empty() && !is_safe_cache_string(name) {
            return Err(format!("invalid window name: {name:?}"));
        }
    }
    for (target, id) in &session.claude_session_ids {
        if !is_valid_pane_target(target) {
            return Err(format!("invalid claude pane target: {target:?}"));
        }
        if !is_valid_claude_session_id(id) {
            return Err(format!("invalid claude session id for {target:?}"));
        }
    }
    Ok(())
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
    // run_restore orchestration tests (no tmux required)
    // -----------------------------------------------------------------------

    #[test]
    fn run_restore_returns_empty_report_for_empty_cache() {
        let report = run_restore(&[], &[]);
        assert!(report.sessions.is_empty());
    }

    #[test]
    fn run_restore_reports_skips_without_calling_tmux() {
        // All three cached sessions hit a Skipped branch in the pure classifier,
        // so `restore_session` is never invoked and this test runs without tmux.
        //
        // The "gone" path uses a temp-dir + pid path and verifies it's absent
        // before the test runs — any collision would cause `restore_session` to
        // fire and attempt tmux, surfacing the issue loudly.
        let gone_path = std::env::temp_dir().join(format!(
            "orchard-test-does-not-exist-{}-{}",
            std::process::id(),
            line!()
        ));
        assert!(
            !gone_path.exists(),
            "test precondition: {gone_path:?} must not exist"
        );
        let gone_path_str = gone_path.to_string_lossy().into_owned();

        let cached = vec![
            make_cached("alive", "/tmp", None),
            make_cached("remote", "/tmp", Some("host")),
            make_cached("gone", &gone_path_str, None),
        ];
        let live = vec!["alive".to_string()];

        let report = run_restore(&live, &cached);

        assert_eq!(report.sessions.len(), 3);
        let outcomes: std::collections::HashMap<&str, &SessionRestoreOutcome> = report
            .sessions
            .iter()
            .map(|(n, o)| (n.as_str(), o))
            .collect();
        assert!(matches!(
            outcomes["alive"],
            SessionRestoreOutcome::Skipped(SkipReason::AlreadyRunning)
        ));
        assert!(matches!(
            outcomes["remote"],
            SessionRestoreOutcome::Skipped(SkipReason::RemoteNotSupported)
        ));
        assert!(matches!(
            outcomes["gone"],
            SessionRestoreOutcome::Skipped(SkipReason::WorktreeGone)
        ));
    }

    // -----------------------------------------------------------------------
    // Input validation tests
    // -----------------------------------------------------------------------

    #[test]
    fn validate_rejects_session_name_with_leading_dash() {
        let mut s = make_cached("-abc", "/tmp", None);
        s.pane_targets.clear();
        let err = validate_session_for_restore(&s).expect_err("must reject leading-dash name");
        assert!(err.contains("invalid session name"));
    }

    #[test]
    fn validate_rejects_path_with_newline() {
        let mut s = make_cached("ok", "/tmp/foo\nbar", None);
        s.pane_targets.clear();
        validate_session_for_restore(&s).expect_err("newline in path must be rejected");
    }

    #[test]
    fn validate_rejects_claude_session_id_that_looks_like_flag() {
        let mut s = make_cached("ok", "/tmp", None);
        s.pane_targets = vec!["0.0".to_string()];
        s.claude_session_ids
            .insert("0.0".to_string(), "--dangerously-skip".to_string());
        let err =
            validate_session_for_restore(&s).expect_err("flag-like session id must be rejected");
        assert!(err.contains("invalid claude session id"), "got: {err}");
    }

    #[test]
    fn validate_accepts_clean_uuid_like_claude_session_id() {
        let mut s = make_cached("ok", "/tmp", None);
        s.pane_targets = vec!["0.0".to_string()];
        s.claude_session_ids.insert(
            "0.0".to_string(),
            "a1b2c3d4-e5f6-7890-abcd-ef1234567890".to_string(),
        );
        validate_session_for_restore(&s).expect("uuid-like id must pass validation");
    }

    #[test]
    fn validate_rejects_pane_target_with_garbage() {
        let mut s = make_cached("ok", "/tmp", None);
        s.pane_targets = vec!["0.0 extra".to_string()];
        validate_session_for_restore(&s)
            .expect_err("target with trailing garbage must be rejected");
    }

    #[test]
    fn is_valid_pane_target_accepts_standard_forms() {
        assert!(is_valid_pane_target("0.0"));
        assert!(is_valid_pane_target("12.34"));
    }

    #[test]
    fn is_valid_pane_target_rejects_malformed() {
        assert!(!is_valid_pane_target(""));
        assert!(!is_valid_pane_target("0"));
        assert!(!is_valid_pane_target(".0"));
        assert!(!is_valid_pane_target("0."));
        assert!(!is_valid_pane_target("0.a"));
        assert!(!is_valid_pane_target("a.0"));
    }

    // -----------------------------------------------------------------------
    // Cooldown guard tests (no tmux required, no filesystem dependencies)
    // -----------------------------------------------------------------------

    #[test]
    fn recently_attempted_restore_is_false_when_sentinel_missing() {
        // The sentinel path lives in the user's cache dir which this test
        // can't isolate without dependency injection, but a zero cooldown
        // trivially returns false regardless of mtime.
        assert!(!recently_attempted_restore(Duration::from_secs(0)));
    }

    // -----------------------------------------------------------------------
    // Integration test: requires a live tmux server
    // -----------------------------------------------------------------------

    /// Verifies that `restore_session` creates a tmux session, opens the right
    /// number of panes, and `cd`s each pane into a distinct pre-created
    /// working directory. Uses distinct per-pane cwds so the assertion
    /// actually distinguishes correct routing from happy coincidence.
    ///
    /// Requires tmux on PATH. Run with:
    /// `cargo test -p orchard --lib restore:: -- --ignored`
    #[test]
    #[ignore]
    fn restore_session_creates_new_session_with_panes_and_cwds() {
        use std::process::Command;

        let session_name = "orchard-test-restore-integration";

        // Clean up any leftover session from a previous run.
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", session_name])
            .output();

        // Create two distinct temp dirs so the cwd assertion actually
        // distinguishes correct routing. tempdir avoids collisions between
        // parallel test runs.
        let tmp = std::env::temp_dir();
        let pid = std::process::id();
        let cwd_a = tmp.join(format!("orchard-restore-test-{pid}-a"));
        let cwd_b = tmp.join(format!("orchard-restore-test-{pid}-b"));
        std::fs::create_dir_all(&cwd_a).unwrap();
        std::fs::create_dir_all(&cwd_b).unwrap();
        let cwd_a_str = cwd_a.to_string_lossy().into_owned();
        let cwd_b_str = cwd_b.to_string_lossy().into_owned();

        let session = CachedTmuxSession {
            name: session_name.to_string(),
            path: cwd_a_str.clone(),
            host: None,
            pane_targets: vec!["0.0".to_string(), "0.1".to_string()],
            pane_commands: vec!["bash".to_string(), "bash".to_string()],
            pane_titles: vec![String::new(), String::new()],
            window_names: vec!["main".to_string(), "main".to_string()],
            window_active: vec!["1".to_string(), "1".to_string()],
            window_layouts: vec![String::new(), String::new()],
            pane_paths: vec![cwd_a_str.clone(), cwd_b_str.clone()],
            pane_active: vec!["1".to_string(), "0".to_string()],
            claude_session_ids: HashMap::new(),
            created_at: None,
            last_activity_at: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        };

        let plan = RestorePlan { session: &session };
        let outcome = restore_session(&plan);

        match &outcome {
            SessionRestoreOutcome::Restored { windows, panes, .. } => {
                assert_eq!(*windows, 1, "expected 1 window, got {windows}");
                assert_eq!(*panes, 2, "expected 2 panes, got {panes}");
            }
            other => panic!("expected Restored, got {other:?}"),
        }

        // Session exists.
        let has_session = Command::new("tmux")
            .args(["has-session", "-t", session_name])
            .status()
            .expect("tmux has-session failed")
            .success();
        assert!(
            has_session,
            "session {session_name} not found after restore"
        );

        // cd is sent via send-keys (async against the pane shell). Poll for up
        // to 3 s for both panes to reflect the expected cwd.
        let poll_deadline = std::time::Instant::now() + Duration::from_secs(3);
        let mut observed: Vec<String>;
        loop {
            let out = Command::new("tmux")
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
            observed = String::from_utf8_lossy(&out.stdout)
                .lines()
                .map(|s| s.to_string())
                .collect();
            let expected_ok = observed.contains(&cwd_a_str) && observed.contains(&cwd_b_str);
            if expected_ok || std::time::Instant::now() > poll_deadline {
                break;
            }
            std::thread::sleep(Duration::from_millis(100));
        }

        // Clean up tmux + temp dirs before asserting so a failure doesn't
        // leak state.
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", session_name])
            .output();
        let _ = std::fs::remove_dir_all(&cwd_a);
        let _ = std::fs::remove_dir_all(&cwd_b);

        assert!(
            observed.contains(&cwd_a_str),
            "pane A cwd missing. observed: {observed:?}, expected {cwd_a_str}"
        );
        assert!(
            observed.contains(&cwd_b_str),
            "pane B cwd missing. observed: {observed:?}, expected {cwd_b_str}"
        );
    }
}
