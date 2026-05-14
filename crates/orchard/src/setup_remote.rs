//! Remote host provisioning for orchard's SSH-based worktree management.
//!
//! Implements `orchard setup-remote <host>`, which checks SSH connectivity,
//! required dependencies (tmux, git, gh, claude), and optional repo access.

use crate::global_config::{self, GlobalConfig, RemoteConfig};
use crate::remote::{shell_escape, ssh_exec};
#[cfg(test)]
use crate::remote_adapter::RemoteKind;

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

    let dep_tmux = check_dependency(host, "tmux");
    let dep_git = check_dependency(host, "git");
    let dep_gh = check_dependency(host, "gh");
    let dep_claude = check_dependency(host, "claude");

    let repo_access = if let Some(ref path) = info.repo_path {
        check_repo_access(host, path)
    } else {
        Ok(())
    };

    // Collect results for summary.
    let mut results: Vec<(&str, StepResult)> = vec![
        ("SSH connectivity", connectivity),
        ("tmux", dep_tmux),
        ("git", dep_git),
        ("gh", dep_gh),
        ("claude", dep_claude),
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
                    kind: RemoteKind::Remmy,
                    allow_transitive: false,
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
}
