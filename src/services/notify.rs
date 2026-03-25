use std::process::Command;

/// Command-based implementation of `NotifyService`.
pub struct CommandNotify;

/// Escapes a string for use inside AppleScript double-quoted literals.
pub fn escape_applescript(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

impl super::NotifyService for CommandNotify {
    fn send_notification(&self, title: &str, message: &str) {
        let script = format!(
            r#"display notification "{}" with title "{}""#,
            escape_applescript(message),
            escape_applescript(title),
        );
        let _ = Command::new("osascript")
            .args(["-e", &script])
            .output();
    }
}
