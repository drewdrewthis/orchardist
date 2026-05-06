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

    Command::new("rm")
        .args(["-rf", &resolved_str])
        .status()
        .context("rm -rf worktree")?;

    Command::new("git")
        .args(["worktree", "prune"])
        .status()
        .context("git worktree prune")?;

    Ok(())
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
