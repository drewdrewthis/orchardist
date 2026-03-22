use std::process::Command;
use tempfile::TempDir;

// ---------------------------------------------------------------------------
// Macros
// ---------------------------------------------------------------------------

/// Returns early from the test if tmux is not available on this system.
/// Use at the top of any test that creates real tmux sessions so that CI
/// environments without tmux remain green and visible (not skipped via #[ignore]).
#[macro_export]
macro_rules! requires_tmux {
    () => {
        if !common::tmux_available() {
            eprintln!("tmux not available — skipping");
            return;
        }
    };
}

// ---------------------------------------------------------------------------
// GitFixture
// ---------------------------------------------------------------------------

/// A disposable git repository in a temporary directory.
/// The underlying `TempDir` is kept alive for the lifetime of the fixture.
pub struct GitFixture {
    _dir: TempDir,
    path: String,
}

impl GitFixture {
    /// Creates a new temp git repository initialised on branch `main`.
    pub fn new() -> Self {
        let dir = TempDir::new().expect("TempDir::new failed");
        let path = dir.path().to_str().expect("non-UTF8 temp path").to_string();

        Command::new("git")
            .args(["init", "-b", "main", &path])
            .output()
            .expect("git init failed");

        Command::new("git")
            .args(["-C", &path, "config", "user.email", "test@example.com"])
            .output()
            .expect("git config user.email failed");

        Command::new("git")
            .args(["-C", &path, "config", "user.name", "Test"])
            .output()
            .expect("git config user.name failed");

        Command::new("git")
            .args(["-C", &path, "commit", "--allow-empty", "-m", "initial"])
            .output()
            .expect("git commit failed");

        GitFixture { _dir: dir, path }
    }

    /// Returns the absolute path to the repository root.
    pub fn path(&self) -> &str {
        &self.path
    }

    /// Creates a worktree for `branch` inside a `worktrees/` subdirectory.
    /// Returns the absolute path to the new worktree.
    pub fn add_worktree(&self, branch: &str) -> String {
        // Sanitize branch name to a safe directory component.
        let dir_name = branch.replace('/', "-");
        let worktree_path = format!("{}/worktrees/{}", self.path, dir_name);

        Command::new("git")
            .args(["-C", &self.path, "worktree", "add", "-b", branch, &worktree_path])
            .output()
            .expect("git worktree add failed");

        worktree_path
    }

    /// Writes raw JSON to `.git/orchard.json` inside this repository.
    pub fn write_orchard_config(&self, json: &str) {
        let config_path = format!("{}/.git/orchard.json", self.path);
        std::fs::write(&config_path, json).expect("write orchard.json failed");
    }
}

// ---------------------------------------------------------------------------
// TmuxFixture
// ---------------------------------------------------------------------------

/// Manages a set of tmux sessions isolated by a unique name prefix.
/// Kills any pre-existing sessions with the prefix on creation and kills
/// all sessions with the prefix on drop — safe to use concurrently across tests.
pub struct TmuxFixture {
    pub prefix: String,
}

impl TmuxFixture {
    /// Creates a new fixture with a prefix derived from the current process ID.
    /// Any sessions already matching the prefix are killed before returning.
    pub fn new() -> Self {
        let prefix = format!("orchard_test_{}_", std::process::id());
        let fixture = TmuxFixture { prefix };
        fixture.kill_all_with_prefix();
        fixture
    }

    /// Creates a detached tmux session named `{prefix}{suffix}`.
    /// Returns the full session name.
    pub fn create_session(&self, suffix: &str) -> String {
        let name = format!("{}{}", self.prefix, suffix);
        Command::new("tmux")
            .args(["new-session", "-d", "-s", &name])
            .output()
            .expect("tmux new-session failed");
        name
    }

    /// Creates a detached tmux session named `{prefix}{suffix}` starting in `dir`.
    /// Returns the full session name.
    pub fn create_session_at(&self, suffix: &str, dir: &str) -> String {
        let name = format!("{}{}", self.prefix, suffix);
        Command::new("tmux")
            .args(["new-session", "-d", "-s", &name, "-c", dir])
            .output()
            .expect("tmux new-session failed");
        name
    }

    /// Returns true when tmux reports a session with the given name exists.
    pub fn session_exists(&self, name: &str) -> bool {
        Command::new("tmux")
            .args(["has-session", "-t", name])
            .status()
            .map(|s| s.success())
            .unwrap_or(false)
    }

    // Kills all tmux sessions whose names begin with `self.prefix`.
    fn kill_all_with_prefix(&self) {
        let out = Command::new("tmux")
            .args(["list-sessions", "-F", "#{session_name}"])
            .output();

        let sessions = match out {
            Ok(o) if o.status.success() => String::from_utf8_lossy(&o.stdout).into_owned(),
            _ => return, // tmux not running — nothing to kill
        };

        for name in sessions.lines() {
            if name.starts_with(&self.prefix) {
                let _ = Command::new("tmux")
                    .args(["kill-session", "-t", name])
                    .status();
            }
        }
    }
}

impl Drop for TmuxFixture {
    fn drop(&mut self) {
        self.kill_all_with_prefix();
    }
}

// ---------------------------------------------------------------------------
// Legacy helpers (kept for backward compatibility)
// ---------------------------------------------------------------------------

/// Creates a temporary git repository with an initial empty commit.
/// Returns `(TempDir, path_string)`. The `TempDir` must stay alive for the test.
pub fn create_temp_git_repo() -> (TempDir, String) {
    let dir = TempDir::new().unwrap();
    let path = dir.path().to_str().unwrap().to_string();

    Command::new("git")
        .args(["init", &path])
        .output()
        .expect("git init failed");

    Command::new("git")
        .args(["-C", &path, "config", "user.email", "test@example.com"])
        .output()
        .expect("git config user.email failed");

    Command::new("git")
        .args(["-C", &path, "config", "user.name", "Test"])
        .output()
        .expect("git config user.name failed");

    Command::new("git")
        .args(["-C", &path, "commit", "--allow-empty", "-m", "initial"])
        .output()
        .expect("git commit failed");

    (dir, path)
}

/// Returns true when tmux is available on this system.
pub fn tmux_available() -> bool {
    Command::new("tmux")
        .args(["-V"])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Returns true when the `gh` CLI is available and authenticated.
pub fn gh_available() -> bool {
    Command::new("gh")
        .args(["auth", "status"])
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}
