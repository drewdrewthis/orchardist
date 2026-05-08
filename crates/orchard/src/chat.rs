//! Quick-chat popup: send a one-line prompt to a live orchardist tmux pane.
//!
//! The `orchard chat` subcommand resolves the target orchardist session from
//! the global config and delivers a message via `tmux send-keys`.  The design
//! is intentionally fire-and-forget: the orchardist processes the message in
//! its own context and replies through its existing channels (Telegram, tmux
//! status bar, etc.).

use std::io;
use std::process::Output;

use crate::global_config::GlobalConfig;

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

/// Errors that can occur during the chat subcommand.
#[derive(Debug)]
pub enum ChatError {
    /// No orchardist session could be resolved from config or the `--target` flag.
    NoTarget(String),
    /// A tmux command failed (non-zero exit or I/O error).
    TmuxError(String),
}

impl std::fmt::Display for ChatError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ChatError::NoTarget(msg) => write!(f, "{msg}"),
            ChatError::TmuxError(msg) => write!(f, "{msg}"),
        }
    }
}

// ---------------------------------------------------------------------------
// Target resolution
// ---------------------------------------------------------------------------

/// Resolves the orchardist tmux session name to target.
///
/// Precedence:
/// 1. `--target` CLI flag (explicit override).
/// 2. `global_config.chat_target` (persisted wizard choice).
/// 3. First entry in `global_config.tmux_sessions` (implicit fallback).
/// 4. Error if none of the above yield a name.
///
/// # Errors
///
/// Returns [`ChatError::NoTarget`] with `"--target cannot be empty"` when an
/// explicit but empty `--target ""` is passed, which is distinct from the
/// general "no session configured" error.
///
/// # Examples
///
/// ```
/// use orchard::chat::resolve_target;
/// use orchard::global_config::GlobalConfig;
///
/// let cfg = GlobalConfig::default();
/// let err = resolve_target(&cfg, None);
/// assert!(err.is_err());
/// ```
pub fn resolve_target(
    config: &GlobalConfig,
    explicit_target: Option<&str>,
) -> Result<String, ChatError> {
    // 1. Explicit --target flag wins.
    if let Some(t) = explicit_target {
        if t.is_empty() {
            return Err(ChatError::NoTarget("--target cannot be empty".to_string()));
        }
        return Ok(t.to_string());
    }

    // 2. Wizard-persisted chat_target.
    if let Some(ref t) = config.chat_target
        && !t.is_empty()
    {
        return Ok(t.clone());
    }

    // 3. First standalone tmux session.
    if let Some(session) = config.tmux_sessions.first() {
        return Ok(session.name.clone());
    }

    Err(ChatError::NoTarget(
        "orchardist session not running — start it with `claude` in a tmux session and configure \
         `chat_target` in ~/.orchard/config.json, or pass --target <session>"
            .to_string(),
    ))
}

// ---------------------------------------------------------------------------
// Sending the message
// ---------------------------------------------------------------------------

/// Checks that a tmux command output indicates success, returning a [`ChatError::TmuxError`]
/// with the given `context` message and any stderr output if it failed.
fn check_tmux_ok(out: &Output, context: &str) -> Result<(), ChatError> {
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr).trim().to_string();
        return Err(ChatError::TmuxError(format!("{context}: {stderr}")));
    }
    Ok(())
}

/// Sends `message` to the orchardist tmux pane at `<session>:0.0`.
///
/// Uses `tmux send-keys -t <session>:0.0 -l <message>` (literal, no escaping
/// needed) followed by `tmux send-keys -t <session>:0.0 Enter`.
///
/// `runner` is injected for testability; production code passes
/// [`run_command`].
///
/// # Errors
///
/// Returns [`ChatError::TmuxError`] if either tmux invocation fails or exits
/// non-zero.
pub fn send_to_orchardist<R>(session: &str, message: &str, runner: R) -> Result<(), ChatError>
where
    R: Fn(&str, &[&str]) -> io::Result<Output>,
{
    let target = format!("{session}:0.0");

    // First call: send the literal text.
    let out = runner("tmux", &["send-keys", "-t", &target, "-l", message])
        .map_err(|e| ChatError::TmuxError(format!("tmux send-keys failed: {e}")))?;
    check_tmux_ok(&out, "tmux send-keys exited non-zero")?;

    // Second call: press Enter to submit.
    let out = runner("tmux", &["send-keys", "-t", &target, "Enter"])
        .map_err(|e| ChatError::TmuxError(format!("tmux send-keys (Enter) failed: {e}")))?;
    check_tmux_ok(&out, "tmux send-keys Enter exited non-zero")?;

    Ok(())
}

/// Production runner: executes the command with `std::process::Command`.
pub fn run_command(program: &str, args: &[&str]) -> io::Result<Output> {
    std::process::Command::new(program).args(args).output()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::global_config::GlobalConfig;
    use crate::session::StandaloneConfig;

    fn cfg_with_sessions(names: &[&str]) -> GlobalConfig {
        GlobalConfig {
            tmux_sessions: names
                .iter()
                .map(|n| StandaloneConfig {
                    name: n.to_string(),
                    command: String::new(),
                    cwd: String::new(),
                    start_on_launch: false,
                })
                .collect(),
            ..GlobalConfig::default()
        }
    }

    // ------------------------------------------------------------------
    // resolve_target
    // ------------------------------------------------------------------

    #[test]
    fn resolve_target_no_sessions_returns_error() {
        let cfg = GlobalConfig::default();
        assert!(resolve_target(&cfg, None).is_err());
    }

    #[test]
    fn resolve_target_one_session_returns_it() {
        let cfg = cfg_with_sessions(&["orchardist"]);
        assert_eq!(resolve_target(&cfg, None).unwrap(), "orchardist");
    }

    #[test]
    fn resolve_target_multiple_sessions_without_target_returns_first() {
        let cfg = cfg_with_sessions(&["first", "second"]);
        assert_eq!(resolve_target(&cfg, None).unwrap(), "first");
    }

    #[test]
    fn resolve_target_explicit_target_wins_over_sessions() {
        let cfg = cfg_with_sessions(&["orchardist"]);
        assert_eq!(resolve_target(&cfg, Some("custom")).unwrap(), "custom");
    }

    #[test]
    fn resolve_target_explicit_target_empty_string_returns_error() {
        let cfg = cfg_with_sessions(&["orchardist"]);
        let err = resolve_target(&cfg, Some("")).unwrap_err();
        assert!(
            err.to_string().contains("--target cannot be empty"),
            "expected '--target cannot be empty' error, got: {err}"
        );
    }

    #[test]
    fn resolve_target_chat_target_wins_over_sessions() {
        let mut cfg = cfg_with_sessions(&["first"]);
        cfg.chat_target = Some("orchardist".to_string());
        assert_eq!(resolve_target(&cfg, None).unwrap(), "orchardist");
    }

    #[test]
    fn resolve_target_explicit_target_wins_over_chat_target() {
        let mut cfg = cfg_with_sessions(&["first"]);
        cfg.chat_target = Some("orchardist".to_string());
        assert_eq!(resolve_target(&cfg, Some("override")).unwrap(), "override");
    }

    #[test]
    fn resolve_target_chat_target_empty_string_falls_through_to_sessions() {
        let mut cfg = cfg_with_sessions(&["orchardist"]);
        cfg.chat_target = Some(String::new());
        assert_eq!(resolve_target(&cfg, None).unwrap(), "orchardist");
    }

    // ------------------------------------------------------------------
    // send_to_orchardist
    // ------------------------------------------------------------------

    #[test]
    fn send_invokes_tmux_send_keys_with_literal_flag() {
        let calls: std::cell::RefCell<Vec<(String, Vec<String>)>> =
            std::cell::RefCell::new(Vec::new());

        let result = send_to_orchardist("mysession", "hello world", |prog: &str, args: &[&str]| {
            calls.borrow_mut().push((
                prog.to_string(),
                args.iter().map(|s| s.to_string()).collect(),
            ));
            Ok(std::process::Command::new("true").output().unwrap())
        });

        assert!(result.is_ok());
        let recorded = calls.into_inner();
        assert_eq!(recorded.len(), 2, "expected 2 tmux invocations");

        // First call: send-keys with -l (literal) flag.
        assert_eq!(recorded[0].0, "tmux");
        let first_args = &recorded[0].1;
        assert_eq!(first_args[0], "send-keys");
        assert!(
            first_args.contains(&"-t".to_string()),
            "first call must include -t"
        );
        assert!(
            first_args.contains(&"-l".to_string()),
            "first call must include -l (literal)"
        );
        assert!(
            first_args.contains(&"hello world".to_string()),
            "first call must include the message"
        );

        // Second call: Enter.
        assert_eq!(recorded[1].0, "tmux");
        let second_args = &recorded[1].1;
        assert_eq!(second_args[0], "send-keys");
        assert!(
            second_args.contains(&"Enter".to_string()),
            "second call must send Enter"
        );
    }

    #[test]
    fn send_propagates_non_zero_exit() {
        let result = send_to_orchardist("mysession", "hi", |_prog: &str, _args: &[&str]| {
            let output = std::process::Command::new("false").output().unwrap();
            Ok(output)
        });

        assert!(result.is_err());
        match result {
            Err(ChatError::TmuxError(_)) => {}
            _ => panic!("expected TmuxError"),
        }
    }

    #[test]
    fn send_propagates_io_error() {
        let result = send_to_orchardist("mysession", "hi", |_prog: &str, _args: &[&str]| {
            Err(io::Error::new(io::ErrorKind::NotFound, "tmux not found"))
        });

        assert!(result.is_err());
        match result {
            Err(ChatError::TmuxError(msg)) => assert!(msg.contains("tmux not found")),
            _ => panic!("expected TmuxError"),
        }
    }

    #[test]
    fn send_targets_session_colon_0_dot_0() {
        let captured_targets: std::cell::RefCell<Vec<String>> = std::cell::RefCell::new(Vec::new());

        let _ = send_to_orchardist("my-session", "test", |_prog: &str, args: &[&str]| {
            if let Some(pos) = args.iter().position(|&a| a == "-t")
                && let Some(target) = args.get(pos + 1)
            {
                captured_targets.borrow_mut().push(target.to_string());
            }
            Ok(std::process::Command::new("true").output().unwrap())
        });

        let targets = captured_targets.into_inner();
        for t in &targets {
            assert_eq!(t, "my-session:0.0", "target must be <session>:0.0");
        }
    }
}
