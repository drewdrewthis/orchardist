//! TEA (The Elm Architecture) pattern Message enum for TUI event handling.
//!
//! Decouples raw keyboard events from state mutations by introducing a
//! semantic `Message` enum. The event loop becomes:
//!
//! 1. `handle_event(&self, key) -> Option<Message>` -- pure key-to-intent mapping
//! 2. `update(&mut self, msg) -> UpdateResult` -- all state mutation
//! 3. `render(&self, frame)` -- stateless view of current state
//!
//! This eliminates borrow-checker workarounds (`std::mem::replace` on `self.view`)
//! and makes keybindings independently testable from state transitions.

/// Semantic action produced by mapping a raw key event to user intent.
///
/// Each variant represents a single user action that the `update` function
/// knows how to process. Payloads are kept minimal -- the `update` function
/// reads whatever additional context it needs from `&mut self`.
#[derive(Debug, Clone, PartialEq)]
pub enum Message {
    // -- List actions --
    /// Exit the TUI.
    Quit,
    /// Move the cursor up one row.
    CursorUp,
    /// Move the cursor down one row.
    CursorDown,
    /// Jump the cursor to a specific index (from digit keys 1-9).
    CursorTo(usize),
    /// Activate / join the session for the selected row (also clears the active filter).
    Enter,
    /// Open the selected row's PR in the browser.
    OpenPR,
    /// Open the selected row's issue in the browser.
    OpenIssue,
    /// Toggle the branch column visibility.
    ToggleBranchColumn,
    /// Open the delete-worktree confirmation dialog.
    Delete,
    /// Toggle the priority flag for the selected worktree.
    TogglePriority,
    /// Open the new-session name-entry dialog.
    NewSession,
    /// Open the stale-worktree cleanup dialog.
    Cleanup,
    /// Switch to the previous repo filter.
    PrevRepo,
    /// Switch to the next repo filter.
    NextRepo,
    /// Expand the current row's pane sub-rows.
    ExpandRow,
    /// Collapse the current row's pane sub-rows.
    CollapseRow,
    /// Toggle expand/collapse on all multi-pane rows.
    ToggleExpandAll,
    /// Trigger a full background refresh.
    Refresh,
    /// Re-probe unreachable SSH hosts.
    ReconnectHosts,
    /// Toggle the keybinding help overlay.
    ToggleHelp,

    // -- Instant filter actions --
    /// Append a character to the instant filter (bare keystroke in Filtering phase).
    FilterChar(char),
    /// Remove the last character from the instant filter.
    FilterBackspace,
    /// Space pressed — enter the leader-key phase for action dispatch.
    LeaderKey,
    /// Unrecognized key in leader phase — cancel and return to filtering.
    LeaderCancel,

    // -- Dialog actions --
    /// Confirm a yes/no dialog (delete).
    ConfirmYes,
    /// Decline a yes/no dialog.
    ConfirmNo,
    /// Cancel and return to the list view.
    Cancel,
    /// Toggle selection of a row in the cleanup dialog.
    ToggleSelection,
    /// Confirm the cleanup with selected items.
    ConfirmCleanup,
    /// Append a character to the new-session name input.
    InputChar(char),
    /// Delete the last character from the new-session name input.
    DeleteChar,
    /// Confirm creation of the new session.
    ConfirmNewSession,
    /// Open the new-worktree branch-entry dialog.
    NewWorktree,
    /// Append a character to the new-worktree branch input.
    InputWorktreeChar(char),
    /// Delete the last character from the new-worktree branch input.
    DeleteWorktreeChar,
    /// Confirm creation of the new worktree.
    ConfirmNewWorktree,
    /// Dismiss a Done/Error phase dialog (any-key screens).
    DismissDialog,
    /// Activate a row by index (double-click: set cursor, then chain Enter).
    ActivateRow(usize),
    /// Open the attribution URL in the browser.
    OpenAttribution,

    // -- Preview scroll actions --
    /// Scroll the preview pane up by one page.
    PreviewPageUp,
    /// Scroll the preview pane down by one page.
    PreviewPageDown,
}

/// Result of processing a [`Message`] through the `update` function.
///
/// The `quit` flag tells the event loop to exit. The optional `next_msg`
/// allows chaining: when an update produces a follow-up action, the loop
/// processes it immediately without waiting for another key event.
pub struct UpdateResult {
    /// Whether the TUI should exit after this update.
    pub quit: bool,
    /// Optional follow-up message to process immediately.
    pub next_msg: Option<Message>,
}
