//! Worktree lifecycle operations for the TUI.
//!
//! Contains stale-filtering logic and worktree deletion (both local and remote).

use crate::derive;
use crate::git;
use crate::global_config;
use crate::remote;
use crate::tmux;

// ---------------------------------------------------------------------------
// Stale worktree filter
// ---------------------------------------------------------------------------

/// Filters a slice of worktree rows down to those that are stale (merged or
/// closed PR, or completed/closed issue).
pub(super) fn filter_stale(rows: &[derive::WorktreeRow]) -> Vec<derive::WorktreeRow> {
    rows.iter()
        .filter(|row| {
            if let Some(ref pr) = row.pr {
                let state = pr.state.as_deref().unwrap_or("");
                return state == "merged" || state == "closed";
            }
            if let Some(ref state) = row.issue_state {
                return state == "completed" || state == "closed";
            }
            false
        })
        .cloned()
        .collect()
}

// ---------------------------------------------------------------------------
// Delete worktree (shared by single-delete and cleanup)
// ---------------------------------------------------------------------------

/// Deletes the worktree represented by a `WorktreeRow`.
///
/// Handles both remote (SSH) and local worktrees. Kills any associated tmux
/// session before removing the worktree from git.
pub(super) fn delete_task_row(
    row: &derive::WorktreeRow,
    global_config: &global_config::GlobalConfig,
) -> anyhow::Result<()> {
    let session_name = row.sessions.first().map(|s| s.tmux.name.as_str());
    if let Some(ref host) = row.worktree_host {
        // Remote deletion
        if let Some(sess) = session_name {
            let _ = remote::kill_remote_tmux_session(host, sess);
        }
        let slug = crate::paths::sanitize_branch_slug(&row.branch);
        let _ = remote::remove_remote_registry_entry(host, &slug);
        // Find the remote config matching this host to get the repo_path.
        let remote_cfg = global_config
            .repos
            .iter()
            .find_map(|repo| repo.remote_for_host(host));
        if let Some(remote_cfg) = remote_cfg {
            remote::remove_remote_worktree(host, &remote_cfg.path, &row.worktree_path)?;
        }
        return Ok(());
    }

    // Local deletion
    if let Some(sess) = session_name {
        let _ = tmux::kill_tmux_session(sess);
    }
    if git::remove_worktree(&row.worktree_path, false).is_err() {
        git::remove_worktree(&row.worktree_path, true)?;
    }
    Ok(())
}
