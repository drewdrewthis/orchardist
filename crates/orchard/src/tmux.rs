//! Tmux session management for Orchard.
//!
//! Lists active sessions, resolves pane titles, creates new sessions for
//! worktrees, switches the current client to a session, and orchestrates the
//! full "switch to worktree" flow including PR-aware notification banners.
use std::process::{Command, Output};

use anyhow::{Context, Result};

use crate::logger::LOG;
use crate::types::{SwitchToSessionOptions, TmuxSession};

/// Finds the tmux session associated with a worktree.
/// Matches by session path first, then by session name using several fallback patterns.
pub fn find_session_for_worktree<'a>(
    sessions: &'a [TmuxSession],
    path: &str,
    branch: Option<&str>,
) -> Option<&'a TmuxSession> {
    // Highest priority: exact session path match.
    if let Some(s) = sessions.iter().find(|s| s.path == path) {
        return Some(s);
    }

    let dir_name = std::path::Path::new(path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("");

    let branch_slug = branch.map(|b| b.replace('/', "-"));

    for s in sessions {
        let name_suffix = {
            let underscore = s.name.rfind('_');
            let colon = s.name.rfind(':');
            match (underscore, colon) {
                (None, None) => s.name.as_str(),
                (Some(u), None) => &s.name[u + 1..],
                (None, Some(c)) => &s.name[c + 1..],
                (Some(u), Some(c)) => &s.name[u.max(c) + 1..],
            }
        };

        if s.name == dir_name || name_suffix == dir_name {
            return Some(s);
        }
        if let Some(b) = branch
            && s.name == b
        {
            return Some(s);
        }
        if let Some(slug) = &branch_slug
            && (s.name == slug.as_str() || name_suffix == slug.as_str())
        {
            return Some(s);
        }
    }

    None
}

/// Replaces dots with underscores in a repo name to avoid tmux target-session
/// parsing issues (`.` is a window/pane separator in tmux).
pub fn sanitize_repo_name(name: &str) -> String {
    name.replace('.', "_")
}

/// Replaces slashes with dashes in a branch name so it is valid as part of a
/// tmux session name (slashes are path separators in tmux target syntax).
fn sanitize_branch(branch: &str) -> String {
    branch.replace('/', "-")
}

/// Derives the main session name for the worktree origin.
/// Format: `{sanitized_repo_name}_{branch}`. Uses "HEAD" when detached.
pub fn derive_main_session_name(origin_path: &str, branch: Option<&str>) -> String {
    let repo_name = std::path::Path::new(origin_path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("orchard");
    let sanitized = sanitize_repo_name(repo_name);
    let branch_part = branch
        .map(sanitize_branch)
        .unwrap_or_else(|| "HEAD".to_string());
    format!("{sanitized}_{branch_part}")
}

/// Creates a new detached tmux session with the given name at the given directory.
pub fn new_detached_session(name: &str, start_dir: &str) -> Result<()> {
    let expanded_dir = shellexpand::tilde(start_dir);
    let output = Command::new("tmux")
        .args(["new-session", "-d", "-s", name, "-c", expanded_dir.as_ref()])
        .output()
        .context("tmux new-session")?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(anyhow::anyhow!(
            "tmux new-session failed: {}",
            stderr.trim()
        ));
    }

    LOG.info(&format!("newDetachedSession: {} at {}", name, start_dir));
    Ok(())
}

/// Creates a detached tmux session that runs a specific command.
///
/// Unlike `new_detached_session` (which opens a shell), this creates a session
/// whose initial window runs the given command. Used for standalone sessions.
pub fn new_session_with_command(name: &str, start_dir: &str, command: &str) -> Result<()> {
    let expanded_dir = shellexpand::tilde(start_dir);
    let output = Command::new("tmux")
        .args([
            "new-session",
            "-d",
            "-s",
            name,
            "-c",
            expanded_dir.as_ref(),
            command,
        ])
        .output()
        .context("tmux new-session with command")?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        return Err(anyhow::anyhow!(
            "tmux new-session failed for '{}': {}",
            name,
            stderr.trim()
        ));
    }

    LOG.info(&format!(
        "newSessionWithCommand: {} at {} running '{}'",
        name, start_dir, command
    ));
    Ok(())
}

/// Checks if a tmux session with the given name exists.
pub fn session_exists(name: &str) -> bool {
    Command::new("tmux")
        .args(["has-session", "-t", name])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Kills the tmux session with the given name.
pub fn kill_tmux_session(name: &str) -> Result<()> {
    Command::new("tmux")
        .args(["kill-session", "-t", name])
        .status()
        .context("tmux kill-session")?;
    LOG.info(&format!("killTmuxSession: {}", name));
    Ok(())
}

/// Captures the pane content of a tmux session, returning the last `lines` lines.
///
/// When `pane_index` is `Some(n)`, captures from `session.n` (a specific pane).
/// When `None`, captures from the default pane (pane 0).
pub fn capture_pane_content(session: &str, lines: u32) -> Result<String> {
    capture_pane_content_at(session, None, lines)
}

/// Captures pane content with an explicit pane target.
///
/// The tmux target is `session` when `pane_target` is `None`, or
/// `session:{target}` when `pane_target` is `Some(target)` (e.g., "0.1").
pub fn capture_pane_content_at(
    session: &str,
    pane_target: Option<&str>,
    lines: u32,
) -> Result<String> {
    let target = match pane_target {
        Some(t) => format!("{}:{}", session, t),
        None => session.to_string(),
    };
    let lines_arg = format!("-{lines}");
    let out = Command::new("tmux")
        .args(["capture-pane", "-t", &target, "-p", "-J", "-S", &lines_arg])
        .output()
        .context("tmux capture-pane")?;

    let text = String::from_utf8_lossy(&out.stdout);
    Ok(text.trim_end_matches('\n').to_string())
}

/// Selects a specific pane within a tmux session.
///
/// Switches to a specific window within a tmux session.
///
/// Runs `tmux select-window -t session:window_index` to switch
/// the session's active window.
pub fn select_window(session: &str, window_index: usize) -> Result<()> {
    let target = format!("{}:{}", session, window_index);
    let out = Command::new("tmux")
        .args(["select-window", "-t", &target])
        .output()
        .context("tmux select-window")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("tmux select-window failed: {}", stderr.trim());
    }
    Ok(())
}

/// Runs `tmux select-pane -t session:{pane_target}` where `pane_target` is
/// a window.pane address like "0.1" (window 0, pane 1).
pub fn select_pane(session: &str, pane_target: &str) -> Result<()> {
    let target = format!("{}:{}", session, pane_target);
    let out = Command::new("tmux")
        .args(["select-pane", "-t", &target])
        .output()
        .context("tmux select-pane")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("tmux select-pane failed: {}", stderr.trim());
    }
    Ok(())
}

/// Zooms (maximises) a specific pane within a tmux session.
///
/// Checks `#{window_zoomed_flag}` first and skips the zoom if the pane's
/// window is already zoomed, making this call idempotent. Typically called
/// after [`select_pane`] so the user lands in a focused, full-screen view.
pub fn zoom_pane(session: &str, pane_target: &str) -> Result<()> {
    let target = format!("{}:{}", session, pane_target);

    // Check if the window is already zoomed to avoid toggling it off.
    let check = Command::new("tmux")
        .args([
            "display-message",
            "-t",
            &target,
            "-p",
            "#{window_zoomed_flag}",
        ])
        .output()
        .context("tmux display-message (zoom check)")?;

    if check.status.success() {
        let flag = String::from_utf8_lossy(&check.stdout);
        if flag.trim() == "1" {
            return Ok(());
        }
    }

    let out = Command::new("tmux")
        .args(["resize-pane", "-Z", "-t", &target])
        .output()
        .context("tmux resize-pane -Z")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("tmux resize-pane -Z failed: {}", stderr.trim());
    }
    Ok(())
}

/// Builds a tmux target string: `session` or `session.N`.
pub fn build_pane_target(session: &str, pane_index: Option<usize>) -> String {
    match pane_index {
        Some(n) => format!("{}.{}", session, n),
        None => session.to_string(),
    }
}

/// Derives the tmux session name in the format "repoName_branch".
/// Slashes in the branch name are replaced with dashes.
/// Falls back to the last path segment, then "orchard".
pub fn derive_session_name(repo_name: &str, branch: Option<&str>, worktree_path: &str) -> String {
    let suffix = match branch {
        Some(b) => sanitize_branch(b),
        None => {
            let base = std::path::Path::new(worktree_path)
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or("");
            if base.is_empty() || base == "." {
                "orchard".to_string()
            } else {
                base.to_string()
            }
        }
    };
    format!("{repo_name}_{suffix}")
}

/// Creates the tmux session for the given worktree if it does not already exist.
/// Does NOT switch the client.
pub fn create_session(opts: &SwitchToSessionOptions) -> Result<()> {
    let exists = Command::new("tmux")
        .args(["has-session", "-t", &opts.session_name])
        .status()
        .map(|s| s.success())
        .unwrap_or(false);

    if !exists {
        Command::new("tmux")
            .args([
                "new-session",
                "-d",
                "-s",
                &opts.session_name,
                "-c",
                &opts.worktree_path,
            ])
            .status()
            .with_context(|| format!("creating session {}", opts.session_name))?;
    }

    LOG.info(&format!(
        "createSession: {} ({})",
        opts.session_name,
        if exists { "existing" } else { "new" }
    ));

    Ok(())
}

// ---------------------------------------------------------------------------
// Current-session detection
// ---------------------------------------------------------------------------

/// Inner implementation, accepting the `$TMUX` env value and an executor
/// closure. Extracted for hermetic unit testing without env-var manipulation.
///
/// `tmux_var` is the value of `$TMUX` (pass `None` to simulate "not inside tmux").
/// `exec` is called to run `tmux display-message -p '#S'`; it must return an
/// `std::io::Result<Output>`.
pub(crate) fn current_session_name_inner(
    tmux_var: Option<&str>,
    exec: impl Fn() -> std::io::Result<Output>,
) -> Option<String> {
    // Not inside tmux — $TMUX is unset.
    tmux_var?;

    let output = exec().ok()?;
    if !output.status.success() {
        return None;
    }
    let name = String::from_utf8_lossy(&output.stdout);
    let trimmed = name.trim().to_string();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed)
    }
}

/// Returns the name of the tmux session this process is running inside, if any.
///
/// Returns `None` when not inside tmux (`$TMUX` env var unset) or when
/// `tmux display-message -p '#S'` fails (e.g. the tmux server is gone).
/// The returned string is trimmed of trailing whitespace/newlines.
pub fn current_session_name() -> Option<String> {
    let tmux_var = std::env::var("TMUX").ok();
    current_session_name_inner(tmux_var.as_deref(), || {
        Command::new("tmux")
            .args(["display-message", "-p", "#S"])
            .output()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_sessions() -> Vec<TmuxSession> {
        vec![
            TmuxSession {
                name: "other_main".into(),
                path: "/other/path".into(),
                attached: false,
                pane_title: None,
                active_pane_cwd: None,
            },
            TmuxSession {
                name: "myrepo_feature-x".into(),
                path: "/home/user/myrepo-feature-x".into(),
                attached: false,
                pane_title: None,
                active_pane_cwd: None,
            },
            TmuxSession {
                name: "orchard".into(),
                path: "/home/user/orchard".into(),
                attached: true,
                pane_title: None,
                active_pane_cwd: None,
            },
        ]
    }

    #[test]
    fn find_session_matches_by_path() {
        let sessions = make_sessions();
        let result = find_session_for_worktree(&sessions, "/home/user/myrepo-feature-x", None);
        assert!(result.is_some());
        assert_eq!(result.unwrap().name, "myrepo_feature-x");
    }

    #[test]
    fn find_session_matches_by_branch_slug() {
        let sessions = make_sessions();
        // The session "myrepo_feature-x" suffix after "_" is "feature-x"
        let result = find_session_for_worktree(&sessions, "/no/match", Some("feature/x"));
        assert!(result.is_some());
        assert_eq!(result.unwrap().name, "myrepo_feature-x");
    }

    #[test]
    fn find_session_returns_none_when_no_match() {
        let sessions = make_sessions();
        let result = find_session_for_worktree(&sessions, "/no/match", Some("no-match-branch"));
        assert!(result.is_none());
    }

    #[test]
    fn find_session_matches_by_dir_name() {
        let sessions = make_sessions();
        // "orchard" session name == dir name of path "/home/user/orchard"
        let result = find_session_for_worktree(&sessions, "/different/path/orchard", None);
        assert!(result.is_some());
        assert_eq!(result.unwrap().name, "orchard");
    }

    #[test]
    fn derive_session_name_with_branch() {
        assert_eq!(
            derive_session_name("myrepo", Some("main"), "/any"),
            "myrepo_main"
        );
    }

    #[test]
    fn derive_session_name_replaces_slashes() {
        assert_eq!(
            derive_session_name("myrepo", Some("feature/my-work"), "/any"),
            "myrepo_feature-my-work"
        );
    }

    #[test]
    fn derive_session_name_uses_path_when_no_branch() {
        assert_eq!(
            derive_session_name("myrepo", None, "/home/user/my-worktree"),
            "myrepo_my-worktree"
        );
    }

    #[test]
    fn derive_session_name_fallback_to_orchard() {
        assert_eq!(derive_session_name("myrepo", None, "/"), "myrepo_orchard");
    }

    // -----------------------------------------------------------------------
    // sanitize_repo_name
    // -----------------------------------------------------------------------

    #[test]
    fn sanitize_repo_name_replaces_dots_with_underscores() {
        assert_eq!(sanitize_repo_name("my.repo-v2"), "my_repo-v2");
    }

    #[test]
    fn sanitize_repo_name_preserves_names_without_dots() {
        assert_eq!(sanitize_repo_name("myrepo"), "myrepo");
    }

    #[test]
    fn sanitize_repo_name_replaces_multiple_dots() {
        assert_eq!(sanitize_repo_name("a.b.c"), "a_b_c");
    }

    // -----------------------------------------------------------------------
    // derive_main_session_name
    // -----------------------------------------------------------------------

    #[test]
    fn derive_main_session_name_with_branch() {
        assert_eq!(
            derive_main_session_name("/home/user/myrepo", Some("main")),
            "myrepo_main"
        );
    }

    #[test]
    fn derive_main_session_name_uses_head_when_detached() {
        assert_eq!(
            derive_main_session_name("/home/user/myrepo", None),
            "myrepo_HEAD"
        );
    }

    #[test]
    fn derive_main_session_name_sanitizes_dots() {
        assert_eq!(
            derive_main_session_name("/home/user/my.repo-v2", Some("main")),
            "my_repo-v2_main"
        );
    }

    #[test]
    fn derive_main_session_name_with_non_main_branch() {
        assert_eq!(
            derive_main_session_name("/home/user/myrepo", Some("develop")),
            "myrepo_develop"
        );
    }

    #[test]
    fn derive_main_session_name_derives_repo_from_path() {
        assert_eq!(
            derive_main_session_name("/home/user/my-project", Some("main")),
            "my-project_main"
        );
    }

    #[test]
    fn derive_main_session_name_sanitizes_branch_slashes() {
        assert_eq!(
            derive_main_session_name("/home/user/myrepo", Some("feature/login")),
            "myrepo_feature-login"
        );
    }

    #[test]
    fn build_pane_target_without_index() {
        assert_eq!(build_pane_target("my-session", None), "my-session");
    }

    #[test]
    fn build_pane_target_with_index_zero() {
        assert_eq!(build_pane_target("my-session", Some(0)), "my-session.0");
    }

    #[test]
    fn build_pane_target_with_index_two() {
        assert_eq!(build_pane_target("my-session", Some(2)), "my-session.2");
    }
    #[test]
    fn zoom_pane_returns_error_for_nonexistent_session() {
        // zoom_pane calls `tmux resize-pane -Z`; when the target session does
        // not exist tmux exits non-zero, so the function must return Err.
        let result = zoom_pane("nonexistent-session-xyz", "0.0");
        assert!(result.is_err());
    }

    // -----------------------------------------------------------------------
    // current_session_name
    // -----------------------------------------------------------------------

    #[test]
    fn current_session_name_returns_none_when_tmux_env_unset() {
        // Pass None for tmux_var to simulate $TMUX being unset.
        // The exec closure must never be called in this case.
        let result = current_session_name_inner(None, || {
            panic!("exec should not be called when TMUX is unset");
        });
        assert_eq!(result, None);
    }

    #[test]
    fn current_session_name_returns_some_when_inside_tmux() {
        // This test is only meaningful when actually running inside tmux.
        if std::env::var("TMUX").is_err() {
            return;
        }
        let name = current_session_name();
        assert!(name.is_some(), "expected Some(_) when inside tmux");
        let name = name.unwrap();
        assert!(!name.is_empty(), "session name must not be empty");
        assert!(
            !name.contains('\n'),
            "session name must not contain newlines"
        );
    }
}
