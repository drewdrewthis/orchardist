use std::process::Command;

/// Sends a macOS desktop notification via osascript.
pub fn send_notification(title: &str, message: &str) {
    let script = format!(
        r#"display notification "{}" with title "{}""#,
        message.replace('"', r#"\""#),
        title.replace('"', r#"\""#),
    );
    let _ = Command::new("osascript")
        .args(["-e", &script])
        .output();
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn notification_message_escapes_quotes() {
        // Smoke test: verifies escaping logic doesn't panic.
        // We cannot assert on osascript output in CI, but we can confirm
        // the function handles embedded double-quotes without panicking.
        send_notification("Test \"title\"", "Test \"message\"");
    }
}
