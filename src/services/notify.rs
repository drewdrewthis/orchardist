use std::process::Command;

/// Command-based implementation of `NotifyService`.
pub struct CommandNotify;

impl super::NotifyService for CommandNotify {
    fn send_notification(&self, title: &str, message: &str) {
        let script = format!(
            r#"display notification "{}" with title "{}""#,
            message.replace('"', r#"\""#),
            title.replace('"', r#"\""#),
        );
        let _ = Command::new("osascript")
            .args(["-e", &script])
            .output();
    }
}
