use std::collections::HashSet;
use std::fmt;

use crate::derive::TaskRow;
use crate::types::Worktree;

// ---------------------------------------------------------------------------
// Filter mode for the task list
// ---------------------------------------------------------------------------

/// Determines which rows are shown in the task list.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FilterMode {
    All,
    HasSession,
    HasClaude,
    HasPR,
}

impl FilterMode {
    /// Cycles to the next filter mode in order: All→HasSession→HasClaude→HasPR→All.
    pub fn next(self) -> Self {
        match self {
            Self::All => Self::HasSession,
            Self::HasSession => Self::HasClaude,
            Self::HasClaude => Self::HasPR,
            Self::HasPR => Self::All,
        }
    }
}

impl fmt::Display for FilterMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let label = match self {
            Self::All => "All",
            Self::HasSession => "Has Session",
            Self::HasClaude => "Has Claude",
            Self::HasPR => "Has PR",
        };
        write!(f, "{}", label)
    }
}

// ---------------------------------------------------------------------------
// View state (sum type carrying dialog state)
// ---------------------------------------------------------------------------

pub enum ViewState {
    List,
    ConfirmDelete(DeleteState),
    Transfer(TransferState),
    Cleanup(CleanupState),
    NewSession(NewSessionState),
    Help,
}

pub struct DeleteState {
    pub target: Worktree,
    pub phase: Phase,
    pub error: Option<String>,
}

pub struct TransferState {
    pub target: Worktree,
    pub phase: Phase,
    pub error: Option<String>,
}

pub struct CleanupState {
    pub stale: Vec<TaskRow>,
    pub selected: HashSet<String>,
    pub cursor: usize,
    pub phase: Phase,
    pub deleted: Vec<String>,
    pub errors: Vec<String>,
}

pub struct NewSessionState {
    pub name: String,
    pub cursor: usize,
}

// ---------------------------------------------------------------------------
// Phase enum
// ---------------------------------------------------------------------------

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Phase {
    Idle,
    Confirm,
    InProgress,
    Done,
    Error,
}

// ---------------------------------------------------------------------------
// Messages from background threads
// ---------------------------------------------------------------------------

pub enum AppMsg {
    PaneContent(String, String), // (session_name, content)
    CacheRefreshed,
    HostReachability(String, bool), // (host, is_reachable)
    DeleteDone,
    DeleteErr(String),
    TransferDone,
    TransferErr(String),
    CleanupDone {
        deleted: Vec<String>,  // paths successfully deleted
        errors: Vec<String>,   // error messages
    },
}
