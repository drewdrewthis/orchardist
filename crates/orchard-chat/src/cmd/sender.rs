//! Sender resolution helper.
//!
//! Per #495 AC-8:
//! 1. Explicit `--as <handle>` flag wins.
//! 2. Otherwise auto-derive from `$TMUX` / current tmux session name.
//! 3. Otherwise fail loudly with exit code 3.

use std::process::Command;

use anyhow::Result;

/// Resolve the sender's handle.
///
/// `as_override` is the value of `--as` (already stripped of leading `@` or
/// not — we accept both). Returns the handle WITH a leading `@` so JSONL
/// entries are uniform.
pub fn resolve(as_override: Option<&str>) -> Result<String> {
    if let Some(h) = as_override {
        return Ok(normalize(h));
    }
    if let Some(h) = current_tmux_session() {
        return Ok(format!("@{}", chat_core::derive_handle(&h, None)));
    }
    Err(anyhow::anyhow!(
        "cannot derive sender (not in a tmux session); use --as <handle>"
    ))
}

fn normalize(h: &str) -> String {
    if h.starts_with('@') {
        h.to_string()
    } else {
        format!("@{h}")
    }
}

fn current_tmux_session() -> Option<String> {
    if std::env::var_os("TMUX").is_none() {
        return None;
    }
    let out = Command::new("tmux")
        .args(["display-message", "-p", "#S"])
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    let s = String::from_utf8_lossy(&out.stdout).trim().to_string();
    if s.is_empty() { None } else { Some(s) }
}
