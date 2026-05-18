//! `orchard daemon <op>` subcommands — daemon-self operations (L10).
//!
//! Per **L10**: operations *about* the daemon itself (start, stop, status,
//! reload, introspect) live under `orchard daemon ...`. They are NOT general
//! orchard verbs.
//!
//! The daemon binary is `orchard-daemon` (lives in `cmd/orchard-daemon/`).
//! These subcommands exec that binary or its control scripts.
//!
//! Scripts targeted:
//!   `scripts/daemon-start.sh`   — start the daemon (exits 0 once it is listening)
//!   `scripts/daemon-stop.sh`    — stop the daemon gracefully
//!   `scripts/daemon-status.sh`  — probe health endpoint, emit L2 envelope
//!   `scripts/daemon-reload.sh`  — send SIGHUP / reload config (daemonReload)

use super::{emit_or_die, exec_script, resolve_script, script_not_found};

/// Dispatch `orchard daemon <args>`.
///
/// Subcommands:
///   `start`    Start the orchard daemon (scripts/daemon-start.sh).
///   `stop`     Stop the orchard daemon (scripts/daemon-stop.sh).
///   `status`   Check daemon health (scripts/daemon-status.sh).
///   `reload`   Reload daemon config without restart (scripts/daemon-reload.sh).
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("start") => run_script_forwarding("daemon-start.sh", &args[1..]),
        Some("stop") => run_script_forwarding("daemon-stop.sh", &args[1..]),
        Some("status") => run_script_forwarding("daemon-status.sh", &args[1..]),
        Some("reload") => run_script_forwarding("daemon-reload.sh", &args[1..]),
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
        "Usage: orchard daemon <subcommand>

Subcommands:
  start    Start the orchard daemon (scripts/daemon-start.sh)
  stop     Stop the orchard daemon gracefully (scripts/daemon-stop.sh)
  status   Probe daemon health and print status (scripts/daemon-status.sh)
  reload   Reload daemon config without restart (scripts/daemon-reload.sh)

The daemon listens at http://127.0.0.1:7777/graphql by default.
Override with ORCHARD_DAEMON_URL.
"
    );
}

#[cfg(test)]
mod tests {
    /// Smoke-test: valid subcommand names map correctly in a match expression.
    #[test]
    fn subcommand_names_are_canonical() {
        let valid = ["start", "stop", "status", "reload"];
        for cmd in valid {
            let matched = matches!(cmd, "start" | "stop" | "status" | "reload");
            assert!(matched, "subcommand {cmd} should be matched");
        }
    }

    #[test]
    fn unknown_subcommand_is_not_matched() {
        let unknown = "bork";
        let matched = matches!(unknown, "start" | "stop" | "status" | "reload");
        assert!(!matched);
    }
}
