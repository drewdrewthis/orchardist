//! Parsers for git plumbing output.
//!
//! Single source of truth for `git worktree list --porcelain` parsing that
//! returns `CachedWorktree` entries. Used by both `cache_sources` (local
//! worktrees) and `remote_adapter` (remote adapters over SSH).
//!
//! A separate `worktree_core::parse_porcelain` exists and returns
//! `Vec<WorktreeEntry>` (the unenriched git-only shape used by the
//! worktree-core library); it is not consolidated here because `CachedWorktree`
//! carries enrichment metadata that core does not.

use crate::cache::{CachedWorktree, WorktreeLayout};

/// Parses the output of `git worktree list --porcelain` into `CachedWorktree` entries.
///
/// Every returned entry uses `layout = WorktreeLayout::Bare` — the porcelain
/// format is only produced by bare-repo layouts. Callers tagging flat-clone
/// entries must set `WorktreeLayout::Flat` themselves (they do not go through
/// this parser).
///
/// The local-refresh caller resolves `.git/modules/<name>` submodule paths to
/// their working-tree root by shelling out to `git rev-parse --show-toplevel`.
/// That is IO and cannot run against remote hosts, so it is performed in the
/// caller, not here — the parser is pure.
pub fn parse_worktree_porcelain(output: &str) -> Vec<CachedWorktree> {
    let mut worktrees = Vec::new();

    for block in output.trim().split("\n\n") {
        let block = block.trim();
        if block.is_empty() {
            continue;
        }

        let mut path = String::new();
        let mut branch = String::new();
        let mut is_bare = false;
        let mut is_locked = false;

        for line in block.lines() {
            if let Some(rest) = line.strip_prefix("worktree ") {
                path = rest.to_string();
            } else if let Some(rest) = line.strip_prefix("branch ") {
                let name = rest.strip_prefix("refs/heads/").unwrap_or(rest);
                branch = name.to_string();
            } else if line == "bare" {
                is_bare = true;
            } else if line.starts_with("locked") {
                is_locked = true;
            }
        }

        if path.is_empty() {
            continue;
        }

        worktrees.push(CachedWorktree {
            path,
            branch,
            is_bare,
            is_locked,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: WorktreeLayout::Bare,
        });
    }

    worktrees
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_bare_and_linked_worktrees() {
        let input = "\
worktree /home/u/repo
bare

worktree /home/u/repo/worktrees/feat
HEAD abc123
branch refs/heads/feat
";
        let out = parse_worktree_porcelain(input);
        assert_eq!(out.len(), 2);
        assert!(out[0].is_bare);
        assert_eq!(out[0].path, "/home/u/repo");
        assert!(!out[1].is_bare);
        assert_eq!(out[1].branch, "feat");
        assert_eq!(out[1].layout, WorktreeLayout::Bare);
    }

    #[test]
    fn skips_blocks_without_worktree_line() {
        let input = "\
worktree /home/u/repo
bare

HEAD abc123
branch refs/heads/stray
";
        let out = parse_worktree_porcelain(input);
        assert_eq!(out.len(), 1);
    }

    #[test]
    fn empty_input_returns_empty() {
        assert_eq!(parse_worktree_porcelain("").len(), 0);
        assert_eq!(parse_worktree_porcelain("\n\n\n").len(), 0);
    }
}
