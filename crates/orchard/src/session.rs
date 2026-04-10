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
    /// The session is running. `attached` is true when a terminal client is connected.
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
// WindowInfo
// ---------------------------------------------------------------------------

/// Per-window metadata for a tmux session, containing the panes in that window.
///
/// Windows group panes in tmux. The `index` field uses tmux's stable window
/// index (not sequential 0..N), so closing a window doesn't shift indices
/// for remaining windows and expansion state keys remain stable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowInfo {
    /// Tmux's stable window index (not sequential — survives window closes).
    pub index: usize,
    /// Window name from tmux (e.g., "main", "editor").
    pub name: String,
    /// Whether this is the active window in the session.
    pub is_active: bool,
    /// Panes belonging to this window.
    pub panes: Vec<PaneInfo>,
}

/// Parses the window index from a tmux pane target like "1.2" → 1.
///
/// Returns 0 if the target is malformed.
fn parse_window_index(target: &str) -> usize {
    target
        .split_once('.')
        .and_then(|(w, _)| w.parse().ok())
        .unwrap_or(0)
}

/// Builds a `Vec<WindowInfo>` by grouping panes by window index.
///
/// - `pane_targets`: tmux window.pane addresses, e.g. `["0.0", "0.1", "1.0"]`
/// - `pane_commands`: command running in each pane (parallel to targets)
/// - `pane_titles`: pane title for each pane (parallel to targets)
/// - `window_names`: window name per pane row (parallel to targets). When empty,
///   synthetic names like `"window:0"` are derived from the window index.
/// - `window_active`: "1" means active window, anything else means inactive
///   (parallel to targets). When empty, all windows are marked inactive.
///
/// Windows are returned sorted by their tmux window index. Within each window,
/// panes appear in the order they were encountered in `pane_targets`.
pub fn build_windows(
    pane_targets: &[String],
    pane_commands: &[String],
    pane_titles: &[String],
    window_names: &[String],
    window_active: &[String],
) -> Vec<WindowInfo> {
    if pane_targets.is_empty() {
        return Vec::new();
    }

    // Use an ordered vec of (window_index, WindowInfo) to preserve insertion order.
    // We'll sort by index at the end.
    let mut window_order: Vec<usize> = Vec::new();
    let mut window_map: std::collections::HashMap<usize, WindowInfo> =
        std::collections::HashMap::new();

    let empty = String::new();

    for (flat_idx, target) in pane_targets.iter().enumerate() {
        let win_idx = parse_window_index(target);
        let cmd = pane_commands.get(flat_idx).unwrap_or(&empty);
        let title = pane_titles.get(flat_idx).unwrap_or(&empty);

        let pane = PaneInfo::new(flat_idx, target, cmd, title);

        if let Some(window) = window_map.get_mut(&win_idx) {
            window.panes.push(pane);
        } else {
            // Derive window name: use provided name or synthesize.
            let name = if window_names.is_empty() {
                format!("window:{win_idx}")
            } else {
                window_names
                    .get(flat_idx)
                    .cloned()
                    .unwrap_or_else(|| format!("window:{win_idx}"))
            };

            // Active flag: "1" means active.
            let is_active = window_active
                .get(flat_idx)
                .map(|s| s == "1")
                .unwrap_or(false);

            window_order.push(win_idx);
            window_map.insert(
                win_idx,
                WindowInfo {
                    index: win_idx,
                    name,
                    is_active,
                    panes: vec![pane],
                },
            );
        }
    }

    // Return windows sorted by tmux window index.
    window_order.sort_unstable();
    window_order.dedup();
    window_order
        .into_iter()
        .filter_map(|idx| window_map.remove(&idx))
        .collect()
}

/// Builds both the structured window hierarchy and the denormalized flat pane list.
///
/// The flat `panes` vec is kept on `EnrichedSession` for backward compatibility —
/// it's all panes in window order, concatenated. The 28+ call sites that use
/// `session.panes` continue working without changes.
///
/// # Arguments
/// - `pane_targets`: tmux window.pane addresses (e.g. "0.0", "1.2")
/// - `pane_commands`: command running in each pane
/// - `pane_titles`: title for each pane
/// - `window_names`: window name per pane row; empty → synthetic names
/// - `window_active`: "1" or "0" per pane row; empty → all inactive
pub fn build_windows_and_panes(
    pane_targets: &[String],
    pane_commands: &[String],
    pane_titles: &[String],
    window_names: &[String],
    window_active: &[String],
) -> (Vec<WindowInfo>, Vec<PaneInfo>) {
    let windows = build_windows(
        pane_targets,
        pane_commands,
        pane_titles,
        window_names,
        window_active,
    );
    let panes = windows.iter().flat_map(|w| w.panes.clone()).collect();
    (windows, panes)
}

// ---------------------------------------------------------------------------
// EnrichedSession
// ---------------------------------------------------------------------------

/// A tmux session enriched with optional Claude data and per-pane info.
///
/// This is the primary session type consumed by the TUI and JSON output.
/// The `claude` field is `None` when no Claude process is detected.
/// The `windows` field contains the structured session → window → pane hierarchy.
/// The `panes` field is a denormalized flat list derived from `windows`, kept
/// for backward compatibility with the 28+ call sites that access it directly.
#[derive(Debug, Clone)]
pub struct EnrichedSession {
    /// Pure tmux session data.
    pub tmux: TmuxSessionInfo,
    /// Claude-specific enrichment, if a Claude process is active.
    pub claude: Option<ClaudeSessionInfo>,
    /// Structured window hierarchy for this session.
    pub windows: Vec<WindowInfo>,
    /// Per-pane metadata for all panes in this session (denormalized flat list).
    ///
    /// Derived from `windows` — all panes in window order concatenated.
    /// Preserved for backward compatibility; use `windows` for hierarchy-aware code.
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

    // -- WindowInfo / build_windows tests -----------------------------------

    fn svec(items: &[&str]) -> Vec<String> {
        items.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn build_windows_groups_panes_by_window_index() {
        let targets = svec(&["0.0", "0.1", "1.0", "1.1"]);
        let cmds = svec(&["bash", "vim", "nvim", "cargo"]);
        let titles = svec(&["bash", "vim", "nvim", "cargo"]);
        let names = svec(&["main", "main", "editor", "editor"]);
        let active = svec(&["1", "1", "0", "0"]);

        let windows = build_windows(&targets, &cmds, &titles, &names, &active);

        assert_eq!(windows.len(), 2);
        assert_eq!(windows[0].index, 0);
        assert_eq!(windows[0].name, "main");
        assert!(windows[0].is_active);
        assert_eq!(windows[0].panes.len(), 2);
        assert_eq!(windows[1].index, 1);
        assert_eq!(windows[1].name, "editor");
        assert!(!windows[1].is_active);
        assert_eq!(windows[1].panes.len(), 2);
    }

    #[test]
    fn build_windows_contains_correct_pane_references() {
        let targets = svec(&["0.0", "0.1", "1.0"]);
        let cmds = svec(&["bash", "vim", "nvim"]);
        let titles = svec(&["bash", "vim", "nvim"]);
        let names = svec(&["shell", "shell", "code"]);
        let active = svec(&["1", "1", "0"]);

        let windows = build_windows(&targets, &cmds, &titles, &names, &active);

        assert_eq!(windows[0].panes[0].tmux_target, "0.0");
        assert_eq!(windows[0].panes[1].tmux_target, "0.1");
        assert_eq!(windows[1].panes[0].tmux_target, "1.0");
    }

    #[test]
    fn build_windows_single_window_produces_one_entry() {
        let targets = svec(&["0.0", "0.1"]);
        let cmds = svec(&["bash", "vim"]);
        let titles = svec(&["bash", "vim"]);
        let names = svec(&["main", "main"]);
        let active = svec(&["1", "1"]);

        let windows = build_windows(&targets, &cmds, &titles, &names, &active);

        assert_eq!(windows.len(), 1);
        assert_eq!(windows[0].panes.len(), 2);
    }

    #[test]
    fn build_windows_empty_input_returns_empty() {
        let windows = build_windows(&[], &[], &[], &[], &[]);
        assert!(windows.is_empty());
    }

    #[test]
    fn build_windows_missing_window_names_synthesizes_fallback() {
        let targets = svec(&["0.0", "0.1", "1.0"]);
        let cmds = svec(&["bash", "vim", "nvim"]);
        let titles = svec(&["bash", "vim", "nvim"]);

        // Pass empty window_names and window_active
        let windows = build_windows(&targets, &cmds, &titles, &[], &[]);

        assert_eq!(windows.len(), 2);
        assert_eq!(windows[0].name, "window:0");
        assert_eq!(windows[1].name, "window:1");
        assert!(!windows[0].is_active);
        assert!(!windows[1].is_active);
    }

    #[test]
    fn build_windows_discontinuous_indices() {
        let targets = svec(&["0.0", "2.0", "5.0"]);
        let cmds = svec(&["bash", "vim", "nvim"]);
        let titles = svec(&["bash", "vim", "nvim"]);
        let names = svec(&["main", "editor", "logs"]);
        let active = svec(&["0", "1", "0"]);

        let windows = build_windows(&targets, &cmds, &titles, &names, &active);

        assert_eq!(windows.len(), 3);
        assert_eq!(windows[0].index, 0);
        assert_eq!(windows[1].index, 2);
        assert_eq!(windows[2].index, 5);
    }

    #[test]
    fn build_windows_and_panes_denormalized_matches_flattened() {
        let targets = svec(&["0.0", "0.1", "1.0"]);
        let cmds = svec(&["bash", "vim", "nvim"]);
        let titles = svec(&["bash", "vim", "nvim"]);
        let names = svec(&["main", "main", "editor"]);
        let active = svec(&["1", "1", "0"]);

        let (windows, panes) = build_windows_and_panes(&targets, &cmds, &titles, &names, &active);

        let expected: Vec<PaneInfo> = windows.iter().flat_map(|w| w.panes.clone()).collect();

        assert_eq!(panes, expected);
        assert_eq!(panes.len(), 3);
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
