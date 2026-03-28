//! Desktop notifications with platform-specific backends.
//!
//! - **macOS**: Uses `terminal-notifier` when available (click opens terminal and switches
//!   tmux session). Falls back to `osascript` if not installed.
//! - **Linux**: Uses `notify-send` (libnotify) when available.
//! - **Other platforms**: Notifications are silently ignored.

use std::process::Command;

// ── macOS implementation ──────────────────────────────────────────────────────

#[cfg(target_os = "macos")]
use std::sync::OnceLock;

#[cfg(target_os = "macos")]
use crate::remote;

/// Cached check for terminal-notifier availability.
#[cfg(target_os = "macos")]
fn has_terminal_notifier() -> bool {
    static AVAILABLE: OnceLock<bool> = OnceLock::new();
    *AVAILABLE.get_or_init(|| {
        Command::new("which")
            .arg("terminal-notifier")
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
    })
}

/// Sends a desktop notification.
///
/// On macOS, when `terminal-notifier` is installed, clicking the notification
/// opens Warp and switches to the given tmux session. Falls back to `osascript`
/// if `terminal-notifier` is not available.
///
/// On Linux, uses `notify-send`. The `session_name` argument is ignored because
/// `notify-send` does not support action callbacks from the CLI.
///
/// On other platforms, this is a no-op.
pub fn send_notification(title: &str, message: &str) {
    send_notification_with_session(title, message, None);
}

/// Sends a notification with an optional tmux session to switch to on click.
///
/// On macOS with `terminal-notifier`, clicking the notification activates Warp
/// and runs `tmux switch-client -t <session_name>`.
///
/// On Linux, `session_name` is ignored.
///
/// On other platforms, this is a no-op.
#[cfg(target_os = "macos")]
pub fn send_notification_with_session(title: &str, message: &str, session_name: Option<&str>) {
    if has_terminal_notifier() {
        let mut args = vec![
            "-title".to_string(),
            title.to_string(),
            "-message".to_string(),
            message.to_string(),
            "-group".to_string(),
            "orchard".to_string(),
        ];

        if let Some(session) = session_name {
            // Click activates Warp and switches tmux to the session
            args.extend([
                "-activate".to_string(),
                "dev.warp.Warp-Stable".to_string(),
                "-execute".to_string(),
                format!("tmux switch-client -t {}", remote::shell_escape(session)),
            ]);
        } else {
            args.extend(["-activate".to_string(), "dev.warp.Warp-Stable".to_string()]);
        }

        let _ = Command::new("terminal-notifier").args(&args).output();
    } else {
        // Fallback: osascript (no click action)
        let script = format!(
            r#"display notification "{}" with title "{}""#,
            message.replace('"', r#"\""#),
            title.replace('"', r#"\""#),
        );
        let _ = Command::new("osascript").args(["-e", &script]).output();
    }
}

// ── Linux implementation ──────────────────────────────────────────────────────

/// Sends a notification using `notify-send` (libnotify).
///
/// Session switching is not supported on Linux via `notify-send`, so
/// `session_name` is ignored.
#[cfg(target_os = "linux")]
pub fn send_notification_with_session(title: &str, message: &str, _session_name: Option<&str>) {
    let _ = Command::new("notify-send")
        .args(["-a", "orchard", title, message])
        .output();
}

// ── Fallback (unsupported platforms) ─────────────────────────────────────────

/// No-op on unsupported platforms.
#[cfg(not(any(target_os = "macos", target_os = "linux")))]
pub fn send_notification_with_session(
    _title: &str,
    _message: &str,
    _session_name: Option<&str>,
) {
}

// ── Tests ─────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(target_os = "macos")]
    #[test]
    fn notification_message_escapes_quotes() {
        // Smoke test: verifies escaping logic doesn't panic.
        send_notification("Test \"title\"", "Test \"message\"");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn has_terminal_notifier_returns_bool() {
        // Just verify it doesn't panic
        let _ = has_terminal_notifier();
    }

    #[test]
    fn session_name_is_shell_escaped() {
        // Verify the execute arg uses shell_escape (not raw quotes)
        let session = "test'session";
        let escaped = crate::remote::shell_escape(session);
        let cmd = format!("tmux switch-client -t {}", escaped);
        assert_eq!(cmd, "tmux switch-client -t 'test'\\''session'");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_send_notification_with_session_ignores_session_name() {
        // Smoke test: verifies Linux path doesn't panic with or without session_name.
        send_notification("Title", "Message");
        send_notification_with_session("Title", "Message", Some("my-session"));
    }
}
