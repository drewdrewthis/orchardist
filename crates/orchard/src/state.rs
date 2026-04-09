//! Persistent task state for Orchard.
//!
//! Defines `AppState` and its contained `Task`, `TaskSource`, and `TaskStatus`
//! types, together with JSON serialisation, a versioned on-disk format, and
//! atomic read/write helpers. This is the single source of truth for task
//! lifecycle data between invocations.
use std::path::{Path, PathBuf};

use anyhow::Context;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/// Persistent application state serialized to `~/.local/state/git-orchard/state.json`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct AppState {
    /// Schema version; currently always `1`.
    pub version: u32,
    /// All tracked tasks.
    pub tasks: Vec<Task>,
}

impl Default for AppState {
    fn default() -> Self {
        AppState {
            version: 1,
            tasks: Vec::new(),
        }
    }
}

/// A unit of work tracked by Orchard, derived from a GitHub issue.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Task {
    /// Stable unique identifier for this task (e.g. `"acme/webapp#47"`).
    pub id: String,
    #[serde(default)]
    /// Human-readable title, synced from the issue title.
    pub title: String,
    /// Where this task originated (currently always a GitHub issue).
    pub source: TaskSource,
    /// Current workflow status of the task.
    pub status: TaskStatus,
    /// Relative priority; lower numbers are higher priority.
    pub priority: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// Absolute path of the worktree associated with this task, if any.
    pub worktree: Option<String>,
    #[serde(default)]
    /// Names of tmux sessions associated with this task.
    pub sessions: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// GitHub PR number linked to this task, if a PR has been opened.
    pub pr: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    /// Remote host for this task's worktree, if it is on a remote machine.
    pub remote_host: Option<String>,
    /// UTC timestamp when this task was first created.
    pub created_at: DateTime<Utc>,
    /// UTC timestamp of the most recent update to this task.
    pub updated_at: DateTime<Utc>,
}

/// The origin system that produced a task.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "type")]
pub enum TaskSource {
    #[serde(rename = "github_issue")]
    /// Task was created from a GitHub issue.
    GithubIssue {
        /// Repository slug in `owner/name` format.
        repo: String,
        /// GitHub issue number.
        number: u32,
    },
}

/// Workflow stage of a task, modelled as a simple kanban column.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TaskStatus {
    /// Task is queued but not yet ready to start.
    Backlog,
    /// Task is ready to be picked up.
    Ready,
    /// Work is actively in progress on this task.
    InProgress,
    /// A PR has been opened and is awaiting review.
    InReview,
    /// The task is complete and the PR has been merged (or work is discarded).
    Done,
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/// Returns the state directory path (~/.local/state/git-orchard/).
pub fn state_dir() -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join(".local/state/git-orchard")
}

/// Loads state from disk. Returns default state if the file doesn't exist or is invalid.
pub fn load_state() -> AppState {
    load_state_from(&state_dir())
}

/// Saves state to disk atomically (write tmp file, then rename).
pub fn save_state(state: &AppState) -> anyhow::Result<()> {
    save_state_to(state, &state_dir())
}

// ---------------------------------------------------------------------------
// Internal helpers (path-parameterised for testing)
// ---------------------------------------------------------------------------

fn load_state_from(dir: &Path) -> AppState {
    let path = dir.join("state.json");
    let Ok(contents) = std::fs::read_to_string(&path) else {
        return AppState::default();
    };
    serde_json::from_str(&contents).unwrap_or_default()
}

fn save_state_to(state: &AppState, dir: &Path) -> anyhow::Result<()> {
    std::fs::create_dir_all(dir).context("create state directory")?;

    let tmp_path = dir.join("state.json.tmp");
    let final_path = dir.join("state.json");

    let json = serde_json::to_string_pretty(state).context("serialize state")?;
    std::fs::write(&tmp_path, &json).context("write state.json.tmp")?;
    std::fs::rename(&tmp_path, &final_path).context("rename state.json.tmp to state.json")?;

    Ok(())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::TimeZone;
    use tempfile::tempdir;

    fn make_task() -> Task {
        Task {
            id: "git-orchard-rs#47".to_string(),
            title: "Task-centric state system".to_string(),
            source: TaskSource::GithubIssue {
                repo: "acme/my-project".to_string(),
                number: 47,
            },
            status: TaskStatus::InProgress,
            priority: 1,
            worktree: Some("/home/user/workspace/git-orchard-rs-47".to_string()),
            sessions: vec!["git-orchard-rs_47_main".to_string()],
            pr: Some(53),
            remote_host: None,
            created_at: Utc.with_ymd_and_hms(2026, 3, 18, 10, 0, 0).unwrap(),
            updated_at: Utc.with_ymd_and_hms(2026, 3, 20, 14, 32, 0).unwrap(),
        }
    }

    #[test]
    fn default_state_has_version_1_and_empty_tasks() {
        let state = AppState::default();
        assert_eq!(state.version, 1);
        assert!(state.tasks.is_empty());
    }

    #[test]
    fn task_status_serialization_roundtrip() {
        let statuses = [
            TaskStatus::Backlog,
            TaskStatus::Ready,
            TaskStatus::InProgress,
            TaskStatus::InReview,
            TaskStatus::Done,
        ];
        for status in statuses {
            let json = serde_json::to_string(&status).expect("serialize");
            let back: TaskStatus = serde_json::from_str(&json).expect("deserialize");
            assert_eq!(status, back);
        }
    }

    #[test]
    fn task_serialization_roundtrip() {
        let task = make_task();
        let json = serde_json::to_string(&task).expect("serialize");
        let back: Task = serde_json::from_str(&json).expect("deserialize");
        assert_eq!(task, back);
    }

    #[test]
    fn state_file_schema_matches_spec() {
        let state = AppState {
            version: 1,
            tasks: vec![make_task()],
        };
        let json = serde_json::to_string_pretty(&state).expect("serialize");

        // Field names must be snake_case (no camelCase)
        assert!(json.contains("\"version\""));
        assert!(json.contains("\"tasks\""));
        assert!(json.contains("\"created_at\""));
        assert!(json.contains("\"updated_at\""));
        assert!(json.contains("\"in_progress\""));
        assert!(json.contains("\"github_issue\""));
        // Ensure no camelCase leakage
        assert!(!json.contains("\"createdAt\""));
        assert!(!json.contains("\"updatedAt\""));
        assert!(!json.contains("\"inProgress\""));
    }

    #[test]
    fn save_and_load_roundtrip() {
        let dir = tempdir().expect("tempdir");
        let state = AppState {
            version: 1,
            tasks: vec![make_task(), {
                let mut t = make_task();
                t.id = "git-orchard-rs#48".to_string();
                t.status = TaskStatus::Ready;
                t.pr = None;
                t.worktree = None;
                t.sessions = Vec::new();
                t
            }],
        };

        save_state_to(&state, dir.path()).expect("save");
        let loaded = load_state_from(dir.path());
        assert_eq!(state, loaded);
    }

    #[test]
    fn load_returns_default_on_missing_file() {
        let dir = tempdir().expect("tempdir");
        let state = load_state_from(dir.path());
        assert_eq!(state, AppState::default());
    }

    #[test]
    fn load_returns_default_on_invalid_json() {
        let dir = tempdir().expect("tempdir");
        std::fs::write(dir.path().join("state.json"), b"not valid json {{{")
            .expect("write garbage");
        let state = load_state_from(dir.path());
        assert_eq!(state, AppState::default());
    }
}
