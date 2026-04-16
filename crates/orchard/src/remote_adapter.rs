//! Hexagonal port for remote worktree and session access.
//!
//! Defines the `RemoteAdapter` enum (the port), the `SshExec` seam for
//! injection, and three adapter variants (`RemmyAdapter`, `BoxdSharedAdapter`,
//! `BoxdForkAdapter`). `RemoteAdapter::from_config` selects the right variant
//! from a `global_config::RemoteConfig` and the production caller
//! (`cache_sources::refresh_remote_worktrees`) dispatches through it.
//!
//! # Design decision (recorded per feature.feature:30)
//!
//! `RemoteAdapter` is an enum, not `Box<dyn Trait>`. CLAUDE.md reserves trait
//! objects for "genuinely polymorphic behaviour — cases where multiple implementations
//! exist at runtime". Three adapters known at compile time is textbook enum dispatch.
//! The `SshExec` seam IS a trait object (`Box<dyn SshExec>`) because that IS
//! polymorphic at runtime: the real process runner vs. test doubles both
//! implement it.

use std::collections::HashMap;

use anyhow::Result;

use serde::{Deserialize, Serialize};

use crate::cache::{CachedTmuxSession, CachedWorktree, WorktreeLayout};

// ---------------------------------------------------------------------------
// AdapterError — slice 2 (feature.feature:185)
// ---------------------------------------------------------------------------

/// Errors that a `RemoteAdapter` method can return.
///
/// `ParseFailure` is used when an adapter receives a response that is
/// syntactically invalid (e.g. malformed JSON from `ssh boxd.sh list --json`).
/// The caller decides whether to fall back to cached data; the adapter itself
/// does not make that decision.
///
/// SSH failures are currently converted to `Ok(vec![])` inside the adapters so
/// the TUI retains its last cached state; if a typed SSH variant is needed
/// once AC6 (hard timeouts) lands, add it alongside `ParseFailure` here.
#[derive(Debug)]
pub enum AdapterError {
    /// The response from the remote was received but could not be parsed.
    ///
    /// `raw` is sanitized (ASCII-graphic + space only) and truncated at 256
    /// characters so malicious remotes cannot inject ANSI escapes or
    /// control bytes into logs or terminals via error messages.
    ParseFailure {
        /// Sanitized, truncated payload for diagnostic logging.
        raw: String,
    },
}

impl std::fmt::Display for AdapterError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AdapterError::ParseFailure { raw } => {
                write!(f, "boxd.sh list parse failure; raw payload: {raw}")
            }
        }
    }
}

impl std::error::Error for AdapterError {}

// ---------------------------------------------------------------------------
// RemoteKind — the `"type"` field on remote config entries
// ---------------------------------------------------------------------------

/// The kind of remote adapter to use for a configured remote host.
///
/// Serialized as a lowercase-hyphenated string via `serde(rename_all = "kebab-case")`.
/// The JSON field is `"type"` on `global_config::RemoteConfig`
/// (`#[serde(rename = "type")] kind: RemoteKind`).
///
/// Serde rejects any value not in this enum, producing an error that names the
/// unknown value — this covers scenario 4 / feature.feature:52.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum RemoteKind {
    /// Remmy-style adapter: bare repo + worktrees over SSH.
    Remmy,
    /// Boxd shared-VM adapter: single VM with multiple worktrees.
    BoxdShared,
    /// Boxd fork-per-issue adapter: one VM per open issue.
    BoxdFork,
}

// ---------------------------------------------------------------------------
// SSH exec seam
// ---------------------------------------------------------------------------

/// Output produced by an SSH command execution.
#[derive(Debug, Clone)]
pub struct SshOutput {
    /// Standard output captured from the remote command.
    pub stdout: String,
    /// Standard error captured from the remote command.
    pub stderr: String,
    /// Exit status code of the remote command.
    pub exit_code: i32,
}

/// Seam for injecting SSH command execution.
///
/// Real implementation: `ProcessSshExec`, which spawns an `ssh` subprocess.
/// Test implementation: `FakeSshExec`, keyed on `(host, cmd)`.
///
/// A trait object is used here (not an enum) because the caller does not know
/// at compile time whether it holds a real runner or a test double.
pub trait SshExec: Send + Sync {
    /// Executes `cmd` on `host` and returns stdout, stderr, and exit code.
    fn exec(&self, host: &str, cmd: &str) -> Result<SshOutput>;
}

// ---------------------------------------------------------------------------
// Real SSH executor
// ---------------------------------------------------------------------------

/// SSH executor that spawns a real `ssh` subprocess with a hard wall-clock
/// timeout.
///
/// Delegates to `crate::remote::ssh_exec_with_timeout`, which applies the
/// orchard-wide SSH multiplexing flags (`ControlMaster=auto`, `ControlPath`,
/// `ConnectTimeout=5`, `BatchMode=yes`) and, critically, kills the child if
/// the command does not exit within `DEFAULT_ADAPTER_TIMEOUT`. This bounds
/// `orchard --json` latency when a remote VM accepts SSH but hangs on the
/// actual command — AC6.
///
/// `stderr` is surfaced through the returned error rather than as part of
/// `SshOutput`; successful calls produce `stderr = ""` and `exit_code = 0`.
pub struct ProcessSshExec;

/// Hard wall-clock bound enforced by `ProcessSshExec` on every adapter call.
///
/// 5 seconds matches the SSH `ConnectTimeout` in `remote::ssh_flags()`, so a
/// fully-wedged host is bounded to ~5s per call end-to-end. Refresh
/// pipelines running 3+ remotes concurrently therefore stay under the
/// 5-second wall-clock bound that the feature file (AC6 @e2e, line 487)
/// requires when every host is unreachable.
pub const DEFAULT_ADAPTER_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(5);

impl SshExec for ProcessSshExec {
    fn exec(&self, host: &str, cmd: &str) -> Result<SshOutput> {
        let stdout = crate::remote::ssh_exec_with_timeout(host, cmd, DEFAULT_ADAPTER_TIMEOUT)?;
        Ok(SshOutput {
            stdout,
            stderr: String::new(),
            exit_code: 0,
        })
    }
}

// ---------------------------------------------------------------------------
// Fake SSH executor for unit tests
// ---------------------------------------------------------------------------

/// Test double for `SshExec`.
///
/// Pre-load canned responses with `insert`; any (host, cmd) pair not found
/// returns an error so tests catch unexpected calls.
#[derive(Default)]
pub struct FakeSshExec {
    /// Canned (stdout, stderr, exit_code) responses keyed by (host, cmd).
    responses: HashMap<(String, String), SshOutput>,
}

impl FakeSshExec {
    /// Creates a new empty fake executor.
    pub fn new() -> Self {
        Self::default()
    }

    /// Registers a canned response for `(host, cmd)`.
    pub fn insert(&mut self, host: impl Into<String>, cmd: impl Into<String>, output: SshOutput) {
        self.responses.insert((host.into(), cmd.into()), output);
    }
}

impl SshExec for FakeSshExec {
    fn exec(&self, host: &str, cmd: &str) -> Result<SshOutput> {
        self.responses
            .get(&(host.to_string(), cmd.to_string()))
            .cloned()
            .ok_or_else(|| {
                anyhow::anyhow!("FakeSshExec: no canned response for ({host:?}, {cmd:?})")
            })
    }
}

// ---------------------------------------------------------------------------
// Port: RemoteAdapter enum
// ---------------------------------------------------------------------------

/// The hexagonal port for remote worktree/session access.
///
/// Each variant wraps the configuration needed by that adapter. Methods
/// dispatch via `match` rather than virtual dispatch because all three
/// adapter kinds are known at compile time.
pub enum RemoteAdapter {
    /// Remmy-style adapter: bare repo + worktrees over SSH.
    Remmy(RemmyAdapter),
    /// Boxd shared-VM adapter: single VM with multiple worktrees.
    BoxdShared(BoxdSharedAdapter),
    /// Boxd fork-per-issue adapter: one VM per open issue.
    BoxdFork(BoxdForkAdapter),
}

impl RemoteAdapter {
    /// Constructs the appropriate adapter from `cfg`, wiring it to `ssh`.
    ///
    /// Dispatch is driven by `cfg.kind`; the serde layer rejects unknown
    /// `"type"` strings before this function is called.
    pub fn from_config(cfg: &crate::global_config::RemoteConfig, ssh: Box<dyn SshExec>) -> Self {
        match cfg.kind {
            RemoteKind::Remmy => RemoteAdapter::Remmy(RemmyAdapter {
                host: cfg.host.clone(),
                path: cfg.path.clone(),
                ssh,
            }),
            RemoteKind::BoxdShared => RemoteAdapter::BoxdShared(BoxdSharedAdapter {
                host: cfg.host.clone(),
                path: cfg.path.clone(),
                ssh,
            }),
            RemoteKind::BoxdFork => RemoteAdapter::BoxdFork(BoxdForkAdapter {
                golden_host: cfg.host.clone(),
                fork_repo_path: cfg.path.clone(),
                ssh,
            }),
        }
    }

    /// Returns all non-bare worktrees visible to this adapter.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        match self {
            RemoteAdapter::Remmy(a) => a.list_worktrees(),
            RemoteAdapter::BoxdShared(a) => a.list_worktrees(),
            RemoteAdapter::BoxdFork(a) => a.list_worktrees(),
        }
    }

    /// Returns all tmux sessions visible to this adapter.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        match self {
            RemoteAdapter::Remmy(a) => a.list_sessions(),
            RemoteAdapter::BoxdShared(a) => a.list_sessions(),
            RemoteAdapter::BoxdFork(a) => a.list_sessions(),
        }
    }

    /// Probes reachability and returns optional metadata.
    pub fn probe(&self) -> Result<ProbeResult> {
        match self {
            RemoteAdapter::Remmy(a) => a.probe(),
            RemoteAdapter::BoxdShared(a) => a.probe(),
            RemoteAdapter::BoxdFork(a) => a.probe(),
        }
    }
}

// ---------------------------------------------------------------------------
// Probe result
// ---------------------------------------------------------------------------

/// Outcome of a reachability probe.
#[derive(Debug)]
pub struct ProbeResult {
    /// Whether the remote host responded.
    pub reachable: bool,
    /// Optional metadata returned by the adapter (e.g. version string).
    pub metadata: Option<String>,
}

// ---------------------------------------------------------------------------
// RemmyAdapter
// ---------------------------------------------------------------------------

/// Adapter for Remmy-style remotes: a bare repo on the remote host with
/// worktrees in subdirectories, accessed via `git worktree list --porcelain`
/// over SSH.
pub struct RemmyAdapter {
    /// SSH target host (e.g. `"ubuntu@10.0.3.56"`).
    pub host: String,
    /// Absolute path to the bare repo on the remote host (e.g. `"~/langwatch-workspace"`).
    pub path: String,
    /// SSH executor (real process or test double).
    pub ssh: Box<dyn SshExec>,
}

impl RemmyAdapter {
    /// Returns all non-bare worktrees from the remote via `git worktree list --porcelain`.
    ///
    /// Runs `git -C <path> worktree list --porcelain` on the remote host through
    /// the injected `SshExec`, then parses the porcelain output. Bare worktrees
    /// are excluded and each returned entry is tagged with the host.
    ///
    /// Returns `Ok(vec![])` when SSH is unreachable — the caller (TUI refresh)
    /// treats an empty result the same as a stale cache, keeping the last known
    /// state visible rather than crashing the dashboard.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        ssh_list_worktrees(self.ssh.as_ref(), &self.host, &self.path)
    }

    /// Returns all tmux sessions from the remote.
    ///
    /// Slice 1 stub — full implementation in slice 2.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        Ok(vec![])
    }

    /// Probes whether the remote host is reachable.
    ///
    /// Slice 1 stub — full implementation in slice 2.
    pub fn probe(&self) -> Result<ProbeResult> {
        Ok(ProbeResult {
            reachable: true,
            metadata: None,
        })
    }
}

// ---------------------------------------------------------------------------
// BoxdSharedAdapter
// ---------------------------------------------------------------------------

/// Adapter for a single shared Boxd VM with multiple worktrees.
///
/// Uses the same `git worktree list --porcelain` SSH path as `RemmyAdapter`.
/// All returned worktrees carry `layout = WorktreeLayout::Bare` because the
/// Boxd shared-VM model uses a bare repo with linked worktrees in subdirectories.
pub struct BoxdSharedAdapter {
    /// SSH target host (e.g. `"boxd@orchard-rs.boxd.sh"`).
    pub host: String,
    /// Absolute path to the bare repo on the VM (e.g. `"~/git-orchard-rs"`).
    pub path: String,
    /// SSH executor (real process or test double).
    pub ssh: Box<dyn SshExec>,
}

impl BoxdSharedAdapter {
    /// Returns all non-bare worktrees from the Boxd shared VM.
    ///
    /// Runs `git -C <path> worktree list --porcelain` on the Boxd VM via SSH,
    /// parses porcelain output, and returns non-bare entries tagged with the
    /// host and `layout = WorktreeLayout::Bare`.
    ///
    /// Returns `Ok(vec![])` when SSH is unreachable — identical degraded behaviour
    /// to `RemmyAdapter`, so the TUI keeps the last known cache visible.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        ssh_list_worktrees(self.ssh.as_ref(), &self.host, &self.path)
    }

    /// Returns all tmux sessions from the Boxd shared VM.
    ///
    /// Slice 2 stub — full implementation in slice 3.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        Ok(vec![])
    }

    /// Probes whether the Boxd shared VM is reachable.
    ///
    /// Slice 2 stub — full implementation in slice 3.
    pub fn probe(&self) -> Result<ProbeResult> {
        Ok(ProbeResult {
            reachable: true,
            metadata: None,
        })
    }
}

// ---------------------------------------------------------------------------
// BoxdForkAdapter
// ---------------------------------------------------------------------------

/// A single fork VM entry returned by `ssh boxd.sh list --json`.
///
/// `path` is optional in the JSON payload; when absent, the adapter's
/// configured `fork_repo_path` is used (derived from `RemoteConfig.path`,
/// not a hardcoded tenant path).
#[derive(Debug, Deserialize)]
struct BoxdForkEntry {
    /// Human-readable fork name, typically the issue slug (e.g. `"issue3155"`).
    name: String,
    /// SSH hostname of the fork VM (e.g. `"issue3155.boxd.sh"`).
    host: String,
    /// Repo root path on the fork VM. Optional — falls back to the
    /// adapter's configured path when absent.
    #[serde(default)]
    path: Option<String>,
}

/// Adapter for Boxd fork-per-issue VMs.
///
/// Enumerates live forks via `ssh <golden_host> list --json`, then probes each
/// fork VM individually for its branch and tmux sessions. Forks that advertise
/// a `"path"` in the list JSON use that value; otherwise the adapter's
/// `fork_repo_path` (derived from `RemoteConfig.path`) is used.
pub struct BoxdForkAdapter {
    /// The golden Boxd host used for enumeration (e.g. `"boxd.sh"`).
    pub golden_host: String,
    /// Repo root path on each fork VM (from `RemoteConfig.path`). Used when
    /// the `list --json` payload does not carry a per-fork `path` value.
    pub fork_repo_path: String,
    /// SSH executor (real process or test double).
    pub ssh: Box<dyn SshExec>,
}

impl BoxdForkAdapter {
    /// Returns one `CachedWorktree` per live fork VM.
    ///
    /// Steps:
    /// 1. SSH to `golden_host` and run `list --json` to enumerate live forks.
    /// 2. Parse the JSON array. Return `Err(AdapterError::ParseFailure)` if invalid.
    /// 3. For each fork, SSH to `boxd@<fork.host>` and run
    ///    `cd <fork.path> && git rev-parse --abbrev-ref HEAD`.
    /// 4. If the output is exactly `"HEAD"` (detached HEAD), fall back to
    ///    `git rev-parse --short HEAD` and format branch as `"(detached: <sha>)"`.
    /// 5. Return `Ok(vec![])` when `golden_host` is unreachable (SSH failure on
    ///    the list command) — the TUI keeps the last cached forks visible.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        // Step 1: enumerate live forks from the golden host.
        let list_stdout = match self.ssh.exec(&self.golden_host, "list --json") {
            Ok(output) => output.stdout,
            Err(_) => return Ok(vec![]),
        };

        // Step 2: parse the fork list. Malformed JSON → ParseFailure.
        let entries: Vec<BoxdForkEntry> =
            serde_json::from_str(&list_stdout).map_err(|_| AdapterError::ParseFailure {
                raw: sanitize_raw_payload(&list_stdout),
            })?;

        let mut worktrees = Vec::with_capacity(entries.len());

        // Steps 3-5: per-fork branch resolution.
        for entry in entries {
            let fork_host = format!("boxd@{}", entry.host);
            let fork_path = entry.path.unwrap_or_else(|| self.fork_repo_path.clone());
            let escaped_path = crate::remote::shell_escape(&fork_path);
            let branch_cmd = format!("cd {escaped_path} && git rev-parse --abbrev-ref HEAD");

            let branch = resolve_fork_branch(
                self.ssh.as_ref(),
                &fork_host,
                &escaped_path,
                &branch_cmd,
                &entry.name,
            );

            worktrees.push(CachedWorktree {
                path: fork_path,
                branch,
                is_bare: false,
                is_locked: false,
                host: Some(fork_host),
                ahead: None,
                behind: None,
                last_commit_at: None,
                layout: WorktreeLayout::Flat,
            });
        }

        Ok(worktrees)
    }

    /// Returns tmux sessions from all live fork VMs.
    ///
    /// Slice 2 stub — full implementation in slice 3.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        Ok(vec![])
    }

    /// Probes whether the golden Boxd host is reachable.
    ///
    /// Slice 2 stub — full implementation in slice 3.
    pub fn probe(&self) -> Result<ProbeResult> {
        Ok(ProbeResult {
            reachable: true,
            metadata: None,
        })
    }
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

/// Runs `git -C <path> worktree list --porcelain` on `host` via `ssh`, parses
/// the porcelain output into non-bare `CachedWorktree` entries tagged with
/// `host`, and returns `Ok(vec![])` on SSH failure so the TUI retains the
/// last known cache.
///
/// `path` is shell-escaped before interpolation: config- and JSON-sourced
/// paths reach this code path and must not be able to inject shell
/// metacharacters into the command string.
fn ssh_list_worktrees(ssh: &dyn SshExec, host: &str, path: &str) -> Result<Vec<CachedWorktree>> {
    let cmd = format!(
        "git -C {} worktree list --porcelain",
        crate::remote::shell_escape(path)
    );
    let stdout = match ssh.exec(host, &cmd) {
        Ok(output) => output.stdout,
        Err(_) => return Ok(vec![]),
    };
    let mut worktrees: Vec<CachedWorktree> = crate::git_parse::parse_worktree_porcelain(&stdout)
        .into_iter()
        .filter(|wt| !wt.is_bare)
        .collect();
    for wt in &mut worktrees {
        wt.host = Some(host.to_string());
    }
    Ok(worktrees)
}

/// Resolves a fork's branch name, handling detached HEAD, empty output, and
/// SSH failure uniformly. Returns a string suitable for `CachedWorktree.branch`.
///
/// - Normal branch: returns the trimmed branch name as-is.
/// - Detached HEAD (`"HEAD"`): re-queries with `git rev-parse --short HEAD` and
///   returns `"(detached: <sha>)"`.
/// - Empty output: falls back to `fork_name`.
/// - SSH failure: falls back to `fork_name` (degraded entry, still emitted).
fn resolve_fork_branch(
    ssh: &dyn SshExec,
    fork_host: &str,
    escaped_path: &str,
    branch_cmd: &str,
    fork_name: &str,
) -> String {
    match ssh.exec(fork_host, branch_cmd) {
        Ok(out) => {
            let raw = out.stdout.trim();
            if raw == "HEAD" {
                let commit_cmd = format!("cd {escaped_path} && git rev-parse --short HEAD");
                let sha = ssh
                    .exec(fork_host, &commit_cmd)
                    .map(|o| o.stdout.trim().to_string())
                    .unwrap_or_else(|_| fork_name.to_string());
                format!("(detached: {sha})")
            } else if raw.is_empty() {
                fork_name.to_string()
            } else {
                raw.to_string()
            }
        }
        Err(_) => fork_name.to_string(),
    }
}

/// Sanitizes a remote-sourced raw payload for inclusion in error messages.
///
/// Keeps ASCII-printable characters and spaces; drops control bytes,
/// multibyte sequences, and anything that could inject ANSI escapes or
/// corrupt structured-log output. Truncates to 256 characters.
fn sanitize_raw_payload(raw: &str) -> String {
    raw.chars()
        .filter(|c| c.is_ascii_graphic() || *c == ' ')
        .take(256)
        .collect()
}
