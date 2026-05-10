//! Path helpers for worktree mutations.
//!
//! These exist in `worktree-core` so callers — `orchard-worktree` CLI,
//! `orchard-tui` dialogs, future automation — agree on where a worktree for
//! a given branch lives. Divergence between callers means worktrees created
//! by one tool aren't found by the other.

use std::path::{Path, PathBuf};

/// Resolve the path that a new worktree for `branch` should live at.
///
/// Convention: `<repo_root>/.worktrees/<branch-with-slashes-replaced>`.
/// Slashes become hyphens because `.worktrees/foo/bar` would imply a
/// directory hierarchy that doesn't exist.
pub fn worktree_path_for(repo_root: impl AsRef<Path>, branch: &str) -> PathBuf {
    let slug = branch.replace('/', "-");
    repo_root.as_ref().join(".worktrees").join(slug)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn simple_branch() {
        assert_eq!(
            worktree_path_for("/repo", "feature").to_string_lossy(),
            "/repo/.worktrees/feature"
        );
    }

    #[test]
    fn slashed_branch_replaces_with_hyphen() {
        assert_eq!(
            worktree_path_for("/repo", "feature/foo").to_string_lossy(),
            "/repo/.worktrees/feature-foo"
        );
    }

    #[test]
    fn double_slash_branch() {
        assert_eq!(
            worktree_path_for("/repo", "issue123/feature/sub").to_string_lossy(),
            "/repo/.worktrees/issue123-feature-sub"
        );
    }

    #[test]
    fn accepts_pathbuf_repo_root() {
        let root = PathBuf::from("/repo");
        assert_eq!(
            worktree_path_for(&root, "main").to_string_lossy(),
            "/repo/.worktrees/main"
        );
    }
}
