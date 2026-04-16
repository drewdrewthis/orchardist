//! Hexagonal port for remote worktree and session access.
//!
//! Defines the `RemoteAdapter` enum (the port), the `SshExec` seam for injection,
//! and stub adapter types. The coder fills in real implementations; these stubs
//! compile but return `unimplemented!()` so tests fail red until production code exists.
//!
//! # Design decision (recorded per feature.feature:30)
//!
//! `RemoteAdapter` is an enum, not `Box<dyn Trait>`. CLAUDE.md reserves trait
//! objects for "genuinely polymorphic behaviour — cases where multiple implementations
//! exist at runtime". Three adapters known at compile time is textbook enum dispatch.
//! The `SshExec` seam IS a trait object (`&dyn SshExec`) because that IS polymorphic
//! at runtime: the real process runner vs. test doubles both implement it.

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
#[derive(Debug)]
pub enum AdapterError {
    /// The remote SSH command failed or the host was unreachable.
    SshFailure {
        /// Human-readable description of the failure.
        message: String,
    },
    /// The response from the remote was received but could not be parsed.
    ///
    /// `raw` holds the first 256 bytes of the offending payload for logging.
    ParseFailure {
        /// The raw payload (truncated at 256 bytes if large) for diagnostic logging.
        raw: String,
    },
}

impl std::fmt::Display for AdapterError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AdapterError::SshFailure { message } => write!(f, "SSH failure: {message}"),
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
/// The JSON field name on `RemoteConfigTyped` is `"type"` via `serde(rename = "type")`.
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

/// A remote config entry that requires an explicit `"type"` field.
///
/// This is the target schema for AC4. The coder will migrate `RemoteConfig`
/// in `global_config.rs` to carry this; until then these tests use
/// `RemoteConfigTyped` directly to encode the required shape.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RemoteConfigTyped {
    /// Logical name for this remote (e.g. "remmy", "gpu-box").
    pub name: String,
    /// SSH target, e.g. "user@host".
    pub host: String,
    /// Absolute path on the remote host.
    pub path: String,
    /// The adapter kind. Serialized as the `"type"` field in JSON.
    #[serde(rename = "type")]
    pub kind: RemoteKind,
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
// Real SSH executor (stub — wires to existing remote::ssh_exec later)
// ---------------------------------------------------------------------------

/// SSH executor that spawns a real `ssh` subprocess.
pub struct ProcessSshExec;

impl SshExec for ProcessSshExec {
    fn exec(&self, _host: &str, _cmd: &str) -> Result<SshOutput> {
        unimplemented!("ProcessSshExec: real implementation not yet wired")
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
    pub fn from_config(cfg: &RemoteConfigTyped, ssh: Box<dyn SshExec>) -> Self {
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
        let cmd = format!("git -C {} worktree list --porcelain", self.path);
        let stdout = match self.ssh.exec(&self.host, &cmd) {
            Ok(output) => output.stdout,
            Err(_) => return Ok(vec![]),
        };
        let mut worktrees: Vec<CachedWorktree> = parse_porcelain(&stdout)
            .into_iter()
            .filter(|wt| !wt.is_bare)
            .collect();
        for wt in &mut worktrees {
            wt.host = Some(self.host.clone());
        }
        Ok(worktrees)
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
        let cmd = format!("git -C {} worktree list --porcelain", self.path);
        let stdout = match self.ssh.exec(&self.host, &cmd) {
            Ok(output) => output.stdout,
            Err(_) => return Ok(vec![]),
        };
        let mut worktrees: Vec<CachedWorktree> = parse_porcelain(&stdout)
            .into_iter()
            .filter(|wt| !wt.is_bare)
            .collect();
        for wt in &mut worktrees {
            wt.host = Some(self.host.clone());
            wt.layout = WorktreeLayout::Bare;
        }
        Ok(worktrees)
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
/// `path` is the repo root on the fork VM. It defaults to `"~/langwatch"` via
/// `serde(default)` when the key is absent from the JSON — this matches the
/// langwatch boxd convention where every fork clones the repo to `~/langwatch`.
///
/// When boxd's `list --json` output includes an explicit `"path"` key, that
/// value is used instead.
#[derive(Debug, Deserialize)]
struct BoxdForkEntry {
    /// Human-readable fork name, typically the issue slug (e.g. `"issue3155"`).
    name: String,
    /// SSH hostname of the fork VM (e.g. `"issue3155.boxd.sh"`).
    host: String,
    /// Repo root path on the fork VM. Defaults to `"~/langwatch"` when absent.
    #[serde(default = "default_fork_repo_path")]
    path: String,
}

/// Returns the conventional boxd repo path used as the default when the
/// `list --json` response does not include an explicit `"path"` key.
fn default_fork_repo_path() -> String {
    "~/langwatch".to_string()
}

/// Adapter for Boxd fork-per-issue VMs.
///
/// Enumerates live forks via `ssh boxd.sh list --json` then probes each fork
/// VM individually for its branch and tmux sessions.
pub struct BoxdForkAdapter {
    /// The golden Boxd host used for enumeration (e.g. `"boxd.sh"`).
    pub golden_host: String,
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
                raw: list_stdout.chars().take(256).collect(),
            })?;

        let mut worktrees = Vec::with_capacity(entries.len());

        // Steps 3-5: per-fork branch resolution.
        for entry in entries {
            let fork_host = format!("boxd@{}", entry.host);
            let branch_cmd = format!("cd {} && git rev-parse --abbrev-ref HEAD", entry.path);

            let branch = match self.ssh.exec(&fork_host, &branch_cmd) {
                Ok(out) => {
                    let raw = out.stdout.trim().to_string();
                    if raw == "HEAD" {
                        // Detached HEAD: resolve commit hash.
                        let commit_cmd = format!("cd {} && git rev-parse --short HEAD", entry.path);
                        let sha = self
                            .ssh
                            .exec(&fork_host, &commit_cmd)
                            .map(|o| o.stdout.trim().to_string())
                            .unwrap_or_else(|_| entry.name.clone());
                        format!("(detached: {sha})")
                    } else if raw.is_empty() {
                        // Branch probe returned empty output — use fork name as fallback.
                        entry.name.clone()
                    } else {
                        raw
                    }
                }
                Err(_) => {
                    // SSH to fork VM failed — emit degraded entry with fork name as branch.
                    entry.name.clone()
                }
            };

            worktrees.push(CachedWorktree {
                path: entry.path,
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
// Internal helpers
// ---------------------------------------------------------------------------

/// Parses `git worktree list --porcelain` output into `CachedWorktree` entries.
///
/// Mirrors `cache_sources::parse_worktree_porcelain` — kept here to avoid
/// a circular module dependency (`remote_adapter` → `cache_sources` → `global_config`
/// → `remote_adapter`). Slice 2 should consolidate into a shared `git_parse` module.
fn parse_porcelain(output: &str) -> Vec<CachedWorktree> {
    let mut worktrees = Vec::new();

    for block in output.trim().split("\n\n") {
        let block = block.trim();
        if block.is_empty() {
            continue;
        }

        let mut path = String::new();
        let mut branch = String::new();
        let mut is_bare = false;
        let mut is_locked = false;

        for line in block.lines() {
            if let Some(rest) = line.strip_prefix("worktree ") {
                path = rest.to_string();
            } else if let Some(rest) = line.strip_prefix("branch ") {
                let name = rest.strip_prefix("refs/heads/").unwrap_or(rest);
                branch = name.to_string();
            } else if line == "bare" {
                is_bare = true;
            } else if line.starts_with("locked") {
                is_locked = true;
            }
        }

        if path.is_empty() {
            continue;
        }

        worktrees.push(CachedWorktree {
            path,
            branch,
            is_bare,
            is_locked,
            host: None,
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: WorktreeLayout::Bare,
        });
    }

    worktrees
}
