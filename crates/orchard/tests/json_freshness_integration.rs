//! Integration tests for `orchard --json` and `orchard sessions --json`
//! freshness contracts (issues #374, #375).
//!
//! `--json` is a live read: it must reflect a `git worktree remove` (or any
//! other local mutation) by the time the next invocation returns. The TUI's
//! cache-fast path is unaffected — these tests only exercise the CLI JSON
//! contract.
mod common;

use std::path::Path;

use assert_cmd::Command;
use orchard::sessions_index::{
    PROTECTED_SESSION_KEEPERS, SESSIONS_INDEX_VERSION, SessionsIndexOutput,
};
use serde_json::Value;
use tempfile::TempDir;

// ---------------------------------------------------------------------------
// Test scaffolding
// ---------------------------------------------------------------------------

/// Initialises a parent-of-repo + bare-repo + linked worktree layout that
/// mirrors the production orchard convention.
///
/// The on-disk shape produced (rooted at `parent`):
///
/// ```text
/// parent/
/// ├── repo/             ← regular clone, the orchard `path` for the repo
/// └── worktrees/
///     └── worktree-foo/ ← linked worktree on branch `foo`
/// ```
///
/// Returns `(repo_path, worktree_path)` — both absolute. The repo at
/// `repo_path` always has at least one commit on its default branch so
/// `git worktree add` can succeed.
struct TestRepoLayout {
    /// Owns the parent directory; dropped at end of test.
    _parent: TempDir,
    repo_path: std::path::PathBuf,
    worktree_path: std::path::PathBuf,
    branch: String,
}

impl TestRepoLayout {
    fn new(branch: &str) -> Self {
        let parent = TempDir::new().expect("create temp parent dir");
        let repo_path = parent.path().join("repo");
        let worktrees_dir = parent.path().join("worktrees");
        std::fs::create_dir_all(&repo_path).expect("mkdir repo");
        std::fs::create_dir_all(&worktrees_dir).expect("mkdir worktrees");

        // git init + identity + initial commit so the default branch exists.
        run_git(&repo_path, &["init", "-b", "main"]);
        run_git(&repo_path, &["config", "user.email", "test@example.com"]);
        run_git(&repo_path, &["config", "user.name", "Test"]);
        std::fs::write(repo_path.join("README.md"), "test\n").expect("seed file");
        run_git(&repo_path, &["add", "README.md"]);
        run_git(&repo_path, &["commit", "-m", "init"]);

        // Add a linked worktree on a new branch.
        let worktree_path = worktrees_dir.join(format!("worktree-{branch}"));
        run_git(
            &repo_path,
            &[
                "worktree",
                "add",
                "-b",
                branch,
                worktree_path.to_str().expect("utf-8 worktree path"),
            ],
        );

        Self {
            _parent: parent,
            repo_path,
            worktree_path,
            branch: branch.to_string(),
        }
    }

    fn remove_worktree(&self) {
        run_git(
            &self.repo_path,
            &[
                "worktree",
                "remove",
                "-f",
                self.worktree_path.to_str().expect("utf-8 worktree path"),
            ],
        );
    }
}

fn run_git(cwd: &Path, args: &[&str]) {
    let output = std::process::Command::new("git")
        .args(args)
        .current_dir(cwd)
        .output()
        .unwrap_or_else(|e| panic!("git {args:?} failed to start: {e}"));
    assert!(
        output.status.success(),
        "git {args:?} exited non-zero in {}: stderr={}",
        cwd.display(),
        String::from_utf8_lossy(&output.stderr)
    );
}

/// Writes a global orchard config with a single managed repo at `repo_path`.
///
/// Schema mirrors `~/.config/orchard/config.json`. The `slug` is intentionally
/// deterministic so the per-repo cache filenames are stable across runs.
fn write_orchard_config(home: &Path, slug: &str, repo_path: &Path) {
    let config_dir = home.join(".config/orchard");
    std::fs::create_dir_all(&config_dir).expect("mkdir config");
    let config = serde_json::json!({
        "repos": [{
            "slug": slug,
            "path": repo_path.to_str().expect("utf-8 repo path"),
            "remotes": []
        }],
        "tmux_sessions": []
    });
    std::fs::write(
        config_dir.join("config.json"),
        serde_json::to_string_pretty(&config).expect("serialize config"),
    )
    .expect("write config.json");
}

/// Runs `orchard --json` with `home` as `HOME` and returns the parsed output.
fn run_orchard_json(home: &Path) -> Value {
    let assert = Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("--json")
        .env("HOME", home)
        .env("XDG_CONFIG_HOME", home.join(".config"))
        .env_remove("TMUX")
        .assert()
        .success();
    let stdout = assert.get_output().stdout.clone();
    let s = String::from_utf8(stdout).expect("utf-8 stdout");
    serde_json::from_str(&s).unwrap_or_else(|e| panic!("invalid JSON: {e}\nstdout was:\n{s}"))
}

/// Runs `orchard sessions --json` and returns the parsed [`SessionsIndexOutput`].
fn run_orchard_sessions_json(home: &Path) -> SessionsIndexOutput {
    let assert = Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .args(["sessions", "--json"])
        .env("HOME", home)
        .env("XDG_CONFIG_HOME", home.join(".config"))
        .env_remove("TMUX")
        .assert()
        .success();
    let stdout = assert.get_output().stdout.clone();
    let s = String::from_utf8(stdout).expect("utf-8 stdout");
    serde_json::from_str(&s)
        .unwrap_or_else(|e| panic!("invalid sessions JSON: {e}\nstdout was:\n{s}"))
}

// ---------------------------------------------------------------------------
// AC #374-3 — worktree removal reflected in --json output
// ---------------------------------------------------------------------------

/// `orchard --json` reflects `git worktree remove` immediately on the next
/// invocation. This is the headline freshness contract for issue #374.
///
/// Steps:
/// 1. Build a parent/repo/worktrees layout with one tracked worktree on
///    branch `flake`.
/// 2. First `orchard --json` — assert the worktree path is present.
/// 3. `git worktree remove -f <path>`.
/// 4. Second `orchard --json` — assert the worktree path is absent.
///
/// The two invocations are issued back-to-back; no `orchard refresh` is
/// inserted between them. The contract is that `--json` is the source of
/// truth, not a cache view.
#[test]
fn json_reflects_worktree_remove_on_next_invocation() {
    let layout = TestRepoLayout::new("flake");
    let home = TempDir::new().expect("create home");
    let slug = "owner/repo";
    write_orchard_config(home.path(), slug, &layout.repo_path);

    // First read: worktree should be present.
    let before = run_orchard_json(home.path());
    let before_paths = collect_worktree_paths(&before, slug);
    let wt_str = layout.worktree_path.to_string_lossy().into_owned();
    assert!(
        before_paths.iter().any(|p| p == &wt_str),
        "worktree path should be present before removal; got {before_paths:?}"
    );

    // Mutate: remove the worktree.
    layout.remove_worktree();

    // Second read: worktree must NOT appear.
    let after = run_orchard_json(home.path());
    let after_paths = collect_worktree_paths(&after, slug);
    assert!(
        !after_paths.iter().any(|p| p == &wt_str),
        "worktree path must not appear after `git worktree remove`; \
         orchard --json is supposed to be live, not cached.\n\
         got paths: {after_paths:?}"
    );
}

/// Same shape as [`json_reflects_worktree_remove_on_next_invocation`] but
/// asserts the symmetric direction: a freshly-added worktree shows up
/// without an explicit `orchard refresh`.
#[test]
fn json_reflects_worktree_add_on_next_invocation() {
    let parent = TempDir::new().expect("create parent");
    let repo_path = parent.path().join("repo");
    let worktrees_dir = parent.path().join("worktrees");
    std::fs::create_dir_all(&repo_path).expect("mkdir repo");
    std::fs::create_dir_all(&worktrees_dir).expect("mkdir worktrees");

    run_git(&repo_path, &["init", "-b", "main"]);
    run_git(&repo_path, &["config", "user.email", "test@example.com"]);
    run_git(&repo_path, &["config", "user.name", "Test"]);
    std::fs::write(repo_path.join("README.md"), "test\n").expect("seed file");
    run_git(&repo_path, &["add", "README.md"]);
    run_git(&repo_path, &["commit", "-m", "init"]);

    let home = TempDir::new().expect("create home");
    let slug = "owner/repo";
    write_orchard_config(home.path(), slug, &repo_path);

    // Baseline: only the main worktree exists, no `flake` branch.
    let before = run_orchard_json(home.path());
    let before_branches = collect_worktree_branches(&before, slug);
    assert!(
        !before_branches.iter().any(|b| b == "flake"),
        "branch 'flake' must not exist before add; got {before_branches:?}"
    );

    // Mutate: add a new linked worktree.
    let new_worktree = worktrees_dir.join("worktree-flake");
    run_git(
        &repo_path,
        &[
            "worktree",
            "add",
            "-b",
            "flake",
            new_worktree.to_str().expect("utf-8 worktree path"),
        ],
    );

    // Second read: branch must now be present.
    let after = run_orchard_json(home.path());
    let after_branches = collect_worktree_branches(&after, slug);
    assert!(
        after_branches.iter().any(|b| b == "flake"),
        "branch 'flake' should be present after `git worktree add`; got {after_branches:?}"
    );
}

// ---------------------------------------------------------------------------
// AC #374-2 — help text documents freshness contract
// ---------------------------------------------------------------------------

/// `orchard --help` documents that `--json` is a live read.
///
/// AC #374-2 calls for documenting expected freshness in `orchard --help`.
/// We assert on substrings rather than literal text so help-text rewords
/// don't break the test, but the substrings must be present.
#[test]
fn help_documents_json_freshness_contract() {
    let assert = Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("--help")
        .assert()
        .success();
    let stderr = String::from_utf8(assert.get_output().stderr.clone()).unwrap();
    assert!(
        stderr.contains("--json") && (stderr.contains("Live") || stderr.contains("live")),
        "help text must describe --json as a live read; got:\n{stderr}"
    );
}

/// `orchard --help` mentions `orchard sessions --json` so users can discover it.
#[test]
fn help_mentions_sessions_subcommand() {
    let assert = Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .arg("--help")
        .assert()
        .success();
    let stderr = String::from_utf8(assert.get_output().stderr.clone()).unwrap();
    assert!(
        stderr.contains("orchard sessions --json"),
        "help text must mention `orchard sessions --json`; got:\n{stderr}"
    );
}

// ---------------------------------------------------------------------------
// AC #375-* — `orchard sessions --json` shape and classification
// ---------------------------------------------------------------------------

/// `orchard sessions --json` returns a parseable [`SessionsIndexOutput`] even
/// with an empty config — this proves the wire format and the version
/// constant are wired up end-to-end.
#[test]
fn sessions_json_returns_versioned_output_with_empty_config() {
    let home = TempDir::new().expect("create home");
    // Minimal config so load_global_config doesn't read the host's real one.
    let config_dir = home.path().join(".config/orchard");
    std::fs::create_dir_all(&config_dir).expect("mkdir config");
    let config = serde_json::json!({"repos": [], "tmux_sessions": []});
    std::fs::write(
        config_dir.join("config.json"),
        serde_json::to_string_pretty(&config).unwrap(),
    )
    .expect("write config");

    let out = run_orchard_sessions_json(home.path());
    assert_eq!(out.version, SESSIONS_INDEX_VERSION);
    // Without any tmux state we can't assert specific sessions exist; just
    // confirm the `sessions` field deserialised (it's a Vec, possibly empty).
    let _: usize = out.sessions.len();
}

/// Every record returned by `orchard sessions --json` must carry a non-empty
/// `host` field (#375-3). With no remotes configured the only host is
/// `"local"`, but the assertion shape is universal.
#[test]
fn sessions_json_every_record_carries_host_field() {
    let layout = TestRepoLayout::new("flake");
    let home = TempDir::new().expect("create home");
    write_orchard_config(home.path(), "owner/repo", &layout.repo_path);

    let out = run_orchard_sessions_json(home.path());
    for s in &out.sessions {
        assert!(
            !s.host.is_empty(),
            "every session record must have a host; got: {s:?}"
        );
    }
}

/// Sessions with names in the hardcoded keepers list must classify as
/// `Protected` and have `protected: true`. Uses a real tmux server on an
/// **isolated socket** so the test does not pollute or depend on the
/// developer's main tmux state.
#[cfg(unix)]
#[test]
fn sessions_json_classifies_hardcoded_keeper_as_protected() {
    let Some(harness) = TmuxHarness::start("orchard-prot") else {
        eprintln!("tmux not available — skipping");
        return;
    };
    let home = TempDir::new().expect("create home");
    write_minimal_config(home.path());

    let keeper = PROTECTED_SESSION_KEEPERS[0];
    harness.create_session(keeper, "/tmp", "sleep 30");

    let out = run_orchard_sessions_json_with_tmux(home.path(), &harness);
    let rec = out
        .sessions
        .iter()
        .find(|s| s.name == keeper)
        .unwrap_or_else(|| panic!("no record for keeper '{keeper}'; got {out:?}"));
    assert!(rec.protected, "keeper must have protected=true: {rec:?}");
    assert_eq!(
        rec.classification,
        orchard::sessions_index::SessionClassification::Protected,
        "keeper must classify as Protected: {rec:?}"
    );
    assert_eq!(rec.host, "local");
}

/// A session whose active-pane cwd is INSIDE a tracked worktree path
/// classifies as `WorktreeAttached`. Uses real tmux + real worktree so the
/// full pipeline (git worktree → cache → classify) is exercised.
#[cfg(unix)]
#[test]
fn sessions_json_classifies_session_inside_worktree_as_worktree_attached() {
    let Some(harness) = TmuxHarness::start("orchard-wt") else {
        eprintln!("tmux not available — skipping");
        return;
    };
    let layout = TestRepoLayout::new("flake");
    let home = TempDir::new().expect("create home");
    write_orchard_config(home.path(), "owner/repo", &layout.repo_path);

    let cwd_inside = layout.worktree_path.to_string_lossy().into_owned();
    let session_name = format!("issue42_{}_main", layout.branch);
    harness.create_session(&session_name, &cwd_inside, "sleep 30");

    let out = run_orchard_sessions_json_with_tmux(home.path(), &harness);
    let rec = out
        .sessions
        .iter()
        .find(|s| s.name == session_name)
        .unwrap_or_else(|| panic!("expected session {session_name}, got {out:?}"));
    assert_eq!(
        rec.classification,
        orchard::sessions_index::SessionClassification::WorktreeAttached,
        "session inside worktree path must classify as WorktreeAttached: {rec:?}"
    );
    assert!(!rec.protected);
    assert_eq!(rec.host, "local");
}

/// A session with `^issue\d+` name + `claude` command + cwd outside any
/// known worktree classifies as `DetachedClaude`. Approximates the `claude`
/// command via a no-op shell script named `claude` so the foreground command
/// reads as `claude`.
#[cfg(unix)]
#[test]
fn sessions_json_classifies_orphaned_claude_with_issue_name_as_detached_claude() {
    let Some(harness) = TmuxHarness::start("orchard-detached") else {
        eprintln!("tmux not available — skipping");
        return;
    };
    let home = TempDir::new().expect("create home");
    write_minimal_config(home.path());

    // We need tmux's `pane_current_command` to read as "claude". Put a
    // shim called `claude` on PATH that just sleeps — tmux reports the
    // foreground process name from /proc, so the comm name will be "claude".
    let bin_dir = home.path().join("bin");
    std::fs::create_dir_all(&bin_dir).expect("mkdir bin");
    let claude_shim = bin_dir.join("claude");
    std::fs::write(&claude_shim, "#!/bin/sh\nexec sleep 30\n").expect("write claude shim");
    set_executable(&claude_shim);

    harness.create_session_with_path("issue999_main", "/tmp", "claude", bin_dir.to_str().unwrap());

    let out = run_orchard_sessions_json_with_tmux(home.path(), &harness);
    let rec = out
        .sessions
        .iter()
        .find(|s| s.name == "issue999_main")
        .unwrap_or_else(|| panic!("seeded session must appear; got {out:?}"));
    assert_eq!(
        rec.classification,
        orchard::sessions_index::SessionClassification::DetachedClaude,
        "issue\\d+ + claude + cwd-outside-worktree → DetachedClaude: {rec:?}"
    );
    assert_eq!(rec.host, "local");
    assert_eq!(rec.command, "claude");
}

/// A session with a non-issue name + non-claude command + cwd outside any
/// worktree classifies as `Orphan` — the residual bucket and the `/prune`
/// skill's primary target.
#[cfg(unix)]
#[test]
fn sessions_json_classifies_random_unrelated_session_as_orphan() {
    let Some(harness) = TmuxHarness::start("orchard-orphan") else {
        eprintln!("tmux not available — skipping");
        return;
    };
    let home = TempDir::new().expect("create home");
    write_minimal_config(home.path());

    harness.create_session("scratch-utils", "/tmp", "sleep 30");

    let out = run_orchard_sessions_json_with_tmux(home.path(), &harness);
    let rec = out
        .sessions
        .iter()
        .find(|s| s.name == "scratch-utils")
        .unwrap_or_else(|| panic!("seeded session must appear; got {out:?}"));
    assert_eq!(
        rec.classification,
        orchard::sessions_index::SessionClassification::Orphan,
        "non-issue + non-claude + outside-worktree → Orphan: {rec:?}"
    );
    assert!(!rec.protected);
}

// ---------------------------------------------------------------------------
// helpers (private to this test file)
// ---------------------------------------------------------------------------

/// Returns the list of worktree `path` values for `slug` from a parsed
/// `JsonOutput`. Empty vec when the repo isn't present.
fn collect_worktree_paths(out: &Value, slug: &str) -> Vec<String> {
    let Some(repos) = out.get("repos").and_then(|v| v.as_array()) else {
        return Vec::new();
    };
    let Some(repo) = repos
        .iter()
        .find(|r| r.get("slug").and_then(|s| s.as_str()) == Some(slug))
    else {
        return Vec::new();
    };
    let Some(wts) = repo.get("worktrees").and_then(|v| v.as_array()) else {
        return Vec::new();
    };
    wts.iter()
        .filter_map(|w| {
            w.get("path")
                .and_then(|p| p.as_str())
                .map(|s| s.to_string())
        })
        .collect()
}

/// Returns the list of worktree `branch` values for `slug` from a parsed
/// `JsonOutput`. Empty vec when the repo isn't present.
fn collect_worktree_branches(out: &Value, slug: &str) -> Vec<String> {
    let Some(repos) = out.get("repos").and_then(|v| v.as_array()) else {
        return Vec::new();
    };
    let Some(repo) = repos
        .iter()
        .find(|r| r.get("slug").and_then(|s| s.as_str()) == Some(slug))
    else {
        return Vec::new();
    };
    let Some(wts) = repo.get("worktrees").and_then(|v| v.as_array()) else {
        return Vec::new();
    };
    wts.iter()
        .filter_map(|w| {
            w.get("branch")
                .and_then(|b| b.as_str())
                .map(|s| s.to_string())
        })
        .collect()
}

/// Writes a minimal config (no repos, no remotes) so `load_global_config`
/// doesn't read the host's real config.
fn write_minimal_config(home: &Path) {
    let config_dir = home.join(".config/orchard");
    std::fs::create_dir_all(&config_dir).expect("mkdir config");
    let config = serde_json::json!({"repos": [], "tmux_sessions": []});
    std::fs::write(
        config_dir.join("config.json"),
        serde_json::to_string_pretty(&config).unwrap(),
    )
    .expect("write config");
}

/// Owns an isolated tmux server (its own socket file), so created sessions
/// don't collide with the developer's real tmux state. On drop, the server
/// is killed and the socket dir is cleaned up by `_dir`.
///
/// `start("prefix")` returns `None` when tmux is not on PATH or fails to
/// start (e.g. CI without tmux available). Callers can early-return without
/// failing the test.
#[cfg(unix)]
struct TmuxHarness {
    /// Owns the socket directory; dropped at end of test.
    _dir: TempDir,
    /// Path to the socket the isolated tmux server listens on.
    socket: std::path::PathBuf,
}

#[cfg(unix)]
impl TmuxHarness {
    fn start(prefix: &str) -> Option<Self> {
        let dir = TempDir::new().ok()?;
        // Short directory path: tmux socket paths have an OS-level length
        // limit (~108 bytes on Linux), so we keep the socket name short.
        let socket = dir.path().join(prefix);

        // Kick the server alive by issuing a `start-server` command.
        let status = std::process::Command::new("tmux")
            .arg("-S")
            .arg(&socket)
            .arg("start-server")
            .status()
            .ok()?;
        if !status.success() {
            return None;
        }
        Some(Self { _dir: dir, socket })
    }

    /// Creates a tmux session named `name` running `command` from `cwd`.
    fn create_session(&self, name: &str, cwd: &str, command: &str) {
        let status = std::process::Command::new("tmux")
            .arg("-S")
            .arg(&self.socket)
            .args(["new-session", "-d", "-s", name, "-c", cwd, command])
            .status()
            .expect("tmux new-session");
        assert!(status.success(), "tmux new-session failed for {name}");
    }

    /// Variant of [`Self::create_session`] that prepends `extra_path` to the
    /// tmux session's `PATH`. Used to put a shim binary in front of system
    /// binaries (e.g. a no-op `claude` script).
    fn create_session_with_path(&self, name: &str, cwd: &str, command: &str, extra_path: &str) {
        let new_path = format!(
            "{}:{}",
            extra_path,
            std::env::var("PATH").unwrap_or_default()
        );
        let status = std::process::Command::new("tmux")
            .arg("-S")
            .arg(&self.socket)
            .args(["new-session", "-d", "-s", name, "-c", cwd])
            .arg("-e")
            .arg(format!("PATH={new_path}"))
            .arg(command)
            .status()
            .expect("tmux new-session");
        assert!(status.success(), "tmux new-session failed for {name}");
    }
}

#[cfg(unix)]
impl Drop for TmuxHarness {
    fn drop(&mut self) {
        // Best-effort: kill the server so its child processes (sleep) exit.
        let _ = std::process::Command::new("tmux")
            .arg("-S")
            .arg(&self.socket)
            .arg("kill-server")
            .status();
    }
}

/// Same shape as [`run_orchard_sessions_json`] but routes the binary's tmux
/// invocations to the harness's isolated socket via a wrapper script on PATH.
#[cfg(unix)]
fn run_orchard_sessions_json_with_tmux(home: &Path, harness: &TmuxHarness) -> SessionsIndexOutput {
    let bin_dir = home.join("bin");
    std::fs::create_dir_all(&bin_dir).expect("mkdir bin");
    install_tmux_wrapper(&bin_dir, &harness.socket);
    let path = format!(
        "{}:{}",
        bin_dir.display(),
        std::env::var("PATH").unwrap_or_default()
    );

    let assert = Command::cargo_bin("orchard")
        .expect("orchard binary must exist")
        .args(["sessions", "--json"])
        .env("HOME", home)
        .env("XDG_CONFIG_HOME", home.join(".config"))
        .env("PATH", &path)
        .env_remove("TMUX")
        .assert()
        .success();
    let stdout = assert.get_output().stdout.clone();
    let s = String::from_utf8(stdout).expect("utf-8 stdout");
    serde_json::from_str(&s)
        .unwrap_or_else(|e| panic!("invalid sessions JSON: {e}\nstdout was:\n{s}"))
}

/// Installs a `tmux` wrapper script in `bin_dir` that forces every tmux
/// invocation to use `socket` instead of the system default.
///
/// orchard's tmux source calls `tmux list-panes -a -F ...` (no `-S` flag),
/// which would otherwise read the user's real tmux server. Prepending this
/// wrapper to PATH redirects every call.
#[cfg(unix)]
fn install_tmux_wrapper(bin_dir: &Path, socket: &Path) {
    let real_tmux = which_tmux();
    let wrapper = bin_dir.join("tmux");
    let socket_str = socket.display();
    let script = format!("#!/bin/sh\nexec {real_tmux} -S {socket_str} \"$@\"\n");
    std::fs::write(&wrapper, script).expect("write tmux wrapper");
    set_executable(&wrapper);
}

/// Returns the absolute path to the system `tmux`, falling back to the bare
/// command name if `which` is unavailable. Used by `install_tmux_wrapper` to
/// avoid a wrapper-calls-itself loop.
#[cfg(unix)]
fn which_tmux() -> String {
    let out = std::process::Command::new("which").arg("tmux").output();
    match out {
        Ok(o) if o.status.success() => String::from_utf8_lossy(&o.stdout).trim().to_string(),
        _ => "/usr/bin/tmux".to_string(),
    }
}

/// Sets `0o755` on `path`. Best-effort.
#[cfg(unix)]
fn set_executable(path: &Path) {
    use std::os::unix::fs::PermissionsExt;
    let _ = std::fs::set_permissions(path, std::fs::Permissions::from_mode(0o755));
}
