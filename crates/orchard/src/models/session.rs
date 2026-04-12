//! Canonical session type, merging tmux session + Claude enrichment into one struct.
use serde::{Deserialize, Serialize};

use crate::models::claude::Claude;

/// A single pane within a tmux window.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Pane {
    /// Zero-based pane index within its window.
    pub index: usize,
    /// Tmux target address (e.g. `session:window.pane`).
    pub tmux_target: String,
    /// Command running in this pane.
    pub command: String,
    /// Terminal title of this pane.
    pub title: String,
    /// Whether a Claude process is running in this pane.
    pub has_claude: bool,
}

/// A tmux window containing one or more panes.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Window {
    /// Zero-based window index within the session.
    pub index: usize,
    /// Display name of this window.
    pub name: String,
    /// Whether this is the currently active window in the session.
    pub is_active: bool,
    /// Panes in this window.
    #[serde(default)]
    pub panes: Vec<Pane>,
}

/// Canonical tmux session with optional Claude enrichment.
///
/// Merges `TmuxSessionInfo` + `ClaudeSessionInfo` + `SessionState` into one type,
/// eliminating the three-struct composition chain.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Session {
    /// Tmux session name.
    pub name: String,
    /// Remote host identifier; `None` means the session is local.
    pub host: Option<String>,
    /// Session liveness: `running` or `dead`.
    pub status: String,
    /// ISO 8601 timestamp when the session was created.
    pub started_at: Option<String>,
    /// ISO 8601 timestamp of the last activity in this session.
    pub last_activity_at: Option<String>,
    /// Claude enrichment, if a Claude process is active in this session.
    pub claude: Option<Claude>,
    /// Windows in this session.
    #[serde(default)]
    pub windows: Vec<Window>,
}
