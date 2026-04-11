//! Remote host provisioning for orchard's SSH-based worktree management.
//!
//! Implements `orchard setup-remote <host>`, which runs all read-only checks
//! first (SSH connectivity, dependencies, repo access), then performs mutations
//! (hook install, settings merge). This ordering avoids half-provisioned state
//! on failure.

use crate::global_config::{self, GlobalConfig, RemoteConfig};
use crate::remote::{shell_escape, ssh_exec};

// ---------------------------------------------------------------------------
// ANSI colour helpers
// ---------------------------------------------------------------------------

const BOLD: &str = "\x1b[1m";
const GREEN: &str = "\x1b[32m";
const RED: &str = "\x1b[31m";
const YELLOW: &str = "\x1b[33m";
const CYAN: &str = "\x1b[36m";
const RESET: &str = "\x1b[0m";

// ---------------------------------------------------------------------------
// Hook events — must match shell.rs
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
const HOOK_COMMAND: &str = "~/.claude/hooks/orchard-state.sh";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// Information about a resolved remote target.
#[derive(Debug)]
struct RemoteInfo {
    /// SSH target string (e.g. "ubuntu@10.0.0.1").
    host: String,
    /// Optional absolute repo path on the remote.
    repo_path: Option<String>,
}

/// Result of a single provisioning step.
type StepResult = Result<(), String>;

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/// Provisions a remote host for orchard's remote worktree management.
///
/// `host_arg` may be a logical remote name (e.g. "gpu") or a direct SSH
/// target (e.g. "user@host"). Resolves the target from the global config,
/// runs all checks, then applies mutations.
pub fn run(host_arg: &str) -> Result<(), String> {
    let config = global_config::load_global_config();
    let info = resolve_remote(&config, host_arg)?;
    let host = &info.host;

    eprintln!("{BOLD}{CYAN}Setting up remote: {host}{RESET}");
    eprintln!();

    // Phase 1: checks (read-only).
    let connectivity = check_connectivity(host);
    let conn_ok = connectivity.is_ok();

    let dep_tmux = check_dependency(host, "tmux");
    let dep_git = check_dependency(host, "git");
    let dep_gh = check_dependency(host, "gh");
    let dep_claude = check_dependency(host, "claude");

    let repo_access = if let Some(ref path) = info.repo_path {
        check_repo_access(host, path)
    } else {
        Ok(())
    };

    // Phase 2: mutations (only proceed if SSH works).
    let hook_installed;
    let hooks_registered;

    if conn_ok {
        hook_installed = install_hook_script(host);
        hooks_registered = register_remote_hooks(host);
    } else {
        let msg = "skipped (no SSH connection)".to_string();
        hook_installed = Err(msg.clone());
        hooks_registered = Err(msg);
    }

    // Collect results for summary.
    let mut results: Vec<(&str, StepResult)> = vec![
        ("SSH connectivity", connectivity),
        ("tmux", dep_tmux),
        ("git", dep_git),
        ("gh", dep_gh),
        ("claude", dep_claude),
        ("Hook installed", hook_installed),
        ("Hooks registered", hooks_registered),
    ];

    if info.repo_path.is_some() {
        results.push(("Repo access", repo_access));
    }

    print_summary(&results);

    let all_passed = results.iter().all(|(_, r)| r.is_ok());
    if all_passed {
        Ok(())
    } else {
        Err("setup-remote completed with failures".to_string())
    }
}

// ---------------------------------------------------------------------------
// Host resolution
// ---------------------------------------------------------------------------

/// Resolves a host argument to a `RemoteInfo` by searching the global config.
///
/// Resolution order:
/// 1. Match by remote name across all repos.
/// 2. Match by host field across all repos.
/// 3. If the argument looks like `user@host`, use it directly.
/// 4. Return an error with a suggestion to run `orchard init`.
fn resolve_remote(config: &GlobalConfig, host_arg: &str) -> Result<RemoteInfo, String> {
    // 1. Try by name or host field in a single pass.
    for repo in &config.repos {
        for remote in &repo.remotes {
            if remote.name == host_arg || remote.host == host_arg {
                return Ok(remote_to_info(remote));
            }
        }
    }

    // 3. Direct user@host pattern — no config entry found, but still usable.
    if host_arg.contains('@') {
        return Ok(RemoteInfo {
            host: host_arg.to_string(),
            repo_path: None,
        });
    }

    // 4. Unknown — suggest init.
    Err(format!(
        "Remote '{host_arg}' not found in config.\n\
         Run `orchard init` to configure remotes, or pass a direct SSH target (user@host)."
    ))
}

fn remote_to_info(remote: &RemoteConfig) -> RemoteInfo {
    RemoteInfo {
        host: remote.host.clone(),
        repo_path: if remote.path.is_empty() {
            None
        } else {
            Some(remote.path.clone())
        },
    }
}

// ---------------------------------------------------------------------------
// Check steps (read-only)
// ---------------------------------------------------------------------------

fn check_connectivity(host: &str) -> StepResult {
    ssh_exec(host, "true")
        .map(|_| ())
        .map_err(|e| e.to_string())
}

fn check_dependency(host: &str, dep: &str) -> StepResult {
    ssh_exec(host, &format!("command -v {}", shell_escape(dep)))
        .map(|_| ())
        .map_err(|_| format!("{dep} not found on remote"))
}

fn check_repo_access(host: &str, repo_path: &str) -> StepResult {
    let cmd = format!("git -C {} rev-parse --git-dir", shell_escape(repo_path));
    ssh_exec(host, &cmd)
        .map(|_| ())
        .map_err(|_| format!("repo not accessible at {repo_path}"))
}

// ---------------------------------------------------------------------------
// Mutation steps
// ---------------------------------------------------------------------------

fn install_hook_script(host: &str) -> StepResult {
    // Create the hooks directory.
    ssh_exec(host, "mkdir -p ~/.claude/hooks").map_err(|e| e.to_string())?;

    // Transfer the script via base64 to avoid shell quoting issues.
    let encoded = base64_encode(HOOK_SCRIPT_CONTENT.as_bytes());
    let cmd = format!(
        "echo '{encoded}' | base64 -d > ~/.claude/hooks/orchard-state.sh.tmp \
         && mv ~/.claude/hooks/orchard-state.sh.tmp ~/.claude/hooks/orchard-state.sh"
    );
    ssh_exec(host, &cmd).map_err(|e| format!("writing hook script: {e}"))?;

    // Make executable.
    ssh_exec(host, "chmod +x ~/.claude/hooks/orchard-state.sh")
        .map(|_| ())
        .map_err(|e| format!("chmod hook script: {e}"))
}

fn register_remote_hooks(host: &str) -> StepResult {
    // Read existing settings.json (or empty object if missing).
    let existing = ssh_exec(host, "cat ~/.claude/settings.json 2>/dev/null || echo '{}'")
        .map_err(|e| e.to_string())?;

    let merged = merge_hook_settings(existing.trim(), HOOK_COMMAND)?;

    // Write back atomically via base64 to avoid quoting issues.
    let encoded = base64_encode(merged.as_bytes());
    let cmd = format!(
        "echo '{encoded}' | base64 -d > ~/.claude/settings.json.tmp \
         && mv ~/.claude/settings.json.tmp ~/.claude/settings.json"
    );
    ssh_exec(host, &cmd)
        .map(|_| ())
        .map_err(|e| format!("writing settings.json: {e}"))
}

// ---------------------------------------------------------------------------
// Pure merge logic (testable without SSH)
// ---------------------------------------------------------------------------

/// Merges orchard hook registrations into a settings.json string.
///
/// Reads `existing_json`, adds any missing entries for each hook event, and
/// returns the serialized result. Does not duplicate entries that already exist.
///
/// # Errors
///
/// Returns an error if the JSON is invalid or the hooks structure is malformed.
pub(crate) fn merge_hook_settings(
    existing_json: &str,
    hook_command: &str,
) -> Result<String, String> {
    let mut settings: serde_json::Value =
        serde_json::from_str(existing_json).map_err(|e| format!("invalid settings.json: {e}"))?;

    let hooks_obj = settings
        .as_object_mut()
        .ok_or_else(|| "settings.json root is not an object".to_string())?
        .entry("hooks")
        .or_insert_with(|| serde_json::json!({}))
        .as_object_mut()
        .ok_or_else(|| "hooks field is not an object".to_string())?;

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

    serde_json::to_string_pretty(&settings).map_err(|e| format!("serializing settings: {e}"))
}

// ---------------------------------------------------------------------------
// Output helpers
// ---------------------------------------------------------------------------

fn print_summary(results: &[(&str, StepResult)]) {
    eprintln!();
    eprintln!("{BOLD}Summary:{RESET}");
    eprintln!("{BOLD}{:-<40}{RESET}", "");
    for (label, result) in results {
        match result {
            Ok(()) => eprintln!("  {GREEN}PASS{RESET}  {label}"),
            Err(msg) => eprintln!("  {RED}FAIL{RESET}  {label}: {YELLOW}{msg}{RESET}"),
        }
    }
    eprintln!("{BOLD}{:-<40}{RESET}", "");
}

// ---------------------------------------------------------------------------
// Base64 encoding (no external crate needed for simple encode)
// ---------------------------------------------------------------------------

fn base64_encode(data: &[u8]) -> String {
    const CHARS: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for chunk in data.chunks(3) {
        let b0 = chunk[0] as usize;
        let b1 = if chunk.len() > 1 {
            chunk[1] as usize
        } else {
            0
        };
        let b2 = if chunk.len() > 2 {
            chunk[2] as usize
        } else {
            0
        };
        out.push(CHARS[b0 >> 2] as char);
        out.push(CHARS[((b0 & 0x3) << 4) | (b1 >> 4)] as char);
        if chunk.len() > 1 {
            out.push(CHARS[((b1 & 0xf) << 2) | (b2 >> 6)] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(CHARS[b2 & 0x3f] as char);
        } else {
            out.push('=');
        }
    }
    out
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    fn make_config_with_remote(name: &str, host: &str, path: &str) -> GlobalConfig {
        GlobalConfig {
            repos: vec![global_config::RepoConfig {
                slug: "acme/my-project".to_string(),
                path: "/workspace/my-project".to_string(),
                remotes: vec![RemoteConfig {
                    name: name.to_string(),
                    host: host.to_string(),
                    path: path.to_string(),
                    shell: "ssh".to_string(),
                }],
            }],
            terminal_app: "com.apple.Terminal".to_string(),
            tmux_sessions: vec![],
            chat_target: None,
            watch: global_config::WatchConfig::default(),
            ci_gate_patterns: vec![],
        }
    }

    #[test]
    fn resolve_remote_by_name() {
        let config = make_config_with_remote("gpu", "ubuntu@10.0.0.1", "/home/ubuntu/repo");
        let info = resolve_remote(&config, "gpu").unwrap();
        assert_eq!(info.host, "ubuntu@10.0.0.1");
        assert_eq!(info.repo_path.as_deref(), Some("/home/ubuntu/repo"));
    }

    #[test]
    fn resolve_remote_by_host() {
        let config = make_config_with_remote("gpu", "ubuntu@10.0.0.1", "/home/ubuntu/repo");
        let info = resolve_remote(&config, "ubuntu@10.0.0.1").unwrap();
        assert_eq!(info.host, "ubuntu@10.0.0.1");
    }

    #[test]
    fn resolve_remote_direct_ssh_target() {
        let config = GlobalConfig::default();
        let info = resolve_remote(&config, "user@somehost").unwrap();
        assert_eq!(info.host, "user@somehost");
        assert!(info.repo_path.is_none());
    }

    #[test]
    fn resolve_remote_unknown_returns_error() {
        let config = GlobalConfig::default();
        let err = resolve_remote(&config, "nonexistent").unwrap_err();
        assert!(err.contains("not found"));
        assert!(err.contains("orchard init"));
    }

    #[test]
    fn merge_hooks_into_empty_settings() {
        let result = merge_hook_settings("{}", "~/.claude/hooks/orchard-state.sh").unwrap();
        let v: serde_json::Value = serde_json::from_str(&result).unwrap();
        // All HOOK_EVENTS should be present.
        for event in HOOK_EVENTS {
            let arr = v["hooks"][event].as_array().expect("event array missing");
            assert!(!arr.is_empty(), "event {event} should have a hook entry");
        }
    }

    #[test]
    fn merge_hooks_into_existing_settings_no_duplicates() {
        // Pre-populate with one event already registered.
        let existing = serde_json::json!({
            "hooks": {
                "Stop": [
                    { "hooks": [{ "type": "command", "command": "~/.claude/hooks/orchard-state.sh" }] }
                ]
            }
        })
        .to_string();

        let result = merge_hook_settings(&existing, "~/.claude/hooks/orchard-state.sh").unwrap();
        let v: serde_json::Value = serde_json::from_str(&result).unwrap();

        let stop_arr = v["hooks"]["Stop"].as_array().unwrap();
        // Must still have exactly one entry — no duplicate.
        assert_eq!(stop_arr.len(), 1, "Stop hook should not be duplicated");
    }

    #[test]
    fn merge_hooks_preserves_other_settings() {
        let existing = r#"{ "theme": "dark", "fontSize": 14 }"#;
        let result = merge_hook_settings(existing, "~/.claude/hooks/orchard-state.sh").unwrap();
        let v: serde_json::Value = serde_json::from_str(&result).unwrap();
        assert_eq!(v["theme"], "dark");
        assert_eq!(v["fontSize"], 14);
    }

    #[test]
    fn format_summary_all_pass() {
        let results: Vec<(&str, StepResult)> = vec![
            ("SSH connectivity", Ok(())),
            ("tmux", Ok(())),
            ("git", Ok(())),
        ];
        // Smoke test: should not panic.
        print_summary(&results);
    }

    #[test]
    fn format_summary_with_failures() {
        let results: Vec<(&str, StepResult)> = vec![
            ("SSH connectivity", Ok(())),
            ("gh", Err("gh not found on remote".to_string())),
        ];
        // Smoke test: should not panic.
        print_summary(&results);
    }

    #[test]
    fn base64_encode_roundtrip() {
        let input = b"Hello, world! This is a test.";
        let encoded = base64_encode(input);
        // Decode with standard base64 alphabet to verify correctness.
        let decoded = base64_decode_simple(&encoded);
        assert_eq!(decoded, input);
    }

    #[test]
    fn base64_encode_empty() {
        assert_eq!(base64_encode(b""), "");
    }

    // Simple decoder for testing the encoder — not used in production code.
    fn base64_decode_simple(s: &str) -> Vec<u8> {
        let chars = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
        let val = |c: u8| -> usize { chars.iter().position(|&x| x == c).unwrap_or(0) };
        let bytes: Vec<u8> = s.bytes().filter(|&b| b != b'=').collect();
        let mut out = Vec::new();
        for chunk in bytes.chunks(4) {
            let b = [
                val(chunk[0]),
                if chunk.len() > 1 { val(chunk[1]) } else { 0 },
                if chunk.len() > 2 { val(chunk[2]) } else { 0 },
                if chunk.len() > 3 { val(chunk[3]) } else { 0 },
            ];
            out.push(((b[0] << 2) | (b[1] >> 4)) as u8);
            if chunk.len() > 2 {
                out.push(((b[1] << 4) | (b[2] >> 2)) as u8);
            }
            if chunk.len() > 3 {
                out.push(((b[2] << 6) | b[3]) as u8);
            }
        }
        out
    }
}
