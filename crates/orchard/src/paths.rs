//! Path utilities for Orchard.
//!
//! `tildify` shortens absolute paths by replacing the home directory prefix
//! with `~`; `truncate_left` caps display width by eliding from the left with
//! a `…` character. `sanitize_branch_slug` and `derive_local_worktree_path`
//! produce filesystem-safe paths for worktree directories.
/// Replaces the user's home directory prefix in `path` with `~`.
/// Returns the path unchanged if it does not start with `$HOME`.
pub fn tildify(path: &str) -> String {
    let home = match dirs::home_dir() {
        Some(h) => h,
        None => return path.to_string(),
    };

    let home_str = home.to_string_lossy();
    if home_str.is_empty() {
        return path.to_string();
    }

    if path == home_str.as_ref() {
        return "~".to_string();
    }

    let prefix = format!("{}/", home_str);
    if path.starts_with(prefix.as_str()) {
        return format!("~/{}", &path[prefix.len()..]);
    }

    path.to_string()
}

/// Truncates `path` to at most `max_width` characters (Unicode codepoints).
/// If the path is longer, prepends `…` and takes the rightmost `max_width - 1` codepoints.
pub fn truncate_left(path: &str, max_width: usize) -> String {
    let runes: Vec<char> = path.chars().collect();
    if runes.len() <= max_width {
        return path.to_string();
    }
    if max_width <= 1 {
        return "…".to_string();
    }
    let tail: String = runes[runes.len() - (max_width - 1)..].iter().collect();
    format!("…{tail}")
}

// ---------------------------------------------------------------------------
// Worktree path helpers
// ---------------------------------------------------------------------------

/// Converts a branch name to a filesystem-safe slug by replacing `/` with `-`
/// and stripping non-alphanumeric characters except `.`, `-`, `_`.
pub(crate) fn sanitize_branch_slug(branch: &str) -> String {
    use std::sync::OnceLock;

    use regex::Regex;

    fn non_slug_re() -> &'static Regex {
        static RE: OnceLock<Regex> = OnceLock::new();
        RE.get_or_init(|| Regex::new(r"[^a-zA-Z0-9.\-_]").unwrap())
    }

    let replaced = branch.replace('/', "-");
    non_slug_re().replace_all(&replaced, "").into_owned()
}

/// True when `session_path` is inside `worktree_path` — either equal or a
/// descendant directory. Both paths are compared as strings; a trailing `/`
/// on `worktree_path` is ignored so `/work/repo` and `/work/repo/` match
/// identically.
///
/// Used to decide whether a tmux session's active-pane cwd belongs to a
/// given worktree row or should fall through to the standalone bucket.
pub fn session_belongs_to_worktree(session_path: &str, worktree_path: &str) -> bool {
    let wt = worktree_path.trim_end_matches('/');
    session_path == wt || session_path.starts_with(&format!("{}/", wt))
}

/// Returns the absolute conventional path for a local worktree:
/// `parent(repo_root)/worktrees/worktree-SLUG`.
pub(crate) fn derive_local_worktree_path(repo_root: &str, branch: &str) -> String {
    use std::path::Path;

    let slug = sanitize_branch_slug(branch);
    let parent = Path::new(repo_root)
        .parent()
        .unwrap_or_else(|| Path::new("."));
    let joined = parent.join("worktrees").join(format!("worktree-{}", slug));
    // Try canonicalize to resolve symlinks and get an absolute path when the
    // directory already exists. For new worktrees (the common case), the path
    // doesn't exist yet and canonicalize fails — we fall through to building
    // an absolute path manually instead.
    match joined.canonicalize() {
        Ok(abs) => abs.to_string_lossy().into_owned(),
        Err(_) => {
            if joined.is_absolute() {
                joined.to_string_lossy().into_owned()
            } else {
                std::env::current_dir()
                    .map(|cwd| cwd.join(&joined))
                    .unwrap_or(joined)
                    .to_string_lossy()
                    .into_owned()
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tildify_replaces_home_prefix() {
        let home = dirs::home_dir().unwrap();
        let path = format!("{}/projects/myrepo", home.display());
        let result = tildify(&path);
        assert_eq!(result, "~/projects/myrepo");
    }

    #[test]
    fn tildify_exact_home_dir() {
        let home = dirs::home_dir().unwrap();
        let result = tildify(&home.to_string_lossy());
        assert_eq!(result, "~");
    }

    #[test]
    fn tildify_unrelated_path_unchanged() {
        let result = tildify("/tmp/some/path");
        assert_eq!(result, "/tmp/some/path");
    }

    #[test]
    fn truncate_left_short_path_unchanged() {
        assert_eq!(truncate_left("/short", 20), "/short");
    }

    #[test]
    fn truncate_left_exact_width_unchanged() {
        assert_eq!(truncate_left("abcde", 5), "abcde");
    }

    #[test]
    fn truncate_left_over_max_width() {
        let result = truncate_left("/a/very/long/path/to/somewhere", 10);
        let chars: Vec<char> = result.chars().collect();
        assert_eq!(chars.len(), 10);
        assert_eq!(chars[0], '…');
    }

    #[test]
    fn truncate_left_max_width_one() {
        assert_eq!(truncate_left("hello", 1), "…");
    }

    #[test]
    fn truncate_left_max_width_zero() {
        assert_eq!(truncate_left("hello", 0), "…");
    }

    #[test]
    fn truncate_left_unicode() {
        // 5 multibyte codepoints
        let s = "αβγδε";
        assert_eq!(truncate_left(s, 3), "…δε");
    }

    // --- sanitize_branch_slug ---

    #[test]
    fn sanitize_replaces_slash_with_dash() {
        assert_eq!(sanitize_branch_slug("feat/my-branch"), "feat-my-branch");
    }

    #[test]
    fn sanitize_strips_special_characters() {
        assert_eq!(sanitize_branch_slug("feat/hello world!"), "feat-helloworld");
    }

    #[test]
    fn sanitize_preserves_dots_dashes_underscores() {
        assert_eq!(sanitize_branch_slug("fix/v1.2_patch"), "fix-v1.2_patch");
    }

    #[test]
    fn sanitize_plain_branch_unchanged() {
        assert_eq!(sanitize_branch_slug("main"), "main");
    }

    // --- derive_local_worktree_path ---

    #[test]
    fn local_path_uses_parent_and_slug() {
        let result = derive_local_worktree_path("/home/user/repo", "feat/my-feature");
        assert!(
            result.ends_with("worktrees/worktree-feat-my-feature"),
            "got: {}",
            result
        );
    }

    #[test]
    fn local_path_parent_segment_correct() {
        let result = derive_local_worktree_path("/srv/repos/myrepo", "fix/bug-101");
        assert!(
            result.contains("worktrees/worktree-fix-bug-101"),
            "got: {}",
            result
        );
    }

    #[test]
    fn session_belongs_exact_equality() {
        assert!(session_belongs_to_worktree("/work/repo", "/work/repo"));
    }

    #[test]
    fn session_belongs_subdirectory() {
        assert!(session_belongs_to_worktree(
            "/work/repo/src/foo",
            "/work/repo"
        ));
    }

    #[test]
    fn session_belongs_ignores_trailing_slash_on_worktree() {
        assert!(session_belongs_to_worktree("/work/repo", "/work/repo/"));
        assert!(session_belongs_to_worktree("/work/repo/src", "/work/repo/"));
    }

    #[test]
    fn session_belongs_rejects_sibling_with_shared_prefix() {
        assert!(!session_belongs_to_worktree(
            "/work/repo-feat",
            "/work/repo"
        ));
    }

    #[test]
    fn session_belongs_rejects_outside_path() {
        assert!(!session_belongs_to_worktree("/tmp/scratch", "/work/repo"));
    }
}
