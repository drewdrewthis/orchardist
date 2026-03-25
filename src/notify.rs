use crate::services::notify::CommandNotify;
use crate::services::NotifyService;

/// Sends a macOS desktop notification via osascript.
pub fn send_notification(title: &str, message: &str) {
    CommandNotify.send_notification(title, message);
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
