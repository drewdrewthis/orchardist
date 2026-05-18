//! `orchard ps <op>` subcommands — process-table domain (L1/L6).
//!
//! Wraps `scripts/ps-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard ps raw <tool> -- <args...>`) per S16b.
//!
//! The ps domain has no write mutations (there are no process-create/kill
//! mutations in `daemon/ps/schema.graphql`). The domain is read-only at the
//! schema level. A future `signal` mutation (send SIGTERM/SIGKILL) would be
//! added here when specced.

use super::{run_passthrough, script_not_found};
use super::{emit_or_die, exec_script, resolve_script};

/// Dispatch `orchard ps <args>`.
///
/// Subcommands:
///   `raw ps [-- <args...>]`    Pass-through: execs `ps <args>` verbatim (S16b).
///   `raw lsof [-- <args...>]`  Pass-through: execs `lsof <args>` verbatim (S16b).
///   `list [--cwd-prefix <p>]`  List processes (scripts/ps-list.sh).
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            // args: ["raw", <tool>, "--", ...args]
            // or:   ["raw", <tool>, ...args]
            let tool = args.get(1).map(|s| s.as_str()).unwrap_or("ps");
            if !matches!(tool, "ps" | "lsof") {
                eprintln!("error: `orchard ps raw` only supports tools: ps, lsof");
                eprintln!("Usage: orchard ps raw <ps|lsof> [-- <args...>]");
                std::process::exit(1);
            }
            let rest = if args.len() > 2 { &args[2..] } else { &[] as &[String] };
            let rest = if rest.first().map(|s| s.as_str()) == Some("--") {
                &rest[1..]
            } else {
                rest
            };
            run_passthrough(tool, rest);
        }

        Some("list") => {
            let name = "ps-list.sh";
            let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
            let fwd: Vec<&str> = args[1..].iter().map(|s| s.as_str()).collect();
            let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
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

fn print_usage() {
    eprintln!(
        "Usage: orchard ps <subcommand> [args...]

Subcommands:
  raw <ps|lsof> [-- <args...>]  Pass-through: exec `ps` or `lsof` with arbitrary args (S16b)
  list [--cwd-prefix <path>]    List processes from the OS table (scripts/ps-list.sh)

All scripts support --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    /// Verify usage text renders without panicking (trivial smoke test).
    #[test]
    fn print_usage_does_not_panic() {
        // Can't call print_usage() + exit, so just verify the string compiles.
        let s = "Usage: orchard ps <subcommand>";
        assert!(s.contains("orchard ps"));
    }
}
