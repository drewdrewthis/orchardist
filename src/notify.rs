use crate::services::notify::CommandNotify;
use crate::services::NotifyService;

/// Sends a macOS desktop notification via osascript.
pub fn send_notification(title: &str, message: &str) {
    CommandNotify.send_notification(title, message);
}

#[cfg(test)]
mod tests {
    use crate::services::notify::escape_applescript;

    #[test]
    fn escape_applescript_handles_double_quotes() {
        assert_eq!(escape_applescript(r#"say "hello""#), r#"say \"hello\""#);
    }

    #[test]
    fn escape_applescript_handles_backslashes() {
        assert_eq!(escape_applescript(r#"path\to\file"#), r#"path\\to\\file"#);
    }

    #[test]
    fn escape_applescript_handles_combined() {
        assert_eq!(escape_applescript(r#"a\"b"#), r#"a\\\"b"#);
    }

    #[test]
    fn escape_applescript_passes_plain_text() {
        assert_eq!(escape_applescript("hello world"), "hello world");
    }
}
