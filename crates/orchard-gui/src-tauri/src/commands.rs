//! Tauri command bridges that delegate stateless system ops to `worktree-core`.
//!
//! This is Layer 1 of research/037 §"Two-layer write model": stateless system
//! ops do **not** require the daemon. The CLI binaries call `worktree-core`
//! directly; the GUI calls it via these Tauri command bridges. Same code path,
//! different entry points.
//!
//! Stateful ops (chat send, contract update, cross-host transfer) flow through
//! the daemon write protocol (per research/037 §1) — they are NOT here.

use std::path::PathBuf;
use std::process::Command;

use serde::{Deserialize, Serialize};
use worktree_core as wc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorktreeRow {
    pub path: String,
    pub branch: Option<String>,
    pub head: String,
    pub is_bare: bool,
    pub is_main: bool,
    pub has_conflicts: bool,
}

impl From<wc::WorktreeEntry> for WorktreeRow {
    fn from(e: wc::WorktreeEntry) -> Self {
        Self {
            path: e.path,
            branch: e.branch,
            head: e.head,
            is_bare: e.is_bare,
            is_main: e.is_main,
            has_conflicts: e.has_conflicts,
        }
    }
}

#[tauri::command]
pub fn list_worktrees() -> Result<Vec<WorktreeRow>, String> {
    wc::list_worktrees()
        .map(|v| v.into_iter().map(Into::into).collect())
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub fn create_worktree(
    repo_root: String,
    worktree_path: String,
    branch: String,
) -> Result<String, String> {
    let root = PathBuf::from(repo_root);
    let outcome = wc::create_worktree(&root, &branch, &worktree_path).map_err(|e| e.to_string())?;
    Ok(match outcome {
        wc::CreateOutcome::NewBranch => "new".into(),
        wc::CreateOutcome::ExistingBranch => "existing".into(),
    })
}

#[tauri::command]
pub fn remove_worktree(worktree_path: String, force: bool) -> Result<(), String> {
    wc::remove_worktree(&worktree_path, force).map_err(|e| e.to_string())
}

#[tauri::command]
pub fn prune_worktrees(paths: Vec<String>, force: bool) -> Result<Vec<(String, String)>, String> {
    let refs: Vec<&str> = paths.iter().map(|s| s.as_str()).collect();
    let outcomes = wc::prune(&refs, force);
    Ok(outcomes
        .into_iter()
        .map(|(p, r)| (p, r.map_or_else(|e| e.to_string(), |_| String::new())))
        .collect())
}

/// Type a chat message into a live tmux pane (the Claude REPL).
///
/// We do it in two `send-keys` calls — `-l` (literal) for the text so
/// tmux doesn't interpret backticks or magic strings, then a separate
/// `Enter` press to submit. A single combined call races on long
/// messages and the Enter can land before the text is fully written.
///
/// `pane_id` is the tmux pane id (e.g. `%66`) — the daemon already
/// surfaces these via `tmuxPanes`. Trim the leading `%` is not
/// required; tmux accepts both forms.
#[tauri::command]
pub fn tmux_send_text(pane_id: String, text: String) -> Result<(), String> {
    if pane_id.is_empty() {
        return Err("pane_id is empty".into());
    }
    if text.is_empty() {
        return Err("text is empty".into());
    }
    let status = Command::new("tmux")
        .args(["send-keys", "-t", &pane_id, "-l", &text])
        .status()
        .map_err(|e| format!("tmux send-keys (text) failed to spawn: {e}"))?;
    if !status.success() {
        return Err(format!("tmux send-keys (text) exited {status}"));
    }
    // Tiny delay so tmux fully processes the literal write before we
    // press Enter. 50ms is invisible to the user and reliable.
    std::thread::sleep(std::time::Duration::from_millis(50));
    let status = Command::new("tmux")
        .args(["send-keys", "-t", &pane_id, "Enter"])
        .status()
        .map_err(|e| format!("tmux send-keys (Enter) failed to spawn: {e}"))?;
    if !status.success() {
        return Err(format!("tmux send-keys (Enter) exited {status}"));
    }
    Ok(())
}
