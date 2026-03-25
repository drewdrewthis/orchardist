use anyhow::Result;

use crate::services::git::{CommandGit, parse_porcelain_impl};
use crate::services::GitService;
use crate::types::Worktree;

/// Returns the absolute path of the git repository root, or an empty string on failure.
pub fn find_repo_root() -> String {
    CommandGit.find_repo_root()
}

/// Returns the directory name of the git repository root.
pub fn get_repo_name() -> String {
    CommandGit.get_repo_name()
}

/// Returns all git worktrees for the current repository with conflict status populated.
pub fn list_worktrees() -> Result<Vec<Worktree>> {
    CommandGit.list_worktrees()
}

/// Parses the output of `git worktree list --porcelain` into a `Vec<Worktree>`.
/// Blocks are separated by blank lines.
pub fn parse_porcelain(output: &str) -> Vec<Worktree> {
    parse_porcelain_impl(output)
}

/// Reports whether the worktree at `path` has unmerged (conflicted) files.
pub fn worktree_has_conflicts(path: &str) -> bool {
    CommandGit.worktree_has_conflicts(path)
}

/// Removes the worktree at `path`. If `force` is true, passes `--force` to git.
/// Falls back to `rm -rf` + `git worktree prune` if git worktree remove fails,
/// but only after verifying the path is a known worktree.
pub fn remove_worktree(path: &str, force: bool) -> Result<()> {
    CommandGit.remove_worktree(path, force)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;
    use crate::services::git::normalize_path;

    const SAMPLE: &str = "\
worktree /home/user/project
HEAD abc123def456
branch refs/heads/main

worktree /home/user/project-feature
HEAD 111222333444
branch refs/heads/feature/my-work

worktree /home/user/project-detached
HEAD deadbeef1234
detached

worktree /home/user/bare.git
HEAD 0000000000000
bare";

    #[test]
    fn parse_porcelain_main_worktree() {
        let wts = parse_porcelain(SAMPLE);
        assert_eq!(wts[0].path, "/home/user/project");
        assert_eq!(wts[0].head, "abc123def456");
        assert_eq!(wts[0].branch.as_deref(), Some("main"));
        assert!(!wts[0].is_bare);
    }

    #[test]
    fn parse_porcelain_strips_refs_heads_prefix() {
        let wts = parse_porcelain(SAMPLE);
        assert_eq!(wts[1].branch.as_deref(), Some("feature/my-work"));
    }

    #[test]
    fn parse_porcelain_detached_head() {
        let wts = parse_porcelain(SAMPLE);
        assert!(wts[2].branch.is_none());
        assert!(!wts[2].is_bare);
    }

    #[test]
    fn parse_porcelain_bare_worktree() {
        let wts = parse_porcelain(SAMPLE);
        assert!(wts[3].is_bare);
    }

    #[test]
    fn parse_porcelain_returns_four_entries() {
        let wts = parse_porcelain(SAMPLE);
        assert_eq!(wts.len(), 4);
    }

    #[test]
    fn parse_porcelain_empty_input() {
        assert!(parse_porcelain("").is_empty());
    }

    #[test]
    fn parse_porcelain_skips_empty_blocks() {
        let input = "\n\nworktree /tmp/x\nHEAD abc\nbranch refs/heads/main\n\n\n";
        let wts = parse_porcelain(input);
        assert_eq!(wts.len(), 1);
    }

    #[test]
    fn normalize_path_resolves_parent_components() {
        use std::path::PathBuf;
        let p = Path::new("/a/b/c/../../../d");
        assert_eq!(normalize_path(p), PathBuf::from("/d"));
    }

    #[test]
    fn normalize_path_preserves_clean_path() {
        use std::path::PathBuf;
        let p = Path::new("/a/b/c");
        assert_eq!(normalize_path(p), PathBuf::from("/a/b/c"));
    }

    #[test]
    fn normalize_path_resolves_dot_components() {
        use std::path::PathBuf;
        let p = Path::new("/a/./b/./c");
        assert_eq!(normalize_path(p), PathBuf::from("/a/b/c"));
    }

    #[test]
    fn normalize_path_resolves_git_modules_path() {
        use std::path::PathBuf;
        // .git/modules/sub is 3 components deep from repo root, so 3 x ".." = repo root
        let p = Path::new("/home/user/repo/.git/modules/sub/../../../actual");
        assert_eq!(normalize_path(p), PathBuf::from("/home/user/repo/actual"));
    }
}
