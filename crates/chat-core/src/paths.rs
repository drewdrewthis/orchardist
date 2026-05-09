//! Resolves the on-disk root for chat JSONL files.
//!
//! Default: `~/.orchard/chat/`. Overridable via `ORCHARD_CHAT_DIR` for tests
//! and isolated installs. Resolution and directory creation are eager — the
//! first call ensures the directory exists.

use anyhow::{Context, Result};
use std::path::{Path, PathBuf};

/// Returns the chat data directory, creating it if missing.
///
/// Honors `$ORCHARD_CHAT_DIR` first; falls back to `$HOME/.orchard/chat/`.
/// Returns an error if the directory cannot be created or the home directory
/// cannot be resolved.
pub fn chat_dir() -> Result<PathBuf> {
    let dir = if let Ok(custom) = std::env::var("ORCHARD_CHAT_DIR") {
        PathBuf::from(custom)
    } else {
        let home =
            std::env::var("HOME").context("HOME not set; cannot resolve default chat dir")?;
        PathBuf::from(home).join(".orchard").join("chat")
    };
    std::fs::create_dir_all(&dir)
        .with_context(|| format!("creating chat dir {}", dir.display()))?;
    Ok(dir)
}

/// Returns the path to a room's JSONL file, creating the parent dir.
///
/// `room` is a logical name (no leading `#`, no slashes). Direct-send rooms
/// like `@bob` keep the `@` literal in the filename — `paths::room_path("@bob")`
/// yields `…/chat/@bob.jsonl`.
pub fn room_path(room: &str) -> Result<PathBuf> {
    if room.contains('/') || room.contains('\\') {
        anyhow::bail!("invalid room name (no path separators): {room}");
    }
    Ok(chat_dir()?.join(format!("{room}.jsonl")))
}

/// Returns the path to a room's JSONL file, scoped to a specific dir.
///
/// Useful internally; tests typically just set `ORCHARD_CHAT_DIR`.
pub fn room_path_in(dir: &Path, room: &str) -> Result<PathBuf> {
    if room.contains('/') || room.contains('\\') {
        anyhow::bail!("invalid room name (no path separators): {room}");
    }
    Ok(dir.join(format!("{room}.jsonl")))
}
