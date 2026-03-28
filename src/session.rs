//! Domain types for tmux sessions and Claude enrichment.
//!
//! Separates pure tmux session data (`TmuxSessionInfo`) from Claude-specific
//! enrichment (`ClaudeSessionInfo`), composed into `EnrichedSession`. Also
//! defines `StandaloneConfig` / `StandaloneSessionRow` for non-worktree sessions
//! and the `ListEntry` enum that the TUI will render (worktree or standalone).

use serde::{Deserialize, Serialize};

use crate::claude_state::ClaudeState;
use crate::derive::WorktreeRow;

// ---------------------------------------------------------------------------
// Host
// ---------------------------------------------------------------------------

/// Where a tmux session runs: locally or on a remote SSH target.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Host {
    /// The session runs on the local machine.
    Local,
    /// The session runs on a remote machine via SSH.
    Remote(String),
}

// ---------------------------------------------------------------------------
// SessionStatus
// ---------------------------------------------------------------------------

/// Whether a tmux session is alive and, if so, whether it is attached.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SessionStatus {
    /// The session is running. `attached` is true when a client is connected.
    Running {
        /// True when a terminal client is attached to this session.
        attached: bool,
    },
    /// The session is no longer running.
    Dead,
}

// ---------------------------------------------------------------------------
// TmuxSessionInfo
// ---------------------------------------------------------------------------

/// Pure tmux session data with no Claude enrichment.
///
/// Represents the raw state of a tmux session as discovered from the cache.
/// The `host` field indicates whether this is a local or remote session.
#[derive(Debug, Clone)]
pub struct TmuxSessionInfo {
    /// Where this session runs (local or remote).
    pub host: Host,
    /// The tmux session name.
    pub name: String,
    /// Whether the session is running or dead.
    pub status: SessionStatus,
}

// ---------------------------------------------------------------------------
// ClaudeSessionInfo
// ---------------------------------------------------------------------------

/// Claude-specific enrichment data read from hook state files.
///
/// Grouped separately from tmux data so that sessions without Claude
/// activity carry no unnecessary fields.
#[derive(Debug, Clone)]
pub struct ClaudeSessionInfo {
    /// Structured Claude state (working, idle, input, none).
    pub status: ClaudeState,
    /// Cumulative session cost in USD, if available.
    pub cost_usd: Option<f64>,
    /// Context window usage percentage (0-100), if available.
    pub context_window_pct: Option<f64>,
    /// Model name (e.g., "opus", "sonnet"), if available.
    pub model: Option<String>,
}

// ---------------------------------------------------------------------------
// EnrichedSession
// ---------------------------------------------------------------------------

/// A tmux session enriched with optional Claude data.
///
/// This is the primary session type consumed by the TUI and JSON output.
/// The `claude` field is `None` when no Claude process is detected.
#[derive(Debug, Clone)]
pub struct EnrichedSession {
    /// Pure tmux session data.
    pub tmux: TmuxSessionInfo,
    /// Claude-specific enrichment, if a Claude process is active.
    pub claude: Option<ClaudeSessionInfo>,
}

// ---------------------------------------------------------------------------
// Standalone session types (for Part 2)
// ---------------------------------------------------------------------------

/// Configuration for a standalone tmux session not tied to any worktree.
///
/// Defined now for forward compatibility; constructed in Part 2.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StandaloneConfig {
    /// Display name and tmux session name.
    pub name: String,
    /// Shell command to run in the session.
    pub command: String,
    /// Working directory for the session.
    pub cwd: String,
    /// Whether to auto-create this session when orchard starts.
    #[serde(default)]
    pub start_on_launch: bool,
}

/// A standalone session row for the TUI, pairing runtime state with config.
///
/// Defined now for forward compatibility; constructed in Part 2.
#[derive(Debug, Clone)]
pub struct StandaloneSessionRow {
    /// The enriched session (tmux + optional Claude data).
    pub session: EnrichedSession,
    /// The standalone session configuration.
    pub config: StandaloneConfig,
}

// ---------------------------------------------------------------------------
// ListEntry
// ---------------------------------------------------------------------------

/// What appears in the TUI list: either a worktree row or a standalone session.
///
/// The `Standalone` variant is defined for forward compatibility (Part 2)
/// but is not constructed in Part 1.
#[derive(Debug, Clone)]
pub enum ListEntry {
    /// A worktree row from the derive pipeline.
    Worktree(WorktreeRow),
    /// A standalone session not tied to any worktree.
    Standalone(StandaloneSessionRow),
}
