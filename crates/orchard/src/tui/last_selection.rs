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
use crate::global_config;

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
    /// The slug of the active repo filter tab, e.g. `"owner/repo"`.
    ///
    /// `None` means "all repos" (index 0). Stored as a slug rather than an index
    /// so it stays valid even if the repos list is reordered.
    ///
    /// Defaults to `None` so cache files written by older versions still parse.
    #[serde(default)]
    pub active_repo_slug: Option<String>,
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

/// Resolves a `LastSelection` to an `active_repo_index`.
///
/// Resolution rules:
/// - `active_repo_slug` is `None`: return 0 ("all repos").
/// - Slug found at position `i` in `repos`: return `i + 1` (1-based, index 0 = all repos).
/// - Slug not found (repo removed): return 0 ("all repos").
pub(crate) fn resolve_active_repo_index(
    sel: &LastSelection,
    repos: &[global_config::RepoConfig],
) -> usize {
    match &sel.active_repo_slug {
        None => 0,
        Some(slug) => repos
            .iter()
            .position(|r| &r.slug == slug)
            .map(|i| i + 1)
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
            active_repo_slug: None,
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
            active_repo_slug: None,
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
            active_repo_slug: None,
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
                windows: vec![],
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
            active_repo_slug: None,
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
            active_repo_slug: None,
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
            active_repo_slug: None,
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
            active_repo_slug: None,
        };
        let idx = resolve_cursor(&sel, &[], &rows);
        assert_eq!(idx, 0);
    }

    #[test]
    fn resolve_cursor_returns_zero_for_empty_lists() {
        let sel = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/workspace/repo-1".to_string(),
            active_repo_slug: None,
        };
        let idx = resolve_cursor(&sel, &[], &[]);
        assert_eq!(idx, 0);
    }

    // -----------------------------------------------------------------------
    // active_repo_slug roundtrip tests
    // -----------------------------------------------------------------------

    #[test]
    fn save_then_load_roundtrip_preserves_active_repo_slug() {
        let dir = tempdir().unwrap();
        let original = LastSelection {
            kind: SelectionKind::Worktree,
            key: "/home/user/workspace/my-repo".to_string(),
            active_repo_slug: Some("acme/my-project".to_string()),
        };
        save_to(dir.path(), &original).unwrap();
        let loaded = load_from(dir.path());
        assert_eq!(loaded.active_repo_slug, Some("acme/my-project".to_string()));
    }

    #[test]
    fn load_returns_none_active_repo_for_old_file_format() {
        let dir = tempdir().unwrap();
        // Write a JSON file that lacks the active_repo_slug field (old format).
        std::fs::write(
            dir.path().join("last_selection.json"),
            br#"{"kind":"worktree","key":"/home/user/workspace/my-repo"}"#,
        )
        .unwrap();
        let loaded = load_from(dir.path());
        assert_eq!(loaded.active_repo_slug, None);
    }

    // -----------------------------------------------------------------------
    // resolve_active_repo_index tests
    // -----------------------------------------------------------------------

    fn make_repo_config(slug: &str) -> global_config::RepoConfig {
        global_config::RepoConfig {
            slug: slug.to_string(),
            path: "/home/user/workspace/repo".to_string(),
            remotes: vec![],
        }
    }

    #[test]
    fn resolve_active_repo_index_returns_zero_when_none() {
        let sel = LastSelection::default();
        let repos = vec![
            make_repo_config("acme/alpha"),
            make_repo_config("acme/beta"),
        ];
        assert_eq!(resolve_active_repo_index(&sel, &repos), 0);
    }

    #[test]
    fn resolve_active_repo_index_finds_repo_by_slug() {
        let sel = LastSelection {
            active_repo_slug: Some("acme/beta".to_string()),
            ..Default::default()
        };
        let repos = vec![
            make_repo_config("acme/alpha"),
            make_repo_config("acme/beta"),
        ];
        // "acme/beta" is at index 1 in repos, so active_repo_index = 2 (1-based).
        assert_eq!(resolve_active_repo_index(&sel, &repos), 2);
    }

    #[test]
    fn resolve_active_repo_index_returns_zero_when_slug_not_found() {
        let sel = LastSelection {
            active_repo_slug: Some("acme/removed".to_string()),
            ..Default::default()
        };
        let repos = vec![
            make_repo_config("acme/alpha"),
            make_repo_config("acme/beta"),
        ];
        assert_eq!(resolve_active_repo_index(&sel, &repos), 0);
    }
}
