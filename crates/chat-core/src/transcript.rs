//! Level 3 receipts via Claude transcript JSONL.
//!
//! `tmux capture-pane` verification has a known failure mode against active
//! Claude REPLs: the pane is constantly redrawing, so the prefix our paste
//! contains can be scrolled off / repainted before the verify retry window
//! closes. Result: false-negative `ByteOnly` outcomes when the message
//! actually landed.
//!
//! This module gives us a stronger receipt path: read the recipient's
//! Claude transcript JSONL and look for a `type: "user"` entry whose
//! `message.content` (string form) contains our prefix. The transcript is
//! append-only and authoritative — once the entry is there, the REPL has
//! ingested the message.
//!
//! # Path discovery
//!
//! Claude Code stores transcripts at:
//!
//! ```text
//! ~/.claude/projects/<slug>/<session_uuid>.jsonl
//! ```
//!
//! Where `<slug>` is the recipient's working directory with `/` and `.`
//! replaced by `-`. The recipient's working directory is fetched from
//! tmux (`tmux display-message -p -t <session> '#{pane_current_path}'`).
//! Multiple `.jsonl` files may exist for one project (one per Claude
//! session that ever ran there); we use the most recently modified.
//!
//! # Limitations
//!
//! - Only works when the recipient is a Claude Code REPL — `bash`, `vim`,
//!   etc. don't write transcripts. Callers fall back to capture-pane verify.
//! - Slight latency: Claude buffers transcript writes (~100-500ms typical).
//!   The verify deadline accommodates this.
//! - Cross-host: the transcript is local to the recipient. v1 only verifies
//!   same-machine targets; cross-machine receipt is a v2 problem (likely via
//!   the daemon exposing transcript reads over the federation).

use std::path::PathBuf;
use std::process::Command;
use std::time::{Duration, Instant};

/// Try to verify that the recipient's Claude REPL has ingested a message
/// containing `needle` in its transcript. Returns the RFC3339 timestamp of
/// successful verify, or an error reason on timeout / discovery failure.
///
/// `tmux_session` is the literal target name (the same one passed to
/// `send-keys -t`). The function shells out to tmux to resolve the recipient
/// pane's `pane_current_path`, slugifies it, finds the freshest `.jsonl` in
/// `~/.claude/projects/<slug>/`, and tail-polls it for a `type:"user"` row
/// whose content contains `needle`.
pub fn verify_via_transcript(
    tmux_session: &str,
    needle: &str,
    deadline: Duration,
) -> Result<String, String> {
    let cwd = pane_current_path(tmux_session)
        .ok_or_else(|| "could not resolve recipient cwd".to_string())?;
    let slug = slugify_path(&cwd);
    let project_dir = home_dir()
        .ok_or_else(|| "HOME not set".to_string())?
        .join(".claude")
        .join("projects")
        .join(&slug);
    if !project_dir.exists() {
        return Err(format!(
            "no Claude transcript dir for recipient: {}",
            project_dir.display()
        ));
    }

    let stop_at = Instant::now() + deadline;
    let mut sleep_ms = 100u64;
    loop {
        if let Some(path) = newest_jsonl(&project_dir)
            && let Ok(verified) = scan_for_user_message(&path, needle)
            && verified
        {
            use time::OffsetDateTime;
            use time::format_description::well_known::Rfc3339;
            let ts = OffsetDateTime::now_utc()
                .format(&Rfc3339)
                .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string());
            return Ok(ts);
        }
        if Instant::now() >= stop_at {
            return Err("transcript verify timeout".to_string());
        }
        std::thread::sleep(Duration::from_millis(sleep_ms));
        sleep_ms = (sleep_ms * 2).min(400);
    }
}

/// Resolve the recipient's working directory by querying tmux.
fn pane_current_path(tmux_session: &str) -> Option<String> {
    let out = Command::new("tmux")
        .args([
            "display-message",
            "-p",
            "-t",
            tmux_session,
            "#{pane_current_path}",
        ])
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let s = String::from_utf8_lossy(&out.stdout).trim().to_string();
    if s.is_empty() { None } else { Some(s) }
}

/// Map a path to Claude Code's project-dir slug.
///
/// Rule (matches what Claude Code itself does): replace every `/` and `.`
/// with `-`. Leading separator from absolute paths becomes a leading `-`.
/// Examples:
/// - `/home/user/orchard` → `-home-user-orchard`
/// - `/x/.claude/y` → `-x--claude-y`
pub fn slugify_path(path: &str) -> String {
    let mut out = String::with_capacity(path.len());
    for ch in path.chars() {
        match ch {
            '/' | '.' => out.push('-'),
            other => out.push(other),
        }
    }
    out
}

fn newest_jsonl(dir: &std::path::Path) -> Option<PathBuf> {
    let mut newest: Option<(PathBuf, std::time::SystemTime)> = None;
    let entries = std::fs::read_dir(dir).ok()?;
    for entry in entries.flatten() {
        let path = entry.path();
        if path.extension().and_then(|s| s.to_str()) != Some("jsonl") {
            continue;
        }
        if let Ok(meta) = entry.metadata()
            && let Ok(mtime) = meta.modified()
        {
            match &newest {
                Some((_, prev)) if *prev >= mtime => {}
                _ => newest = Some((path, mtime)),
            }
        }
    }
    newest.map(|(p, _)| p)
}

/// Scan a JSONL file for a `type:"user"` entry whose message content
/// contains `needle`. Tail-only: skip lines that don't parse / aren't user.
///
/// Returns `Ok(true)` if a match is found. `Ok(false)` if no match. `Err`
/// on read errors that prevent any progress.
fn scan_for_user_message(path: &std::path::Path, needle: &str) -> std::io::Result<bool> {
    use std::io::{BufRead, BufReader};
    let f = std::fs::File::open(path)?;
    for line in BufReader::new(f).lines() {
        let line = line?;
        if line.is_empty() {
            continue;
        }
        // Cheap pre-filter: if the needle isn't anywhere in the line, skip
        // JSON parsing entirely.
        if !line.contains(needle) {
            continue;
        }
        let v: serde_json::Value = match serde_json::from_str(&line) {
            Ok(v) => v,
            Err(_) => continue,
        };
        if v.get("type").and_then(|t| t.as_str()) != Some("user") {
            continue;
        }
        let content = v.get("message").and_then(|m| m.get("content"));
        if content_contains(content, needle) {
            return Ok(true);
        }
    }
    Ok(false)
}

fn content_contains(content: Option<&serde_json::Value>, needle: &str) -> bool {
    match content {
        Some(serde_json::Value::String(s)) => s.contains(needle),
        Some(serde_json::Value::Array(parts)) => parts.iter().any(|p| {
            p.get("text")
                .and_then(|t| t.as_str())
                .map(|s| s.contains(needle))
                .unwrap_or(false)
        }),
        _ => false,
    }
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME").map(PathBuf::from)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn slugify_replaces_slashes_and_dots() {
        assert_eq!(slugify_path("/home/user/orchard"), "-home-user-orchard");
        assert_eq!(slugify_path("/x/.claude/y"), "-x--claude-y");
        assert_eq!(slugify_path("relative/path"), "relative-path");
        assert_eq!(slugify_path(""), "");
    }

    #[test]
    fn content_contains_matches_string_form() {
        let v = serde_json::json!("[14:07:22] @alice: hello");
        assert!(content_contains(Some(&v), "@alice"));
        assert!(!content_contains(Some(&v), "@bob"));
    }

    #[test]
    fn content_contains_matches_array_form() {
        let v = serde_json::json!([
            {"type": "text", "text": "intro"},
            {"type": "text", "text": "[14:07:22] @alice: hello"},
        ]);
        assert!(content_contains(Some(&v), "@alice"));
    }

    #[test]
    fn content_contains_handles_none() {
        assert!(!content_contains(None, "x"));
    }

    #[test]
    fn scan_finds_matching_user_line_and_skips_others() {
        let td = tempfile::tempdir().expect("tempdir");
        let path = td.path().join("session.jsonl");
        let lines = [
            r#"{"type":"summary","summary":"foo"}"#,
            r#"{"type":"assistant","message":{"role":"assistant","content":"hi"}}"#,
            r#"{"type":"user","message":{"role":"user","content":"intro"}}"#,
            r#"{"type":"user","message":{"role":"user","content":"[14:07:22] @alice: payload"}}"#,
        ];
        std::fs::write(&path, lines.join("\n") + "\n").unwrap();

        assert!(scan_for_user_message(&path, "@alice: payload").unwrap());
        assert!(!scan_for_user_message(&path, "@bob: payload").unwrap());
    }

    #[test]
    fn newest_jsonl_picks_latest_mtime() {
        let td = tempfile::tempdir().expect("tempdir");
        let old_path = td.path().join("a.jsonl");
        let new_path = td.path().join("b.jsonl");
        std::fs::write(&old_path, "{}\n").unwrap();
        std::thread::sleep(Duration::from_millis(20));
        std::fs::write(&new_path, "{}\n").unwrap();

        let picked = newest_jsonl(td.path()).expect("found a jsonl");
        assert_eq!(picked, new_path);
    }

    #[test]
    fn newest_jsonl_ignores_non_jsonl() {
        let td = tempfile::tempdir().expect("tempdir");
        std::fs::write(td.path().join("a.jsonl"), "{}\n").unwrap();
        std::fs::write(td.path().join("note.txt"), "ignore me\n").unwrap();
        let picked = newest_jsonl(td.path()).expect("found a jsonl");
        assert_eq!(picked, td.path().join("a.jsonl"));
    }
}
