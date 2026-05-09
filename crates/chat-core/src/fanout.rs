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

/// One target of a fanout: a logical handle plus the tmux session to deliver to.
///
/// `handle` is what shows up in receipts and JSONL (`@alice`, `@drudrukungfu_2`).
/// `tmux_session` is the literal session name that `tmux send-keys -t` accepts —
/// dashes preserved, not slugified. The two diverge whenever a session has
/// characters `derive_handle` slugifies (dashes, dots, etc.), so the caller MUST
/// supply both. For room broadcasts, look these up via `chat_core::list_members`;
/// for direct `@handle` sends, the handle and session are the same string.
#[derive(Debug, Clone)]
pub struct Recipient {
    pub handle: String,
    pub tmux_session: String,
}

impl Recipient {
    pub fn new(handle: impl Into<String>, tmux_session: impl Into<String>) -> Self {
        Self {
            handle: handle.into(),
            tmux_session: tmux_session.into(),
        }
    }
}

/// How a `Delivered` outcome was verified.
///
/// - `Transcript` — strongest. Found the prefix as a `type:"user"` entry in
///   the recipient's Claude transcript JSONL. Means the REPL ingested it.
/// - `Scrollback` — weaker. Found the prefix in `tmux capture-pane -p`
///   output. Bytes hit the pane but we don't know if a Claude REPL accepted
///   it (vs e.g. a bash prompt that just echoed it).
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum VerifiedVia {
    Transcript,
    Scrollback,
}

/// Per-recipient outcome of a fanout.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum FanoutOutcome {
    /// `send-keys` succeeded AND a verifier (transcript or scrollback)
    /// confirmed the prefixed text landed at `verified_at`. `verified_via`
    /// records which mechanism succeeded — transcript is the stronger
    /// proof (recipient REPL ingested it); scrollback is the fallback.
    Delivered {
        recipient: String,
        verified_via: VerifiedVia,
        verified_at: String,
    },
    /// `send-keys` succeeded but every verifier timed out — bytes are in
    /// the input buffer but neither transcript nor scrollback confirmed
    /// the recipient processed them.
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
/// Each [`Recipient`] carries both the logical handle (used for receipts /
/// JSONL) and the literal tmux session to deliver to. The sender's own
/// handle is always skipped.
pub fn tmux_fanout(
    recipients: &[Recipient],
    sender: &str,
    text: &str,
) -> Vec<FanoutOutcome> {
    let paste = format_paste(sender, text);
    let sender_handle = sender.trim_start_matches('@');
    let mut out = Vec::with_capacity(recipients.len());
    for r in recipients {
        let recipient_handle = r.handle.trim_start_matches('@');
        let target_session = r.tmux_session.as_str();
        if target_session.is_empty() {
            out.push(FanoutOutcome::Skipped {
                recipient: r.handle.clone(),
                reason: "empty tmux session".to_string(),
            });
            continue;
        }
        if recipient_handle == sender_handle {
            out.push(FanoutOutcome::Skipped {
                recipient: r.handle.clone(),
                reason: "sender".to_string(),
            });
            continue;
        }
        // Step 1: send-keys. We don't pre-flight with `has-session` because
        // tmux's `-t <name>` resolution accepts session, window, or pane
        // targets, but `has-session` only matches sessions. If the target
        // doesn't resolve, `send-keys` itself returns a non-zero exit with a
        // clear error message — we propagate that.
        match send_keys(target_session, &paste) {
            Ok(()) => {}
            Err(e) => {
                // Normalize the most common case so callers can pattern-match
                // on it without parsing tmux's localized strings.
                let normalized = if e.contains("can't find") || e.contains("no current target") {
                    format!("no such tmux session: {target_session}")
                } else {
                    e
                };
                out.push(FanoutOutcome::Failed {
                    recipient: r.handle.clone(),
                    error: normalized,
                });
                continue;
            }
        }
        // Step 2: verify ladder.
        //
        // Try the transcript first — strongest proof, robust against the
        // active-Claude-REPL redraw that breaks scrollback verify. If the
        // recipient isn't a Claude REPL (or its transcript can't be located
        // within the budget), fall back to scrollback verify. If both
        // fail, ByteOnly.
        let outcome = match crate::transcript::verify_via_transcript(
            target_session,
            &paste,
            std::time::Duration::from_millis(2_000),
        ) {
            Ok(verified_at) => FanoutOutcome::Delivered {
                recipient: r.handle.clone(),
                verified_via: VerifiedVia::Transcript,
                verified_at,
            },
            Err(_) => match verify_scrollback(target_session, &paste) {
                Ok(verified_at) => FanoutOutcome::Delivered {
                    recipient: r.handle.clone(),
                    verified_via: VerifiedVia::Scrollback,
                    verified_at,
                },
                Err(reason) => FanoutOutcome::ByteOnly {
                    recipient: r.handle.clone(),
                    reason,
                },
            },
        };
        out.push(outcome);
    }
    out
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
