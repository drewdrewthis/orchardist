//! TUI application state types.
//!
//! Defines `ViewState` (which screen is active), `Phase`,
//! `AppMsg` (messages from the background worker), and the various dialog
//! state structs (`DeleteState`, `CleanupState`, etc.). Consumed by the
//! main TUI event loop, `list`, and `dialogs` modules.
use std::collections::HashSet;

use crate::derive::WorktreeRow;

// ---------------------------------------------------------------------------
// View state (sum type carrying dialog state)
// ---------------------------------------------------------------------------

/// The active screen or dialog the TUI is currently rendering.
pub enum ViewState {
    /// The main worktree task list.
    List,
    /// A confirmation dialog for deleting a worktree.
    ConfirmDelete(Box<DeleteState>),
    /// A multi-select dialog for cleaning up stale worktrees.
    Cleanup(CleanupState),
    /// A text-entry dialog for creating a new tmux session.
    NewSession(NewSessionState),
    /// A text-entry dialog for creating a new git worktree.
    NewWorktree(NewWorktreeState),
    /// The keybinding help overlay.
    Help,
}

/// State carried while the delete-worktree confirmation dialog is open.
pub struct DeleteState {
    /// The worktree row that the user has chosen to delete.
    pub target: WorktreeRow,
    /// Current progress phase of the delete operation.
    pub phase: Phase,
    /// Error message to display if the operation failed.
    pub error: Option<String>,
}

/// State carried while the stale-worktree cleanup dialog is open.
pub struct CleanupState {
    /// The list of stale task rows eligible for deletion.
    pub stale: Vec<WorktreeRow>,
    /// Set of worktree paths the user has toggled for deletion.
    pub selected: HashSet<String>,
    /// Index of the currently highlighted row in the cleanup list.
    pub cursor: usize,
    /// Current progress phase of the cleanup operation.
    pub phase: Phase,
    /// Paths successfully deleted during this cleanup pass.
    pub deleted: Vec<String>,
    /// Error messages collected during the cleanup pass.
    pub errors: Vec<String>,
}

/// State carried while the new-session name-entry dialog is open.
pub struct NewSessionState {
    /// The session name being typed by the user.
    pub name: String,
    /// Byte offset of the cursor within `name`.
    pub cursor: usize,
}

/// State carried while the new-worktree branch-entry dialog is open.
pub struct NewWorktreeState {
    /// The branch name being typed by the user.
    pub branch: String,
}

// ---------------------------------------------------------------------------
// Input phase
// ---------------------------------------------------------------------------

/// Input phase for the main list view.
///
/// `Idle` is the default: bare keys dispatch actions directly (no prefix required).
/// `Searching` means the search bar is open; all printable keystrokes feed the
/// fuzzy filter. Only `Message::CloseSearch` and `Message::Quit` transition from
/// `Searching` back to `Idle`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum InputPhase {
    /// Bare keys dispatch actions directly. This is the default state.
    #[default]
    Idle,
    /// Search bar is open; printable keystrokes feed the filter.
    Searching,
}

// ---------------------------------------------------------------------------
// Phase enum
// ---------------------------------------------------------------------------

/// Progress phase for async dialog operations (delete, cleanup).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Phase {
    /// Waiting for user confirmation before starting.
    Idle,
    /// User has confirmed; ready to begin execution.
    Confirm,
    /// The background operation is currently running.
    InProgress,
    /// The operation completed successfully.
    Done,
    /// The operation failed; an error message is available.
    Error,
}

// ---------------------------------------------------------------------------
// Daemon status
// ---------------------------------------------------------------------------

/// Reflects the orchard daemon's reachability as seen by the most recent
/// refresh tick.
///
/// The TUI renders an explicit status indicator when the daemon is
/// [`DaemonStatus::Unreachable`], so users know they are seeing stale data
/// rather than a blank screen.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DaemonStatus {
    /// Last refresh succeeded — daemon is healthy.
    Reachable,
    /// Daemon has not been polled yet this session (cold start).
    Unknown,
    /// Last refresh failed to reach the daemon.
    Unreachable,
}

// ---------------------------------------------------------------------------
// Messages from background threads
// ---------------------------------------------------------------------------

/// Messages sent from background threads to the main TUI event loop.
pub enum AppMsg {
    /// Pane output captured for display; carries `(session_name, content)`.
    PaneContent(String, String),
    /// The cache refresh cycle completed; the TUI should re-derive task rows.
    CacheRefreshed,
    /// A local-only cache refresh completed (worktrees + tmux sessions).
    LocalCacheRefreshed,
    /// SSH reachability result for a host; carries `(host, is_reachable)`.
    HostReachability(String, bool),
    /// Daemon reachability changed; carries the new status.
    DaemonStatusChanged(DaemonStatus),
    /// The delete operation finished successfully.
    DeleteDone,
    /// The delete operation failed with the given error message.
    DeleteErr(String),
    /// The cleanup batch operation finished; reports per-path outcomes.
    CleanupDone {
        /// Paths that were successfully deleted.
        deleted: Vec<String>,
        /// Error messages for paths that could not be deleted.
        errors: Vec<String>,
    },
    /// The create-worktree operation finished successfully; carries the new session name.
    CreateWorktreeDone {
        /// Name of the tmux session created for the new worktree.
        session_name: String,
    },
    /// The create-worktree operation failed with the given error message.
    CreateWorktreeErr(String),
    /// The create-worktree operation succeeded but with a non-fatal warning
    /// (e.g., setup script failed). Carries the session name and warning text.
    CreateWorktreeWarn {
        /// Name of the tmux session created for the new worktree.
        session_name: String,
        /// Warning message to display (e.g., setup script error details).
        warning: String,
    },
}
