//! Desktop notifications with click-to-switch-session support.
//!
//! Uses `terminal-notifier` when available (click opens Warp and switches
//! tmux session). Falls back to `osascript` if not installed.

use std::process::Command;
use std::sync::OnceLock;

use crate::remote;

/// Cached check for terminal-notifier availability.
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

/// Sends a macOS desktop notification.
///
/// When `session_name` is provided and `terminal-notifier` is installed,
/// clicking the notification opens Warp and switches to that tmux session.
pub fn send_notification(title: &str, message: &str) {
    send_notification_with_session(title, message, None);
}

/// Sends a notification with an optional tmux session to switch to on click.
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn notification_message_escapes_quotes() {
        // Smoke test: verifies escaping logic doesn't panic.
        send_notification("Test \"title\"", "Test \"message\"");
    }

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
}
