use std::path::Path;
use std::process::Command;

use anyhow::{anyhow, Context, Result};

use crate::logger::LOG;
use crate::types::Worktree;

/// Command-based implementation of `GitService`.
pub struct CommandGit;

impl super::GitService for CommandGit {
    fn find_repo_root(&self) -> Result<String> {
        let out = Command::new("git")
            .args(["rev-parse", "--show-toplevel"])
            .output()
            .context("running git rev-parse")?;
        if !out.status.success() {
            return Err(anyhow!("not a git repository"));
        }
        Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
    }

    fn get_repo_name(&self) -> Result<String> {
        let root = self.find_repo_root()?;
        Ok(Path::new(&root)
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("")
            .to_string())
    }

    fn list_worktrees(&self) -> Result<Vec<Worktree>> {
        let root = self.find_repo_root()?;
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

        let raw = Self::parse_porcelain(&String::from_utf8_lossy(&out.stdout));
        let mut trees = Vec::with_capacity(raw.len());

        for mut wt in raw {
            if wt.path.contains("/.git/") {
                wt.path = CommandGit::resolve_main_worktree_path(&wt.path);
            }
            if !wt.is_bare {
                wt.has_conflicts = self.worktree_has_conflicts(&wt.path);
            }
            trees.push(wt);
        }

        LOG.info(&format!("listWorktrees: {} trees", trees.len()));
        Ok(trees)
    }

    fn worktree_has_conflicts(&self, path: &str) -> bool {
        Command::new("git")
            .args(["diff", "--name-only", "--diff-filter=U"])
            .current_dir(path)
            .output()
            .ok()
            .filter(|o| o.status.success())
            .map(|o| !o.stdout.trim_ascii().is_empty())
            .unwrap_or(false)
    }

    fn remove_worktree(&self, path: &str, force: bool) -> Result<()> {
        let mut args = vec!["worktree", "remove", path];
        if force {
            args.push("--force");
        }

        if Command::new("git")
            .args(&args)
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
        {
            LOG.info(&format!("removeWorktree: removed {}", path));
            return Ok(());
        }

        LOG.warn(&format!(
            "removeWorktree: git remove failed for {}, falling back to rm + prune",
            path
        ));
        let resolved = std::fs::canonicalize(path)
            .with_context(|| format!("canonicalizing path: {path}"))?;
        let resolved_str = resolved.to_string_lossy();

        if !CommandGit::is_known_worktree(&resolved_str) {
            return Err(anyhow!(
                "refusing to rm path not listed as a git worktree: {path}"
            ));
        }

        Command::new("rm")
            .args(["-rf", &*resolved_str])
            .status()
            .context("rm -rf worktree")?;

        Command::new("git")
            .args(["worktree", "prune"])
            .status()
            .context("git worktree prune")?;

        Ok(())
    }
}

impl CommandGit {
    /// Parses the output of `git worktree list --porcelain` into a `Vec<Worktree>`.
    fn parse_porcelain(output: &str) -> Vec<Worktree> {
        parse_porcelain_impl(output)
    }

    /// Resolves the actual worktree path by consulting `core.worktree` config
    /// with `GIT_DIR` set. Used for worktrees that live inside a `.git` directory
    /// (bare-repo worktrees).
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

    /// Reports whether `path` appears in `git worktree list --porcelain`.
    fn is_known_worktree(path: &str) -> bool {
        Command::new("git")
            .args(["worktree", "list", "--porcelain"])
            .output()
            .ok()
            .filter(|o| o.status.success())
            .map(|o| {
                let s = String::from_utf8_lossy(&o.stdout);
                s.contains(&format!("worktree {path}"))
            })
            .unwrap_or(false)
    }
}

// ---------------------------------------------------------------------------
// Shared helpers (used by both the service impl and the free functions)
// ---------------------------------------------------------------------------

/// Parses the output of `git worktree list --porcelain` into a `Vec<Worktree>`.
pub(crate) fn parse_porcelain_impl(output: &str) -> Vec<Worktree> {
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

        worktrees.push(Worktree {
            path,
            head,
            branch,
            is_bare,
            has_conflicts: false,
            ..Default::default()
        });
    }

    worktrees
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

