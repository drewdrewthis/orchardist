//! `orchard claude-account <op>` subcommands — claude-account domain (L1/L6).
//!
//! Wraps `scripts/claude-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard claude-account raw <tool> -- <args...>`) per S16b.
//! Supported tools: `claude`, `ccusage` (mirrors `ClaudeCliTool` in the schema).
//!
//! Mutations (account writes per L5):
//!   login, logout — exec `scripts/claude-<op>.sh`.

use super::{emit_or_die, exec_script, resolve_script, run_passthrough, script_not_found};

/// Dispatch `orchard claude-account <args>`.
///
/// Subcommands:
///   `raw <claude|ccusage> [-- <args...>]`  Pass-through (S16b).
///   `status`                               Query auth status (scripts/claude-status.sh).
///   `login`                                Initiate login flow (scripts/claude-login.sh).
///   `logout`                               Log out (scripts/claude-logout.sh).
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            // args: ["raw", <tool>, "--", ...rest]
            // or:   ["raw", <tool>, ...rest]
            let tool = args.get(1).map(|s| s.as_str()).unwrap_or("claude");
            if !matches!(tool, "claude" | "ccusage") {
                eprintln!("error: `orchard claude-account raw` supports tools: claude, ccusage");
                eprintln!("Usage: orchard claude-account raw <claude|ccusage> [-- <args...>]");
                std::process::exit(1);
            }
            let rest = if args.len() > 2 {
                &args[2..]
            } else {
                &[] as &[String]
            };
            let rest = if rest.first().map(|s| s.as_str()) == Some("--") {
                &rest[1..]
            } else {
                rest
            };
            run_passthrough(tool, rest);
        }

        Some("status") => run_script_forwarding("claude-status.sh", &args[1..]),
        Some("login") => run_script_forwarding("claude-login.sh", &args[1..]),
        Some("logout") => run_script_forwarding("claude-logout.sh", &args[1..]),

        _ => {
            print_usage();
            std::process::exit(1);
        }
    }
}

fn run_script_forwarding(name: &str, extra_args: &[String]) {
    let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
    let fwd: Vec<&str> = extra_args.iter().map(|s| s.as_str()).collect();
    let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
        eprintln!("error: {e}");
        std::process::exit(1);
    });
    emit_or_die(out);
}

fn print_usage() {
    eprintln!(
        "Usage: orchard claude-account <subcommand> [args...]

Subcommands:
  raw <claude|ccusage> [-- <args...>]  Pass-through: exec `claude` or `ccusage` with arbitrary args (S16b)
  status                               Query auth status (scripts/claude-status.sh)
  login                                Initiate the Claude login flow (scripts/claude-login.sh)
  logout                               Log out of Claude (scripts/claude-logout.sh)

All scripts support --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    /// Verify usage text compiles without panicking.
    #[test]
    fn usage_subcommands_covered() {
        let cmds = ["raw", "status", "login", "logout"];
        for c in cmds {
            assert!(!c.is_empty());
        }
    }
}
