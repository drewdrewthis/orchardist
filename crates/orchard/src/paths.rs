//! Path utilities for Orchard.
//!
//! `tildify` shortens absolute paths by replacing the home directory prefix
//! with `~`; `truncate_left` caps display width by eliding from the left with
//! a `…` character. `sanitize_branch_slug` produces a filesystem-safe slug
//! for the remote-registry-entry naming. New-worktree path computation lives
//! in `worktree-core` (`worktree_core::worktree_path_for`) — the single
//! source of truth shared with the `orchard-worktree` CLI.
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

/// Resolves the git directory for a repo root using only filesystem reads —
/// no `git` shell-out. Handles both shapes:
///
/// - `<repo_root>/.git` is a directory: return its absolute path.
/// - `<repo_root>/.git` is a file (the worktree marker): parse the
///   `gitdir: <path>` line and return that path absolutely.
///
/// Returns `None` when the repo has no `.git` entry at all (the caller is not
/// inside a checked-out tree). This is the pure-Rust replacement for
/// `git rev-parse --absolute-git-dir`; see #426 thin-shell rip-out.
pub fn resolve_git_dir(repo_root: &std::path::Path) -> Option<std::path::PathBuf> {
    let dot_git = repo_root.join(".git");
    let meta = std::fs::metadata(&dot_git).ok()?;
    if meta.is_dir() {
        return std::fs::canonicalize(&dot_git).ok();
    }
    // .git is a file (worktree). Each line is "key: value"; we want gitdir.
    let contents = std::fs::read_to_string(&dot_git).ok()?;
    for line in contents.lines() {
        if let Some(rest) = line.strip_prefix("gitdir:") {
            let target = std::path::Path::new(rest.trim());
            // Resolve relative paths against the repo root.
            let resolved = if target.is_absolute() {
                target.to_path_buf()
            } else {
                repo_root.join(target)
            };
            return std::fs::canonicalize(&resolved).ok().or(Some(resolved));
        }
    }
    None
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

    // --- resolve_git_dir ---

    #[test]
    fn resolve_git_dir_finds_dir_form() {
        let tmp = tempfile::TempDir::new().unwrap();
        std::fs::create_dir(tmp.path().join(".git")).unwrap();
        let resolved = resolve_git_dir(tmp.path()).expect("resolved");
        assert!(resolved.ends_with(".git"));
        assert!(resolved.is_absolute());
    }

    #[test]
    fn resolve_git_dir_follows_worktree_file() {
        let tmp = tempfile::TempDir::new().unwrap();
        // Create a fake main repo .git dir.
        let main_git = tmp.path().join("main").join(".git");
        std::fs::create_dir_all(&main_git).unwrap();
        // Create a worktree directory whose .git is a file pointing at main.
        let worktree = tmp.path().join("wt");
        std::fs::create_dir(&worktree).unwrap();
        std::fs::write(
            worktree.join(".git"),
            format!("gitdir: {}\n", main_git.display()),
        )
        .unwrap();
        let resolved = resolve_git_dir(&worktree).expect("resolved");
        // Canonicalised should match the canonical form of main_git.
        assert_eq!(resolved, std::fs::canonicalize(&main_git).unwrap());
    }

    #[test]
    fn resolve_git_dir_returns_none_outside_repo() {
        let tmp = tempfile::TempDir::new().unwrap();
        assert!(resolve_git_dir(tmp.path()).is_none());
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
