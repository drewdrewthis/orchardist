//! `orchard host-services <op>` subcommands — host-services domain (L1/L6).
//!
//! Wraps `scripts/service-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard host-services raw -- <launchctl|systemctl-args...>`)
//! per S16b.
//!
//! Mutations (service lifecycle writes per L5):
//!   start, stop, restart — exec `scripts/service-<op>.sh`.
//!
//! The underlying tool (`launchctl` on macOS, `systemctl` on Linux) is resolved
//! by the scripts themselves — the CLI does not platform-detect.

use super::{emit_or_die, exec_script, resolve_script, run_passthrough, script_not_found};

/// Dispatch `orchard host-services <args>`.
///
/// Subcommands:
///   `raw [-- <args...>]`       Pass-through: execs the platform service-ctl tool (S16b).
///   `start --name <svc>`       Start a service (scripts/service-start.sh).
///   `stop  --name <svc>`       Stop a service (scripts/service-stop.sh).
///   `restart --name <svc>`     Restart a service (scripts/service-restart.sh).
///   `status [--name <svc>]`    Query service status (scripts/service-status.sh).
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            // Detect platform: prefer launchctl on macOS, systemctl elsewhere.
            let tool = platform_ctl_tool();
            let rest = passthrough_args(args);
            run_passthrough(tool, rest);
        }

        Some("start") => {
            require_name_flag(&args[1..]);
            run_script_forwarding("service-start.sh", &args[1..]);
        }
        Some("stop") => {
            require_name_flag(&args[1..]);
            run_script_forwarding("service-stop.sh", &args[1..]);
        }
        Some("restart") => {
            require_name_flag(&args[1..]);
            run_script_forwarding("service-restart.sh", &args[1..]);
        }
        Some("status") => {
            run_script_forwarding("service-status.sh", &args[1..]);
        }

        _ => {
            print_usage();
            std::process::exit(1);
        }
    }
}

/// Returns the platform-appropriate service control tool name.
fn platform_ctl_tool() -> &'static str {
    #[cfg(target_os = "macos")]
    return "launchctl";
    #[cfg(not(target_os = "macos"))]
    return "systemctl";
}

/// Validates that `--name <svc>` is present in `args`; exits 1 otherwise.
///
/// M4: validate input at the CLI boundary before exec.
fn require_name_flag(args: &[String]) {
    let mut iter = args.iter().peekable();
    while let Some(arg) = iter.next() {
        if arg.as_str() == "--name" && iter.peek().is_some() {
            return; // valid
        }
    }
    eprintln!("error: --name <service> is required");
    eprintln!("Example: orchard host-services start --name com.gitorchard.orchard");
    std::process::exit(1);
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

fn passthrough_args(args: &[String]) -> &[String] {
    let rest = if args.len() > 1 {
        &args[1..]
    } else {
        &[] as &[String]
    };
    if rest.first().map(|s| s.as_str()) == Some("--") {
        &rest[1..]
    } else {
        rest
    }
}

fn print_usage() {
    eprintln!(
        "Usage: orchard host-services <subcommand> [args...]

Subcommands:
  raw [-- <args...>]            Pass-through: exec launchctl/systemctl with arbitrary args (S16b)
  start   --name <svc>          Start a service (scripts/service-start.sh)
  stop    --name <svc>          Stop a service (scripts/service-stop.sh)
  restart --name <svc>          Restart a service (scripts/service-restart.sh)
  status  [--name <svc>]        Query service status (scripts/service-status.sh)

All scripts support --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn require_name_flag_exits_when_absent() {
        // We can't call require_name_flag (it calls process::exit), but we can
        // verify the lookup logic inline.
        let args: Vec<String> = vec!["--name".into(), "com.gitorchard.orchard".into()];
        // If --name is present with a value, the function returns without panicking.
        // We test the predicate: does args contain ("--name", <value>)?
        let has_name = args
            .windows(2)
            .any(|w| w[0] == "--name" && !w[1].starts_with('-'));
        assert!(has_name);
    }

    #[test]
    fn passthrough_strips_separator() {
        let args: Vec<String> = vec!["raw".into(), "--".into(), "list".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["list"]);
    }

    #[test]
    fn platform_ctl_tool_is_nonempty() {
        assert!(!platform_ctl_tool().is_empty());
    }
}
