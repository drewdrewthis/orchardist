//! Persistent last-selected-row memory for the TUI.
//!
//! Saves the identity of the last highlighted row to `~/.cache/orchard/last_selection.json`
//! on exit and restores it on the next launch. Stored as an identity (path or session name),
//! not a cursor index, because the list reorders on every refresh.
//!
//! This is ephemeral UI state: safe to delete, never user-edited.

use std::path::Path;

use serde::{Deserialize, Serialize};

use crate::cache;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// The kind of row that was last selected.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SelectionKind {
    /// No row was selected (or selection is unknown).
    #[default]
    None,
    /// A worktree row, identified by its absolute worktree path.
    Worktree,
    /// A standalone session row, identified by its session name.
    Standalone,
}

/// The identity of the last row highlighted in the TUI list.
///
/// Persisted to `~/.cache/orchard/last_selection.json` on exit.
/// Loaded and resolved to a cursor index in `App::new`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct LastSelection {
    /// What kind of row was selected.
    pub kind: SelectionKind,
    /// The stable identity: worktree path for `Worktree`, session name for `Standalone`.
    pub key: String,
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

/// Loads the last selection from a specific directory.
///
/// This is the real implementation used by both [`load`] (which uses the global
/// cache dir) and tests (which pass a temp dir for isolation).
///
/// Returns `LastSelection::default()` (kind=None, key="") when:
/// - The file does not exist (first launch).
/// - The file contains invalid JSON (corrupt file).
pub(crate) fn load_from(dir: &Path) -> LastSelection {
    let p = dir.join("last_selection.json");
    let contents = match std::fs::read_to_string(&p) {
        Ok(s) => s,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return LastSelection::default(),
        Err(e) => {
            crate::logger::LOG.warn(&format!("last_selection: read error: {e}"));
            return LastSelection::default();
        }
    };
    match serde_json::from_str::<LastSelection>(&contents) {
        Ok(sel) => sel,
        Err(e) => {
            crate::logger::LOG.warn(&format!("last_selection: parse error: {e}"));
            LastSelection::default()
        }
    }
}

/// Loads the last selection from the global cache directory.
///
/// Errors are logged but never propagated — the TUI must not crash on startup.
pub fn load() -> LastSelection {
    load_from(&cache::cache_dir())
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

/// Saves the current selection to a specific directory atomically (tmp file + rename).
///
/// This is the real implementation used by both [`save`] (which uses the global
/// cache dir) and tests (which pass a temp dir for isolation).
///
/// Errors are returned to the caller.
pub(crate) fn save_to(dir: &Path, sel: &LastSelection) -> anyhow::Result<()> {
    use anyhow::Context as _;

    std::fs::create_dir_all(dir).context("create cache directory")?;

    let tmp_path = dir.join("last_selection.json.tmp");
    let final_path = dir.join("last_selection.json");

    let json = serde_json::to_string_pretty(sel).context("serialize last_selection")?;
    std::fs::write(&tmp_path, &json).context("write last_selection.json.tmp")?;
    std::fs::rename(&tmp_path, &final_path)
        .context("rename last_selection.json.tmp to last_selection.json")?;

    Ok(())
}

/// Saves the current selection to the global cache directory atomically.
///
/// Errors are returned to the caller; the caller is expected to log and swallow them
/// so that exit is never blocked by a failed write.
pub fn save(sel: &LastSelection) -> anyhow::Result<()> {
    let dir = cache::cache_dir();
    save_to(&dir, sel)
}

// ---------------------------------------------------------------------------
// Resolve cursor (pure, testable — no App dependency)
// ---------------------------------------------------------------------------

/// Resolves a `LastSelection` to a cursor index.
///
/// Resolution rules:
/// - `SelectionKind::Standalone`: find by session name in `standalone`.
/// - `SelectionKind::Worktree`: find by `worktree_path` in `rows`, offset by `standalone.len()`.
/// - `SelectionKind::None` or not found: return 0.
pub(crate) fn resolve_cursor(
    sel: &LastSelection,
    standalone: &[crate::session::StandaloneSessionRow],
    rows: &[crate::derive::WorktreeRow],
) -> usize {
    match sel.kind {
        SelectionKind::None => 0,
        SelectionKind::Standalone => standalone
            .iter()
            .position(|ss| ss.session.tmux.name == sel.key)
            .unwrap_or(0),
        SelectionKind::Worktree => rows
            .iter()
            .position(|r| r.worktree_path == sel.key)
            .map(|i| standalone.len() + i)
            .unwrap_or(0),
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    // -----------------------------------------------------------------------
    // load_from / save_to tests
    // -----------------------------------------------------------------------

    #[test]
    fn load_returns_default_when_missing() {
        let dir = tempdir().unwrap();
        let sel = load_from(dir.path());
        assert_eq!(sel.kind, SelectionKind::None);
        assert_eq!(sel.key, "");
    }

    #[test]
    fn save_then_load_roundtrip_worktree_kind() {
        let dir = tempdir().unwrap();
        let original = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/home/user/workspace/my-repo".to_string(),
        };
        save_to(dir.path(), &original).unwrap();
        let loaded = load_from(dir.path());
        assert_eq!(loaded.kind, SelectionKind::Worktree);
        assert_eq!(loaded.key, "/home/user/workspace/my-repo");
    }

    #[test]
    fn save_then_load_roundtrip_standalone_kind() {
        let dir = tempdir().unwrap();
        let original = LastSelection {
            kind: SelectionKind::Standalone,
            key: "my-standalone-session".to_string(),
        };
        save_to(dir.path(), &original).unwrap();
        let loaded = load_from(dir.path());
        assert_eq!(loaded.kind, SelectionKind::Standalone);
        assert_eq!(loaded.key, "my-standalone-session");
    }

    #[test]
    fn load_returns_default_on_invalid_json() {
        let dir = tempdir().unwrap();
        std::fs::write(dir.path().join("last_selection.json"), b"not-valid-json").unwrap();
        let sel = load_from(dir.path());
        assert_eq!(sel.kind, SelectionKind::None);
    }

    #[test]
    fn save_writes_atomically_via_tmp_file() {
        let dir = tempdir().unwrap();
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/home/user/workspace/repo".to_string(),
        };
        save_to(dir.path(), &sel).unwrap();

        // The final file must exist.
        assert!(dir.path().join("last_selection.json").exists());
        // The tmp file must NOT remain after save.
        assert!(!dir.path().join("last_selection.json.tmp").exists());
    }

    // -----------------------------------------------------------------------
    // resolve_cursor tests
    // -----------------------------------------------------------------------

    fn make_standalone_session(name: &str) -> crate::session::StandaloneSessionRow {
        use crate::session::{
            EnrichedSession, Host, SessionStatus, StandaloneConfig, StandaloneSessionRow,
            TmuxSessionInfo,
        };
        StandaloneSessionRow {
            session: EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: name.to_string(),
                    status: SessionStatus::Dead,
                },
                claude: None,
                panes: vec![],
            },
            config: StandaloneConfig {
                name: name.to_string(),
                command: String::new(),
                cwd: String::new(),
                start_on_launch: false,
            },
        }
    }

    fn make_task_row(issue_number: u32) -> crate::derive::WorktreeRow {
        use crate::derive::{DisplayGroup, WorktreeRow};
        WorktreeRow {
            repo_slug: "owner/repo".to_string(),
            worktree_path: format!("/workspace/repo-{issue_number}"),
            branch: format!("feat/issue-{issue_number}"),
            worktree_host: None,
            issue_number: Some(issue_number),
            issue_title: Some(format!("Test task {issue_number}")),
            issue_state: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
        }
    }

    #[test]
    fn resolve_cursor_returns_zero_for_kind_none() {
        let sel = LastSelection::default();
        let idx = resolve_cursor(&sel, &[], &[]);
        assert_eq!(idx, 0);
    }

    #[test]
    fn resolve_cursor_finds_worktree_row() {
        let rows = vec![make_task_row(1), make_task_row(2)];
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/workspace/repo-2".to_string(),
        };
        let idx = resolve_cursor(&sel, &[], &rows);
        assert_eq!(idx, 1);
    }

    #[test]
    fn resolve_cursor_offsets_worktree_by_standalone_count() {
        let standalone = vec![make_standalone_session("my-session")];
        let rows = vec![make_task_row(1)];
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/workspace/repo-1".to_string(),
        };
        let idx = resolve_cursor(&sel, &standalone, &rows);
        // 1 standalone + index 0 in rows = 1
        assert_eq!(idx, 1);
    }

    #[test]
    fn resolve_cursor_finds_standalone_row() {
        let standalone = vec![
            make_standalone_session("alpha"),
            make_standalone_session("beta"),
        ];
        let sel = LastSelection {
            kind: SelectionKind::Standalone,
            key: "beta".to_string(),
        };
        let idx = resolve_cursor(&sel, &standalone, &[]);
        assert_eq!(idx, 1);
    }

    #[test]
    fn resolve_cursor_returns_zero_when_worktree_deleted() {
        let rows = vec![make_task_row(1)];
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/workspace/deleted-worktree".to_string(),
        };
        let idx = resolve_cursor(&sel, &[], &rows);
        assert_eq!(idx, 0);
    }

    #[test]
    fn resolve_cursor_returns_zero_for_empty_lists() {
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/workspace/repo-1".to_string(),
        };
        let idx = resolve_cursor(&sel, &[], &[]);
        assert_eq!(idx, 0);
    }
}
