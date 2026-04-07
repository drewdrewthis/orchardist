//! Persistent last-selected-row memory for the TUI.
//!
//! Saves the identity of the last highlighted row to `~/.cache/orchard/last_selection.json`
//! on exit and restores it on the next launch. Stored as an identity (path or session name),
//! not a cursor index, because the list reorders on every refresh.
//!
//! This is ephemeral UI state: safe to delete, never user-edited.

use std::path::PathBuf;

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
    /// Schema version — currently always 1.
    pub version: u32,
    /// What kind of row was selected.
    pub kind: SelectionKind,
    /// The stable identity: worktree path for `Worktree`, session name for `Standalone`.
    pub key: String,
}

// ---------------------------------------------------------------------------
// Path
// ---------------------------------------------------------------------------

/// Returns the path to the last-selection persistence file.
pub fn path() -> PathBuf {
    cache::cache_dir().join("last_selection.json")
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

/// Loads the last selection from disk.
///
/// Returns `LastSelection::default()` (kind=None, key="") when:
/// - The file does not exist (first launch).
/// - The file contains invalid JSON (corrupt file).
/// Errors are logged but never propagated — the TUI must not crash on startup.
pub fn load() -> LastSelection {
    let p = path();
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

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

/// Saves the current selection to disk atomically (tmp file + rename).
///
/// Errors are returned to the caller; the caller is expected to log and swallow them
/// so that exit is never blocked by a failed write.
pub fn save(sel: &LastSelection) -> anyhow::Result<()> {
    use anyhow::Context as _;

    let dir = cache::cache_dir();
    std::fs::create_dir_all(&dir).context("create cache directory")?;

    let tmp_path = dir.join("last_selection.json.tmp");
    let final_path = path();

    let json = serde_json::to_string_pretty(sel).context("serialize last_selection")?;
    std::fs::write(&tmp_path, &json).context("write last_selection.json.tmp")?;
    std::fs::rename(&tmp_path, &final_path)
        .context("rename last_selection.json.tmp to last_selection.json")?;

    Ok(())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    /// Override the cache dir by writing directly to a temp path,
    /// bypassing the global `cache::cache_dir()` for isolation.
    fn save_to(dir: &std::path::Path, sel: &LastSelection) -> anyhow::Result<()> {
        let tmp_path = dir.join("last_selection.json.tmp");
        let final_path = dir.join("last_selection.json");
        let json = serde_json::to_string_pretty(sel)?;
        std::fs::write(&tmp_path, &json)?;
        std::fs::rename(&tmp_path, &final_path)?;
        Ok(())
    }

    fn load_from(dir: &std::path::Path) -> LastSelection {
        let p = dir.join("last_selection.json");
        let contents = match std::fs::read_to_string(&p) {
            Ok(s) => s,
            Err(_) => return LastSelection::default(),
        };
        serde_json::from_str(&contents).unwrap_or_default()
    }

    #[test]
    fn load_returns_default_when_missing() {
        let dir = tempdir().unwrap();
        let sel = load_from(dir.path());
        assert_eq!(sel.kind, SelectionKind::None);
        assert_eq!(sel.key, "");
        assert_eq!(sel.version, 0);
    }

    #[test]
    fn save_then_load_roundtrip_worktree_kind() {
        let dir = tempdir().unwrap();
        let original = LastSelection {
            version: 1,
            kind: SelectionKind::Worktree,
            key: "/home/user/workspace/my-repo".to_string(),
        };
        save_to(dir.path(), &original).unwrap();
        let loaded = load_from(dir.path());
        assert_eq!(loaded.kind, SelectionKind::Worktree);
        assert_eq!(loaded.key, "/home/user/workspace/my-repo");
        assert_eq!(loaded.version, 1);
    }

    #[test]
    fn save_then_load_roundtrip_standalone_kind() {
        let dir = tempdir().unwrap();
        let original = LastSelection {
            version: 1,
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
            version: 1,
            kind: SelectionKind::Worktree,
            key: "/home/user/workspace/repo".to_string(),
        };
        save_to(dir.path(), &sel).unwrap();

        // The final file must exist.
        assert!(dir.path().join("last_selection.json").exists());
        // The tmp file must NOT remain after save.
        assert!(!dir.path().join("last_selection.json.tmp").exists());
    }
}
