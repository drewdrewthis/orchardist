//! Worktree destruction (`git worktree remove`).
//!
//! On `git worktree remove` failure (broken / corrupted state), falls back to
//! `rm -rf` + `git worktree prune` — but only after re-verifying the path is
//! a known worktree. Refuses to `rm` arbitrary paths.

use std::process::Command;

use anyhow::{Context, Result, anyhow};

/// Removes the worktree at `path`. If `force` is true, passes `--force` to git.
///
/// Falls back to `rm -rf` + `git worktree prune` if `git worktree remove`
/// fails — but only after verifying the path appears in
/// `git worktree list --porcelain`. This guards against accidentally removing
/// paths that aren't worktrees.
pub fn remove_worktree(path: &str, force: bool) -> Result<()> {
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
        return Ok(());
    }

    let resolved =
        std::fs::canonicalize(path).with_context(|| format!("canonicalizing path: {path}"))?;
    let resolved_str = resolved.to_string_lossy();

    if !is_known_worktree(&resolved_str) {
        return Err(anyhow!(
            "refusing to rm path not listed as a git worktree: {path}"
        ));
    }

    let rm_status = Command::new("rm")
        .args(["-rf", &resolved_str])
        .status()
        .context("rm -rf worktree")?;
    if !rm_status.success() {
        return Err(anyhow!(
            "rm -rf {resolved_str} failed (exit {:?})",
            rm_status.code()
        ));
    }

    let prune_status = Command::new("git")
        .args(["worktree", "prune"])
        .status()
        .context("git worktree prune")?;
    if !prune_status.success() {
        return Err(anyhow!(
            "git worktree prune failed (exit {:?})",
            prune_status.code()
        ));
    }

    Ok(())
}

/// Reports whether `path` appears in `git worktree list --porcelain`.
fn is_known_worktree(path: &str) -> bool {
    Command::new("git")
        .args(["worktree", "list", "--porcelain"])
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| porcelain_lists_worktree(&String::from_utf8_lossy(&o.stdout), path))
        .unwrap_or(false)
}

/// Whole-line match for a `worktree <path>` entry in porcelain output.
///
/// Substring matching here is wrong: `worktree /foo/bar` would otherwise also
/// match a line for `/foo/bar-extra`. We need an exact line match.
fn porcelain_lists_worktree(porcelain: &str, path: &str) -> bool {
    let target = format!("worktree {path}");
    porcelain.lines().any(|line| line == target)
}

#[cfg(test)]
mod tests {
    use super::*;

    const PORCELAIN: &str = "\
worktree /home/user/repo
HEAD abc

worktree /home/user/repo-feature
HEAD def
branch refs/heads/feature

worktree /home/user/repo-extra
HEAD ghi
";

    #[test]
    fn porcelain_lists_worktree_exact_match() {
        assert!(porcelain_lists_worktree(PORCELAIN, "/home/user/repo"));
        assert!(porcelain_lists_worktree(PORCELAIN, "/home/user/repo-feature"));
    }

    #[test]
    fn porcelain_lists_worktree_does_not_prefix_match() {
        // `/home/user/repo` is a prefix of `/home/user/repo-feature` and
        // `/home/user/repo-extra` — but querying for the unrelated path
        // `/home/user/rep` (a true prefix that does not appear as a worktree)
        // must not match.
        assert!(!porcelain_lists_worktree(PORCELAIN, "/home/user/rep"));
    }

    #[test]
    fn porcelain_lists_worktree_returns_false_for_missing_path() {
        assert!(!porcelain_lists_worktree(PORCELAIN, "/home/user/other"));
    }

    #[test]
    fn porcelain_lists_worktree_returns_false_for_empty_porcelain() {
        assert!(!porcelain_lists_worktree("", "/anything"));
    }
}
