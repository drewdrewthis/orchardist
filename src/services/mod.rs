pub mod git;
pub mod github;
pub mod notify;
pub mod shell;
pub mod ssh;
pub mod tmux;

#[cfg(test)]
pub mod fake;

use std::collections::HashMap;

use crate::types::{IssueState, PrInfo, SwitchToSessionOptions, TmuxSession, Worktree};

// ---------------------------------------------------------------------------
// Service traits
// ---------------------------------------------------------------------------

pub trait GitService: Send + Sync {
    fn find_repo_root(&self) -> anyhow::Result<String>;
    fn get_repo_name(&self) -> anyhow::Result<String>;
    fn list_worktrees(&self) -> anyhow::Result<Vec<Worktree>>;
    fn worktree_has_conflicts(&self, path: &str) -> bool;
    fn remove_worktree(&self, path: &str, force: bool) -> anyhow::Result<()>;
}

pub trait GithubService: Send + Sync {
    fn get_repo(&self) -> anyhow::Result<(String, String)>;
    fn is_gh_available(&self) -> bool;
    fn get_all_prs(&self, branches: &[String]) -> HashMap<String, PrInfo>;
    fn enrich_pr_details(&self, pr_map: &mut HashMap<String, PrInfo>);
    fn get_issue_states(&self, numbers: &[u32]) -> HashMap<u32, IssueState>;
}

/// Tmux session management operations.
///
/// This trait covers session lifecycle, pane inspection, and styling.
/// A future refactor may split this into focused sub-traits (SessionManager,
/// PaneInspector, StyleApplier) once consumers can depend on narrower interfaces.
pub trait TmuxService: Send + Sync {
    fn list_sessions(&self) -> Vec<TmuxSession>;
    fn new_detached_session(&self, name: &str, start_dir: &str) -> anyhow::Result<()>;
    fn kill_session(&self, name: &str) -> anyhow::Result<()>;
    fn capture_pane_content(&self, session: &str, lines: u32) -> anyhow::Result<String>;
    fn create_session(&self, opts: &SwitchToSessionOptions) -> anyhow::Result<()>;
    fn apply_session_style(
        &self,
        name: &str,
        branch: Option<&str>,
        pr: Option<&PrInfo>,
    ) -> anyhow::Result<()>;
    fn has_session(&self, name: &str) -> bool;
    fn session_pane_dead(&self, name: &str) -> bool;
    fn capture_pane_last_line(&self, name: &str) -> String;
    fn create_proxy_session(
        &self,
        local_name: &str,
        connect_cmd: &str,
    ) -> anyhow::Result<()>;
    fn set_remain_on_exit(&self, name: &str) -> anyhow::Result<()>;
    fn list_panes_for_session(&self, name: &str) -> String;
}

pub trait SshService: Send + Sync {
    fn exec(&self, host: &str, command: &str) -> anyhow::Result<String>;
    fn is_reachable(&self, host: &str) -> bool;
}

pub trait NotifyService: Send + Sync {
    fn send_notification(&self, title: &str, message: &str);
}

pub trait ShellCommandService: Send + Sync {
    fn run(&self, program: &str, args: &[&str]) -> anyhow::Result<String>;
    fn run_in(&self, program: &str, args: &[&str], cwd: &str) -> anyhow::Result<String>;
    fn run_status(&self, program: &str, args: &[&str]) -> anyhow::Result<bool>;
}
