//! Binary entry point for the `orchard` CLI.
//!
//! Parses CLI flags (`--json`, `--version`, `--help`), handles the `init` and
//! `upgrade` sub-commands, and dispatches to either the JSON output path or the
//! Ratatui TUI (re-launching itself inside a tmux popup when appropriate).
use std::env;
use std::io::IsTerminal;

use crossterm::{
    cursor,
    terminal::{self, LeaveAlternateScreen},
};
use orchard::build_state;
use orchard::global_config;
use orchard::heal;
use orchard::json_output::JsonOutput;
use orchard::logger;
use orchard::setup_remote;
use orchard::shell;
use orchard::tui;

fn main() {
    install_panic_hooks();

    let args: Vec<String> = env::args().collect();

    let mut json_flag = false;
    let mut fix_flag = false;
    let mut command = String::new();

    for arg in &args[1..] {
        match arg.as_str() {
            "--json" => json_flag = true,
            "--fix" => fix_flag = true,
            "--version" | "-V" => {
                println!("orchard {}", env!("CARGO_PKG_VERSION"));
                return;
            }
            "--help" | "-h" => {
                print_usage();
                return;
            }
            _ if !arg.starts_with('-') && command.is_empty() => command = arg.clone(),
            _ => {}
        }
    }

    logger::LOG.info(&format!(
        "startup: orchard{}",
        if command.is_empty() {
            String::new()
        } else {
            format!(" command={command}")
        }
    ));

    match command.as_str() {
        "init" => handle_init(),
        "upgrade" => handle_upgrade(),
        "setup-remote" => handle_setup_remote(&args),
        "heal" => handle_heal(fix_flag, json_flag),
        _ => {
            if json_flag {
                handle_json();
            } else {
                handle_tui(&command);
            }
        }
    }
}

fn handle_init() {
    match shell::run_init_wizard() {
        Ok(()) => {}
        Err(e) => {
            eprintln!("{}", e);
            std::process::exit(1);
        }
    }
}

fn handle_setup_remote(args: &[String]) {
    // args[0] = binary, args[1] = "setup-remote", args[2] = host.
    let host = args.get(2).map(|s| s.as_str()).unwrap_or("");

    if host.is_empty() {
        eprintln!("Usage: orchard setup-remote <host>");
        eprintln!("  <host> may be a remote name from config or a direct SSH target (user@host)");
        std::process::exit(1);
    }

    match setup_remote::run(host) {
        Ok(()) => {}
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(1);
        }
    }
}

fn handle_upgrade() {
    eprintln!("Upgrade not yet implemented for the Rust binary.");
    eprintln!(
        "Download the latest from: https://github.com/drewdrewthis/orchard-rs/releases/latest"
    );
}

/// Runs the heal command in CLI mode (non-TUI).
///
/// Gathers live state, diagnoses the environment, and prints a report.
/// When `fix` is true, applies all actionable fixes. When `json` is true,
/// outputs machine-readable JSON instead of the formatted report.
fn handle_heal(fix: bool, json: bool) {
    let config = global_config::load_global_config();
    let sessions = orchard::tmux::list_tmux_sessions();
    let claude_states = heal::gather_claude_states();
    let cache_files = heal::gather_cache_files();
    let known_slugs: Vec<String> = config.repos.iter().map(|r| r.slug.clone()).collect();

    // Build HealWorktree entries from all configured repo caches.
    let worktrees = build_heal_worktrees_for_cli(&config);

    let report = heal::diagnose(
        &sessions,
        &worktrees,
        &claude_states,
        &cache_files,
        &known_slugs,
    );

    if json {
        let output = serde_json::to_string_pretty(&report).unwrap_or_else(|e| {
            eprintln!("Error serializing JSON: {e}");
            std::process::exit(1);
        });
        println!("{output}");
        return;
    }

    let fix_results = if fix {
        Some(heal::apply_fixes(&report.findings))
    } else {
        None
    };

    println!("{}", heal::format_report(&report, fix_results.as_deref()));
}

/// Builds `HealWorktree` entries for the CLI path (no TUI app state available).
fn build_heal_worktrees_for_cli(config: &global_config::GlobalConfig) -> Vec<heal::HealWorktree> {
    let mut result = Vec::new();
    for repo in &config.repos {
        let worktrees = orchard::cache::read_cache::<orchard::cache::CachedWorktree>(
            &orchard::cache::cache_path(repo.owner(), repo.repo_name(), "worktrees"),
        )
        .entries;
        let prs = orchard::cache::read_cache::<orchard::cache::CachedPr>(
            &orchard::cache::cache_path(repo.owner(), repo.repo_name(), "prs"),
        )
        .entries;
        let issues = orchard::cache::read_cache::<orchard::cache::CachedIssue>(
            &orchard::cache::cache_path(repo.owner(), repo.repo_name(), "issues"),
        )
        .entries;

        for wt in worktrees.iter().filter(|w| !w.is_bare) {
            let pr = prs.iter().find(|p| p.branch == wt.branch);
            let issue_number = orchard::github::extract_issue_number(&wt.branch);
            let issue_state = issue_number
                .and_then(|n| issues.iter().find(|i| i.number == n))
                .map(|i| i.state.clone());
            let expected_session_name =
                orchard::tmux::derive_main_session_name(&wt.path, Some(&wt.branch));
            result.push(heal::HealWorktree {
                path: wt.path.clone(),
                branch: wt.branch.clone(),
                expected_session_name: Some(expected_session_name),
                pr_state: pr.map(|p| p.state.clone()),
                pr_number: pr.map(|p| p.number),
                issue_state,
            });
        }
    }
    result
}

fn handle_json() {
    let config = global_config::load_global_config();
    let state = build_state::refresh_and_build(&config);
    let output = JsonOutput::from(&state);
    let json = serde_json::to_string_pretty(&output).unwrap_or_else(|e| {
        eprintln!("Error serializing JSON: {e}");
        std::process::exit(1);
    });
    println!("{json}");
}

/// Runs the TUI. If inside tmux and run directly (not via popup wrapper),
/// re-launches itself as a tmux popup using the wrapper script so that
/// session switching works correctly after the popup closes.
fn handle_tui(command: &str) {
    let inside_tmux = env::var("TMUX").is_ok();
    let is_tty = std::io::stdout().is_terminal();

    // If inside tmux and stdout is a TTY, we were run directly (not via popup).
    // Re-launch as a popup through the wrapper script.
    if inside_tmux && is_tty {
        let wrapper = dirs::home_dir()
            .map(|h| h.join(".local/bin/orchard-popup"))
            .unwrap_or_else(|| std::path::PathBuf::from("orchard-popup"));

        if wrapper.exists() {
            let _ = std::process::Command::new("tmux")
                .args([
                    "display-popup",
                    "-E",
                    "-w",
                    "90%",
                    "-h",
                    "80%",
                    &wrapper.to_string_lossy(),
                ])
                .status();
        } else {
            eprintln!("Wrapper script not found. Run `orchard init` first.");
            std::process::exit(1);
        }
        return;
    }

    // Inside popup (stdout captured) or outside tmux — run the TUI directly.
    match tui::run(command) {
        Ok(Some(session_name)) => {
            if inside_tmux {
                // Inside popup — print for wrapper to switch-client.
                println!("{session_name}");
            } else {
                // Outside tmux — attach to the session.
                let _ = std::process::Command::new("tmux")
                    .args(["attach-session", "-t", &session_name])
                    .status();
            }
        }
        Ok(None) => {}
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(1);
        }
    }
}

/// Installs panic and error hooks that restore the terminal before printing
/// crash output, preventing terminal corruption when the TUI exits abnormally.
///
/// Must be called at the very start of `main()`, before any terminal setup.
fn install_panic_hooks() {
    let (panic_hook, eyre_hook) = color_eyre::config::HookBuilder::default().into_hooks();
    eyre_hook.install().expect("failed to install eyre hook");
    std::panic::set_hook(Box::new(move |info| {
        let _ = crossterm::execute!(std::io::stderr(), LeaveAlternateScreen);
        let _ = terminal::disable_raw_mode();
        let _ = crossterm::execute!(std::io::stderr(), cursor::Show);
        eprintln!("{}", panic_hook.panic_report(info));
    }));
}

fn print_usage() {
    eprintln!(
        r#"Usage:
  orchard                        Interactive worktree manager (popup mode)
  orchard init                   Interactive setup wizard for popup mode
  orchard setup-remote <host>    Provision a remote host for orchard
  orchard upgrade                Upgrade to the latest version
  orchard heal                   Audit and repair drifted state (dry run)
  orchard heal --fix             Apply all safe automatic repairs
  orchard heal --json            Output health check results as JSON

Options:
  --version, -V  Print version and exit
  --json         Output worktree data as JSON and exit

Navigation:
  1-9     Jump to worktree by number
  ↑/↓     Select worktree
  Enter/t tmux into worktree (creates session if needed, then exits)
  d       Delete selected worktree
  c       Cleanup merged worktrees
  h       Run heal check
  r       Refresh list
  q/Esc   Close popup (no switch)"#
    );
}
