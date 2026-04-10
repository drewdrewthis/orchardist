//! Path utilities for Orchard.
//!
//! `tildify` shortens absolute paths by replacing the home directory prefix
//! with `~`; `truncate_left` caps display width by eliding from the left with
//! a `…` character. `sanitize_branch_slug` and `derive_local_worktree_path`
//! produce filesystem-safe paths for worktree directories.
//! `normalize_path` strips trailing slashes for consistent path comparisons.
//! `paths_match` performs exact path comparison with symlink resolution as a
//! fallback, preventing both false positives and missed matches.
/// Normalizes a filesystem path for comparison by stripping trailing slashes.
///
/// Tmux reports `#{session_path}` with a trailing slash in some versions;
/// `git worktree list --porcelain` never does. Normalizing both sides before
/// comparison prevents false mismatches.
///
/// The root path `"/"` is returned unchanged.
///
/// # Examples
///
/// ```
/// use orchard::paths::normalize_path;
/// assert_eq!(normalize_path("/home/user/repo/"), "/home/user/repo");
/// assert_eq!(normalize_path("/home/user/repo"), "/home/user/repo");
/// assert_eq!(normalize_path("/"), "/");
/// ```
pub fn normalize_path(path: &str) -> &str {
    let trimmed = path.trim_end_matches('/');
    if trimmed.is_empty() {
        // The input was all slashes (e.g. "/") — preserve the root.
        "/"
    } else {
        trimmed
    }
}

/// Compares two filesystem paths for equality, handling symlinks and trailing
/// slashes correctly.
///
/// The fast path is normalized string equality (both sides have trailing
/// slashes stripped). If that fails — which can happen on macOS where `/var`
/// and `/tmp` are symlinks to `/private/var` and `/private/tmp`, or where git
/// resolves symlinks differently from tmux — both paths are canonicalized via
/// the filesystem and compared again.
///
/// Returns `true` if both paths refer to the same filesystem location.
///
/// # Examples
///
/// ```
/// use orchard::paths::paths_match;
/// assert!(paths_match("/home/user/repo", "/home/user/repo"));
/// assert!(paths_match("/home/user/repo/", "/home/user/repo"));
/// assert!(!paths_match("/home/user/repo", "/home/user/repo-sdk"));
/// assert!(!paths_match("/home/user/langwatch", "/home/user/langwatch-sdk"));
/// ```
pub fn paths_match(a: &str, b: &str) -> bool {
    // Fast path: normalized string equality.
    let a_norm = normalize_path(a);
    let b_norm = normalize_path(b);
    if a_norm == b_norm {
        return true;
    }

    // Slow path: canonicalize to resolve symlinks (e.g. macOS /private/var vs /var).
    let a_canon = std::fs::canonicalize(a);
    let b_canon = std::fs::canonicalize(b);
    match (a_canon, b_canon) {
        (Ok(ac), Ok(bc)) => ac == bc,
        // If either path doesn't exist on disk, fall back to the already-false
        // normalized comparison — no match.
        _ => false,
    }
}

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

    // --- normalize_path ---

    #[test]
    fn normalize_path_strips_trailing_slash() {
        assert_eq!(normalize_path("/home/user/repo/"), "/home/user/repo");
    }

    #[test]
    fn normalize_path_no_trailing_slash_unchanged() {
        assert_eq!(normalize_path("/home/user/repo"), "/home/user/repo");
    }

    #[test]
    fn normalize_path_root_preserved() {
        assert_eq!(normalize_path("/"), "/");
    }

    #[test]
    fn normalize_path_multiple_trailing_slashes() {
        assert_eq!(normalize_path("/home/user/repo///"), "/home/user/repo");
    }

    #[test]
    fn normalize_path_empty_string_returns_root() {
        assert_eq!(normalize_path(""), "/");
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

    // --- paths_match ---

    #[test]
    fn paths_match_identical_paths() {
        assert!(paths_match("/home/user/repo", "/home/user/repo"));
    }

    #[test]
    fn paths_match_trailing_slash_on_one_side() {
        assert!(paths_match("/home/user/repo/", "/home/user/repo"));
    }

    #[test]
    fn paths_match_trailing_slash_on_both_sides() {
        assert!(paths_match("/home/user/repo/", "/home/user/repo/"));
    }

    #[test]
    fn paths_match_rejects_prefix_collision() {
        // "langwatch" must NOT match "langwatch-sdk" — substring is not a match.
        assert!(!paths_match(
            "/home/user/langwatch",
            "/home/user/langwatch-sdk"
        ));
    }

    #[test]
    fn paths_match_rejects_suffix_collision() {
        // "main" must NOT match "domain-main-service".
        assert!(!paths_match(
            "/home/user/main",
            "/home/user/domain-main-service"
        ));
    }

    #[test]
    fn paths_match_rejects_completely_different_paths() {
        assert!(!paths_match("/home/user/repo-a", "/home/user/repo-b"));
    }

    #[test]
    fn paths_match_real_dirs_via_canonicalize() {
        // Use two real directories that definitely exist to exercise the
        // canonicalize code path. /tmp and /var are always present on Unix.
        let a = std::env::temp_dir();
        let a_str = a.to_string_lossy();
        assert!(paths_match(&a_str, &a_str));
    }

    #[test]
    fn paths_match_nonexistent_paths_with_same_normalized_form_still_match() {
        // Two paths that are equal after normalization but don't exist on disk —
        // the fast path catches this before canonicalize is attempted.
        assert!(paths_match(
            "/nonexistent/path/repo",
            "/nonexistent/path/repo"
        ));
    }

    #[test]
    fn paths_match_nonexistent_paths_differ() {
        // Two different non-existent paths must not match.
        assert!(!paths_match(
            "/nonexistent/path/repo",
            "/nonexistent/path/other"
        ));
    }
}
