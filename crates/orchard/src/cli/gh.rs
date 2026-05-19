//! `orchard gh <op>` subcommands — GitHub domain (L1/L6).
//!
//! Wraps `scripts/gh-<op>.sh --json` per L1. No daemon required (L6).
//!
//! Pass-through (`orchard gh raw -- <gh-args...>`) per S16b.

use super::{emit_or_die, exec_script, resolve_script, run_passthrough, script_not_found};

/// Dispatch `orchard gh <args>`.
///
/// Subcommands:
///   `raw -- <args...>`   Pass-through: execs `gh <args>` verbatim (S16b).
///
/// Mutations (enumerated in `daemon/gh/schema.graphql` — pending #613):
///   Typed mutation stubs are included below and will exec the corresponding
///   script once `scripts/gh-<op>.sh` files are authored in a follow-up PR.
///
/// Usage examples:
///   orchard gh raw -- pr list --repo owner/repo
pub fn run(args: &[String]) {
    match args.first().map(|s| s.as_str()) {
        Some("raw") => {
            // Everything after the optional `--` separator is forwarded to gh.
            let rest = passthrough_args(args);
            run_passthrough("gh", rest);
        }

        // ----------------------------------------------------------------
        // Mutation stubs (daemon/gh/schema.graphql lists these as pending
        // #613 — exec scripts once they exist).
        // ----------------------------------------------------------------

        // pr-review: add a review to a pull request.
        // Script: scripts/gh-pr-review.sh --repo <r> --number <n> --event <e> [--body <b>]
        Some("pr-review") => {
            let name = "gh-pr-review.sh";
            let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
            let fwd: Vec<&str> = args[1..].iter().map(|s| s.as_str()).collect();
            let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
                eprintln!("error: {e}");
                std::process::exit(1);
            });
            emit_or_die(out);
        }

        // pr-label: set/clear labels on a pull request.
        Some("pr-label") => {
            let name = "gh-pr-label.sh";
            let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
            let fwd: Vec<&str> = args[1..].iter().map(|s| s.as_str()).collect();
            let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
                eprintln!("error: {e}");
                std::process::exit(1);
            });
            emit_or_die(out);
        }

        // issue-create: create a new issue.
        Some("issue-create") => {
            let name = "gh-issue-create.sh";
            let path = resolve_script(name).unwrap_or_else(|| script_not_found(name));
            let fwd: Vec<&str> = args[1..].iter().map(|s| s.as_str()).collect();
            let out = exec_script(&path, &fwd).unwrap_or_else(|e| {
                eprintln!("error: {e}");
                std::process::exit(1);
            });
            emit_or_die(out);
        }

        // pr-comment: add a comment to a pull request.
        Some("pr-comment") => {
            let name = "gh-pr-comment.sh";
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

/// Strips a leading `--` separator (if present) and returns the remaining slice.
fn passthrough_args(args: &[String]) -> &[String] {
    // args[0] == "raw"; args[1] might be "--".
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
        "Usage: orchard gh <subcommand> [args...]

Subcommands:
  raw [-- <gh-args...>]      Pass-through: exec `gh` with arbitrary args (S16b)
  pr-review                  Add a review to a pull request (scripts/gh-pr-review.sh)
  pr-label                   Set or clear labels on a pull request (scripts/gh-pr-label.sh)
  pr-comment                 Comment on a pull request (scripts/gh-pr-comment.sh)
  issue-create               Create a GitHub issue (scripts/gh-issue-create.sh)

Flags forwarded to scripts are passed as-is. Every script supports --json (injected automatically).
"
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn passthrough_args_strips_separator() {
        let args: Vec<String> = vec!["raw".into(), "--".into(), "pr".into(), "list".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["pr", "list"]);
    }

    #[test]
    fn passthrough_args_without_separator() {
        let args: Vec<String> = vec!["raw".into(), "pr".into(), "list".into()];
        let rest = passthrough_args(&args);
        assert_eq!(rest, &["pr", "list"]);
    }

    #[test]
    fn passthrough_args_empty_after_raw() {
        let args: Vec<String> = vec!["raw".into()];
        let rest = passthrough_args(&args);
        assert!(rest.is_empty());
    }
}
