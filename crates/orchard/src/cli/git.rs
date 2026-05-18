//! `orchard git <op>` subcommands — git domain (L1/L6).
//!
//! Wraps `scripts/git-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard git raw <worktree-path> -- <git-args...>`) per S16b.
//!
//! Mutations enumerated in `daemon/git/schema.graphql` (pending #613):
//!   worktreeCreate, worktreeRemove, worktreeMove, fetch, pull, push.

use super::{emit_or_die, exec_script, resolve_script, run_passthrough, script_not_found};

/// Dispatch `orchard git <args>`.
///
/// Subcommands:
///   `raw -- <git-args...>`         Pass-through: execs `git <args>` verbatim (S16b).
///   `worktree-create --path <p> --branch <b> [--base <ref>]`
///   `worktree-remove --path <p>`
///   `worktree-move   --path <p> --dest <d>`
///   `fetch           --path <p> [--remote <r>]`
///   `pull            --path <p>`
///   `push            --path <p> [--remote <r>] [--force]`
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            let rest = passthrough_args(args);
            run_passthrough("git", rest);
        }

        Some("worktree-create") => run_script_forwarding("git-worktree-create.sh", &args[1..]),
        Some("worktree-remove") => run_script_forwarding("git-worktree-remove.sh", &args[1..]),
        Some("worktree-move") => run_script_forwarding("git-worktree-move.sh", &args[1..]),
        Some("fetch") => run_script_forwarding("git-fetch.sh", &args[1..]),
        Some("pull") => run_script_forwarding("git-pull.sh", &args[1..]),
        Some("push") => run_script_forwarding("git-push.sh", &args[1..]),

        _ => {
            print_usage();
            std::process::exit(1);
        }
    }
}

/// Resolve, exec, and emit for a named script — shared helper used by all
/// typed mutation handlers above.
fn run_script_forwarding(name: &str, extra_args: &[String]) {
    let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
    let fwd: Vec<&str> = extra_args.iter().map(|s| s.as_str()).collect();
    let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
        eprintln!("error: {e}");
        std::process::exit(1);
    });
    emit_or_die(out);
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
        "Usage: orchard git <subcommand> [args...]

Subcommands:
  raw [-- <git-args...>]    Pass-through: exec `git` with arbitrary args (S16b)
  worktree-create           Create a new worktree (scripts/git-worktree-create.sh)
                              --path <dest>  --branch <name>  [--base <ref>]
  worktree-remove           Remove a worktree (scripts/git-worktree-remove.sh)
                              --path <path>
  worktree-move             Move a worktree (scripts/git-worktree-move.sh)
                              --path <src>  --dest <dst>
  fetch                     git fetch (scripts/git-fetch.sh)
                              --path <path>  [--remote <name>]
  pull                      git pull (scripts/git-pull.sh)
                              --path <path>
  push                      git push (scripts/git-push.sh)
                              --path <path>  [--remote <name>]  [--force]

All scripts support --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn passthrough_strips_separator() {
        let args: Vec<String> = vec!["raw".into(), "--".into(), "status".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["status"]);
    }

    #[test]
    fn passthrough_without_separator() {
        let args: Vec<String> = vec!["raw".into(), "log".into(), "--oneline".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["log", "--oneline"]);
    }

    #[test]
    fn passthrough_empty_after_raw() {
        let args: Vec<String> = vec!["raw".into()];
        let rest = passthrough_args(&args);
        assert!(rest.is_empty());
    }
}
