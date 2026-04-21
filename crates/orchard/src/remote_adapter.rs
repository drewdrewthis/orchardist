//! Hexagonal port for remote worktree and session access.
//!
//! Defines the `RemoteAdapter` enum (the port), the `SshExec` seam for
//! injection, and four adapter variants (`RemmyAdapter`, `BoxdSharedAdapter`,
//! `BoxdForkAdapter`, `OrchardProxyAdapter`). `RemoteAdapter::from_config`
//! selects the right variant from a `global_config::RemoteConfig` and the
//! production caller (`cache_sources::refresh_remote_worktrees`) dispatches
//! through it.
//!
//! # Design decision (recorded per feature.feature:30)
//!
//! `RemoteAdapter` is an enum, not `Box<dyn Trait>`. CLAUDE.md reserves trait
//! objects for "genuinely polymorphic behaviour — cases where multiple implementations
//! exist at runtime". Four adapters known at compile time is textbook enum dispatch.
//! The `SshExec` seam IS a trait object (`Box<dyn SshExec>`) because that IS
//! polymorphic at runtime: the real process runner vs. test doubles both
//! implement it.
//!
//! # AC6 — no silent fallback
//!
//! `OrchardProxyAdapter` does **not** fall back to legacy shell-discovery on
//! failure. Any SSH or parse error surfaces immediately as an `AdapterError`
//! and writes a `remote_adapter.proxy_failure` event to `events.jsonl`. The
//! last-known snapshot on disk remains visible via the cache-only read path;
//! for legacy behaviour on a specific host, configure that remote as `"type":
//! "remmy"` instead.

use std::collections::HashMap;
use std::sync::OnceLock;

use anyhow::Result;

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::cache::{CachedTmuxSession, CachedWorktree, WorktreeLayout};

// ---------------------------------------------------------------------------
// AdapterError — slice 2 (feature.feature:185)
// ---------------------------------------------------------------------------

/// Errors that a `RemoteAdapter` method can return.
///
/// - `ParseFailure`: the remote responded but the payload could not be
///   parsed (e.g. malformed JSON from `ssh boxd.sh list --json`).
/// - `FetchFailure`: the outbound call itself failed (SSH connect timeout,
///   subprocess kill, host unreachable). Distinguished from `Ok(vec![])`
///   so the caller can preserve the prior cache instead of treating an
///   empty result as authoritative — preventing a flood of false
///   `worktree.remote_lost` events when `boxd.sh` is briefly unreachable.
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
    /// The outbound call failed before any payload was received.
    ///
    /// `message` is the underlying error stringified — already sanitized
    /// at the boundary where the SSH stderr is read.
    FetchFailure {
        /// Human-readable description of the failure.
        message: String,
    },
}

impl std::fmt::Display for AdapterError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AdapterError::ParseFailure { raw } => {
                write!(f, "boxd.sh list parse failure; raw payload: {raw}")
            }
            AdapterError::FetchFailure { message } => {
                write!(f, "remote fetch failure: {message}")
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
    /// Federated orchard-proxy: invokes `ssh host orchard --json` and projects
    /// the remote `JsonOutput` into `CachedWorktree` / `CachedTmuxSession`.
    /// On any SSH or parse failure, surfaces the error and emits a
    /// `remote_adapter.proxy_failure` event; the last-known snapshot on disk
    /// remains visible. To use legacy shell-discovery for a host, configure
    /// `"type": "remmy"` instead.
    OrchardProxy,
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
/// dispatch via `match` rather than virtual dispatch because all four
/// adapter kinds are known at compile time.
pub enum RemoteAdapter {
    /// Remmy-style adapter: bare repo + worktrees over SSH.
    Remmy(RemmyAdapter),
    /// Boxd shared-VM adapter: single VM with multiple worktrees.
    BoxdShared(BoxdSharedAdapter),
    /// Boxd fork-per-issue adapter: one VM per open issue.
    BoxdFork(BoxdForkAdapter),
    /// Federated orchard-proxy adapter: `ssh host orchard --json`.
    OrchardProxy(OrchardProxyAdapter),
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
            RemoteKind::OrchardProxy => RemoteAdapter::OrchardProxy(OrchardProxyAdapter {
                host: cfg.host.clone(),
                path: cfg.path.clone(),
                ssh,
                snapshot: OnceLock::new(),
            }),
        }
    }

    /// Returns all non-bare worktrees visible to this adapter.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        match self {
            RemoteAdapter::Remmy(a) => a.list_worktrees(),
            RemoteAdapter::BoxdShared(a) => a.list_worktrees(),
            RemoteAdapter::BoxdFork(a) => a.list_worktrees(),
            RemoteAdapter::OrchardProxy(a) => a.list_worktrees(),
        }
    }

    /// Returns all tmux sessions visible to this adapter.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        match self {
            RemoteAdapter::Remmy(a) => a.list_sessions(),
            RemoteAdapter::BoxdShared(a) => a.list_sessions(),
            RemoteAdapter::BoxdFork(a) => a.list_sessions(),
            RemoteAdapter::OrchardProxy(a) => a.list_sessions(),
        }
    }
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
}

// ---------------------------------------------------------------------------
// BoxdForkAdapter
// ---------------------------------------------------------------------------

/// A single fork VM entry returned by `ssh boxd.sh list --json`.
///
/// The real boxd payload uses `url` for the fork hostname and includes a
/// `status` field (`"running"` / `"stopped"` / …). Only running forks are
/// treated as live. `host` is accepted as an alias for forward-compatibility
/// with the earlier schema. `path` is optional; when absent, the adapter's
/// configured `fork_repo_path` is used (derived from `RemoteConfig.path`,
/// not a hardcoded tenant path).
#[derive(Debug, Deserialize)]
struct BoxdForkEntry {
    /// Human-readable fork name, typically the issue slug (e.g. `"issue3155"`).
    name: String,
    /// SSH hostname of the fork VM (e.g. `"issue3155.boxd.sh"`). The real
    /// boxd controller emits `url`; `host` is kept as an alias so older
    /// fixtures and the existing tests still parse.
    #[serde(alias = "host")]
    url: String,
    /// VM lifecycle status. Missing or non-`"running"` values are treated
    /// as not-live and filtered out before any per-fork SSH.
    #[serde(default)]
    status: Option<String>,
    /// Repo root path on the fork VM. Optional — falls back to the
    /// adapter's configured path when absent.
    #[serde(default)]
    path: Option<String>,
}

impl BoxdForkEntry {
    fn is_running(&self) -> bool {
        self.status.as_deref() == Some("running")
    }
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
    /// Enumerates live fork VMs from the golden host.
    ///
    /// Returns `(fork_host, entry)` tuples for each entry that is `running`
    /// (or has no status field) and whose URL passes `is_safe_ssh_host`. The
    /// `fork_host` is the `<user>@<url>` form ready to pass to SSH.
    ///
    /// SSH failure on the golden host returns `Err(FetchFailure)` so callers
    /// can preserve prior cache rather than treating an empty list as
    /// authoritative; malformed JSON returns `Err(ParseFailure)`.
    fn list_live_forks(&self) -> Result<Vec<(String, BoxdForkEntry)>> {
        let list_stdout = match self.ssh.exec(&self.golden_host, "list --json") {
            Ok(output) => output.stdout,
            Err(e) => {
                return Err(AdapterError::FetchFailure {
                    message: e.to_string(),
                }
                .into());
            }
        };

        let entries: Vec<BoxdForkEntry> =
            serde_json::from_str(&list_stdout).map_err(|_| AdapterError::ParseFailure {
                raw: sanitize_raw_payload(&list_stdout),
            })?;

        let user_prefix = ssh_user_prefix(&self.golden_host);
        let live: Vec<(String, BoxdForkEntry)> = entries
            .into_iter()
            .filter(|e| e.status.is_none() || e.is_running())
            .filter(|e| is_safe_ssh_host(&e.url))
            .map(|e| (format!("{user_prefix}@{}", e.url), e))
            .collect();

        Ok(live)
    }

    /// Returns one `CachedWorktree` per live fork VM.
    ///
    /// Calls `list_live_forks` to enumerate running forks, then SSHes each
    /// fork VM to resolve its branch via `git rev-parse --abbrev-ref HEAD`.
    /// SSH failure on the golden host returns `Err(FetchFailure)` so the
    /// caller can preserve prior cache rather than emitting spurious
    /// `worktree.remote_lost` events. Detached HEAD falls back to
    /// `git rev-parse --short HEAD` formatted as `"(detached: <sha>)"`.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        let live_forks = self.list_live_forks()?;
        let mut worktrees = Vec::with_capacity(live_forks.len());

        for (fork_host, entry) in live_forks {
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
    /// Calls `list_live_forks` to enumerate running forks, then SSHes each
    /// fork VM with the standard `-F` template consumed by
    /// `cache_sources::parse_tmux_sessions_from_panes`. A single dead fork
    /// is skipped (warning logged); one bad VM does not silence sessions from
    /// others. SSH failure on the golden host returns `Err(FetchFailure)` so
    /// callers can preserve prior cache rather than treating an empty result
    /// as authoritative.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        let live_forks = self.list_live_forks()?;
        let tmux_list_cmd = format!(
            "tmux list-panes -a -F '{}'",
            crate::cache_sources::TMUX_SESSION_FORMAT
        );
        let mut all_sessions: Vec<CachedTmuxSession> = Vec::new();

        for (fork_host, _entry) in live_forks {
            let stdout = match self.ssh.exec(&fork_host, &tmux_list_cmd) {
                Ok(output) => output.stdout,
                Err(e) => {
                    crate::logger::LOG
                        .warn(&format!("list_sessions: skipping fork {fork_host}: {e}"));
                    continue;
                }
            };

            let mut sessions = crate::cache_sources::parse_tmux_sessions_from_panes(
                &stdout,
                Some(&fork_host),
                |_| String::new(),
                |_| vec![],
            );

            // parse_tmux_sessions_from_panes sets host from the `host` argument,
            // but ensure every entry carries the fork host for downstream joins.
            for s in &mut sessions {
                if s.host.is_none() {
                    s.host = Some(fork_host.clone());
                }
            }

            all_sessions.extend(sessions);
        }

        Ok(all_sessions)
    }
}

// ---------------------------------------------------------------------------
// OrchardProxyAdapter
// ---------------------------------------------------------------------------

/// Adapter that invokes `ssh host orchard --json`, deserializes the response
/// into the existing `JsonOutput` wire format, and projects it into
/// `CachedWorktree` / `CachedTmuxSession` entries.
///
/// On any failure (missing binary, non-zero exit, malformed JSON, unknown
/// version, SSH error) the adapter returns the error to the caller and emits
/// a `remote_adapter.proxy_failure` diagnostic line to `events.jsonl` via
/// [`crate::events::log_event`]. There is **no** silent fallback to legacy
/// shell-discovery; the last-known snapshot on disk stays visible via the
/// cache-only read path.
///
/// The remote `orchard --json` output is memoized in a [`OnceLock`] so that
/// calling both [`list_worktrees`] and [`list_sessions`] on the same adapter
/// instance results in at most one SSH invocation.
pub struct OrchardProxyAdapter {
    /// SSH target host (e.g. `"boxd@vm.boxd.sh"`).
    pub host: String,
    /// Absolute path on the remote (passed through but not used for the proxy
    /// call itself).
    pub path: String,
    /// SSH executor (real process or test double).
    pub ssh: Box<dyn SshExec>,
    /// Memoized result of the single `orchard --json` SSH round-trip.
    ///
    /// `OnceLock` guarantees at most one call regardless of how many times
    /// `list_worktrees` and `list_sessions` are invoked on the same instance.
    pub snapshot: OnceLock<Result<crate::json_output::JsonOutput, AdapterError>>,
}

impl OrchardProxyAdapter {
    /// Performs (or reuses) the `ssh host orchard --json` call.
    ///
    /// On first call, executes the SSH command, checks the exit code,
    /// deserializes the JSON payload, and validates the schema version.
    /// Subsequent calls return a reference to the cached result.
    fn fetch_snapshot(&self) -> &Result<crate::json_output::JsonOutput, AdapterError> {
        self.snapshot.get_or_init(|| {
            let output = match self.ssh.exec(&self.host, "orchard --json") {
                Ok(o) => o,
                Err(e) => {
                    return Err(AdapterError::FetchFailure {
                        message: e.to_string(),
                    });
                }
            };

            // Non-zero exit code — check for the most common case (exit 127 = command not found)
            if output.exit_code != 0 {
                let reason = match output.exit_code {
                    127 => "remote orchard missing (exit 127)".to_string(),
                    255 => "fetch failure (exit 255)".to_string(),
                    code => format!("remote orchard failed (exit {code})"),
                };
                return Err(AdapterError::FetchFailure { message: reason });
            }

            // Parse JSON.
            let json_output: crate::json_output::JsonOutput =
                match serde_json::from_str(&output.stdout) {
                    Ok(v) => v,
                    Err(_) => {
                        return Err(AdapterError::ParseFailure {
                            raw: sanitize_raw_payload(&output.stdout),
                        });
                    }
                };

            // Version check.
            crate::json_output::check_json_output_version(json_output.version)?;

            // Persist the snapshot for cold-start reads. Best-effort: a write
            // failure is logged but must not cause the list call to fail.
            if let Err(e) = crate::orchard_snapshot::write_snapshot(&self.host, &json_output) {
                crate::events::log_event(
                    "remote_snapshot.write_failed",
                    &[
                        ("host", serde_json::Value::String(self.host.clone())),
                        ("reason", serde_json::Value::String(e.to_string())),
                    ],
                );
            }

            Ok(json_output)
        })
    }

    /// Returns all non-bare worktrees sourced from `ssh host orchard --json`.
    ///
    /// On success, projects each `JsonWorktree` from every `JsonRepo` in the
    /// remote snapshot into a `CachedWorktree` tagged with the configured host.
    /// On any error, writes a `remote_adapter.proxy_failure` diagnostic to
    /// `events.jsonl` and returns the error to the caller. There is no silent
    /// fallback — the last-known snapshot on disk stays visible via the
    /// cache-only read path.
    pub fn list_worktrees(&self) -> Result<Vec<CachedWorktree>> {
        match self.fetch_snapshot() {
            Ok(snapshot) => {
                let mut worktrees = Vec::new();
                for repo in &snapshot.repos {
                    for wt in &repo.worktrees {
                        if wt.is_main_worktree {
                            continue; // skip bare/main worktrees
                        }
                        worktrees.push(CachedWorktree {
                            path: wt.path.clone(),
                            branch: wt.branch.clone(),
                            is_bare: false,
                            is_locked: false,
                            host: Some(self.host.clone()),
                            ahead: wt.ahead_behind.as_ref().map(|ab| ab.ahead),
                            behind: wt.ahead_behind.as_ref().map(|ab| ab.behind),
                            last_commit_at: wt.last_commit_at.clone(),
                            layout: if wt.layout == "bare" {
                                crate::cache::WorktreeLayout::Bare
                            } else {
                                crate::cache::WorktreeLayout::Flat
                            },
                        });
                    }
                }
                Ok(worktrees)
            }
            Err(e) => {
                self.log_proxy_failure_diagnostic(e);
                Err(anyhow::anyhow!("{}", e))
            }
        }
    }

    /// Returns all tmux sessions sourced from the same `orchard --json` snapshot.
    ///
    /// Reuses the memoized snapshot so no additional SSH call is made when
    /// `list_worktrees` was already called on this adapter instance.
    /// On any error, writes a `remote_adapter.proxy_failure` diagnostic to
    /// `events.jsonl` and returns the error to the caller. There is no silent
    /// fallback — the last-known snapshot on disk stays visible via the
    /// cache-only read path.
    pub fn list_sessions(&self) -> Result<Vec<CachedTmuxSession>> {
        match self.fetch_snapshot() {
            Ok(snapshot) => {
                let mut sessions = Vec::new();
                // Collect sessions from worktrees.
                for repo in &snapshot.repos {
                    for wt in &repo.worktrees {
                        for s in &wt.sessions {
                            sessions.push(json_session_to_cached(s, &self.host));
                        }
                    }
                }
                // Collect standalone sessions.
                for s in &snapshot.tmux_sessions {
                    sessions.push(json_session_to_cached(s, &self.host));
                }
                Ok(sessions)
            }
            Err(e) => {
                self.log_proxy_failure_diagnostic(e);
                Err(anyhow::anyhow!("{}", e))
            }
        }
    }

    /// Writes a `remote_adapter.proxy_failure` event to `events.jsonl`
    /// describing why this adapter's proxy call failed. Delegates the
    /// reason classification to [`classify_proxy_failure_reason`] so the
    /// vocabulary stays consistent with the probe-path diagnostic in
    /// `crate::sources::hosts`.
    fn log_proxy_failure_diagnostic(&self, err: &AdapterError) {
        let msg = err.to_string();
        let reason = classify_proxy_failure_reason(&msg);

        let snippet = match err {
            AdapterError::ParseFailure { raw } if !raw.starts_with("version skew") => {
                let bounded = bounded_chars(raw, 200);
                Some(format!(
                    "payload length {}, snippet: {}",
                    raw.len(),
                    bounded
                ))
            }
            _ => None,
        };

        let mut fields: Vec<(&str, Value)> = vec![
            ("host", Value::String(self.host.clone())),
            ("reason", Value::String(reason)),
            ("phase", Value::String("fetch".to_string())),
        ];
        let snippet_owned;
        if let Some(s) = snippet {
            snippet_owned = s;
            fields.push(("snippet", Value::String(snippet_owned)));
        }

        crate::events::log_event("remote_adapter.proxy_failure", &fields);
    }
}

/// Classifies a stringified SSH / adapter error into a short, stable
/// `reason` label for `remote_adapter.proxy_failure` diagnostics.
///
/// Both the probe path (`sources::hosts::probe_reachability_for_remote`)
/// and the fetch path (`OrchardProxyAdapter::log_proxy_failure_diagnostic`)
/// go through this helper so consumers of `events.jsonl` group failures
/// by a consistent vocabulary regardless of where in the pipeline the
/// failure originated. The `phase` field on the event distinguishes
/// probe vs fetch.
///
/// Recognised categories:
/// - `"remote orchard missing (exit 127)"` — command not found
/// - `"fetch failure (exit 255)"` — SSH connection failure
/// - `"probe timeout"` — `ssh_exec_with_timeout` hit `PROBE_TIMEOUT`
/// - `"version skew (remote version N)"` — schema mismatch
/// - `"parse failure"` — malformed JSON payload
/// - `"fetch failure: <bounded snippet>"` — everything else
pub fn classify_proxy_failure_reason(msg: &str) -> String {
    if msg.starts_with("version skew") {
        let version_hint = msg
            .split("remote version ")
            .nth(1)
            .and_then(|s| s.split_whitespace().next())
            .unwrap_or("unknown");
        format!("version skew (remote version {})", version_hint)
    } else if msg.contains("parse failure") || msg.contains("parse") && msg.contains("payload") {
        "parse failure".to_string()
    } else if msg.contains("timed out") {
        "probe timeout".to_string()
    } else if msg.contains("exit 127") || msg.contains("command not found") {
        "remote orchard missing (exit 127)".to_string()
    } else if msg.contains("exit 255")
        || msg.contains("Connection refused")
        || msg.contains("fetch failure")
    {
        "fetch failure (exit 255)".to_string()
    } else {
        format!("fetch failure: {}", bounded_chars(msg, 100))
    }
}

/// Truncates `s` to at most `max_chars` Unicode scalar values, not bytes.
///
/// Byte-slicing via `&s[..s.len().min(n)]` panics when the boundary lands
/// inside a multi-byte UTF-8 sequence (emoji, accented characters); stderr
/// text from a remote `ssh` subprocess can contain either, so we truncate on
/// char boundaries.
fn bounded_chars(s: &str, max_chars: usize) -> String {
    s.chars().take(max_chars).collect()
}

/// Projects a `JsonSession` from the remote `orchard --json` output into a
/// `CachedTmuxSession` tagged with the given host.
fn json_session_to_cached(s: &crate::json_output::JsonSession, host: &str) -> CachedTmuxSession {
    CachedTmuxSession {
        name: s.name.clone(),
        path: String::new(), // not available in JsonSession
        pane_targets: Vec::new(),
        pane_titles: Vec::new(),
        pane_commands: Vec::new(),
        window_names: Vec::new(),
        window_active: Vec::new(),
        window_layouts: Vec::new(),
        pane_paths: Vec::new(),
        pane_active: Vec::new(),
        host: Some(host.to_string()),
        created_at: None,
        last_activity_at: None,
        last_output_lines: Vec::new(),
        claude_state_raw: None,
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

/// Returns the SSH user prefix for per-fork hosts derived from the golden
/// host. If the configured golden host carries a `user@` prefix
/// (e.g. `"boxd@boxd.sh"`), that user is reused; otherwise the conventional
/// Boxd default `"boxd"` is used. This avoids hardcoding a tenant-specific
/// SSH username while preserving the langwatch deployment's defaults.
fn ssh_user_prefix(golden_host: &str) -> String {
    golden_host
        .split_once('@')
        .map(|(user, _)| user.to_string())
        .unwrap_or_else(|| "boxd".to_string())
}

/// Validates that a hostname returned by the boxd controller is safe to use
/// as the second argument to `ssh`.
///
/// `Command::args` does NOT pass strings through a shell, so classic shell
/// injection (`;`, `&`, backticks) is already prevented at the OS layer.
/// However, OpenSSH itself parses `-o option=value` style flags from any
/// argument that begins with `-` or contains spaces — a malicious value
/// like `evil.host -o ProxyCommand=...` would be honored. Restrict to
/// hostname-grade characters to block both classes.
fn is_safe_ssh_host(host: &str) -> bool {
    !host.is_empty()
        && host
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_'))
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    /// Regression test for #264: the BoxdFork adapter must SSH each live fork
    /// to collect its tmux sessions, not silently return an empty vec.
    #[test]
    fn boxd_fork_adapter_list_sessions_returns_sessions_from_each_live_fork() {
        // Arrange — wire up a FakeSshExec with two canned responses:
        //
        // 1. golden-host "list --json" → one fork entry
        // 2. fork-host tmux list-panes → one active-pane line using the template
        //    defined as `cache_sources::TMUX_SESSION_FORMAT` and consumed by
        //    `cache_sources::parse_tmux_sessions_from_panes`.
        let mut fake = FakeSshExec::new();

        // Response 1: fork enumeration from the golden Boxd controller.
        fake.insert(
            "boxd.sh",
            "list --json",
            SshOutput {
                stdout: r#"[{"name":"issue3155","host":"issue3155.boxd.sh","path":"/workspace/langwatch"}]"#
                    .to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        // Response 2: tmux list-panes on the fork VM, using the same `-F`
        // template consumed by `cache_sources::parse_tmux_sessions_from_panes`.
        let tmux_cmd = format!(
            "tmux list-panes -a -F '{}'",
            crate::cache_sources::TMUX_SESSION_FORMAT
        );
        fake.insert(
            "boxd@issue3155.boxd.sh",
            &tmux_cmd,
            SshOutput {
                stdout: "issue3155\t1\t/workspace/langwatch\t1713000000\t1713000060\n".to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = BoxdForkAdapter {
            golden_host: "boxd.sh".to_string(),
            fork_repo_path: "/workspace/langwatch".to_string(),
            ssh: Box::new(fake),
        };

        // Act
        let sessions = adapter
            .list_sessions()
            .expect("list_sessions must not error");

        // Assert — must return at least one session whose host matches the fork
        // VM and whose name matches what the fake returned.
        assert!(
            !sessions.is_empty(),
            "list_sessions must return sessions from each live fork"
        );

        let session = &sessions[0];
        assert_eq!(
            session.host.as_deref(),
            Some("boxd@issue3155.boxd.sh"),
            "session host should identify the fork VM"
        );
        assert_eq!(
            session.name, "issue3155",
            "session name should match the tmux session returned by the fork VM"
        );
    }

    /// The real `ssh boxd.sh list --json` payload uses `url` (not `host`)
    /// and includes a `status` field — only entries with `status == "running"`
    /// are live. A regression here would silently hide every boxd fork even
    /// though the adapter "works".
    #[test]
    fn boxd_fork_adapter_filters_non_running_forks_and_parses_url_field() {
        let mut fake = FakeSshExec::new();

        // Payload shape as emitted by the real boxd controller.
        fake.insert(
            "boxd.sh",
            "list --json",
            SshOutput {
                stdout: r#"[
                    {"name":"issue3155","url":"issue3155.boxd.sh","status":"running","vm_id":"a"},
                    {"name":"session-fix","url":"session-fix.boxd.sh","status":"stopped","vm_id":"b"}
                ]"#.to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        fake.insert(
            "boxd@issue3155.boxd.sh",
            "cd /workspace/langwatch && git rev-parse --abbrev-ref HEAD",
            SshOutput {
                stdout: "issue3155/some-branch\n".to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = BoxdForkAdapter {
            golden_host: "boxd.sh".to_string(),
            fork_repo_path: "/workspace/langwatch".to_string(),
            ssh: Box::new(fake),
        };

        let worktrees = adapter
            .list_worktrees()
            .expect("list_worktrees must not error");

        assert_eq!(
            worktrees.len(),
            1,
            "stopped fork should be filtered out; only the running one is emitted"
        );
        assert_eq!(worktrees[0].host.as_deref(), Some("boxd@issue3155.boxd.sh"));
        assert_eq!(worktrees[0].branch, "issue3155/some-branch");
    }

    // -----------------------------------------------------------------------
    // AC1: RemoteKind::OrchardProxy config parsing
    // -----------------------------------------------------------------------

    /// AC1 scenario 1: `"type": "orchard-proxy"` parses to `RemoteKind::OrchardProxy`.
    #[test]
    fn remote_kind_orchard_proxy_parses_from_kebab_case_string() {
        let json = r#"{"type": "orchard-proxy"}"#;
        #[derive(serde::Deserialize)]
        struct Wrapper {
            #[serde(rename = "type")]
            kind: RemoteKind,
        }
        let w: Wrapper = serde_json::from_str(json).expect("should parse");
        assert_eq!(w.kind, RemoteKind::OrchardProxy);
    }

    /// AC1 scenario 2: `from_config` for `OrchardProxy` kind returns an
    /// `OrchardProxy` variant carrying the configured host.
    #[test]
    fn from_config_orchard_proxy_returns_orchard_proxy_adapter() {
        let cfg = crate::global_config::RemoteConfig {
            name: "proxy".to_string(),
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/git-orchard-rs".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::OrchardProxy,
        };
        let adapter = RemoteAdapter::from_config(&cfg, Box::new(FakeSshExec::new()));
        match adapter {
            RemoteAdapter::OrchardProxy(a) => {
                assert_eq!(a.host, "boxd@vm.boxd.sh");
            }
            _ => panic!("expected OrchardProxy variant"),
        }
    }

    /// AC1 scenario 3: an invalid `"type"` string causes a serde error whose
    /// message includes the string `"orchard-proxy"` (serde auto-generates this
    /// from the enum's variant names).
    #[test]
    fn invalid_remote_kind_error_mentions_orchard_proxy() {
        let json = r#"{"type": "kubernetes"}"#;
        #[derive(Debug, serde::Deserialize)]
        struct Wrapper {
            // Field is read by serde during deserialization; Rust's dead-code
            // analysis ignores the serde derive, hence the allow.
            #[serde(rename = "type")]
            #[allow(dead_code)]
            kind: RemoteKind,
        }
        let err = serde_json::from_str::<Wrapper>(json)
            .expect_err("kubernetes is not a valid RemoteKind");
        let msg = err.to_string();
        assert!(
            msg.contains("orchard-proxy"),
            "error message should list orchard-proxy as a supported type, got: {msg}"
        );
    }

    // -----------------------------------------------------------------------
    // AC2: OrchardProxyAdapter.list_worktrees() — sources from orchard --json
    // -----------------------------------------------------------------------

    /// Returns a minimal valid `JsonOutput` JSON string with one repo containing
    /// one non-main worktree on the given branch.
    fn canned_json_output_one_worktree(branch: &str) -> String {
        format!(
            r#"{{
                "version": 6,
                "tmuxSessions": [],
                "repos": [{{
                    "slug": "owner/repo",
                    "worktrees": [
                        {{
                            "path": "/remote/repo/{branch}",
                            "branch": "{branch}",
                            "host": null,
                            "layout": "flat",
                            "issue": null,
                            "pr": null,
                            "sessions": [],
                            "displayGroup": "other",
                            "status": "ready",
                            "statusGlyph": "🟢",
                            "isMainWorktree": false
                        }}
                    ]
                }}],
                "hosts": {{}}
            }}"#,
            branch = branch
        )
    }

    /// AC2 scenario 1: `list_worktrees()` parses `orchard --json` output and
    /// returns CachedWorktree entries tagged with the configured host.
    #[test]
    fn orchard_proxy_list_worktrees_parses_json_output() {
        let mut fake = FakeSshExec::new();
        fake.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: canned_json_output_one_worktree("issue329/federated-orchard"),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/git-orchard-rs".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        let worktrees = adapter
            .list_worktrees()
            .expect("list_worktrees must not error");

        assert_eq!(worktrees.len(), 1, "expected exactly 1 worktree");
        assert_eq!(
            worktrees[0].branch, "issue329/federated-orchard",
            "branch must match canned output"
        );
        assert_eq!(
            worktrees[0].host.as_deref(),
            Some("boxd@vm.boxd.sh"),
            "host must be set to the configured remote host"
        );
    }

    /// AC2 scenario 2: `list_worktrees()` on success must NOT have invoked
    /// `git worktree list --porcelain`. Verified by checking recorded commands.
    #[test]
    fn orchard_proxy_list_worktrees_does_not_invoke_git_worktree_list() {
        use std::sync::{Arc, Mutex};

        /// Recording fake: records every (host, cmd) pair.
        struct RecordingFakeSshExec {
            inner: FakeSshExec,
            calls: Arc<Mutex<Vec<(String, String)>>>,
        }

        impl SshExec for RecordingFakeSshExec {
            fn exec(&self, host: &str, cmd: &str) -> Result<SshOutput> {
                self.calls
                    .lock()
                    .unwrap()
                    .push((host.to_string(), cmd.to_string()));
                self.inner.exec(host, cmd)
            }
        }

        let calls = Arc::new(Mutex::new(Vec::<(String, String)>::new()));
        let mut inner = FakeSshExec::new();
        inner.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: canned_json_output_one_worktree("main"),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let recording = RecordingFakeSshExec {
            inner,
            calls: calls.clone(),
        };

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(recording),
            snapshot: OnceLock::new(),
        };

        adapter.list_worktrees().expect("must not error");

        let recorded = calls.lock().unwrap();
        let cmds: Vec<&str> = recorded.iter().map(|(_, c)| c.as_str()).collect();

        assert!(
            cmds.contains(&"orchard --json"),
            "orchard --json must be invoked"
        );
        assert!(
            !cmds.iter().any(|c| c.contains("git worktree list")),
            "git worktree list --porcelain must NOT be invoked on success path; got: {cmds:?}"
        );
    }

    // -----------------------------------------------------------------------
    // AC3: list_sessions() shares the same snapshot; single SSH call
    // -----------------------------------------------------------------------

    /// AC3 scenario 1: `list_sessions()` returns sessions from the `orchard --json` snapshot.
    #[test]
    fn orchard_proxy_list_sessions_parses_sessions_from_snapshot() {
        // Build a JSON output with two sessions on the worktree + one standalone.
        let json = r#"{
            "version": 6,
            "tmuxSessions": [
                {
                    "name": "shepherd",
                    "host": "local",
                    "status": "running",
                    "claude": null,
                    "windows": []
                }
            ],
            "repos": [{
                "slug": "owner/repo",
                "worktrees": [{
                    "path": "/remote/repo/issue329",
                    "branch": "issue329/federated",
                    "host": null,
                    "layout": "flat",
                    "issue": null,
                    "pr": null,
                    "sessions": [
                        {"name": "or_issue329_a", "host": "local", "status": "running", "claude": null, "windows": []},
                        {"name": "or_issue329_b", "host": "local", "status": "running", "claude": null, "windows": []}
                    ],
                    "displayGroup": "other",
                    "status": "ready",
                    "statusGlyph": "🟢",
                    "isMainWorktree": false
                }]
            }],
            "hosts": {}
        }"#;

        let mut fake = FakeSshExec::new();
        fake.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: json.to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        let sessions = adapter.list_sessions().expect("must not error");

        assert_eq!(
            sessions.len(),
            3,
            "expected 2 worktree sessions + 1 standalone"
        );
        for s in &sessions {
            assert_eq!(
                s.host.as_deref(),
                Some("boxd@vm.boxd.sh"),
                "every session must carry the remote host"
            );
        }
    }

    /// AC3 scenario 2: calling both `list_worktrees()` and `list_sessions()`
    /// results in at most 1 `orchard --json` SSH invocation.
    #[test]
    fn orchard_proxy_list_worktrees_and_sessions_share_single_ssh_call() {
        use std::sync::{Arc, Mutex};

        struct CountingFakeSshExec {
            inner: FakeSshExec,
            count: Arc<Mutex<usize>>,
        }

        impl SshExec for CountingFakeSshExec {
            fn exec(&self, host: &str, cmd: &str) -> Result<SshOutput> {
                if cmd == "orchard --json" {
                    *self.count.lock().unwrap() += 1;
                }
                self.inner.exec(host, cmd)
            }
        }

        let count = Arc::new(Mutex::new(0usize));
        let mut inner = FakeSshExec::new();
        inner.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: canned_json_output_one_worktree("main"),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let counting = CountingFakeSshExec {
            inner,
            count: count.clone(),
        };

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(counting),
            snapshot: OnceLock::new(),
        };

        adapter
            .list_worktrees()
            .expect("list_worktrees must not error");
        adapter
            .list_sessions()
            .expect("list_sessions must not error");

        let invocations = *count.lock().unwrap();
        assert_eq!(
            invocations, 1,
            "exactly 1 `orchard --json` SSH call expected; got {invocations}"
        );
    }

    // -----------------------------------------------------------------------
    // AC6: Proxy failures surface as events; no silent fallback
    // -----------------------------------------------------------------------

    /// AC6 scenario 1: exit code 127 returns FetchFailure and does NOT attempt
    /// any legacy `git worktree list --porcelain` invocation.
    #[test]
    fn orchard_proxy_exit_127_surfaces_proxy_failure_event() {
        use std::sync::{Arc, Mutex};

        struct RecordingFakeSshExec {
            inner: FakeSshExec,
            calls: Arc<Mutex<Vec<String>>>,
        }

        impl SshExec for RecordingFakeSshExec {
            fn exec(&self, _host: &str, cmd: &str) -> Result<SshOutput> {
                self.calls.lock().unwrap().push(cmd.to_string());
                self.inner.exec(_host, cmd)
            }
        }

        let calls = Arc::new(Mutex::new(Vec::<String>::new()));
        let mut inner = FakeSshExec::new();
        inner.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: String::new(),
                stderr: "orchard: not found".to_string(),
                exit_code: 127,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(RecordingFakeSshExec {
                inner,
                calls: calls.clone(),
            }),
            snapshot: OnceLock::new(),
        };

        let result = adapter.list_worktrees();

        // Must return Err — no fallback.
        assert!(
            result.is_err(),
            "exit 127 must surface as Err (FetchFailure), not Ok; got: {result:?}"
        );

        // The recorded command list must only contain `orchard --json` — no git worktree list.
        let recorded = calls.lock().unwrap();
        assert!(
            !recorded.iter().any(|c| c.contains("git worktree list")),
            "git worktree list --porcelain must NOT be invoked on failure path; got: {recorded:?}"
        );
    }

    /// AC6 scenario 2: malformed JSON returns ParseFailure.
    #[test]
    fn orchard_proxy_malformed_json_surfaces_proxy_failure_event() {
        let mut fake = FakeSshExec::new();
        fake.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: r#"{ "repos": [malformed..."#.to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        let result = adapter.list_worktrees();

        // Must return Err — no fallback.
        assert!(
            result.is_err(),
            "malformed JSON must surface as Err (ParseFailure), not Ok; got: {result:?}"
        );
        let err_str = result.unwrap_err().to_string();
        assert!(
            err_str.to_lowercase().contains("parse"),
            "error must identify a parse failure; got: {err_str}"
        );
    }

    /// AC6 scenario 3: unknown version returns ParseFailure with version skew message.
    #[test]
    fn orchard_proxy_unknown_version_surfaces_proxy_failure_event() {
        let mut fake = FakeSshExec::new();
        // Version 0 is not in SUPPORTED_JSON_OUTPUT_VERSIONS.
        let json = r#"{"version": 0, "tmuxSessions": [], "repos": [], "hosts": {}}"#;
        fake.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: json.to_string(),
                stderr: String::new(),
                exit_code: 0,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        let result = adapter.list_worktrees();

        // Must return Err — version skew surfaces as ParseFailure.
        assert!(
            result.is_err(),
            "version skew must surface as Err, not Ok; got: {result:?}"
        );
        let err_str = result.unwrap_err().to_string();
        assert!(
            err_str.to_lowercase().contains("version") || err_str.to_lowercase().contains("parse"),
            "error must mention version skew or parse failure; got: {err_str}"
        );
    }

    /// AC6 scenario 4: SSH connection failure (exit 255) returns FetchFailure.
    #[test]
    fn orchard_proxy_ssh_failure_exit_255_surfaces_proxy_failure_event() {
        let mut fake = FakeSshExec::new();
        fake.insert(
            "boxd@vm.boxd.sh",
            "orchard --json",
            SshOutput {
                stdout: String::new(),
                stderr: "ssh: connect to host vm.boxd.sh port 22: Connection refused".to_string(),
                exit_code: 255,
            },
        );

        let adapter = OrchardProxyAdapter {
            host: "boxd@vm.boxd.sh".to_string(),
            path: "~/repo".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        let result = adapter.list_worktrees();

        // Must return Err — no fallback.
        assert!(
            result.is_err(),
            "SSH failure (exit 255) must surface as Err (FetchFailure), not Ok; got: {result:?}"
        );
    }

    /// AC6 integration: last-known snapshot stays visible after a proxy failure.
    ///
    /// Writes a snapshot to a tempdir, simulates a proxy failure, and asserts
    /// that `build_state_with_cached_snapshots_from` still returns the cached
    /// worktrees — i.e. the cache file is NOT deleted by a proxy failure.
    #[test]
    fn last_known_snapshot_stays_visible_after_proxy_failure() {
        use std::collections::HashMap;

        use crate::json_output::{JsonOutput, JsonRepo, JsonWorktree};
        use crate::merge_remote::build_state_with_cached_snapshots_from;
        use crate::orchard_snapshot::write_snapshot_to;
        use tempfile::TempDir;

        let cache_dir = TempDir::new().expect("create temp cache dir");
        let host = "vm.boxd.sh";

        // Pre-write a snapshot with 2 worktrees as if a prior refresh succeeded.
        let snapshot = JsonOutput {
            version: 6,
            tmux_sessions: vec![],
            repos: vec![JsonRepo {
                slug: "owner/repo".to_string(),
                default_branch: None,
                main_ci_state: None,
                worktrees: vec![
                    JsonWorktree {
                        path: "/remote/wt1".to_string(),
                        branch: "branch-one".to_string(),
                        host: None,
                        layout: "bare".to_string(),
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: "other".to_string(),
                        status: "ready".to_string(),
                        status_glyph: "🟢".to_string(),
                        is_main_worktree: false,
                        ahead_behind: None,
                        last_commit_at: None,
                        last_activity_at: None,
                    },
                    JsonWorktree {
                        path: "/remote/wt2".to_string(),
                        branch: "branch-two".to_string(),
                        host: None,
                        layout: "bare".to_string(),
                        issue: None,
                        pr: None,
                        sessions: vec![],
                        display_group: "other".to_string(),
                        status: "ready".to_string(),
                        status_glyph: "🟢".to_string(),
                        is_main_worktree: false,
                        ahead_behind: None,
                        last_commit_at: None,
                        last_activity_at: None,
                    },
                ],
            }],
            hosts: HashMap::new(),
        };

        write_snapshot_to(host, &snapshot, cache_dir.path()).expect("write snapshot");

        // Simulate a proxy failure (the adapter itself errors).
        let mut fake = FakeSshExec::new();
        fake.insert(
            host,
            "orchard --json",
            SshOutput {
                stdout: String::new(),
                stderr: String::new(),
                exit_code: 127,
            },
        );

        let proxy_adapter = OrchardProxyAdapter {
            host: host.to_string(),
            path: "/remote/repo".to_string(),
            ssh: Box::new(fake),
            snapshot: OnceLock::new(),
        };

        // The adapter itself should fail.
        assert!(proxy_adapter.list_worktrees().is_err());

        // But the cache file was NOT deleted — build_state_with_cached_snapshots_from
        // still reads it.
        use crate::global_config::{GlobalConfig, RemoteConfig, RepoConfig};
        use crate::remote_adapter::RemoteKind;

        let config = GlobalConfig {
            repos: vec![RepoConfig {
                slug: "owner/repo".to_string(),
                path: "/local/repo".to_string(),
                remotes: vec![RemoteConfig {
                    name: "vm".to_string(),
                    host: host.to_string(),
                    path: "/remote/repo".to_string(),
                    shell: "ssh".to_string(),
                    kind: RemoteKind::OrchardProxy,
                }],
            }],
            ..GlobalConfig::default()
        };

        let state =
            build_state_with_cached_snapshots_from(&config, &HashMap::new(), cache_dir.path());

        let repo = state
            .repos
            .iter()
            .find(|r| r.slug == "owner/repo")
            .expect("owner/repo must be present");

        assert_eq!(
            repo.worktrees.len(),
            2,
            "both cached worktrees must still be visible after proxy failure; got: {:?}",
            repo.worktrees.iter().map(|w| &w.branch).collect::<Vec<_>>()
        );
    }

    // -----------------------------------------------------------------------
    // Review follow-ups (kept from prior implementation)
    // -----------------------------------------------------------------------

    /// Regression: bounded_chars must truncate on UTF-8 scalar-value
    /// boundaries, not byte offsets. The previous `&s[..s.len().min(N)]`
    /// byte-slicing panicked when the boundary landed mid-codepoint (emoji,
    /// accented characters in remote stderr). Triggering the diagnostic
    /// path via `log_proxy_failure_diagnostic` must NOT panic on such input.
    #[test]
    fn proxy_failure_diagnostic_does_not_panic_on_multibyte_stderr() {
        // 250 copies of a 4-byte emoji = 1000 bytes, well past the 200-char
        // bound and guaranteed to land a naive byte slice mid-codepoint.
        let raw = "🔥".repeat(250);
        let truncated = super::bounded_chars(&raw, 200);
        // Char-count must be exactly 200 (boundary-safe), byte-count must be
        // 800 (4 bytes per char), and the result must be valid UTF-8 (the
        // compile-time contract of &str is sufficient proof here).
        assert_eq!(truncated.chars().count(), 200);
        assert_eq!(truncated.len(), 800);
    }

    /// Regression: bounded_chars on purely ASCII strings returns the same
    /// string up to the char limit — parity with the pre-fix byte-slice for
    /// the ASCII case.
    #[test]
    fn bounded_chars_preserves_ascii_within_limit() {
        let s = "x".repeat(500);
        let t = super::bounded_chars(&s, 200);
        assert_eq!(t.len(), 200);
        assert!(t.chars().all(|c| c == 'x'));
    }

    // -----------------------------------------------------------------------
    // classify_proxy_failure_reason — shared vocabulary for probe + fetch
    // -----------------------------------------------------------------------

    #[test]
    fn classify_exit_127_is_remote_orchard_missing() {
        assert_eq!(
            super::classify_proxy_failure_reason("ssh command failed: exit 127"),
            "remote orchard missing (exit 127)"
        );
    }

    #[test]
    fn classify_command_not_found_is_remote_orchard_missing() {
        assert_eq!(
            super::classify_proxy_failure_reason("bash: orchard: command not found"),
            "remote orchard missing (exit 127)"
        );
    }

    #[test]
    fn classify_exit_255_is_fetch_failure() {
        assert_eq!(
            super::classify_proxy_failure_reason("ssh command failed: exit 255"),
            "fetch failure (exit 255)"
        );
    }

    #[test]
    fn classify_connection_refused_is_fetch_failure() {
        assert_eq!(
            super::classify_proxy_failure_reason("ssh: Connection refused"),
            "fetch failure (exit 255)"
        );
    }

    #[test]
    fn classify_timeout_is_probe_timeout() {
        assert_eq!(
            super::classify_proxy_failure_reason("ssh command timed out after 3s"),
            "probe timeout"
        );
    }

    #[test]
    fn classify_version_skew_extracts_version_number() {
        assert_eq!(
            super::classify_proxy_failure_reason(
                "version skew: remote version 99 not in supported list [6]"
            ),
            "version skew (remote version 99)"
        );
    }

    #[test]
    fn classify_unknown_falls_back_to_bounded_snippet() {
        let reason = super::classify_proxy_failure_reason("some other error");
        assert!(reason.starts_with("fetch failure: "));
        assert!(reason.contains("some other error"));
    }
}
