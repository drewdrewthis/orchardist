//! Persistence helpers for the latest [`WorkViewSnapshot`].
//!
//! Writes the daemon's response to `~/.cache/orchard/work_view_snapshot.json`
//! after every successful refresh tick. On TUI cold-start (or any time the
//! daemon is unreachable), [`read_snapshot`] returns the last-written value so
//! the dashboard has something to show immediately.
//!
//! # File format
//!
//! The file is a JSON object with a `version` discriminator and a `snapshot`
//! payload:
//!
//! ```json
//! { "version": 1, "snapshot": { "repos": [], ... } }
//! ```
//!
//! Callers should treat `version != CURRENT_VERSION` as absent (the next
//! successful refresh overwrites it). Do **not** bump the version on additive
//! schema changes — only bump when old clients would misinterpret the data.
//!
//! # Atomicity
//!
//! Writes are tmp-then-rename so a crashed write never leaves a half-written
//! file. Reads are best-effort — a missing or malformed file returns `None`.

use std::io;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::cache::cache_dir;
use crate::daemon::types::WorkViewSnapshot;

// ---------------------------------------------------------------------------
// Version constant
// ---------------------------------------------------------------------------

/// Schema version for the persisted work-view snapshot envelope.
///
/// Increment only on breaking schema changes. Additive fields (new `Option`
/// fields with `#[serde(default)]`) do not require a bump.
pub const CURRENT_VERSION: u32 = 1;

// ---------------------------------------------------------------------------
// Persisted envelope
// ---------------------------------------------------------------------------

/// Versioned envelope wrapping a [`WorkViewSnapshot`] on disk.
#[derive(Debug, Serialize, Deserialize)]
struct SnapshotEnvelope {
    /// Schema version. Checked on read; mismatches are treated as absent.
    version: u32,
    /// The persisted snapshot payload.
    snapshot: WorkViewSnapshot,
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

/// Returns the default path for the persisted work-view snapshot.
///
/// This is `~/.cache/orchard/work_view_snapshot.json`.
pub fn snapshot_path() -> PathBuf {
    snapshot_path_in(&cache_dir())
}

/// Returns the snapshot path rooted under `cache_dir`.
///
/// Intended for tests that redirect to a [`tempfile::TempDir`].
pub fn snapshot_path_in(cache_dir: &Path) -> PathBuf {
    cache_dir.join("work_view_snapshot.json")
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

/// Atomically writes `snapshot` to the default cache path.
///
/// The write is best-effort: the caller should log `io::Error` on failure but
/// must not propagate it — a write failure does not affect the current session.
///
/// # Errors
///
/// Returns `io::Error` when the directory cannot be created or the file
/// cannot be written or renamed into place.
pub fn write_snapshot(snapshot: &WorkViewSnapshot) -> Result<(), io::Error> {
    write_snapshot_to(snapshot, &snapshot_path())
}

/// Like [`write_snapshot`] but writes to an explicit `path`.
///
/// Intended for tests that redirect to a [`tempfile::TempDir`].
pub fn write_snapshot_to(snapshot: &WorkViewSnapshot, path: &Path) -> Result<(), io::Error> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }

    let envelope = SnapshotEnvelope {
        version: CURRENT_VERSION,
        snapshot: snapshot.clone(),
    };

    let json = serde_json::to_string_pretty(&envelope)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;

    let tmp_path = path.with_extension("json.tmp");
    std::fs::write(&tmp_path, &json)?;

    // 0600 permissions — snapshot may carry project/issue metadata.
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let _ = std::fs::set_permissions(&tmp_path, std::fs::Permissions::from_mode(0o600));
    }

    std::fs::rename(&tmp_path, path)?;

    Ok(())
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

/// Reads the persisted snapshot from the default cache path.
///
/// Returns `None` when the file is absent, unreadable, contains malformed
/// JSON, or carries an unrecognised `version`. Never panics.
pub fn read_snapshot() -> Option<WorkViewSnapshot> {
    read_snapshot_from(&snapshot_path())
}

/// Like [`read_snapshot`] but reads from an explicit `path`.
///
/// Intended for tests that redirect to a [`tempfile::TempDir`].
pub fn read_snapshot_from(path: &Path) -> Option<WorkViewSnapshot> {
    let contents = std::fs::read_to_string(path).ok()?;
    let envelope: SnapshotEnvelope = serde_json::from_str(&contents).ok()?;

    if envelope.version != CURRENT_VERSION {
        return None;
    }

    Some(envelope.snapshot)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::daemon::types::{WorkViewRepo, WorkViewSnapshot};
    use tempfile::TempDir;

    fn sample_snapshot() -> WorkViewSnapshot {
        WorkViewSnapshot {
            repos: vec![WorkViewRepo {
                slug: "repo".to_string(),
                path: "/repos/owner/repo".to_string(),
                worktrees: vec![],
            }],
            tmux_sessions: vec![],
            claude_instances: vec![],
        }
    }

    /// Round-trip: write then read returns the same snapshot.
    #[test]
    fn round_trip_write_then_read() {
        let dir = TempDir::new().unwrap();
        let path = snapshot_path_in(dir.path());

        let snap = sample_snapshot();
        write_snapshot_to(&snap, &path).unwrap();

        let read_back = read_snapshot_from(&path).expect("should read back the snapshot");
        assert_eq!(read_back.repos.len(), 1);
        assert_eq!(read_back.repos[0].slug, "repo");
        assert_eq!(read_back.repos[0].path, "/repos/owner/repo");
    }

    /// Version mismatch returns `None` without panicking.
    #[test]
    fn version_mismatch_returns_none() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("work_view_snapshot.json");

        // Write a file with a version we don't recognise.
        let raw = r#"{ "version": 999, "snapshot": { "repos": [], "tmuxSessions": [], "claudeInstances": [] } }"#;
        std::fs::write(&path, raw).unwrap();

        let result = read_snapshot_from(&path);
        assert!(result.is_none(), "version mismatch must return None");
    }

    /// Missing file returns `None` without panicking.
    #[test]
    fn missing_file_returns_none() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("no_such_file.json");
        assert!(read_snapshot_from(&path).is_none());
    }

    /// Malformed JSON returns `None` without panicking.
    #[test]
    fn malformed_json_returns_none() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("work_view_snapshot.json");
        std::fs::write(&path, b"not valid json at all").unwrap();
        assert!(read_snapshot_from(&path).is_none());
    }

    /// Atomic write: the .tmp file does not remain after a successful write.
    #[test]
    fn atomic_write_no_tmp_file_remains() {
        let dir = TempDir::new().unwrap();
        let path = snapshot_path_in(dir.path());
        let tmp = path.with_extension("json.tmp");

        write_snapshot_to(&sample_snapshot(), &path).unwrap();

        assert!(path.exists(), "final file must exist");
        assert!(
            !tmp.exists(),
            ".tmp file must be removed after atomic rename"
        );
    }

    /// Content is valid JSON with version and snapshot keys.
    #[test]
    fn written_file_has_version_and_snapshot_keys() {
        let dir = TempDir::new().unwrap();
        let path = snapshot_path_in(dir.path());

        write_snapshot_to(&sample_snapshot(), &path).unwrap();

        let contents = std::fs::read_to_string(&path).unwrap();
        let v: serde_json::Value = serde_json::from_str(&contents).unwrap();
        assert_eq!(v["version"], CURRENT_VERSION);
        assert!(v["snapshot"].is_object(), "snapshot must be an object");
    }
}
