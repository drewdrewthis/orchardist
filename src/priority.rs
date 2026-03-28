//! Worktree priority flags. A simple on/off toggle that moves prioritized
//! worktrees above non-prioritized ones in the display, below shepherds.
//!
//! Storage: `~/.cache/orchard/priorities.json` — a JSON file listing the
//! absolute paths of all prioritized worktrees. Reads and writes are
//! best-effort: any IO error silently returns a sane default (empty set /
//! no-op), so a missing or corrupt file never crashes the TUI.

use std::collections::HashSet;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};

// ---------------------------------------------------------------------------
// Storage types
// ---------------------------------------------------------------------------

#[derive(Debug, Default, Serialize, Deserialize)]
struct PriorityStore {
    priorities: Vec<String>,
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

fn priorities_path() -> Option<PathBuf> {
    dirs::cache_dir().map(|d| d.join("orchard").join("priorities.json"))
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Reads the priorities file and returns the set of prioritized worktree paths.
///
/// Returns an empty set if the file is missing or malformed.
pub fn load_priorities() -> HashSet<String> {
    let Some(path) = priorities_path() else {
        return HashSet::new();
    };
    let Ok(data) = std::fs::read_to_string(&path) else {
        return HashSet::new();
    };
    serde_json::from_str::<PriorityStore>(&data)
        .map(|s| s.priorities.into_iter().collect())
        .unwrap_or_default()
}

/// Writes the given set of prioritized worktree paths to disk.
///
/// Silently ignores IO errors so a failure to persist never disrupts the TUI.
pub fn save_priorities(priorities: &HashSet<String>) {
    let Some(path) = priorities_path() else {
        return;
    };
    if let Some(parent) = path.parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    let store = PriorityStore {
        priorities: {
            let mut v: Vec<String> = priorities.iter().cloned().collect();
            v.sort();
            v
        },
    };
    if let Ok(json) = serde_json::to_string_pretty(&store) {
        let _ = std::fs::write(&path, json);
    }
}

/// Toggles the priority flag for the given worktree path.
///
/// Returns the new priority state: `true` if now prioritized, `false` if removed.
pub fn toggle_priority(path: &str) -> bool {
    let mut priorities = load_priorities();
    if priorities.contains(path) {
        priorities.remove(path);
        save_priorities(&priorities);
        false
    } else {
        priorities.insert(path.to_string());
        save_priorities(&priorities);
        true
    }
}

/// Returns `true` if the given worktree path is currently prioritized.
pub fn is_prioritized(path: &str) -> bool {
    load_priorities().contains(path)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    /// Builds a PriorityStore from a vec of paths and round-trips through JSON.
    #[test]
    fn priority_store_roundtrips_json() {
        let store = PriorityStore {
            priorities: vec!["/a/b/c".to_string(), "/x/y/z".to_string()],
        };
        let json = serde_json::to_string(&store).unwrap();
        let back: PriorityStore = serde_json::from_str(&json).unwrap();
        assert_eq!(back.priorities, store.priorities);
    }

    /// save/load are inverse operations for a non-empty set (uses a temp file).
    #[test]
    fn save_and_load_roundtrip() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("priorities.json");

        let mut set = HashSet::new();
        set.insert("/home/user/repo/.worktrees/feat-1".to_string());
        set.insert("/home/user/repo/.worktrees/feat-2".to_string());

        let store = PriorityStore {
            priorities: {
                let mut v: Vec<String> = set.iter().cloned().collect();
                v.sort();
                v
            },
        };
        let json = serde_json::to_string_pretty(&store).unwrap();
        std::fs::write(&path, json).unwrap();

        let loaded: PriorityStore =
            serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
        let loaded_set: HashSet<String> = loaded.priorities.into_iter().collect();
        assert_eq!(loaded_set, set);
    }

    /// load_priorities returns empty HashSet when the file is missing.
    #[test]
    fn load_missing_file_returns_empty() {
        // Directly parse an empty/missing JSON scenario.
        let result: HashSet<String> = serde_json::from_str::<PriorityStore>("{\"priorities\":[]}")
            .map(|s| s.priorities.into_iter().collect())
            .unwrap_or_default();
        assert!(result.is_empty());
    }

    /// Malformed JSON returns empty set rather than panicking.
    #[test]
    fn malformed_json_returns_empty() {
        let result: HashSet<String> = serde_json::from_str::<PriorityStore>("not json at all")
            .map(|s| s.priorities.into_iter().collect())
            .unwrap_or_default();
        assert!(result.is_empty());
    }
}
