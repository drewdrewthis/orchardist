//! Desktop notifications with platform-specific backends.
//!
//! - **macOS**: Uses `terminal-notifier` when available (click activates the
//!   configured terminal app and switches the tmux session). Falls back to
//!   `osascript` if not installed. The terminal app bundle ID is supplied by
//!   the caller from `GlobalConfig::terminal_app`.
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

/// Builds the argument list for `terminal-notifier`.
///
/// Extracted for testability — the returned `Vec<String>` can be asserted
/// without executing an external process.
#[cfg(target_os = "macos")]
pub fn build_notification_args(
    title: &str,
    message: &str,
    session_name: Option<&str>,
    terminal_app: &str,
) -> Vec<String> {
    let mut args = vec![
        "-title".to_string(),
        title.to_string(),
        "-message".to_string(),
        message.to_string(),
        "-group".to_string(),
        "orchard".to_string(),
    ];

    if let Some(session) = session_name {
        args.extend([
            "-activate".to_string(),
            terminal_app.to_string(),
            "-execute".to_string(),
            format!("tmux switch-client -t {}", remote::shell_escape(session)),
        ]);
    } else {
        args.extend(["-activate".to_string(), terminal_app.to_string()]);
    }

    args
}

/// Sends a notification with an optional tmux session to switch to on click.
///
/// On macOS with `terminal-notifier`, clicking the notification activates
/// `terminal_app` and runs `tmux switch-client -t <session_name>`.
///
/// On Linux, `session_name` and `terminal_app` are ignored.
///
/// On other platforms, this is a no-op.
#[cfg(target_os = "macos")]
pub fn send_notification_with_session(
    title: &str,
    message: &str,
    session_name: Option<&str>,
    terminal_app: &str,
) {
    if has_terminal_notifier() {
        let args = build_notification_args(title, message, session_name, terminal_app);
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
/// `session_name` and `terminal_app` are ignored.
#[cfg(target_os = "linux")]
pub fn send_notification_with_session(
    title: &str,
    message: &str,
    _session_name: Option<&str>,
    _terminal_app: &str,
) {
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
    _terminal_app: &str,
) {
}

// ── Tests ─────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(target_os = "macos")]
    #[test]
    fn has_terminal_notifier_returns_bool() {
        let _ = has_terminal_notifier();
    }

    #[test]
    fn session_name_is_shell_escaped() {
        let session = "test'session";
        let escaped = crate::remote::shell_escape(session);
        let cmd = format!("tmux switch-client -t {}", escaped);
        assert_eq!(cmd, "tmux switch-client -t 'test'\\''session'");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn build_args_with_session_uses_configured_terminal() {
        let args =
            build_notification_args("Title", "Body", Some("my-session"), "com.googlecode.iterm2");
        assert!(args.contains(&"-activate".to_string()));
        let activate_idx = args.iter().position(|a| a == "-activate").unwrap();
        assert_eq!(args[activate_idx + 1], "com.googlecode.iterm2");
        assert!(args.contains(&"-execute".to_string()));
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn build_args_without_session_still_activates_terminal() {
        let args = build_notification_args("Title", "Body", None, "dev.warp.Warp-Stable");
        let activate_idx = args.iter().position(|a| a == "-activate").unwrap();
        assert_eq!(args[activate_idx + 1], "dev.warp.Warp-Stable");
        assert!(!args.contains(&"-execute".to_string()));
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn build_args_includes_title_message_group() {
        let args = build_notification_args("My Title", "My Body", None, "com.apple.Terminal");
        assert_eq!(args[0], "-title");
        assert_eq!(args[1], "My Title");
        assert_eq!(args[2], "-message");
        assert_eq!(args[3], "My Body");
        assert_eq!(args[4], "-group");
        assert_eq!(args[5], "orchard");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn build_args_session_name_is_shell_escaped() {
        let args = build_notification_args("T", "M", Some("bad'name"), "com.apple.Terminal");
        let execute_idx = args.iter().position(|a| a == "-execute").unwrap();
        assert_eq!(
            args[execute_idx + 1],
            "tmux switch-client -t 'bad'\\''name'"
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_send_notification_with_session_ignores_session_name() {
        send_notification_with_session("Title", "Message", Some("my-session"), "com.apple.Terminal");
    }
}
