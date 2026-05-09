//! tmux fanout with Level 2 receipts.
//!
//! Two-step delivery:
//! 1. `tmux send-keys -t <session> -l <prefixed-text>` then `tmux send-keys
//!    -t <session> Enter` — bytes-delivered.
//! 2. `tmux capture-pane -p -t <session>` with sleep-and-retry — verifies
//!    the prefixed message appears in the pane scrollback.
//!
//! Step 1 returning 0 only proves bytes-delivered to the input buffer;
//! recipients can have stacked input that swallows our line. Step 2 is the
//! real receipt.

use serde::Serialize;
use std::process::Command;
use std::time::{Duration, Instant};

/// Per-recipient outcome of a fanout.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum FanoutOutcome {
    /// `send-keys` succeeded AND `capture-pane` confirmed the prefixed text
    /// landed in the recipient's scrollback by `scrollback_verified_at`.
    Delivered {
        recipient: String,
        scrollback_verified_at: String,
    },
    /// `send-keys` succeeded but scrollback verify timed out — bytes are in
    /// the input buffer but the recipient hasn't visibly processed them.
    ByteOnly { recipient: String, reason: String },
    /// `send-keys` itself errored (e.g. no such session). The message has
    /// NOT reached the recipient's input buffer.
    Failed { recipient: String, error: String },
    /// Skipped silently (sender's own pane, offline member, etc.).
    Skipped { recipient: String, reason: String },
}

impl FanoutOutcome {
    pub fn recipient(&self) -> &str {
        match self {
            FanoutOutcome::Delivered { recipient, .. }
            | FanoutOutcome::ByteOnly { recipient, .. }
            | FanoutOutcome::Failed { recipient, .. }
            | FanoutOutcome::Skipped { recipient, .. } => recipient,
        }
    }

    pub fn is_delivered(&self) -> bool {
        matches!(self, FanoutOutcome::Delivered { .. })
    }
}

/// The prefix used in fanout output. `[<ts>] @<sender>: <text>` is consistent
/// across room broadcasts and direct sends.
pub fn format_paste(sender: &str, text: &str) -> String {
    let ts = short_ts();
    let sender_at = if sender.starts_with('@') {
        sender.to_string()
    } else {
        format!("@{sender}")
    };
    format!("[{ts}] {sender_at}: {text}")
}

/// Send `text` to every recipient via tmux send-keys, with Level 2 receipt
/// verification.
///
/// `recipients` is a list of handles. Each handle maps directly to a tmux
/// session of the same name (without the `@` sigil). The sender's own handle
/// is always skipped.
pub fn tmux_fanout(
    recipients: &[String],
    sender: &str,
    text: &str,
) -> Vec<FanoutOutcome> {
    let paste = format_paste(sender, text);
    let sender_session = sender.trim_start_matches('@');
    let mut out = Vec::with_capacity(recipients.len());
    for r in recipients {
        let target_session = r.trim_start_matches('@');
        if target_session.is_empty() {
            out.push(FanoutOutcome::Skipped {
                recipient: r.clone(),
                reason: "empty handle".to_string(),
            });
            continue;
        }
        if target_session == sender_session {
            out.push(FanoutOutcome::Skipped {
                recipient: r.clone(),
                reason: "sender".to_string(),
            });
            continue;
        }
        if !tmux_session_exists(target_session) {
            out.push(FanoutOutcome::Failed {
                recipient: r.clone(),
                error: "no such tmux session".to_string(),
            });
            continue;
        }
        // Step 1: send-keys.
        match send_keys(target_session, &paste) {
            Ok(()) => {}
            Err(e) => {
                out.push(FanoutOutcome::Failed {
                    recipient: r.clone(),
                    error: e,
                });
                continue;
            }
        }
        // Step 2: scrollback verify.
        match verify_scrollback(target_session, &paste) {
            Ok(verified_at) => out.push(FanoutOutcome::Delivered {
                recipient: r.clone(),
                scrollback_verified_at: verified_at,
            }),
            Err(reason) => out.push(FanoutOutcome::ByteOnly {
                recipient: r.clone(),
                reason,
            }),
        }
    }
    out
}

fn tmux_session_exists(session: &str) -> bool {
    Command::new("tmux")
        .args(["has-session", "-t", session])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Send a literal line + Enter via `tmux send-keys`.
///
/// Two invocations: one with `-l` for the literal text (no key translation),
/// then a second sending `Enter`. This avoids `;` / `$` / etc. being
/// interpreted as tmux command separators.
fn send_keys(session: &str, paste: &str) -> Result<(), String> {
    let out = Command::new("tmux")
        .args(["send-keys", "-l", "-t", session, paste])
        .output()
        .map_err(|e| format!("invoking tmux send-keys: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "tmux send-keys failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    let out2 = Command::new("tmux")
        .args(["send-keys", "-t", session, "Enter"])
        .output()
        .map_err(|e| format!("invoking tmux send-keys Enter: {e}"))?;
    if !out2.status.success() {
        return Err(format!(
            "tmux send-keys Enter failed: {}",
            String::from_utf8_lossy(&out2.stderr).trim()
        ));
    }
    Ok(())
}

/// Verify the prefix appears in the recipient's pane scrollback, retrying
/// for up to ~500ms total.
///
/// Returns the RFC3339 timestamp at which verification succeeded. On
/// timeout, returns `Err("scrollback verify timeout")`.
fn verify_scrollback(session: &str, paste: &str) -> Result<String, String> {
    let deadline = Instant::now() + Duration::from_millis(500);
    let mut sleep_ms = 30u64;
    loop {
        let out = Command::new("tmux")
            .args(["capture-pane", "-p", "-t", session])
            .output()
            .map_err(|e| format!("invoking tmux capture-pane: {e}"))?;
        if out.status.success() {
            let captured = String::from_utf8_lossy(&out.stdout);
            if captured.contains(paste) {
                use time::format_description::well_known::Rfc3339;
                use time::OffsetDateTime;
                let ts = OffsetDateTime::now_utc()
                    .format(&Rfc3339)
                    .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string());
                return Ok(ts);
            }
        }
        if Instant::now() >= deadline {
            return Err("scrollback verify timeout".to_string());
        }
        std::thread::sleep(Duration::from_millis(sleep_ms));
        sleep_ms = (sleep_ms * 2).min(150);
    }
}

fn short_ts() -> String {
    // HH:MM:SS for human-readable scrollback. Full RFC3339 lives in JSONL.
    use time::format_description::FormatItem;
    use time::macros::format_description;
    use time::OffsetDateTime;
    const FMT: &[FormatItem<'_>] = format_description!("[hour]:[minute]:[second]");
    OffsetDateTime::now_utc()
        .format(&FMT)
        .unwrap_or_else(|_| "??:??:??".to_string())
}
