//! End-to-end harness for `/launch-remote` visibility (AC1 + AC2 of #337).
//!
//! These tests are gated behind `#[ignore]` because they require:
//! - A real boxd VM (or shared VM) reachable from the test runner
//! - The user's `~/.config/orchard/config.json` configured for that remote
//! - Real GitHub credentials available to `gh`
//! - A real test issue number on the target repo
//!
//! CI does not run them. Run on demand with:
//!
//! ```bash
//! ORCHARD_E2E_TEST_ISSUE=999 cargo test -p orchard --test e2e_launch_remote -- --ignored
//! ```
//!
//! Each test:
//! 1. Records `t0`
//! 2. Invokes the production launch flow against a configured remote
//! 3. Polls `orchard refresh && orchard-tui --json` for the new session row
//! 4. Records `t1` when the session appears
//! 5. Asserts visibility under the AC's budget (30s) and prints elapsed time
//!
//! The polling interleaves an explicit `orchard refresh` per the Plan: the
//! follow-up issue for `orchard refresh --remote <host>` (Phase 7) is what
//! eventually closes the AC1 30s budget honestly. Until then this harness
//! documents the refresh-on-launch gap empirically — run it 5–10 times to
//! capture a baseline distribution.
//!
//! ## Why no automatic CI run
//!
//! - Real boxd cycles cost money and require credentials.
//! - The launch flow is a markdown skill (not the orchard binary), so we can
//!   only invoke its constituent pieces here, not the user-facing entry point.
//! - The contract being verified is "after `/launch-remote` completes, the
//!   session appears in `orchard-tui --json` within 30s". The harness measures
//!   the second half (refresh + --json visibility) given a session that
//!   already exists on the remote — the first half (skill execution) is out
//!   of scope for an in-tree test.

use std::time::{Duration, Instant};

use assert_cmd::Command;

const VISIBILITY_BUDGET: Duration = Duration::from_secs(30);
const POLL_INTERVAL: Duration = Duration::from_millis(500);

/// AC1: After a session appears on a boxd-fork host (e.g., the LangWatch
/// fleet's per-issue forks), `orchard refresh && orchard-tui --json` surfaces
/// it locally within `VISIBILITY_BUDGET`.
///
/// Setup before running:
/// 1. Have a boxd-fork remote configured in `~/.config/orchard/config.json`
///    for `langwatch/langwatch` (or any repo with `type: boxd-fork`).
/// 2. Manually create a tmux session on a real boxd fork (or run
///    `/launch-remote langwatch/langwatch#$ORCHARD_E2E_TEST_ISSUE` in
///    your shell beforehand).
/// 3. Set `ORCHARD_E2E_TEST_ISSUE` to the issue number used.
/// 4. Optionally set `ORCHARD_E2E_REPO_SLUG` (default: `langwatch/langwatch`).
///
/// On success, prints the elapsed seconds to stdout — capture this in the PR
/// description as the AC1 baseline measurement.
#[test]
#[ignore = "requires real boxd-fork remote + GitHub credentials; run on demand with --ignored"]
fn launch_remote_boxd_fork_visibility_within_30s() {
    let issue = std::env::var("ORCHARD_E2E_TEST_ISSUE")
        .expect("set ORCHARD_E2E_TEST_ISSUE to the issue number with a live session on the boxd-fork remote");
    let repo = std::env::var("ORCHARD_E2E_REPO_SLUG")
        .unwrap_or_else(|_| "langwatch/langwatch".to_string());

    let elapsed = poll_for_session_visibility(&repo, &issue);
    print_baseline("AC1 boxd-fork", &repo, &issue, elapsed);
    assert!(
        elapsed < VISIBILITY_BUDGET,
        "AC1 budget exceeded: {elapsed:?} > {VISIBILITY_BUDGET:?}"
    );
}

/// AC2: After a session appears on a boxd-shared remote (e.g.,
/// `boxd@orchard-rs.boxd.sh`), `orchard refresh && orchard-tui --json` surfaces
/// it locally within `VISIBILITY_BUDGET`.
///
/// Setup before running:
/// 1. Have a boxd-shared (or remmy-typed boxd) remote configured for
///    `drewdrewthis/git-orchard-rs`.
/// 2. Manually create a tmux session on the shared VM with a name that
///    references the issue (e.g., `git-orchard-rs_issue<N>`).
/// 3. Set `ORCHARD_E2E_TEST_ISSUE` to the issue number used.
/// 4. Optionally set `ORCHARD_E2E_REPO_SLUG`
///    (default: `drewdrewthis/git-orchard-rs`).
#[test]
#[ignore = "requires real boxd-shared remote + GitHub credentials; run on demand with --ignored"]
fn launch_remote_boxd_shared_visibility_within_30s() {
    let issue = std::env::var("ORCHARD_E2E_TEST_ISSUE")
        .expect("set ORCHARD_E2E_TEST_ISSUE to the issue number with a live session on the boxd-shared remote");
    let repo = std::env::var("ORCHARD_E2E_REPO_SLUG")
        .unwrap_or_else(|_| "drewdrewthis/git-orchard-rs".to_string());

    let elapsed = poll_for_session_visibility(&repo, &issue);
    print_baseline("AC2 boxd-shared", &repo, &issue, elapsed);
    assert!(
        elapsed < VISIBILITY_BUDGET,
        "AC2 budget exceeded: {elapsed:?} > {VISIBILITY_BUDGET:?}"
    );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Polls `orchard refresh && orchard-tui --json` until the output contains a
/// session row whose name references the issue number (substring match on
/// `issue<N>`), or until `VISIBILITY_BUDGET` elapses. Returns the elapsed
/// `Duration` on success; panics on timeout.
fn poll_for_session_visibility(repo_slug: &str, issue_number: &str) -> Duration {
    let needle = format!("issue{issue_number}");
    let started = Instant::now();

    loop {
        // Run the same two-step the user runs: refresh, then read.
        let _ = Command::cargo_bin("orchard-tui")
            .unwrap()
            .arg("refresh")
            .timeout(Duration::from_secs(15))
            .output()
            .expect("orchard refresh must run");

        let output = Command::cargo_bin("orchard-tui")
            .unwrap()
            .arg("--json")
            .timeout(Duration::from_secs(10))
            .output()
            .expect("orchard-tui --json must run");

        let stdout = String::from_utf8_lossy(&output.stdout);
        if session_row_visible(&stdout, repo_slug, &needle) {
            return started.elapsed();
        }

        if started.elapsed() >= VISIBILITY_BUDGET {
            panic!(
                "session referencing '{needle}' on repo '{repo_slug}' did not appear within \
                 {VISIBILITY_BUDGET:?}; last orchard-tui --json stdout begins with:\n{}",
                stdout.lines().take(20).collect::<Vec<_>>().join("\n")
            );
        }

        std::thread::sleep(POLL_INTERVAL);
    }
}

/// Returns true when the JSON payload contains a worktree on the given repo
/// slug whose `sessions` array contains a session whose name includes
/// `needle`.
///
/// Uses substring search rather than full JSON parsing for two reasons:
/// 1. Robust against minor schema drift (we want this harness to keep
///    working through small JsonOutput version bumps).
/// 2. The assertion the user cares about is "the session appeared in some
///    form" — an exact-shape match would be brittle.
fn session_row_visible(json_stdout: &str, repo_slug: &str, needle: &str) -> bool {
    // Quick gate: both the slug and the needle must appear at all.
    if !json_stdout.contains(repo_slug) || !json_stdout.contains(needle) {
        return false;
    }
    // Belt-and-suspenders: parse and check the structure if we can. If parse
    // fails, fall back to the substring gate (which is already permissive).
    if let Ok(json) = serde_json::from_str::<serde_json::Value>(json_stdout)
        && let Some(repos) = json.get("repos").and_then(|v| v.as_array())
        && let Some(repo) = repos
            .iter()
            .find(|r| r.get("slug").and_then(|s| s.as_str()) == Some(repo_slug))
        && let Some(worktrees) = repo.get("worktrees").and_then(|v| v.as_array())
    {
        for wt in worktrees {
            if let Some(sessions) = wt.get("sessions").and_then(|v| v.as_array()) {
                for s in sessions {
                    if let Some(name) = s.get("name").and_then(|v| v.as_str())
                        && name.contains(needle)
                    {
                        return true;
                    }
                }
            }
        }
        return false;
    }
    // Fallback: substring already matched both gate strings.
    true
}

/// Prints the AC baseline measurement to stdout in a parseable form.
///
/// Format: `[AC?-baseline] strategy=... repo=... issue=... elapsed_s=...`
///
/// Capture this in PR descriptions or follow-up issues to ground the
/// "30s budget" claim empirically.
fn print_baseline(label: &str, repo: &str, issue: &str, elapsed: Duration) {
    println!(
        "[{label}-baseline] repo={repo} issue={issue} elapsed_s={:.3}",
        elapsed.as_secs_f64()
    );
}
