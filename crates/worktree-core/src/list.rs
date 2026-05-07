//! `git worktree list` parsing and merge-conflict detection.
//!
//! Returns [`WorktreeEntry`] values containing only the fields produced by
//! `git worktree list --porcelain` plus a derived `has_conflicts` flag.
//! Higher-level enrichment (PR, tmux, issue) belongs to consuming crates.

use std::path::Path;
use std::process::Command;

use anyhow::{Context, Result, anyhow};
use serde::{Deserialize, Serialize};

/// A single git worktree as reported by `git worktree list --porcelain`.
///
/// This is the *unenriched* shape — no PR, tmux, or issue data. Consumers that
/// need an enriched view convert this into their own type.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, Default)]
pub struct WorktreeEntry {
    /// Absolute filesystem path to the worktree root.
    pub path: String,
    /// The branch checked out in this worktree, if any.
    pub branch: Option<String>,
    /// The commit SHA at HEAD.
    pub head: String,
    /// Whether this entry represents the bare repository (`.git` root).
    pub is_bare: bool,
    /// Whether this is the **main** worktree (the one the repo was originally
    /// cloned into). Always true for the first non-bare entry returned by
    /// `git worktree list --porcelain`. Distinct from `is_bare`: a bare
    /// repository has no main worktree at all.
    #[serde(default)]
    pub is_main: bool,
    /// Whether the worktree has unresolved merge conflicts.
    pub has_conflicts: bool,
}

/// Returns all git worktrees for the current repository with conflict status populated.
///
/// Bare-repo worktrees whose path lives inside a `.git` directory are resolved
/// via `core.worktree` config so the returned path matches the actual checkout.
pub fn list_worktrees() -> Result<Vec<WorktreeEntry>> {
    let root = crate::repo::find_repo_root();
    if root.is_empty() {
        return Err(anyhow!("not in a git repository"));
    }
    let out = Command::new("git")
        .args(["worktree", "list", "--porcelain"])
        .current_dir(&root)
        .output()
        .context("running git worktree list")?;

    if !out.status.success() {
        return Err(anyhow!(
            "git worktree list failed: {}",
            String::from_utf8_lossy(&out.stderr)
        ));
    }

    let raw = parse_porcelain(&String::from_utf8_lossy(&out.stdout));
    let mut trees = Vec::with_capacity(raw.len());
    let mut seen_non_bare = false;

    for mut wt in raw {
        if wt.path.contains("/.git/") {
            wt.path = resolve_main_worktree_path(&wt.path);
        }
        if !wt.is_bare {
            wt.has_conflicts = worktree_has_conflicts(&wt.path);
            // `git worktree list --porcelain` always lists the main worktree
            // first among non-bare entries; subsequent non-bare entries are
            // additional worktrees.
            if !seen_non_bare {
                wt.is_main = true;
                seen_non_bare = true;
            }
        }
        trees.push(wt);
    }

    Ok(trees)
}

/// Parses the output of `git worktree list --porcelain` into [`WorktreeEntry`] values.
///
/// Blocks are separated by blank lines; each block describes one worktree.
pub fn parse_porcelain(output: &str) -> Vec<WorktreeEntry> {
    let mut worktrees = Vec::new();

    for block in output.trim().split("\n\n") {
        let block = block.trim();
        if block.is_empty() {
            continue;
        }

        let mut path = String::new();
        let mut head = String::new();
        let mut branch: Option<String> = None;
        let mut is_bare = false;

        for line in block.lines() {
            if let Some(rest) = line.strip_prefix("worktree ") {
                path = rest.to_string();
            } else if let Some(rest) = line.strip_prefix("HEAD ") {
                head = rest.to_string();
            } else if let Some(rest) = line.strip_prefix("branch ") {
                let name = rest.strip_prefix("refs/heads/").unwrap_or(rest);
                branch = Some(name.to_string());
            } else if line == "bare" {
                is_bare = true;
            } else if line == "detached" {
                branch = None;
            }
        }

        if path.is_empty() {
            continue;
        }

        worktrees.push(WorktreeEntry {
            path,
            head,
            branch,
            is_bare,
            is_main: false,
            has_conflicts: false,
        });
    }

    worktrees
}

/// Reports whether the worktree at `path` has unmerged (conflicted) files.
pub fn worktree_has_conflicts(path: &str) -> bool {
    Command::new("git")
        .args(["diff", "--name-only", "--diff-filter=U"])
        .current_dir(path)
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| !o.stdout.trim_ascii().is_empty())
        .unwrap_or(false)
}

// Resolves the actual worktree path by consulting core.worktree config with GIT_DIR set.
fn resolve_main_worktree_path(git_dir: &str) -> String {
    let out = Command::new("git")
        .args(["config", "--get", "core.worktree"])
        .env("GIT_DIR", git_dir)
        .output();

    match out {
        Ok(o) if o.status.success() => {
            let rel = String::from_utf8_lossy(&o.stdout).trim().to_string();
            let joined = Path::new(git_dir).join(&rel);
            joined
                .canonicalize()
                .unwrap_or_else(|_| normalize_path(&joined))
                .to_string_lossy()
                .into_owned()
        }
        _ => git_dir.to_string(),
    }
}

/// Normalize a path by resolving `.` and `..` components without hitting the filesystem.
pub(crate) fn normalize_path(path: &Path) -> std::path::PathBuf {
    use std::path::Component;
    let mut components: Vec<Component> = Vec::new();
    for component in path.components() {
        match component {
            Component::ParentDir => {
                if !components.is_empty() {
                    components.pop();
                }
            }
            Component::CurDir => {}
            other => components.push(other),
        }
    }
    components.iter().collect()
}

#[cfg(test)]
mod tests {
    use super::*;

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
    fn parse_porcelain_does_not_set_is_main() {
        // is_main is set by list_worktrees() based on porcelain ordering,
        // not by the parser itself — the parser doesn't know which is main.
        let wts = parse_porcelain(SAMPLE);
        for wt in &wts {
            assert!(!wt.is_main);
        }
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
        let p = Path::new("/home/user/repo/.git/modules/sub/../../../actual");
        assert_eq!(normalize_path(p), PathBuf::from("/home/user/repo/actual"));
    }
}
