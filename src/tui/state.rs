//! TUI application state types.
//!
//! Defines `ViewState` (which screen is active), `Phase`,
//! `AppMsg` (messages from the background worker), and the various dialog
//! state structs (`DeleteState`, `CleanupState`, etc.). Consumed by the
//! main TUI event loop, `list`, and `dialogs` modules.
use std::collections::HashSet;

use crate::derive::WorktreeRow;
use crate::heal::{FixResult, HealFinding, HealReport};
use crate::types::Worktree;

// ---------------------------------------------------------------------------
// View state (sum type carrying dialog state)
// ---------------------------------------------------------------------------

/// The active screen or dialog the TUI is currently rendering.
pub enum ViewState {
    /// The main worktree task list.
    List,
    /// A confirmation dialog for deleting a worktree.
    ConfirmDelete(DeleteState),
    /// A dialog for transferring a worktree to a remote host.
    Transfer(TransferState),
    /// A multi-select dialog for cleaning up stale worktrees.
    Cleanup(CleanupState),
    /// A text-entry dialog for creating a new tmux session.
    NewSession(NewSessionState),
    /// A text-entry dialog for creating a new git worktree.
    NewWorktree(NewWorktreeState),
    /// The keybinding help overlay.
    Help,
    /// The heal results view.
    Heal(HealState),
}

/// State carried while the delete-worktree confirmation dialog is open.
pub struct DeleteState {
    /// The worktree that the user has chosen to delete.
    pub target: Worktree,
    /// Current progress phase of the delete operation.
    pub phase: Phase,
    /// Error message to display if the operation failed.
    pub error: Option<String>,
}

/// State carried while the remote-transfer dialog is open.
pub struct TransferState {
    /// The worktree being transferred to a remote host.
    pub target: Worktree,
    /// Current progress phase of the transfer operation.
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

/// State carried while the heal results view is displayed.
pub struct HealState {
    /// The full report from the last heal diagnosis.
    ///
    /// Retained for potential export or re-display logic; the `findings` field
    /// contains a clone of `report.findings` for cursor-indexed rendering.
    #[allow(dead_code)]
    pub report: HealReport,
    /// All findings as a flat list for cursor navigation.
    pub findings: Vec<HealFinding>,
    /// Index of the currently highlighted finding.
    pub cursor: usize,
    /// Whether a fix pass is currently running.
    pub fixing: bool,
    /// Results from the last `--fix` pass, if one has run.
    pub fix_results: Option<Vec<FixResult>>,
}

// ---------------------------------------------------------------------------
// Phase enum
// ---------------------------------------------------------------------------

/// Progress phase for async dialog operations (delete, transfer, cleanup).
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
// Messages from background threads
// ---------------------------------------------------------------------------

/// Messages sent from background threads to the main TUI event loop.
pub enum AppMsg {
    /// Pane output captured for display; carries `(session_name, content)`.
    PaneContent(String, String),
    /// The cache refresh cycle completed; the TUI should re-derive task rows.
    CacheRefreshed,
    /// SSH reachability result for a host; carries `(host, is_reachable)`.
    HostReachability(String, bool),
    /// The delete operation finished successfully.
    DeleteDone,
    /// The delete operation failed with the given error message.
    DeleteErr(String),
    /// The transfer operation finished successfully.
    TransferDone,
    /// The transfer operation failed with the given error message.
    TransferErr(String),
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
    /// The heal diagnosis finished; carries the computed report.
    HealDone(HealReport),
    /// The heal fix pass finished; carries the per-action results.
    HealFixDone(Vec<FixResult>),
}
