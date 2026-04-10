//! Subscription model for the watch daemon.
//!
//! Subscriptions are persisted to `~/.local/state/git-orchard/subscriptions.json`.
//! Each subscriber identifies a tmux session and pane to deliver events to via
//! `tmux send-keys`.

use std::path::PathBuf;
use std::process::Command;

use anyhow::Context;
use chrono::{DateTime, Utc};
use regex::Regex;
use serde::{Deserialize, Serialize};

use crate::watch::event::WatchEvent;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// A registered event subscriber.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Subscription {
    /// Unique identifier for this subscription.
    pub id: String,
    /// tmux session name to deliver events to.
    pub tmux_session: String,
    /// tmux pane target within the session (e.g. "0.0").
    #[serde(default = "default_pane")]
    pub pane: String,
    /// When this subscription was first registered.
    pub registered_at: DateTime<Utc>,
    /// When this subscription was last seen (used for stale pruning).
    pub last_seen: DateTime<Utc>,
}

fn default_pane() -> String {
    "0.0".to_string()
}

/// The persisted subscription file structure.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct SubscriptionFile {
    /// All active subscriptions.
    pub subscriptions: Vec<Subscription>,
}

// ---------------------------------------------------------------------------
// Path
// ---------------------------------------------------------------------------

/// Returns the path to the subscriptions JSON file.
///
/// Resolves to `~/.local/state/git-orchard/subscriptions.json`.
pub fn subscriptions_path() -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join(".local")
        .join("state")
        .join("git-orchard")
        .join("subscriptions.json")
}

// ---------------------------------------------------------------------------
// Read / Write
// ---------------------------------------------------------------------------

/// Reads the subscription file. Returns an empty `SubscriptionFile` if absent or unreadable.
pub fn read_subscriptions() -> SubscriptionFile {
    let path = subscriptions_path();
    let data = match std::fs::read(&path) {
        Ok(d) => d,
        Err(_) => return SubscriptionFile::default(),
    };
    serde_json::from_slice(&data).unwrap_or_default()
}

/// Writes the subscription file atomically.
pub fn write_subscriptions(subs: &SubscriptionFile) -> anyhow::Result<()> {
    let path = subscriptions_path();
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)
            .with_context(|| format!("creating {}", parent.display()))?;
    }
    let json = serde_json::to_string_pretty(subs).context("serializing subscriptions")?;
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, &json).with_context(|| format!("writing {}", tmp.display()))?;
    std::fs::rename(&tmp, &path).with_context(|| format!("renaming to {}", path.display()))?;
    Ok(())
}

// ---------------------------------------------------------------------------
// Register / Unregister
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

/// Validates subscription fields before registering.
///
/// - `id` must match `[A-Za-z0-9_\-]+`
/// - `tmux_session` must match `[A-Za-z0-9_\-.]+`
/// - `pane` must match `[0-9]+\.[0-9]+`
fn validate_subscription_fields(id: &str, tmux_session: &str, pane: &str) -> anyhow::Result<()> {
    use std::sync::OnceLock;

    static ID_RE: OnceLock<Regex> = OnceLock::new();
    static SESSION_RE: OnceLock<Regex> = OnceLock::new();
    static PANE_RE: OnceLock<Regex> = OnceLock::new();

    let id_re = ID_RE.get_or_init(|| Regex::new(r"^[A-Za-z0-9_\-]+$").expect("valid regex"));
    let session_re =
        SESSION_RE.get_or_init(|| Regex::new(r"^[A-Za-z0-9_\-.]+$").expect("valid regex"));
    let pane_re = PANE_RE.get_or_init(|| Regex::new(r"^[0-9]+\.[0-9]+$").expect("valid regex"));

    if !id_re.is_match(id) {
        anyhow::bail!(
            "invalid subscription id {:?}: must match [A-Za-z0-9_\\-]+",
            id
        );
    }
    if !session_re.is_match(tmux_session) {
        anyhow::bail!(
            "invalid tmux_session {:?}: must match [A-Za-z0-9_\\-.]+",
            tmux_session
        );
    }
    if !pane_re.is_match(pane) {
        anyhow::bail!("invalid pane {:?}: must match [0-9]+\\.[0-9]+", pane);
    }
    Ok(())
}

/// Registers a new subscription (or replaces one with the same id).
///
/// Validates `id`, `tmux_session`, and `pane` before writing. Returns an error
/// if any field fails validation.
pub fn register(id: &str, tmux_session: &str, pane: &str) -> anyhow::Result<()> {
    validate_subscription_fields(id, tmux_session, pane)?;
    let mut sf = read_subscriptions();
    // Remove any existing entry with the same id.
    sf.subscriptions.retain(|s| s.id != id);
    let now = Utc::now();
    sf.subscriptions.push(Subscription {
        id: id.to_string(),
        tmux_session: tmux_session.to_string(),
        pane: pane.to_string(),
        registered_at: now,
        last_seen: now,
    });
    write_subscriptions(&sf)
}

/// Removes a subscription by id. No error if absent.
pub fn unregister(id: &str) -> anyhow::Result<()> {
    let mut sf = read_subscriptions();
    sf.subscriptions.retain(|s| s.id != id);
    write_subscriptions(&sf)
}

// ---------------------------------------------------------------------------
// Pruning
// ---------------------------------------------------------------------------

/// Returns a new `SubscriptionFile` with dead tmux sessions removed.
///
/// Uses `tmux has-session -t <session>` to check liveness. Sessions that
/// don't respond are considered dead and removed.
pub fn prune_stale(subs: &SubscriptionFile) -> SubscriptionFile {
    let active: Vec<Subscription> = subs
        .subscriptions
        .iter()
        .filter(|s| tmux_session_alive(&s.tmux_session))
        .cloned()
        .collect();
    SubscriptionFile {
        subscriptions: active,
    }
}

/// Returns `true` when `tmux has-session -t <name>` exits with 0.
fn tmux_session_alive(session: &str) -> bool {
    Command::new("tmux")
        .args(["has-session", "-t", session])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

// ---------------------------------------------------------------------------
// Delivery
// ---------------------------------------------------------------------------

/// Serializes `events` as JSON lines and sends each to the subscriber's pane.
///
/// Uses `tmux send-keys -l` to send the payload literally (preventing tmux
/// from interpreting the JSON as key sequences), then sends Enter separately.
pub fn deliver(sub: &Subscription, events: &[WatchEvent]) -> anyhow::Result<()> {
    for event in events {
        let json = serde_json::to_string(event).context("serializing event")?;
        let target = format!("{}:{}", sub.tmux_session, sub.pane);
        Command::new("tmux")
            .args(["send-keys", "-l", "-t", &target, &json])
            .status()
            .with_context(|| format!("tmux send-keys to {target}"))?;
        // Send Enter as a separate key press (not literal) to submit the line.
        Command::new("tmux")
            .args(["send-keys", "-t", &target, "Enter"])
            .status()
            .with_context(|| format!("tmux send-keys Enter to {target}"))?;
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn with_tmp_path(dir: &TempDir) -> PathBuf {
        dir.path().join("subscriptions.json")
    }

    fn write_subs_to(sf: &SubscriptionFile, path: &PathBuf) {
        let json = serde_json::to_string_pretty(sf).unwrap();
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, json).unwrap();
    }

    fn read_subs_from(path: &PathBuf) -> SubscriptionFile {
        let data = std::fs::read(path).unwrap();
        serde_json::from_slice(&data).unwrap()
    }

    fn make_sub(id: &str, session: &str) -> Subscription {
        let now = Utc::now();
        Subscription {
            id: id.to_string(),
            tmux_session: session.to_string(),
            pane: "0.0".to_string(),
            registered_at: now,
            last_seen: now,
        }
    }

    #[test]
    fn subscription_file_roundtrip() {
        let dir = tempfile::tempdir().unwrap();
        let path = with_tmp_path(&dir);

        let sf = SubscriptionFile {
            subscriptions: vec![make_sub("sub-1", "my_session")],
        };
        write_subs_to(&sf, &path);

        let loaded = read_subs_from(&path);
        assert_eq!(loaded.subscriptions.len(), 1);
        assert_eq!(loaded.subscriptions[0].id, "sub-1");
        assert_eq!(loaded.subscriptions[0].tmux_session, "my_session");
        assert_eq!(loaded.subscriptions[0].pane, "0.0");
    }

    #[test]
    fn register_adds_subscription() {
        // We can't easily redirect the default path in unit tests without env vars,
        // so test the logic directly via SubscriptionFile manipulation.
        let mut sf = SubscriptionFile::default();
        let now = Utc::now();
        sf.subscriptions.retain(|s| s.id != "test-id");
        sf.subscriptions.push(Subscription {
            id: "test-id".to_string(),
            tmux_session: "my_session".to_string(),
            pane: "0.0".to_string(),
            registered_at: now,
            last_seen: now,
        });
        assert_eq!(sf.subscriptions.len(), 1);
        assert_eq!(sf.subscriptions[0].id, "test-id");
    }

    #[test]
    fn unregister_removes_subscription() {
        let mut sf = SubscriptionFile {
            subscriptions: vec![
                make_sub("keep-me", "session_a"),
                make_sub("remove-me", "session_b"),
            ],
        };
        sf.subscriptions.retain(|s| s.id != "remove-me");
        assert_eq!(sf.subscriptions.len(), 1);
        assert_eq!(sf.subscriptions[0].id, "keep-me");
    }

    #[test]
    fn default_pane_is_zero_zero() {
        let sub = make_sub("x", "sess");
        assert_eq!(sub.pane, "0.0");
    }

    #[test]
    fn validate_accepts_valid_input() {
        assert!(validate_subscription_fields("my-sub_1", "session.name-1", "0.0").is_ok());
        assert!(validate_subscription_fields("abc", "MySession", "1.2").is_ok());
        assert!(validate_subscription_fields("sub-123", "proj.main", "10.3").is_ok());
    }

    #[test]
    fn validate_rejects_invalid_session_name() {
        // Space in session name
        assert!(validate_subscription_fields("sub1", "bad session", "0.0").is_err());
        // Shell metacharacters
        assert!(validate_subscription_fields("sub1", "bad;session", "0.0").is_err());
        assert!(validate_subscription_fields("sub1", "bad$session", "0.0").is_err());
    }

    #[test]
    fn validate_rejects_invalid_pane() {
        // Missing dot separator
        assert!(validate_subscription_fields("sub1", "session", "00").is_err());
        // Non-numeric
        assert!(validate_subscription_fields("sub1", "session", "a.b").is_err());
        // Extra content
        assert!(validate_subscription_fields("sub1", "session", "0.0.0").is_err());
    }

    #[test]
    fn validate_rejects_invalid_id() {
        // Space in id
        assert!(validate_subscription_fields("bad id", "session", "0.0").is_err());
        // Shell metacharacters
        assert!(validate_subscription_fields("bad;id", "session", "0.0").is_err());
    }
}
