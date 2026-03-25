use anyhow::Result;

use crate::services::tmux::CommandTmux;
use crate::services::TmuxService;
use crate::types::{PrInfo, SwitchToSessionOptions, TmuxSession};
use crate::types::resolve_pr_status;

// ---------------------------------------------------------------------------
// Delegating wrappers (Command-based operations)
// ---------------------------------------------------------------------------

/// Lists all active tmux sessions. Returns an empty vec when tmux is not running.
pub fn list_tmux_sessions() -> Vec<TmuxSession> {
    CommandTmux.list_sessions()
}

/// Creates a new detached tmux session with the given name at the given directory.
pub fn new_detached_session(name: &str, start_dir: &str) -> Result<()> {
    CommandTmux.new_detached_session(name, start_dir)
}

/// Kills the tmux session with the given name.
pub fn kill_tmux_session(name: &str) -> Result<()> {
    CommandTmux.kill_session(name)
}

/// Captures the pane content of a tmux session, returning the last `lines` lines.
pub fn capture_pane_content(session: &str, lines: u32) -> Result<String> {
    CommandTmux.capture_pane_content(session, lines)
}

/// Creates the tmux session for the given worktree if it does not already exist.
/// Applies orchard status bar style. Does NOT switch the client.
pub fn create_session(opts: &SwitchToSessionOptions) -> Result<()> {
    CommandTmux.create_session(opts)
}

pub(crate) const CHEATSHEET: &str =
    "#[fg=colour8]prefix: ctrl-b | o: orchard | (/): prev/next | %%: split-v | \": split-h | arrows: pane | z: zoom | x: close | d: detach";

/// Applies the orchard status bar style to a tmux session.
pub fn apply_session_style(
    name: &str,
    branch: Option<&str>,
    pr: Option<&PrInfo>,
) -> Result<()> {
    CommandTmux.apply_session_style(name, branch, pr)
}

// ---------------------------------------------------------------------------
// Pure functions (no Command calls, kept here)
// ---------------------------------------------------------------------------

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
            && s.name == b {
                return Some(s);
            }
        if let Some(slug) = &branch_slug
            && (s.name == slug.as_str() || name_suffix == slug.as_str()) {
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
    let branch_part = branch.map(sanitize_branch).unwrap_or_else(|| "HEAD".to_string());
    format!("{sanitized}_{branch_part}")
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

/// Formats the tmux status-left string for an orchard session.
pub fn format_status_left(branch: Option<&str>, pr: Option<&PrInfo>) -> String {
    let branch_label = branch.unwrap_or("detached");
    let mut parts = vec![format!(
        "#[fg=colour2,bold] {branch_label} #[fg=colour248,nobold]"
    )];

    if let Some(pr) = pr {
        let status = resolve_pr_status(pr);
        let display = status.display();
        parts.push(format!("PR#{} {} {}", pr.number, display.icon, display.label));
    }

    parts.join(" ")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::{ChecksStatus, PrInfo, ReviewDecision};

    fn make_sessions() -> Vec<TmuxSession> {
        vec![
            TmuxSession { name: "other_main".into(), path: "/other/path".into(), attached: false, pane_title: None },
            TmuxSession { name: "myrepo_feature-x".into(), path: "/home/user/myrepo-feature-x".into(), attached: false, pane_title: None },
            TmuxSession { name: "orchard".into(), path: "/home/user/orchard".into(), attached: true, pane_title: None },
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
        assert_eq!(derive_session_name("myrepo", Some("main"), "/any"), "myrepo_main");
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

    // -----------------------------------------------------------------------
    // format_status_left
    // -----------------------------------------------------------------------

    #[test]
    fn format_status_left_detached() {
        let s = format_status_left(None, None);
        assert!(s.contains("detached"));
    }

    #[test]
    fn format_status_left_with_branch() {
        let s = format_status_left(Some("main"), None);
        assert!(s.contains("main"));
        assert!(!s.contains("PR#"));
    }

    #[test]
    fn format_status_left_with_pr() {
        let pr = PrInfo {
            number: 42,
            state: "open".into(),
            title: "My PR".into(),
            url: "https://example.com".into(),
            review_decision: ReviewDecision::None,
            unresolved_threads: 0,
            checks_status: ChecksStatus::Pass,
            has_conflicts: false,
        };
        let s = format_status_left(Some("feat"), Some(&pr));
        assert!(s.contains("PR#42"));
    }
}
