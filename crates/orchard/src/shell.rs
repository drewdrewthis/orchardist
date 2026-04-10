//! Shell environment setup for the `orchard init` wizard.
//!
//! Guides users through installing the tmux popup wrapper script, configuring
//! keybindings, selecting their preferred terminal app for notifications, and
//! optionally installing Claude Code hooks. Writes the resulting choices back
//! to `~/.config/orchard/config.json` via [`crate::global_config::save_global_config`].

use std::io::Write;
use std::path::Path;

// ---------------------------------------------------------------------------
// ANSI colour helpers (used only for stderr wizard output)
// ---------------------------------------------------------------------------

const BOLD: &str = "\x1b[1m";
const GREEN: &str = "\x1b[32m";
const YELLOW: &str = "\x1b[33m";
const CYAN: &str = "\x1b[36m";
const RESET: &str = "\x1b[0m";

/// Returns the orchard popup wrapper script content.
///
/// The `orchard_bin` parameter is the absolute path to the orchard binary,
/// embedded directly into the script so it works even when `~/.cargo/bin`
/// is not in PATH.
pub fn get_wrapper_script(orchard_bin: &str) -> String {
    format!(
        r#"#!/bin/sh
errfile=$(mktemp "${{TMPDIR:-/tmp}}/orchard-err.XXXXXX")
session=$("{orchard_bin}" "$@" 2>"$errfile")
rc=$?
if [ $rc -ne 0 ]; then
  msg=$(head -1 "$errfile" 2>/dev/null)
  rm -f "$errfile"
  tmux display-message "orchard: ${{msg:-unknown error}}"
elif [ -n "$session" ]; then
  rm -f "$errfile"
  tmux switch-client -t "$session"
else
  rm -f "$errfile"
fi"#,
        orchard_bin = orchard_bin
    )
}

/// Returns the tmux.conf keybinding line for the popup, using the given key.
pub fn get_tmux_binding(key: &str) -> String {
    format!(r#"bind-key {key} display-popup -E -w 90% -h 80% "$HOME/.local/bin/orchard-popup""#)
}

/// Returns the orchard-chat wrapper script content.
///
/// The script reads a single line from the user, passes it to `orchard chat
/// --message`, and surfaces any errors via `tmux display-message`.
///
/// The `orchard_bin` parameter is the absolute path to the orchard binary,
/// embedded directly into the script so it works even when `~/.cargo/bin`
/// is not in PATH.
pub fn get_chat_wrapper_script(orchard_bin: &str) -> String {
    format!(
        r#"#!/bin/sh
printf "> "
read -r prompt
if [ -z "$prompt" ]; then
  exit 0
fi
errfile=$(mktemp "${{TMPDIR:-/tmp}}/orchard-chat-err.XXXXXX")
trap 'rm -f "$errfile"' EXIT
"{orchard_bin}" chat --message "$prompt" 2>"$errfile"
rc=$?
if [ $rc -ne 0 ]; then
  msg=$(head -1 "$errfile" 2>/dev/null)
  rm -f "$errfile"
  tmux display-message "orchard chat: ${{msg:-unknown error}}"
else
  rm -f "$errfile"
fi"#,
        orchard_bin = orchard_bin
    )
}

/// Returns the tmux.conf keybinding line for the chat popup, using the given key.
///
/// Default key is `O` (capital O) to avoid conflicting with the existing
/// `o` binding for the main orchard TUI.
pub fn get_chat_tmux_binding(key: &str) -> String {
    format!(r#"bind-key {key} display-popup -E -w 60% -h 20% "$HOME/.local/bin/orchard-chat""#)
}

const MARKER_START: &str = "# >>> orchard >>>";
const MARKER_END: &str = "# <<< orchard <<<";

/// Total number of wizard steps — update when adding or removing steps.
const TOTAL_STEPS: usize = 9;

/// Runs the interactive `orchard init` wizard.
///
/// Walks the user through:
///  1. Checking tmux version
///  2. Removing old shell function markers from RC files
///  3. Installing the wrapper script
///  4. Configuring the tmux keybinding
///  5. Optionally adding a status bar segment
///  6. Reloading tmux config
///  7. Installing Claude Code hooks
///  8. Selecting the terminal app for notifications (macOS only)
///
/// Persists the chosen terminal app to `~/.config/orchard/config.json`.
pub fn run_init_wizard() -> Result<(), String> {
    let home = dirs::home_dir().ok_or_else(|| "Could not determine home directory".to_string())?;

    // Step 1: Check tmux version.
    let tmux_version = check_tmux_version_step()?;

    // Step 2: Check for old shell function.
    remove_old_shell_function_step(&home)?;

    // Step 3: Create wrapper script.
    install_wrapper(&home)?;

    // Steps 4 & 5: Configure tmux keybinding (and optionally status bar).
    let key = configure_tmux_binding_step(&home, &tmux_version)?;

    // Step 6: Reload tmux config.
    reload_tmux_config_step(&home);

    // Step 7: Install Claude Code hooks.
    if let Err(e) = install_claude_hooks(&home) {
        eprintln!("  {YELLOW}Warning: could not install Claude hooks: {e}{RESET}");
    }

    // Step 8: Select terminal app for notifications (macOS only).
    let terminal_app = select_terminal_app_step();

    // Step 9: Offer shepherd session setup.
    let shepherd_config = suggest_shepherd_session_step();

    // Persist choices to global config.
    let mut cfg = crate::global_config::load_global_config();
    cfg.terminal_app = terminal_app.clone();
    if let Some(shepherd) = shepherd_config {
        cfg.tmux_sessions.push(shepherd);
    }
    if let Err(e) = crate::global_config::save_global_config(&cfg) {
        eprintln!("  {YELLOW}Warning: could not save config: {e}{RESET}");
    }

    // Summary.
    eprintln!();
    eprintln!("{BOLD}{GREEN}Setup complete!{RESET}");
    eprintln!("  Tmux key binding : prefix + {BOLD}{key}{RESET}");
    if cfg!(target_os = "macos") {
        eprintln!("  Terminal app     : {BOLD}{terminal_app}{RESET}");
    }
    eprintln!();
    eprintln!("Press prefix + {key} to open orchard.");
    Ok(())
}

/// Step 1: Check tmux version. Returns the version string (e.g. "3.4") on success.
fn check_tmux_version_step() -> Result<String, String> {
    eprintln!("{BOLD}{CYAN}[1/{TOTAL_STEPS}] Checking tmux version{RESET}");
    let out = std::process::Command::new("tmux")
        .args(["-V"])
        .output()
        .map_err(|e| format!("Could not run tmux: {e}"))?;

    let version_str = String::from_utf8_lossy(&out.stdout).trim().to_string();
    let version_part = version_str.trim_start_matches("tmux").trim().to_string();

    match parse_tmux_version(&version_part) {
        Some(v) if v >= (3, 2) => {
            eprintln!("  tmux {version_part} detected");
        }
        Some(_) => {
            return Err(format!(
                "tmux >= 3.2 is required for popup mode (you have {version_part}). Please upgrade tmux."
            ));
        }
        None => {
            return Err(format!(
                "Could not parse tmux version from {:?}",
                version_str
            ));
        }
    }

    Ok(version_part)
}

/// Step 2: Check RC files for old shell function markers and optionally remove them.
fn remove_old_shell_function_step(home: &Path) -> Result<(), String> {
    eprintln!("{BOLD}{CYAN}[2/{TOTAL_STEPS}] Checking for old shell function{RESET}");
    for rc in &[".zshrc", ".bashrc"] {
        let rc_path = home.join(rc);
        if let Ok(content) = std::fs::read_to_string(&rc_path)
            && content.contains(MARKER_START)
        {
            eprintln!("  Found old orchard shell function in ~/{rc}");
            if prompt_yn("  Remove it? [Y/n]", true) {
                remove_old_shell_function(&rc_path)?;
                eprintln!("  Removed old shell function from ~/{rc}");
            } else {
                eprintln!("  Old shell function left in place — it may conflict with popup mode");
            }
        }
    }
    Ok(())
}

/// Removes the marker block from the given RC file.
pub fn remove_old_shell_function(rc_path: &Path) -> Result<(), String> {
    let content = std::fs::read_to_string(rc_path)
        .map_err(|e| format!("reading {}: {e}", rc_path.display()))?;
    let cleaned = remove_marker_block(&content);
    std::fs::write(rc_path, cleaned).map_err(|e| format!("writing {}: {e}", rc_path.display()))?;
    Ok(())
}

/// Strips all content between (and including) marker lines from the given string.
fn remove_marker_block(content: &str) -> String {
    let mut result = String::new();
    let mut inside = false;
    for line in content.lines() {
        if line.contains(MARKER_START) {
            inside = true;
            continue;
        }
        if line.contains(MARKER_END) {
            inside = false;
            continue;
        }
        if !inside {
            result.push_str(line);
            result.push('\n');
        }
    }
    result
}

/// Step 3: Install the wrapper scripts to ~/.local/bin/.
///
/// Writes both `orchard-popup` (TUI popup) and `orchard-chat` (quick-chat
/// popup) and makes both executable. Safe to run multiple times (idempotent).
fn install_wrapper(home: &Path) -> Result<(), String> {
    eprintln!("{BOLD}{CYAN}[3/{TOTAL_STEPS}] Creating wrapper scripts{RESET}");

    // Resolve the absolute path to the running orchard binary so the wrapper
    // scripts work even when ~/.cargo/bin is not in PATH.
    // Note: we intentionally skip canonicalize() to preserve symlink paths
    // (e.g. ~/Library/pnpm/orchard -> target/release/orchard).
    let orchard_bin = std::env::current_exe()
        .ok()
        .map(|p| p.to_string_lossy().into_owned())
        .unwrap_or_else(|| {
            eprintln!("  {YELLOW}Warning: could not resolve orchard binary path, using bare 'orchard'{RESET}");
            "orchard".to_string()
        });

    let bin_dir = home.join(".local/bin");
    std::fs::create_dir_all(&bin_dir).map_err(|e| format!("creating ~/.local/bin: {e}"))?;

    // Install orchard-popup.
    let popup_path = bin_dir.join("orchard-popup");
    std::fs::write(&popup_path, get_wrapper_script(&orchard_bin))
        .map_err(|e| format!("writing {}: {e}", popup_path.display()))?;

    // Install orchard-chat.
    let chat_path = bin_dir.join("orchard-chat");
    std::fs::write(&chat_path, get_chat_wrapper_script(&orchard_bin))
        .map_err(|e| format!("writing {}: {e}", chat_path.display()))?;

    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        for script_path in &[&popup_path, &chat_path] {
            let mut perms = std::fs::metadata(script_path)
                .map_err(|e| format!("stat {}: {e}", script_path.display()))?
                .permissions();
            perms.set_mode(0o755);
            std::fs::set_permissions(script_path, perms)
                .map_err(|e| format!("chmod {}: {e}", script_path.display()))?;
        }
    }

    // Warn if ~/.local/bin is not in PATH.
    let path_var = std::env::var("PATH").unwrap_or_default();
    let bin_str = bin_dir.to_string_lossy();
    if !path_var
        .split(':')
        .any(|p| p == bin_str.as_ref() || p == "~/.local/bin")
    {
        eprintln!("  Warning: ~/.local/bin is not in your PATH");
        eprintln!(r#"    Add: export PATH="$HOME/.local/bin:$PATH""#);
    }

    eprintln!("  Created ~/.local/bin/orchard-popup");
    eprintln!("  Created ~/.local/bin/orchard-chat");
    Ok(())
}

/// Returns the recommended `tmux set -g status-right` line for orchard.
///
/// The `state_dir` parameter should be the path to `~/.local/state/orchard`.
/// The returned string is suitable for pasting directly into `tmux.conf` or
/// running via `tmux source-file`.
pub fn get_tmux_status_line(state_dir: &Path) -> String {
    format!(
        "set -g status-right \"#(cat {}/status.txt) | %H:%M\"",
        state_dir.display()
    )
}

/// Steps 4 & 5: Prompt for key, inject tmux binding (and optionally status bar) into tmux.conf.
/// Returns the key chosen by the user.
fn configure_tmux_binding_step(home: &Path, _tmux_version: &str) -> Result<String, String> {
    eprintln!("{BOLD}{CYAN}[4/{TOTAL_STEPS}] Configuring tmux keybinding{RESET}");
    let tmux_conf = detect_tmux_conf(home);

    let existing = std::fs::read_to_string(&tmux_conf).unwrap_or_else(|e| {
        tracing::warn!("could not read tmux config at {}: {e}", tmux_conf.display());
        String::new()
    });

    // Warn about existing bind-key o conflict (only if it's not our binding).
    if (existing.contains("bind-key o") || existing.contains("bind o "))
        && !existing.contains("orchard-popup")
    {
        eprintln!("  Warning: prefix + o is already bound in tmux.conf");
    }

    let key = prompt_key("  Bind popup to which key? [o]", "o");

    // Step 5: Optionally add status bar segment.
    eprintln!("{BOLD}{CYAN}[5/{TOTAL_STEPS}] Status bar{RESET}");
    let state_dir = home.join(".local/state/orchard");
    let status_line = get_tmux_status_line(&state_dir);

    let add_status = prompt_yn("  Add orchard status to tmux status bar? [Y/n]", true);

    // Warn about conflict between the main popup key and chat key.
    // Then prompt the user for the chat key (default O).
    let default_chat_key = if key == "O" { "P" } else { "O" };
    if key == "O" {
        eprintln!("  Warning: prefix + O would conflict with the main popup key; defaulting to P");
    }
    let chat_key = prompt_key(
        &format!("  Bind quick-chat popup to which key? [{default_chat_key}]"),
        default_chat_key,
    );
    if chat_key == key {
        eprintln!("  Warning: quick-chat key conflicts with main popup key (prefix + {key})");
    }
    let binding = get_tmux_binding(&key);
    let chat_binding = get_chat_tmux_binding(&chat_key);
    let block_content = if add_status {
        format!("{binding}\n{chat_binding}\n{status_line}")
    } else {
        format!("{binding}\n{chat_binding}")
    };

    let updated = inject_config_block(&existing, &block_content);
    std::fs::write(&tmux_conf, &updated)
        .map_err(|e| format!("writing {}: {e}", tmux_conf.display()))?;

    eprintln!("  Added keybinding to {}", tmux_conf.display());
    if add_status {
        eprintln!("  Added status bar segment");
    } else {
        eprintln!("  To configure the tmux status bar later, add this to your tmux.conf:");
        eprintln!("    {status_line}");
    }

    Ok(key)
}

/// Detects the tmux.conf path: prefers XDG location, falls back to ~/.tmux.conf.
fn detect_tmux_conf(home: &Path) -> std::path::PathBuf {
    let xdg = home.join(".config/tmux/tmux.conf");
    if xdg.exists() {
        return xdg;
    }
    home.join(".tmux.conf")
}

/// Step 6: Reload tmux config if inside a tmux session.
fn reload_tmux_config_step(home: &Path) {
    eprintln!("{BOLD}{CYAN}[6/{TOTAL_STEPS}] Cleaning up and reloading{RESET}");
    let tmux_conf = detect_tmux_conf(home);

    if std::env::var("TMUX").is_ok() {
        // Remove the old session-closed hook that would unbind 'o' when
        // the legacy orchard session dies.
        if let Err(e) = std::process::Command::new("tmux")
            .args(["set-hook", "-gu", "session-closed[99]"])
            .status()
        {
            tracing::warn!("tmux cleanup command failed: {e}");
        }

        // Kill any legacy *_orchard sessions so they don't interfere.
        if let Ok(output) = std::process::Command::new("tmux")
            .args(["list-sessions", "-F", "#{session_name}"])
            .output()
        {
            let sessions = String::from_utf8_lossy(&output.stdout);
            for session in sessions.lines() {
                if session.ends_with("_orchard") {
                    eprintln!("  Killing legacy session: {session}");
                    if let Err(e) = std::process::Command::new("tmux")
                        .args(["kill-session", "-t", session])
                        .status()
                    {
                        tracing::warn!("tmux cleanup command failed: {e}");
                    }
                }
            }
        }

        // Unbind the old 'o' key first — runtime bindings from the old
        // orchard binary take precedence over config-file bindings, so
        // source-file alone won't override them.
        if let Err(e) = std::process::Command::new("tmux")
            .args(["unbind-key", "o"])
            .status()
        {
            tracing::warn!("tmux cleanup command failed: {e}");
        }

        // Reload the tmux config to activate the new popup binding.
        let result = std::process::Command::new("tmux")
            .args(["source-file", &tmux_conf.to_string_lossy()])
            .status();
        match result {
            Ok(s) if s.success() => {
                eprintln!("  ✓ Tmux config reloaded");
            }
            _ => {
                eprintln!("  Warning: could not reload tmux config");
                eprintln!("  Run: tmux source-file {}", tmux_conf.display());
            }
        }
    } else {
        eprintln!(
            "  Run `tmux source-file {}` to activate, or restart tmux",
            tmux_conf.display()
        );
    }
}

/// Prompts the user with a Y/n question and returns true for yes, false for no.
/// Loops until a valid response is given. Empty line returns `default_yes`.
pub fn prompt_yn(question: &str, default_yes: bool) -> bool {
    loop {
        eprint!("{question} ");
        std::io::stderr().flush().ok();

        let mut line = String::new();
        if std::io::stdin().read_line(&mut line).is_err() {
            return default_yes;
        }

        match line.trim().to_lowercase().as_str() {
            "" => return default_yes,
            "y" => return true,
            "n" => return false,
            _ => {
                eprintln!("  Please enter 'y' or 'n'");
            }
        }
    }
}

/// Prompts the user for a single alphanumeric key character.
/// Loops until a valid response is given. Empty line returns `default`.
pub fn prompt_key(question: &str, default: &str) -> String {
    loop {
        eprint!("{question} ");
        std::io::stderr().flush().ok();

        let mut line = String::new();
        if std::io::stdin().read_line(&mut line).is_err() {
            return default.to_string();
        }

        let trimmed = line.trim();
        if trimmed.is_empty() {
            return default.to_string();
        }

        let chars: Vec<char> = trimmed.chars().collect();
        if chars.len() == 1 && chars[0].is_ascii_alphanumeric() {
            return trimmed.to_string();
        }

        eprintln!("  Please enter a single alphanumeric character");
    }
}

// ---------------------------------------------------------------------------
// Terminal app selection (Step 8, macOS only)
// ---------------------------------------------------------------------------

/// Known terminal options presented in the wizard menu.
///
/// Each entry is `(display_label, bundle_id)`. The last entry is the
/// "Other" catch-all that prompts for a custom bundle ID.
pub const TERMINAL_OPTIONS: &[(&str, &str)] = &[
    ("Terminal.app (default)", "com.apple.Terminal"),
    ("iTerm2", "com.googlecode.iterm2"),
    ("Warp", "dev.warp.Warp-Stable"),
    ("Alacritty", "org.alacritty"),
    ("Ghostty", "com.mitchellh.ghostty"),
];

/// Parses a numbered menu selection (1-based) into the corresponding bundle ID.
///
/// Returns `None` for selections that are out of range or not a valid number.
/// Selection `TERMINAL_OPTIONS.len() + 1` maps to the "Other" option and also
/// returns `None` (the caller must prompt for a custom bundle ID).
pub fn parse_terminal_selection(input: &str) -> Option<String> {
    let n: usize = input.trim().parse().ok()?;
    if n == 0 || n > TERMINAL_OPTIONS.len() + 1 {
        return None;
    }
    if n == TERMINAL_OPTIONS.len() + 1 {
        // "Other" — caller handles custom input
        return None;
    }
    Some(TERMINAL_OPTIONS[n - 1].1.to_string())
}

/// Step 8: Prompt the user to select their preferred terminal app.
///
/// On non-macOS platforms this step is skipped and the default is returned.
fn select_terminal_app_step() -> String {
    if !cfg!(target_os = "macos") {
        return "com.apple.Terminal".to_string();
    }

    eprintln!("{BOLD}{CYAN}[8/{TOTAL_STEPS}] Terminal app for notifications{RESET}");
    prompt_terminal_app()
}

/// Step 9: Offer to configure a shepherd tmux session.
///
/// Returns `Some(StandaloneConfig)` if the user accepts, `None` if declined.
fn suggest_shepherd_session_step() -> Option<crate::session::StandaloneConfig> {
    eprintln!("{BOLD}{CYAN}[9/{TOTAL_STEPS}] Shepherd session (optional){RESET}");
    eprintln!("  A shepherd is a persistent tmux session for cross-repo orchestration.");
    eprintln!("  It can run a Claude agent that monitors all your repos via Telegram/Discord.");
    eprintln!();
    if prompt_yn("  Configure a shepherd session? [y/N]", false) {
        Some(crate::session::StandaloneConfig {
            name: "shepherd".to_string(),
            command: "claude --agent shepherd".to_string(),
            cwd: "~/.config/orchard".to_string(),
            start_on_launch: true,
        })
    } else {
        eprintln!("  Skipped. You can add one later in ~/.config/orchard/config.json");
        None
    }
}

/// Presents a numbered terminal menu and returns the chosen bundle ID.
///
/// Only called on macOS. Empty input defaults to `"com.apple.Terminal"`.
pub fn prompt_terminal_app() -> String {
    eprintln!("  Which terminal should open when you click a notification?");
    eprintln!();
    for (i, (label, _bundle)) in TERMINAL_OPTIONS.iter().enumerate() {
        eprintln!("    {}. {}", i + 1, label);
    }
    eprintln!(
        "    {}. Other (enter bundle ID)",
        TERMINAL_OPTIONS.len() + 1
    );
    eprintln!();

    let other_choice = TERMINAL_OPTIONS.len() + 1;

    loop {
        eprint!("  Choice [1]: ");
        std::io::stderr().flush().ok();

        let mut line = String::new();
        if std::io::stdin().read_line(&mut line).is_err() {
            return "com.apple.Terminal".to_string();
        }

        let trimmed = line.trim();

        // Empty → default
        if trimmed.is_empty() {
            return "com.apple.Terminal".to_string();
        }

        if let Some(bundle_id) = parse_terminal_selection(trimmed) {
            return bundle_id;
        }

        // "Other" option — parse_terminal_selection returns None for this too
        if trimmed.parse::<usize>() == Ok(other_choice) {
            return prompt_custom_bundle_id();
        }

        eprintln!("  {YELLOW}Please enter a number between 1 and {other_choice}{RESET}");
    }
}

/// Prompts for a custom macOS bundle ID when the user selects "Other".
fn prompt_custom_bundle_id() -> String {
    loop {
        eprint!("  Enter bundle ID (e.g. net.kovidgoyal.kitty): ");
        std::io::stderr().flush().ok();

        let mut line = String::new();
        if std::io::stdin().read_line(&mut line).is_err() {
            return "com.apple.Terminal".to_string();
        }

        let trimmed = line.trim().to_string();
        if !trimmed.is_empty() {
            return trimmed;
        }

        eprintln!("  {YELLOW}Bundle ID cannot be empty{RESET}");
    }
}

/// Parses a tmux version string like "3.2", "3.2a", "next-3.5" into (major, minor).
/// Returns `None` for unparseable strings.
pub fn parse_tmux_version(s: &str) -> Option<(u32, u32)> {
    // Strip leading "next-" or similar alphabetic prefix.
    let s = s.trim();
    let s = if let Some(pos) = s.rfind('-') {
        &s[pos + 1..]
    } else {
        s
    };
    // Now s looks like "3.2" or "3.2a" or "3.4".
    let dot = s.find('.')?;
    let major: u32 = s[..dot].parse().ok()?;
    // Minor part may have trailing letters: "2a" → 2.
    let minor_str = &s[dot + 1..];
    let minor_end = minor_str
        .find(|c: char| !c.is_ascii_digit())
        .unwrap_or(minor_str.len());
    let minor: u32 = minor_str[..minor_end].parse().ok()?;
    Some((major, minor))
}

/// Injects the tmux binding block between markers into `existing`, replacing any existing block.
/// Safe to run multiple times (idempotent).
pub fn inject_config_block(existing: &str, content: &str) -> String {
    let new_block = format!("{MARKER_START}\n{content}\n{MARKER_END}");

    if let Some(start) = existing.find(MARKER_START)
        && let Some(end_offset) = existing[start..].find(MARKER_END)
    {
        let end = start + end_offset + MARKER_END.len();
        return format!("{}{}{}", &existing[..start], new_block, &existing[end..]);
    }

    if existing.is_empty() || existing.ends_with('\n') {
        format!("{existing}{new_block}\n")
    } else {
        format!("{existing}\n{new_block}\n")
    }
}

// ---------------------------------------------------------------------------
// Claude Code hook installation
// ---------------------------------------------------------------------------

const HOOK_EVENTS: &[&str] = &[
    "PreToolUse",
    "PostToolUse",
    "Stop",
    "Notification",
    "SessionStart",
    "SessionEnd",
];

const HOOK_SCRIPT_CONTENT: &str = include_str!("../hooks/orchard-state.sh");

/// Installs the orchard Claude Code hook script and registers it in settings.json.
///
/// Idempotent: re-running will update the script and avoid duplicate hook entries.
pub fn install_claude_hooks(home: &Path) -> Result<(), String> {
    eprintln!("{BOLD}{CYAN}[7/{TOTAL_STEPS}] Installing Claude Code hooks{RESET}");

    let hooks_dir = home.join(".claude/hooks");
    std::fs::create_dir_all(&hooks_dir).map_err(|e| format!("creating ~/.claude/hooks: {e}"))?;

    // Install the hook script.
    let script_path = hooks_dir.join("orchard-state.sh");
    std::fs::write(&script_path, HOOK_SCRIPT_CONTENT)
        .map_err(|e| format!("writing {}: {e}", script_path.display()))?;

    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut perms = std::fs::metadata(&script_path)
            .map_err(|e| format!("stat {}: {e}", script_path.display()))?
            .permissions();
        perms.set_mode(0o755);
        std::fs::set_permissions(&script_path, perms)
            .map_err(|e| format!("chmod {}: {e}", script_path.display()))?;
    }

    eprintln!("  Installed ~/.claude/hooks/orchard-state.sh");

    // Register hooks in settings.json.
    let settings_path = home.join(".claude/settings.json");
    let hook_command = "~/.claude/hooks/orchard-state.sh";
    register_claude_hooks(&settings_path, hook_command)?;

    eprintln!("  Registered hooks in ~/.claude/settings.json");
    Ok(())
}

/// Merges orchard hook registrations into ~/.claude/settings.json.
///
/// Reads the existing JSON, adds any missing hook event entries, and writes
/// back atomically. Does not duplicate entries that already exist.
pub fn register_claude_hooks(settings_path: &Path, hook_command: &str) -> Result<(), String> {
    let existing = if settings_path.exists() {
        std::fs::read_to_string(settings_path)
            .map_err(|e| format!("reading {}: {e}", settings_path.display()))?
    } else {
        "{}".to_string()
    };

    let mut settings: serde_json::Value =
        serde_json::from_str(&existing).unwrap_or_else(|_| serde_json::json!({}));

    let hooks_obj = settings
        .as_object_mut()
        .ok_or_else(|| "settings.json root is not an object".to_string())?
        .entry("hooks")
        .or_insert_with(|| serde_json::json!({}))
        .as_object_mut()
        .ok_or_else(|| "hooks field is not an object".to_string())?;

    // Ensure all required hook events are registered without duplicating.
    for &event in HOOK_EVENTS {
        let event_hooks = hooks_obj
            .entry(event)
            .or_insert_with(|| serde_json::json!([]))
            .as_array_mut()
            .ok_or_else(|| format!("hooks.{event} is not an array"))?;

        let already_registered = event_hooks.iter().any(|entry| {
            entry
                .get("hooks")
                .and_then(|h| h.as_array())
                .map(|arr| {
                    arr.iter().any(|hook| {
                        hook.get("command").and_then(|c| c.as_str()) == Some(hook_command)
                    })
                })
                .unwrap_or(false)
        });

        if !already_registered {
            event_hooks.push(serde_json::json!({
                "hooks": [{ "type": "command", "command": hook_command }]
            }));
        }
    }

    // Write atomically via temp file.
    let dir = settings_path
        .parent()
        .ok_or_else(|| "settings.json has no parent dir".to_string())?;
    std::fs::create_dir_all(dir).map_err(|e| format!("creating {}: {e}", dir.display()))?;

    let tmp_path = settings_path.with_extension("json.tmp");
    let json = serde_json::to_string_pretty(&settings)
        .map_err(|e| format!("serializing settings: {e}"))?;
    std::fs::write(&tmp_path, &json).map_err(|e| format!("writing {}: {e}", tmp_path.display()))?;
    std::fs::rename(&tmp_path, settings_path)
        .map_err(|e| format!("renaming to {}: {e}", settings_path.display()))?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wrapper_script_contains_git_orchard_call() {
        assert!(get_wrapper_script("/usr/local/bin/orchard").contains("orchard"));
    }

    #[test]
    fn wrapper_script_captures_stdout_as_session() {
        assert!(
            get_wrapper_script("/usr/local/bin/orchard")
                .contains("session=$(\"/usr/local/bin/orchard\"")
        );
    }

    #[test]
    fn wrapper_script_forwards_arguments() {
        assert!(get_wrapper_script("/usr/local/bin/orchard").contains("\"$@\""));
    }

    #[test]
    fn wrapper_script_calls_switch_client() {
        assert!(get_wrapper_script("/usr/local/bin/orchard").contains("tmux switch-client -t"));
    }

    #[test]
    fn wrapper_script_shows_error_on_failure() {
        assert!(get_wrapper_script("/usr/local/bin/orchard").contains("tmux display-message"));
    }

    #[test]
    fn tmux_binding_uses_display_popup() {
        assert!(get_tmux_binding("o").contains("display-popup"));
    }

    #[test]
    fn tmux_binding_binds_key_o() {
        assert!(get_tmux_binding("o").contains("bind-key o"));
    }

    #[test]
    fn tmux_binding_references_orchard_popup_script() {
        assert!(get_tmux_binding("o").contains("orchard-popup"));
    }

    #[test]
    fn tmux_binding_custom_key_g() {
        let binding = get_tmux_binding("g");
        assert!(binding.contains("bind-key g"));
        assert!(!binding.contains("bind-key o"));
    }

    #[test]
    fn inject_into_empty_file() {
        let result = inject_config_block("", "bind-key o display-popup");
        assert!(result.contains(MARKER_START));
        assert!(result.contains(MARKER_END));
        assert!(result.contains("bind-key o display-popup"));
    }

    #[test]
    fn inject_appends_to_existing_content() {
        let result = inject_config_block("set -g status on\n", "bind-key o display-popup");
        assert!(result.starts_with("set -g status on\n"));
        assert!(result.contains(MARKER_START));
    }

    #[test]
    fn inject_replaces_existing_block() {
        let existing = format!(
            "before\n{}\nold binding\n{}\nafter\n",
            MARKER_START, MARKER_END
        );
        let result = inject_config_block(&existing, "new binding");
        assert!(result.contains("new binding"));
        assert!(!result.contains("old binding"));
        assert!(result.starts_with("before\n"));
        assert!(result.ends_with("after\n"));
    }

    #[test]
    fn inject_is_idempotent() {
        let first = inject_config_block("", "bind-key o display-popup");
        let second = inject_config_block(&first, "bind-key o display-popup");
        assert_eq!(first, second);
    }

    #[test]
    fn parse_version_plain() {
        assert_eq!(parse_tmux_version("3.2"), Some((3, 2)));
    }

    #[test]
    fn parse_version_with_letter_suffix() {
        assert_eq!(parse_tmux_version("3.2a"), Some((3, 2)));
    }

    #[test]
    fn parse_version_higher_minor() {
        assert_eq!(parse_tmux_version("3.4"), Some((3, 4)));
    }

    #[test]
    fn parse_version_lower_than_required() {
        assert_eq!(parse_tmux_version("3.0a"), Some((3, 0)));
    }

    #[test]
    fn parse_version_next_prefix() {
        // "next-3.5" → (3, 5)
        let v = parse_tmux_version("next-3.5").unwrap();
        assert!(v >= (3, 2));
    }

    #[test]
    fn parse_version_unparseable_returns_none() {
        assert_eq!(parse_tmux_version("garbage"), None);
    }

    #[test]
    fn parse_version_ordering() {
        // 3.2 >= 3.2
        assert!(parse_tmux_version("3.2").unwrap() >= (3, 2));
        // 3.4 >= 3.2
        assert!(parse_tmux_version("3.4").unwrap() >= (3, 2));
        // 3.0a NOT >= 3.2
        assert!(parse_tmux_version("3.0a").unwrap() < (3, 2));
        // 3.1c NOT >= 3.2
        assert!(parse_tmux_version("3.1c").unwrap() < (3, 2));
    }

    #[test]
    fn remove_old_shell_function_strips_marker_block() {
        let content =
            format!("# before\n{MARKER_START}\norchard() {{ echo old; }}\n{MARKER_END}\n# after\n");
        let result = remove_marker_block(&content);
        assert!(!result.contains("orchard()"));
        assert!(!result.contains(MARKER_START));
        assert!(!result.contains(MARKER_END));
        assert!(result.contains("# before"));
        assert!(result.contains("# after"));
    }

    #[test]
    fn remove_old_shell_function_leaves_content_without_markers_unchanged() {
        let content = "# just some config\nexport FOO=bar\n";
        let result = remove_marker_block(content);
        assert_eq!(result, content);
    }

    #[test]
    fn remove_old_shell_function_handles_multiple_blocks() {
        let content = format!(
            "{MARKER_START}\nfirst\n{MARKER_END}\nmiddle\n{MARKER_START}\nsecond\n{MARKER_END}\n"
        );
        let result = remove_marker_block(&content);
        assert!(!result.contains("first"));
        assert!(!result.contains("second"));
        assert!(result.contains("middle"));
    }

    #[test]
    fn register_claude_hooks_creates_settings_from_empty() {
        let dir = tempfile::tempdir().unwrap();
        let settings_path = dir.path().join("settings.json");

        register_claude_hooks(&settings_path, "~/.claude/hooks/orchard-state.sh").unwrap();

        let content = std::fs::read_to_string(&settings_path).unwrap();
        let settings: serde_json::Value = serde_json::from_str(&content).unwrap();

        assert!(settings["hooks"]["PreToolUse"].is_array());
        assert!(settings["hooks"]["Stop"].is_array());
        assert!(settings["hooks"]["SessionEnd"].is_array());
    }

    #[test]
    fn register_claude_hooks_does_not_duplicate() {
        let dir = tempfile::tempdir().unwrap();
        let settings_path = dir.path().join("settings.json");

        register_claude_hooks(&settings_path, "~/.claude/hooks/orchard-state.sh").unwrap();
        register_claude_hooks(&settings_path, "~/.claude/hooks/orchard-state.sh").unwrap();

        let content = std::fs::read_to_string(&settings_path).unwrap();
        let settings: serde_json::Value = serde_json::from_str(&content).unwrap();

        let pre_tool_hooks = settings["hooks"]["PreToolUse"].as_array().unwrap();
        assert_eq!(pre_tool_hooks.len(), 1, "should not duplicate hook entries");
    }

    #[test]
    fn register_claude_hooks_merges_with_existing_hooks() {
        let dir = tempfile::tempdir().unwrap();
        let settings_path = dir.path().join("settings.json");

        // Pre-populate with an existing hook for PreToolUse
        let existing = serde_json::json!({
            "hooks": {
                "PreToolUse": [{"hooks": [{"type": "command", "command": "other-hook.sh"}]}]
            }
        });
        std::fs::write(
            &settings_path,
            serde_json::to_string_pretty(&existing).unwrap(),
        )
        .unwrap();

        register_claude_hooks(&settings_path, "~/.claude/hooks/orchard-state.sh").unwrap();

        let content = std::fs::read_to_string(&settings_path).unwrap();
        let settings: serde_json::Value = serde_json::from_str(&content).unwrap();

        let pre_tool_hooks = settings["hooks"]["PreToolUse"].as_array().unwrap();
        assert_eq!(
            pre_tool_hooks.len(),
            2,
            "should preserve existing hook and add new one"
        );
    }

    #[test]
    fn register_claude_hooks_registers_all_required_events() {
        let dir = tempfile::tempdir().unwrap();
        let settings_path = dir.path().join("settings.json");

        register_claude_hooks(&settings_path, "~/.claude/hooks/orchard-state.sh").unwrap();

        let content = std::fs::read_to_string(&settings_path).unwrap();
        let settings: serde_json::Value = serde_json::from_str(&content).unwrap();

        for event in &[
            "PreToolUse",
            "PostToolUse",
            "Stop",
            "Notification",
            "SessionEnd",
        ] {
            assert!(
                settings["hooks"][event].is_array(),
                "expected hook for event: {event}"
            );
        }
    }

    // -----------------------------------------------------------------------
    // Terminal selection parsing
    // -----------------------------------------------------------------------

    #[test]
    fn parse_terminal_selection_picks_first_option() {
        assert_eq!(
            parse_terminal_selection("1"),
            Some("com.apple.Terminal".to_string())
        );
    }

    #[test]
    fn parse_terminal_selection_picks_iterm2() {
        assert_eq!(
            parse_terminal_selection("2"),
            Some("com.googlecode.iterm2".to_string())
        );
    }

    #[test]
    fn parse_terminal_selection_picks_warp() {
        assert_eq!(
            parse_terminal_selection("3"),
            Some("dev.warp.Warp-Stable".to_string())
        );
    }

    #[test]
    fn parse_terminal_selection_picks_alacritty() {
        assert_eq!(
            parse_terminal_selection("4"),
            Some("org.alacritty".to_string())
        );
    }

    #[test]
    fn parse_terminal_selection_picks_ghostty() {
        assert_eq!(
            parse_terminal_selection("5"),
            Some("com.mitchellh.ghostty".to_string())
        );
    }

    #[test]
    fn parse_terminal_selection_other_returns_none() {
        // "Other" selection — caller must prompt for custom ID.
        let other_idx = (TERMINAL_OPTIONS.len() + 1).to_string();
        assert_eq!(parse_terminal_selection(&other_idx), None);
    }

    #[test]
    fn parse_terminal_selection_empty_returns_none() {
        assert_eq!(parse_terminal_selection(""), None);
    }

    #[test]
    fn parse_terminal_selection_out_of_range_returns_none() {
        assert_eq!(parse_terminal_selection("99"), None);
    }

    #[test]
    fn parse_terminal_selection_zero_returns_none() {
        assert_eq!(parse_terminal_selection("0"), None);
    }

    #[test]
    fn parse_terminal_selection_non_numeric_returns_none() {
        assert_eq!(parse_terminal_selection("abc"), None);
    }

    #[test]
    fn parse_terminal_selection_trims_whitespace() {
        assert_eq!(
            parse_terminal_selection("  1  "),
            Some("com.apple.Terminal".to_string())
        );
    }

    #[test]
    fn terminal_options_has_five_known_entries() {
        assert_eq!(TERMINAL_OPTIONS.len(), 5);
    }

    // -----------------------------------------------------------------------
    // Chat wrapper script and binding
    // -----------------------------------------------------------------------

    #[test]
    fn chat_wrapper_script_contains_orchard_chat_message() {
        assert!(
            get_chat_wrapper_script("/usr/local/bin/orchard")
                .contains("\"/usr/local/bin/orchard\" chat --message")
        );
    }

    #[test]
    fn chat_wrapper_script_reads_input_from_user() {
        assert!(get_chat_wrapper_script("/usr/local/bin/orchard").contains("read -r prompt"));
    }

    #[test]
    fn chat_wrapper_script_exits_on_empty_input() {
        assert!(get_chat_wrapper_script("/usr/local/bin/orchard").contains("exit 0"));
    }

    #[test]
    fn chat_wrapper_script_shows_error_via_tmux_display_message() {
        assert!(get_chat_wrapper_script("/usr/local/bin/orchard").contains("tmux display-message"));
    }

    #[test]
    fn chat_wrapper_script_cleans_up_tempfile_on_exit() {
        // The script must install a trap so the tempfile is removed on signal/exit.
        assert!(
            get_chat_wrapper_script("/usr/local/bin/orchard").contains("trap"),
            "script must contain a trap to clean up errfile"
        );
        assert!(
            get_chat_wrapper_script("/usr/local/bin/orchard").contains("EXIT"),
            "trap must fire on EXIT"
        );
    }

    #[test]
    fn chat_tmux_binding_uses_display_popup() {
        assert!(get_chat_tmux_binding("O").contains("display-popup"));
    }

    #[test]
    fn chat_tmux_binding_binds_given_key() {
        assert!(get_chat_tmux_binding("O").contains("bind-key O"));
    }

    #[test]
    fn chat_tmux_binding_references_orchard_chat_script() {
        assert!(get_chat_tmux_binding("O").contains("orchard-chat"));
    }

    #[test]
    fn chat_tmux_binding_custom_key() {
        let binding = get_chat_tmux_binding("C");
        assert!(binding.contains("bind-key C"));
        assert!(!binding.contains("bind-key O"));
    }

    #[test]
    fn chat_tmux_binding_uses_smaller_popup_dimensions() {
        // Chat popup should be smaller than the TUI popup (60% wide, 20% tall).
        let binding = get_chat_tmux_binding("O");
        assert!(binding.contains("60%"), "chat popup should be 60% wide");
        assert!(binding.contains("20%"), "chat popup should be 20% tall");
    }

    #[test]
    fn wrapper_script_embeds_absolute_path() {
        let script = get_wrapper_script("/custom/path/orchard");
        assert!(script.contains("\"/custom/path/orchard\""));
    }

    #[test]
    fn chat_wrapper_script_embeds_absolute_path() {
        let script = get_chat_wrapper_script("/custom/path/orchard");
        assert!(script.contains("\"/custom/path/orchard\""));
    }

    // -----------------------------------------------------------------------
    // get_tmux_status_line
    // -----------------------------------------------------------------------

    #[test]
    fn tmux_status_line_contains_set_status_right() {
        let state_dir = std::path::Path::new("/home/user/.local/state/orchard");
        let line = get_tmux_status_line(state_dir);
        assert!(
            line.contains("set -g status-right"),
            "must be a status-right directive"
        );
    }

    #[test]
    fn tmux_status_line_references_status_txt() {
        let state_dir = std::path::Path::new("/home/user/.local/state/orchard");
        let line = get_tmux_status_line(state_dir);
        assert!(
            line.contains("status.txt"),
            "must reference the status.txt file"
        );
    }

    #[test]
    fn tmux_status_line_embeds_state_dir_path() {
        let state_dir = std::path::Path::new("/home/user/.local/state/orchard");
        let line = get_tmux_status_line(state_dir);
        assert!(
            line.contains("/home/user/.local/state/orchard"),
            "must embed the provided state directory path"
        );
    }

    #[test]
    fn block_without_status_does_not_contain_status_right() {
        // When the user declines the status bar, the injected block must not
        // set status-right and thereby clobber the user's existing config.
        let binding = get_tmux_binding("o");
        let chat_binding = get_chat_tmux_binding("O");
        let block_content = format!("{binding}\n{chat_binding}");
        let result = inject_config_block("", &block_content);
        assert!(
            !result.contains("status-right"),
            "block without status must not contain status-right"
        );
    }

    #[test]
    fn block_with_status_contains_status_right() {
        let state_dir = std::path::Path::new("/home/user/.local/state/orchard");
        let binding = get_tmux_binding("o");
        let chat_binding = get_chat_tmux_binding("O");
        let status_line = get_tmux_status_line(state_dir);
        let block_content = format!("{binding}\n{chat_binding}\n{status_line}");
        let result = inject_config_block("", &block_content);
        assert!(
            result.contains("status-right"),
            "block with status must contain status-right"
        );
    }
}
