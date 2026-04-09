//! Tauri command implementations for the Orchard GUI session deep view.
//!
//! Exposes three commands to the webview:
//!
//! - [`list_sessions`] — enumerates Claude Code session JSONL files under
//!   `~/.claude/projects/` sorted by most-recently-modified.
//! - [`read_session`] — reads a session JSONL file and returns each line parsed
//!   as a [`serde_json::Value`] so the frontend can filter and render blocks.
//! - [`send_to_tmux`] — writes a literal string into a target tmux session via
//!   `tmux send-keys -l`.
//!
//! The tmux command runs an external binary; to keep it testable it is
//! parameterised by a [`Shell`] trait. Tests inject a `MockShell` that records
//! invocations instead of spawning a real process.

use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

/// Metadata for a single Claude Code session JSONL file.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct SessionMeta {
    /// Absolute path to the `.jsonl` file on disk.
    pub path: String,
    /// File name without extension — Claude's internal session id.
    pub session_id: String,
    /// Claude projects sub-directory this session lives in (slug of the cwd).
    pub project_slug: String,
    /// File size in bytes.
    pub size_bytes: u64,
    /// Last-modified time as Unix seconds. `0` if unavailable.
    pub modified_unix: i64,
}

/// Abstraction over spawning a shell command. Real code uses [`RealShell`];
/// tests use a mock that captures invocations without executing anything.
pub trait Shell: Send + Sync {
    /// Run `program` with `args`, returning the process exit status as an `i32`.
    /// Non-zero exit codes are surfaced to the caller rather than turned into
    /// errors — the tmux commands we issue are advisory.
    fn run(&self, program: &str, args: &[&str]) -> anyhow::Result<i32>;
}

/// Default [`Shell`] implementation that actually spawns subprocesses.
pub struct RealShell;

impl Shell for RealShell {
    fn run(&self, program: &str, args: &[&str]) -> anyhow::Result<i32> {
        let status = Command::new(program).args(args).status()?;
        Ok(status.code().unwrap_or(-1))
    }
}

/// Returns the root directory containing Claude Code session JSONL files —
/// `$HOME/.claude/projects` by default.
fn claude_projects_root() -> anyhow::Result<PathBuf> {
    let home = dirs::home_dir().ok_or_else(|| anyhow::anyhow!("no home directory"))?;
    Ok(home.join(".claude").join("projects"))
}

/// Lists Claude Code session JSONL files discovered under `root`, sorted by
/// most-recently-modified first. Pure function — takes the root path so tests
/// can point it at a temp directory.
pub fn list_sessions_in(root: &Path) -> anyhow::Result<Vec<SessionMeta>> {
    let mut out = Vec::new();
    if !root.exists() {
        return Ok(out);
    }
    for project_entry in fs::read_dir(root)? {
        let project_entry = project_entry?;
        if !project_entry.file_type()?.is_dir() {
            continue;
        }
        let project_slug = project_entry.file_name().to_string_lossy().into_owned();
        for session_entry in fs::read_dir(project_entry.path())? {
            let session_entry = session_entry?;
            let path = session_entry.path();
            if path.extension().and_then(|e| e.to_str()) != Some("jsonl") {
                continue;
            }
            let meta = session_entry.metadata()?;
            let modified_unix = meta
                .modified()
                .ok()
                .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
                .map(|d| d.as_secs() as i64)
                .unwrap_or(0);
            let session_id = path
                .file_stem()
                .and_then(|s| s.to_str())
                .unwrap_or("")
                .to_string();
            out.push(SessionMeta {
                path: path.to_string_lossy().into_owned(),
                session_id,
                project_slug: project_slug.clone(),
                size_bytes: meta.len(),
                modified_unix,
            });
        }
    }
    out.sort_by(|a, b| b.modified_unix.cmp(&a.modified_unix));
    Ok(out)
}

/// Tauri command: list Claude Code sessions under `~/.claude/projects/`.
#[tauri::command]
pub fn list_sessions() -> Result<Vec<SessionMeta>, String> {
    let root = claude_projects_root().map_err(|e| e.to_string())?;
    list_sessions_in(&root).map_err(|e| e.to_string())
}

/// Reads a Claude Code session JSONL file and returns one `serde_json::Value`
/// per non-empty line. Lines that fail to parse are skipped — the v0 GUI is
/// read-only so lenient parsing is preferable to hard-failing a whole session.
pub fn read_session_file(path: &Path) -> anyhow::Result<Vec<serde_json::Value>> {
    let raw = fs::read_to_string(path)?;
    let mut out = Vec::new();
    for line in raw.lines() {
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        if let Ok(value) = serde_json::from_str::<serde_json::Value>(trimmed) {
            out.push(value);
        }
    }
    Ok(out)
}

/// Tauri command: read the JSONL file at `path` and return its parsed events.
#[tauri::command]
pub fn read_session(path: String) -> Result<Vec<serde_json::Value>, String> {
    read_session_file(Path::new(&path)).map_err(|e| e.to_string())
}

/// Sends `text` to the given tmux target using `tmux send-keys -l` via the
/// provided [`Shell`]. The `-l` flag disables key-name lookup so the text is
/// written literally. Multi-line strings are passed through as-is; handling
/// the newline-vs-paste-buffer tradeoff is deferred to a later commit.
pub fn send_to_tmux_with<S: Shell>(
    shell: &S,
    target: &str,
    text: &str,
) -> anyhow::Result<i32> {
    shell.run("tmux", &["send-keys", "-t", target, "-l", text])
}

/// Tauri command: write `text` into the tmux session at `target`.
#[tauri::command]
pub fn send_to_tmux(target: String, text: String) -> Result<(), String> {
    let code = send_to_tmux_with(&RealShell, &target, &text).map_err(|e| e.to_string())?;
    if code == 0 {
        Ok(())
    } else {
        Err(format!("tmux send-keys exited with code {code}"))
    }
}

// Tests live in ../tests/commands.rs as integration tests because a Tauri lib
// with cdylib+staticlib crate types does not generate inner #[cfg(test)] test
// binaries reliably. See #161 follow-up.
