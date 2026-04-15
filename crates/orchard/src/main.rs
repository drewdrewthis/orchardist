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
use orchard::chat;
use orchard::global_config;
use orchard::heal;
use orchard::hook_enrich;
use orchard::json_output::JsonOutput;
use orchard::logger;
use orchard::setup_remote;
use orchard::shell;
use orchard::toon_output;
use orchard::tui;

fn main() {
    install_panic_hooks();

    let args: Vec<String> = env::args().collect();

    let mut json_flag = false;
    let mut toon_flag = false;
    let mut fix_flag = false;
    let mut command = String::new();

    let mut chat_target: Option<String> = None;
    let mut chat_message: Option<String> = None;
    let mut transcript_path: Option<String> = None;
    let mut skip_next = false;

    for (i, arg) in args[1..].iter().enumerate() {
        if skip_next {
            skip_next = false;
            continue;
        }
        match arg.as_str() {
            "--json" => json_flag = true,
            "--toon" => toon_flag = true,
            "--fix" => fix_flag = true,
            "--version" | "-V" => {
                println!("orchard {}", env!("CARGO_PKG_VERSION"));
                return;
            }
            "--help" | "-h" => {
                print_usage();
                return;
            }
            "--target" => {
                chat_target = args.get(i + 2).cloned();
                skip_next = true;
            }
            "--message" => {
                chat_message = args.get(i + 2).cloned();
                skip_next = true;
            }
            "--transcript" => {
                transcript_path = args.get(i + 2).cloned();
                skip_next = true;
            }
            _ if !arg.starts_with('-') && command.is_empty() => command = arg.clone(),
            _ => {}
        }
    }

    if json_flag && toon_flag {
        eprintln!("error: --json and --toon are mutually exclusive");
        std::process::exit(2);
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
        "chat" => handle_chat(chat_target.as_deref(), chat_message.as_deref()),
        "watch" => handle_watch(&args),
        "hook-enrich" => handle_hook_enrich(transcript_path.as_deref()),
        "webhook-serve" => handle_webhook_serve(&args),
        _ => {
            if toon_flag {
                handle_toon();
            } else if json_flag {
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
        "Download the latest from: https://github.com/drewdrewthis/git-orchard-rs/releases/latest"
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

/// Handles `orchard chat [--target <session>] [--message <text>]`.
///
/// Resolves the orchardist session and delivers the message via `tmux send-keys`.
/// Exits non-zero with usage if `--message` is missing or empty — the no-op-on-empty
/// behavior belongs in the wrapper script (`orchard-chat`), not the CLI.
fn handle_chat(target: Option<&str>, message: Option<&str>) {
    let message = match message {
        Some(m) if !m.is_empty() => m.to_string(),
        _ => {
            eprintln!("Error: --message is required and must not be empty");
            eprintln!();
            eprintln!("Usage: orchard chat [--target <session>] --message <text>");
            std::process::exit(1);
        }
    };

    let config = global_config::load_global_config();
    let session = match chat::resolve_target(&config, target) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(1);
        }
    };

    if let Err(e) = chat::send_to_orchardist(&session, &message, chat::run_command) {
        eprintln!("{e}");
        std::process::exit(1);
    }
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

/// Emits the same data as `--json`, serialized as TOON v2.0.
///
/// TOON (Token-Oriented Object Notation) is a token-efficient alternative to
/// JSON for AI-agent consumption, using a header row for uniform arrays.
/// The underlying schema is identical to `--json` — `JsonOutput` is the
/// single source of truth.
fn handle_toon() {
    let config = global_config::load_global_config();
    let state = build_state::refresh_and_build(&config);
    let output = JsonOutput::from(&state);
    let toon = toon_output::render(&output).unwrap_or_else(|e| {
        eprintln!("Error serializing TOON: {e}");
        std::process::exit(1);
    });
    println!("{toon}");
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
            let mut cmd = std::process::Command::new("tmux");
            cmd.args([
                "display-popup",
                "-E",
                "-w",
                "90%",
                "-h",
                "80%",
                &wrapper.to_string_lossy(),
            ]);
            if !command.is_empty() {
                cmd.arg(command);
            }
            let _ = cmd.status();
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

/// Handles `orchard hook-enrich --transcript <path>`.
///
/// Reads the JSONL transcript and prints a JSON enrichment object to stdout.
/// Prints `{}` and exits 0 on any error (missing path, missing file, etc.).
fn handle_hook_enrich(transcript: Option<&str>) {
    match transcript {
        Some(path) => hook_enrich::run(path),
        None => println!("{{}}"),
    }
}

fn print_usage() {
    eprintln!(
        r#"Usage:
  orchard                        Interactive worktree manager (popup mode)
  orchard init                   Interactive setup wizard for popup mode
  orchard chat --message <msg>   Send a prompt to the orchardist tmux session
  orchard chat [--target <s>] [--message <msg>]
                                   Default target: global_config.chat_target or first tmux_session
  orchard setup-remote <host>    Provision a remote host for orchard
  orchard upgrade                Upgrade to the latest version
  orchard heal                   Audit and repair drifted state (dry run)
  orchard heal --fix             Apply all safe automatic repairs
  orchard heal --json            Output health check results as JSON
  orchard watch                  Run event-driven watch daemon (Ctrl-C to stop)
  orchard watch --subscribe --id <id> --session <session> [--pane <pane>]
                                   Register a tmux subscriber for watch events
  orchard watch --unsubscribe --id <id>
                                   Unregister a tmux subscriber
  orchard webhook-serve [--port <n>]
                                   Receive GitHub webhooks and append to events.jsonl
                                   Requires ORCHARD_WEBHOOK_SECRET env var

Options:
  --version, -V  Print version and exit
  --json         Output worktree data as JSON and exit
  --toon         Output worktree data as TOON v2.0 and exit
                 (token-efficient format intended for AI agent consumption;
                 mutually exclusive with --json)

Navigation:
  1-9     Jump to worktree by number
  ↑/↓     Select worktree
  Enter/t tmux into worktree (creates session if needed, then exits)
  d       Delete selected worktree
  c       Cleanup merged worktrees
  h       Run heal check
  r       Refresh list
  q/Esc   Close popup (no switch)

Keybindings (after orchard init --install):
  prefix + o   Open orchard TUI popup
  prefix + O   Quick-chat to orchardist"#
    );
}

/// Handles the `orchard watch` command.
///
/// Without flags: runs the event-driven daemon loop (Ctrl-C to stop).
/// With `--subscribe`: registers a tmux subscriber.
/// With `--unsubscribe`: removes a subscriber.
fn handle_watch(args: &[String]) {
    let config = global_config::load_global_config();

    let mut subscribe = false;
    let mut unsubscribe = false;
    let mut id = String::new();
    let mut session = String::new();
    let mut pane = "0.0".to_string();
    let mut skip_next = false;

    for (i, arg) in args.iter().enumerate() {
        if skip_next {
            skip_next = false;
            continue;
        }
        match arg.as_str() {
            "--subscribe" => subscribe = true,
            "--unsubscribe" => unsubscribe = true,
            "--id" => {
                id = args.get(i + 1).cloned().unwrap_or_default();
                skip_next = true;
            }
            "--session" => {
                session = args.get(i + 1).cloned().unwrap_or_default();
                skip_next = true;
            }
            "--pane" => {
                pane = args.get(i + 1).cloned().unwrap_or_default();
                skip_next = true;
            }
            _ => {}
        }
    }

    if subscribe {
        if id.is_empty() || session.is_empty() {
            eprintln!(
                "Usage: orchard watch --subscribe --id <id> --session <session> [--pane <pane>]"
            );
            std::process::exit(1);
        }
        match orchard::watch::subscription::register(&id, &session, &pane) {
            Ok(()) => eprintln!("Subscribed: {id}"),
            Err(e) => {
                eprintln!("{e}");
                std::process::exit(1);
            }
        }
        return;
    }

    if unsubscribe {
        if id.is_empty() {
            eprintln!("Usage: orchard watch --unsubscribe --id <id>");
            std::process::exit(1);
        }
        match orchard::watch::subscription::unregister(&id) {
            Ok(()) => eprintln!("Unsubscribed: {id}"),
            Err(e) => {
                eprintln!("{e}");
                std::process::exit(1);
            }
        }
        return;
    }

    // Default: run the daemon.
    if let Err(e) = orchard::watch::daemon::run(&config) {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

/// Handles the `orchard webhook-serve` command.
///
/// Parses `--port <n>`, resolves the final port via
/// `webhook::port::resolve_port(flag, env, config)`, validates
/// ORCHARD_WEBHOOK_SECRET is set and non-empty, and runs the hyper server.
fn handle_webhook_serve(args: &[String]) {
    // 1. Parse --port flag from args.
    let mut port_flag: Option<u16> = None;
    let mut skip_next = false;
    for (i, arg) in args.iter().enumerate() {
        if skip_next {
            skip_next = false;
            continue;
        }
        if arg == "--port"
            && let Some(v) = args.get(i + 1)
        {
            port_flag = v.parse::<u16>().ok();
            skip_next = true;
        }
    }

    // 2. Read ORCHARD_WEBHOOK_PORT env var.
    let port_env: Option<u16> = env::var("ORCHARD_WEBHOOK_PORT")
        .ok()
        .and_then(|v| v.parse::<u16>().ok());

    // 3. Load global config, read config.watch.webhook_port.
    let config = global_config::load_global_config();
    let port_config: Option<u16> = config.watch.webhook_port;

    // 4. Resolve port.
    let port = orchard::webhook::port::resolve_port(port_flag, port_env, port_config);

    // 5. Read ORCHARD_WEBHOOK_SECRET env var.
    let secret = match env::var("ORCHARD_WEBHOOK_SECRET") {
        Ok(s) if !s.is_empty() => s,
        _ => {
            eprintln!(
                "webhook-serve: ORCHARD_WEBHOOK_SECRET is not set. \
                 Set it to your GitHub webhook secret."
            );
            std::process::exit(1);
        }
    };

    // 6. Run the server.
    if let Err(e) = orchard::webhook::server::run(port, secret.into_bytes()) {
        eprintln!("{e}");
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    fn should_exit(message: Option<&str>) -> bool {
        !matches!(message, Some(m) if !m.is_empty())
    }

    #[test]
    fn handle_chat_missing_message_exits_nonzero() {
        // We can't call handle_chat directly (it calls process::exit), but we can
        // verify the guard logic by checking the predicate.
        // The production path for None/empty is tested via binary integration.
        // This test documents the expected behaviour contract.
        assert!(
            should_exit(None),
            "missing message must trigger non-zero exit"
        );
    }

    #[test]
    fn handle_chat_empty_string_message_exits_nonzero() {
        assert!(
            should_exit(Some("")),
            "empty message must trigger non-zero exit"
        );
    }

    #[test]
    fn handle_chat_nonempty_message_does_not_exit() {
        assert!(
            !should_exit(Some("hello")),
            "non-empty message must not trigger exit"
        );
    }
}
