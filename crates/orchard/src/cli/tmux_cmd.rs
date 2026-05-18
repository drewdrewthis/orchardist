//! `orchard tmux <op>` subcommands — tmux domain (L1/L6).
//!
//! Wraps `scripts/tmux-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard tmux raw -- <tmux-args...>`) per S16b.
//!
//! Mutations enumerated in `daemon/tmux/schema.graphql`:
//!   `sendTextToPane` — execs `scripts/tmux-send-text.sh --pane <id> --text <body>`.

use super::{emit_or_die, exec_script, resolve_script, run_passthrough, script_not_found};

/// Dispatch `orchard tmux <args>`.
///
/// Subcommands:
///   `raw [-- <tmux-args...>]`          Pass-through: execs `tmux <args>` verbatim (S16b).
///   `send-text --pane <id> --text <t>` Sends text to a tmux pane, followed by Enter.
///                                       Maps to `Mutation.sendTextToPane` (L5).
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            let rest = passthrough_args(args);
            run_passthrough("tmux", rest);
        }

        Some("send-text") => {
            // Validate that required flags are present before exec.
            // M4: validate input at the CLI boundary.
            let pane = extract_flag(&args[1..], "--pane");
            let text = extract_flag(&args[1..], "--text");

            let pane = pane.unwrap_or_else(|| {
                eprintln!("error: --pane <id> is required");
                eprintln!("Usage: orchard tmux send-text --pane <paneId> --text <message>");
                std::process::exit(1);
            });
            let text = text.unwrap_or_else(|| {
                eprintln!("error: --text <message> is required");
                eprintln!("Usage: orchard tmux send-text --pane <paneId> --text <message>");
                std::process::exit(1);
            });

            // Per L5: exec scripts/tmux-send-text.sh --pane <id> --text <body> --json.
            let name = "tmux-send-text.sh";
            let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
            let out = exec_script(&path, &["--pane", &pane, "--text", &text]).unwrap_or_else(|e| {
                eprintln!("error: {e}");
                std::process::exit(1);
            });
            emit_or_die(out);
        }

        _ => {
            print_usage();
            std::process::exit(1);
        }
    }
}

/// Extract the value of a named `--flag <value>` pair from `args`.
///
/// Returns `None` when the flag is absent or is the last element with no
/// value following it.
fn extract_flag<'a>(args: &'a [String], flag: &str) -> Option<String> {
    let mut iter = args.iter().peekable();
    while let Some(arg) = iter.next() {
        if arg.as_str() == flag {
            return iter.next().map(|v| v.clone());
        }
    }
    None
}

/// Strips a leading `--` separator (if present) and returns the remainder.
fn passthrough_args(args: &[String]) -> &[String] {
    let rest = if args.len() > 1 { &args[1..] } else { &[] as &[String] };
    if rest.first().map(|s| s.as_str()) == Some("--") {
        &rest[1..]
    } else {
        rest
    }
}

fn print_usage() {
    eprintln!(
        "Usage: orchard tmux <subcommand> [args...]

Subcommands:
  raw [-- <tmux-args...>]                 Pass-through: exec `tmux` with arbitrary args (S16b)
  send-text --pane <paneId> --text <msg>  Send text to a tmux pane + Enter
                                            (Mutation.sendTextToPane via scripts/tmux-send-text.sh)

All scripts support --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extract_flag_present() {
        let args: Vec<String> = vec!["--pane".into(), "%15".into(), "--text".into(), "hello".into()];
        assert_eq!(extract_flag(&args, "--pane"), Some("%15".to_string()));
        assert_eq!(extract_flag(&args, "--text"), Some("hello".to_string()));
    }

    #[test]
    fn extract_flag_absent() {
        let args: Vec<String> = vec!["--pane".into(), "%15".into()];
        assert_eq!(extract_flag(&args, "--text"), None);
    }

    #[test]
    fn extract_flag_trailing_without_value() {
        let args: Vec<String> = vec!["--pane".into()];
        assert_eq!(extract_flag(&args, "--pane"), None);
    }

    #[test]
    fn passthrough_strips_separator() {
        let args: Vec<String> = vec!["raw".into(), "--".into(), "list-sessions".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["list-sessions"]);
    }

    #[test]
    fn passthrough_without_separator() {
        let args: Vec<String> = vec!["raw".into(), "new-session".into(), "-d".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["new-session", "-d"]);
    }
}
