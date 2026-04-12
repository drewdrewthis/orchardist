//! Port resolution for the webhook server.
//!
//! Resolves the port to bind from multiple sources in precedence order:
//! CLI flag > environment variable > config file > built-in default (8477).

/// The default port the webhook server binds to when no other source provides one.
pub const DEFAULT_WEBHOOK_PORT: u16 = 8477;

/// Resolve the webhook server port from flag > env > config > default (8477).
///
/// All inputs are `Option<u16>`; `None` means "fall through to the next
/// precedence level". Returns the first `Some` value encountered, or
/// [`DEFAULT_WEBHOOK_PORT`] if all are `None`.
///
/// # Examples
///
/// ```
/// use orchard::webhook::port::{resolve_port, DEFAULT_WEBHOOK_PORT};
///
/// // Flag wins when present.
/// assert_eq!(resolve_port(Some(9000), Some(9001), Some(9002)), 9000);
///
/// // Falls through to env when flag is absent.
/// assert_eq!(resolve_port(None, Some(9001), Some(9002)), 9001);
///
/// // Falls through to config when flag and env are absent.
/// assert_eq!(resolve_port(None, None, Some(9002)), 9002);
///
/// // Falls through to default when all are absent.
/// assert_eq!(resolve_port(None, None, None), DEFAULT_WEBHOOK_PORT);
/// ```
pub fn resolve_port(flag: Option<u16>, env: Option<u16>, config: Option<u16>) -> u16 {
    flag.or(env).or(config).unwrap_or(DEFAULT_WEBHOOK_PORT)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn flag_wins_over_env_and_config() {
        assert_eq!(resolve_port(Some(9000), Some(9001), Some(9002)), 9000);
    }

    #[test]
    fn env_wins_when_flag_absent() {
        assert_eq!(resolve_port(None, Some(9001), Some(9002)), 9001);
    }

    #[test]
    fn config_wins_when_flag_and_env_absent() {
        assert_eq!(resolve_port(None, None, Some(9002)), 9002);
    }

    #[test]
    fn default_used_when_all_absent() {
        assert_eq!(resolve_port(None, None, None), DEFAULT_WEBHOOK_PORT);
        assert_eq!(resolve_port(None, None, None), 8477);
    }
}
