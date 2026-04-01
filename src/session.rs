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
// PaneInfo
// ---------------------------------------------------------------------------

/// Per-pane metadata extracted from a tmux session's pane list.
///
/// Each pane in a session gets a `PaneInfo` entry. The `has_claude` flag
/// enables pane-level Claude detection (case-insensitive check against
/// both the pane command and title).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PaneInfo {
    /// Zero-based sequential index in the flat pane list.
    pub index: usize,
    /// Tmux window.pane target address (e.g., "0.1" for window 0, pane 1).
    ///
    /// Used with `tmux select-pane -t session:{target}` for correct
    /// pane selection across multiple windows.
    pub tmux_target: String,
    /// Command running in this pane (e.g., "claude", "nvim", "cargo watch -x test").
    pub command: String,
    /// Tmux pane title (often more descriptive than command).
    pub title: String,
    /// True when the pane is running a Claude process (detected from command or title).
    pub has_claude: bool,
}

impl PaneInfo {
    /// Constructs a `PaneInfo`, detecting Claude from command and title strings.
    ///
    /// Detection is case-insensitive: any occurrence of "claude" in either
    /// the command or title marks `has_claude` as true.
    pub fn new(index: usize, tmux_target: &str, command: &str, title: &str) -> Self {
        let has_claude =
            command.to_lowercase().contains("claude") || title.to_lowercase().contains("claude");
        PaneInfo {
            index,
            tmux_target: tmux_target.to_string(),
            command: command.to_string(),
            title: title.to_string(),
            has_claude,
        }
    }
}

/// Builds a `Vec<PaneInfo>` from parallel slices of pane targets, commands, and titles.
///
/// When input slices have different lengths, the shorter ones are padded with
/// empty strings or fallback indices. This handles edge cases where tmux
/// reports an unequal number of entries across fields.
pub fn build_pane_infos(
    pane_targets: &[String],
    pane_commands: &[String],
    pane_titles: &[String],
) -> Vec<PaneInfo> {
    let len = pane_targets
        .len()
        .max(pane_commands.len())
        .max(pane_titles.len());
    let empty = String::new();
    (0..len)
        .map(|i| {
            let target = pane_targets
                .get(i)
                .map(|s| s.as_str())
                .unwrap_or_else(|| "");
            // Fall back to sequential index if no target available.
            let effective_target = if target.is_empty() {
                format!("0.{i}")
            } else {
                target.to_string()
            };
            let cmd = pane_commands.get(i).unwrap_or(&empty);
            let title = pane_titles.get(i).unwrap_or(&empty);
            PaneInfo::new(i, &effective_target, cmd, title)
        })
        .collect()
}

// ---------------------------------------------------------------------------
// EnrichedSession
// ---------------------------------------------------------------------------

/// A tmux session enriched with optional Claude data and per-pane info.
///
/// This is the primary session type consumed by the TUI and JSON output.
/// The `claude` field is `None` when no Claude process is detected.
/// The `panes` field contains per-pane metadata for sub-row rendering.
#[derive(Debug, Clone)]
pub struct EnrichedSession {
    /// Pure tmux session data.
    pub tmux: TmuxSessionInfo,
    /// Claude-specific enrichment, if a Claude process is active.
    pub claude: Option<ClaudeSessionInfo>,
    /// Per-pane metadata for all panes in this session.
    pub panes: Vec<PaneInfo>,
}

impl ClaudeSessionInfo {
    /// Constructs `ClaudeSessionInfo` from a hook state file, returning `None`
    /// when the parsed state is `ClaudeState::None` (no active Claude process).
    pub fn from_state_file(sf: &crate::claude_state::ClaudeStateFile) -> Option<Self> {
        let state: ClaudeState = sf.state.parse().unwrap_or(ClaudeState::None);
        if state == ClaudeState::None {
            return None;
        }
        Some(ClaudeSessionInfo {
            status: state,
            cost_usd: sf.cost_usd,
            context_window_pct: sf.context_window_pct,
            model: sf.model.clone(),
        })
    }
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pane_info_detects_claude_in_command() {
        let pane = PaneInfo::new(0, "0.0", "claude --model opus", "bash");
        assert!(pane.has_claude);
        assert_eq!(pane.command, "claude --model opus");
        assert_eq!(pane.index, 0);
        assert_eq!(pane.tmux_target, "0.0");
    }

    #[test]
    fn pane_info_detects_claude_case_insensitive() {
        let pane = PaneInfo::new(0, "0.0", "Claude", "");
        assert!(pane.has_claude);
    }

    #[test]
    fn pane_info_detects_claude_in_title() {
        let pane = PaneInfo::new(0, "0.0", "bash", "claude-session");
        assert!(pane.has_claude);
    }

    #[test]
    fn pane_info_non_claude_command() {
        let pane = PaneInfo::new(1, "0.1", "nvim src/main.rs", "nvim");
        assert!(!pane.has_claude);
    }

    #[test]
    fn build_pane_infos_from_commands_and_titles() {
        let targets = vec!["0.0".to_string(), "0.1".to_string(), "0.2".to_string()];
        let cmds = vec![
            "claude".to_string(),
            "nvim".to_string(),
            "cargo watch -x test".to_string(),
        ];
        let titles = vec![
            "claude".to_string(),
            "nvim".to_string(),
            "cargo".to_string(),
        ];
        let panes = build_pane_infos(&targets, &cmds, &titles);
        assert_eq!(panes.len(), 3);
        assert_eq!(panes[0].index, 0);
        assert_eq!(panes[0].tmux_target, "0.0");
        assert!(panes[0].has_claude);
        assert_eq!(panes[0].command, "claude");
        assert_eq!(panes[1].index, 1);
        assert_eq!(panes[1].tmux_target, "0.1");
        assert!(!panes[1].has_claude);
        assert_eq!(panes[2].index, 2);
        assert_eq!(panes[2].tmux_target, "0.2");
        assert!(!panes[2].has_claude);
        assert_eq!(panes[2].command, "cargo watch -x test");
    }

    #[test]
    fn build_pane_infos_empty_inputs() {
        let panes = build_pane_infos(&[], &[], &[]);
        assert!(panes.is_empty());
    }

    #[test]
    fn build_pane_infos_unequal_lengths() {
        let targets = vec!["0.0".to_string(), "0.1".to_string()];
        let cmds = vec!["claude".to_string(), "nvim".to_string()];
        let titles = vec!["bash".to_string()];
        let panes = build_pane_infos(&targets, &cmds, &titles);
        assert_eq!(panes.len(), 2);
        assert!(panes[0].has_claude);
        // Second pane has no title (empty string padded)
        assert!(!panes[1].has_claude);
    }

    #[test]
    fn build_pane_infos_missing_targets_uses_fallback() {
        let cmds = vec!["claude".to_string(), "nvim".to_string()];
        let titles = vec!["bash".to_string(), "nvim".to_string()];
        let panes = build_pane_infos(&[], &cmds, &titles);
        assert_eq!(panes.len(), 2);
        assert_eq!(panes[0].tmux_target, "0.0");
        assert_eq!(panes[1].tmux_target, "0.1");
    }
}

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
