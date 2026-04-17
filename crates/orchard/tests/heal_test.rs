/// Integration tests for the `orchard heal` command.
///
/// These tests exercise the `heal::diagnose` pure function with realistic inputs,
/// corresponding to acceptance criteria from `specs/features/orchard-heal.feature`.
use orchard::heal::{
    HealAction, HealCategory, HealClaudeState, HealWorktree, Severity, diagnose, format_report,
};
use orchard::types::TmuxSession;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

fn session(name: &str, path: &str) -> TmuxSession {
    TmuxSession {
        name: name.to_string(),
        path: path.to_string(),
        attached: false,
        pane_title: None,
        active_pane_cwd: None,
    }
}

fn worktree(path: &str, branch: &str) -> HealWorktree {
    HealWorktree {
        path: path.to_string(),
        branch: branch.to_string(),
        expected_session_name: None,
        pr_state: None,
        pr_number: None,
        issue_state: None,
    }
}

// ---------------------------------------------------------------------------
// Dry-run: reports what it would do without making changes
// ---------------------------------------------------------------------------

/// Scenario: Dry run reports orphaned session and stale claude state as warnings.
/// No sessions are killed or files deleted.
#[test]
fn dry_run_reports_findings_without_applying_fixes() {
    let sessions = vec![session("myrepo_old-feature", "/tmp")]; // /tmp exists but no matching wt
    let worktrees = vec![worktree("/workspace/main", "main")];
    let claude_states = vec![HealClaudeState {
        path: "/tmp/orchard-claude-abc123.json".to_string(),
        tmux_session: "myrepo_dead".to_string(),
    }];

    let report = diagnose(&sessions, &worktrees, &claude_states, &[], &[]);

    // Orphaned session finding.
    let orphan = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::OrphanedSession);
    assert!(orphan.is_some(), "should find orphaned session");
    assert_eq!(orphan.unwrap().severity, Severity::Warning);

    // Stale claude state finding.
    let stale = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::StaleClaudeState);
    assert!(stale.is_some(), "should find stale claude state");
    assert_eq!(stale.unwrap().severity, Severity::Warning);

    // Suggest fix in report.
    let text = format_report(&report, None);
    assert!(
        text.contains("orchard heal --fix"),
        "dry run should suggest --fix: {text}"
    );
}

// ---------------------------------------------------------------------------
// All-healthy scenario
// ---------------------------------------------------------------------------

/// Scenario: When everything matches, no findings are generated and no --fix is suggested.
#[test]
fn all_healthy_when_sessions_match_worktrees() {
    // Use a real path that exists on disk so the directory-existence check passes.
    let tmp = std::env::temp_dir().to_string_lossy().to_string();
    let sessions = vec![session("myrepo_main", &tmp)];
    let worktrees = vec![worktree(&tmp, "main")];

    let report = diagnose(&sessions, &worktrees, &[], &[], &[]);

    assert_eq!(report.findings.len(), 0, "should have no findings");
    assert!(report.is_all_ok());

    let text = format_report(&report, None);
    assert!(
        !text.contains("orchard heal --fix"),
        "should not suggest --fix when healthy"
    );
}

// ---------------------------------------------------------------------------
// Orphaned tmux sessions
// ---------------------------------------------------------------------------

/// Scenario: tmux session with no matching worktree path is flagged.
#[test]
fn orphaned_session_detected_when_path_exists_but_no_worktree_matches() {
    // /tmp always exists but is not a worktree.
    let sessions = vec![session("myrepo_old-feature", "/tmp")];
    let worktrees = vec![worktree("/workspace/other", "main")];

    let report = diagnose(&sessions, &worktrees, &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::OrphanedSession);
    assert!(
        finding.is_some(),
        "should detect orphaned session: {:?}",
        report.findings
    );
    assert_eq!(finding.unwrap().severity, Severity::Warning);
    assert!(
        matches!(&finding.unwrap().action, HealAction::KillSession(n) if n == "myrepo_old-feature")
    );
}

// ---------------------------------------------------------------------------
// Dead session directory
// ---------------------------------------------------------------------------

/// Scenario: Session pointing to a non-existent path gets an Error-severity finding.
#[test]
fn dead_session_directory_detected_for_nonexistent_path() {
    let sessions = vec![session(
        "myrepo_gone",
        "/tmp/nonexistent-path-xyz-heal-test",
    )];
    let worktrees = vec![];

    let report = diagnose(&sessions, &worktrees, &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::DeadSessionDirectory);
    assert!(finding.is_some(), "should detect dead session directory");
    assert_eq!(finding.unwrap().severity, Severity::Error);
    assert!(matches!(&finding.unwrap().action, HealAction::KillSession(n) if n == "myrepo_gone"));
}

// ---------------------------------------------------------------------------
// Stale claude state files
// ---------------------------------------------------------------------------

/// Scenario: Claude state file for dead session is flagged.
#[test]
fn stale_claude_state_detected_for_dead_session() {
    let sessions = vec![];
    let claude_states = vec![HealClaudeState {
        path: "/tmp/orchard-claude-abc123.json".to_string(),
        tmux_session: "myrepo_dead".to_string(),
    }];

    let report = diagnose(&sessions, &[], &claude_states, &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::StaleClaudeState);
    assert!(finding.is_some(), "should detect stale claude state");
    assert!(
        matches!(&finding.unwrap().action, HealAction::DeleteFile(p) if p == "/tmp/orchard-claude-abc123.json")
    );
    // Report warns about "myrepo_dead".
    assert!(finding.unwrap().message.contains("myrepo_dead"));
}

/// Scenario: Claude state file for a live session is NOT flagged.
#[test]
fn no_stale_claude_state_when_session_is_alive() {
    // Use /tmp so the path-exists check passes.
    let sessions = vec![session("myrepo_live", "/tmp")];
    let claude_states = vec![HealClaudeState {
        path: "/tmp/orchard-claude-xyz.json".to_string(),
        tmux_session: "myrepo_live".to_string(),
    }];

    let report = diagnose(&sessions, &[], &claude_states, &[], &[]);

    let stale = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::StaleClaudeState);
    assert!(
        stale.is_none(),
        "live session's claude state should not be flagged"
    );
}

// ---------------------------------------------------------------------------
// Stale cache files
// ---------------------------------------------------------------------------

/// Scenario: Cache file for unknown repo slug is flagged.
#[test]
fn stale_cache_file_flagged_for_unknown_repo() {
    let cache_files = vec!["ghost_repo_issues.json".to_string()];
    let known_slugs = vec!["owner/known-project".to_string()];

    let report = diagnose(&[], &[], &[], &cache_files, &known_slugs);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::StaleCache);
    assert!(finding.is_some(), "should detect stale cache file");
    assert!(matches!(finding.unwrap().action, HealAction::DeleteFile(_)));
    assert!(finding.unwrap().message.contains("ghost_repo_issues.json"));
}

/// Scenario: Cache file for a known repo slug is NOT flagged.
#[test]
fn no_stale_cache_for_known_repo() {
    let cache_files = vec!["owner_myrepo_issues.json".to_string()];
    let known_slugs = vec!["owner/myrepo".to_string()];

    let report = diagnose(&[], &[], &[], &cache_files, &known_slugs);

    let stale = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::StaleCache);
    assert!(stale.is_none(), "known repo cache should not be flagged");
}

// ---------------------------------------------------------------------------
// Merged/closed PR worktrees
// ---------------------------------------------------------------------------

/// Scenario: Worktree whose PR is merged is flagged (not auto-deleted).
#[test]
fn merged_pr_worktree_flagged_for_manual_cleanup() {
    let mut wt = worktree(".worktrees/issue3-tests", "issue3/tests");
    wt.pr_state = Some("merged".to_string());
    wt.pr_number = Some(12);

    let report = diagnose(&[], &[wt], &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::MergedPrWorktree);
    assert!(finding.is_some(), "should flag merged PR worktree");
    assert!(finding.unwrap().message.contains("PR #12 merged"));
    assert!(matches!(
        finding.unwrap().action,
        HealAction::FlagForCleanup(_)
    ));
}

/// Scenario: Worktree with closed PR is flagged.
#[test]
fn closed_pr_worktree_flagged() {
    let mut wt = worktree(".worktrees/issue5-fix", "issue5/fix");
    wt.pr_state = Some("closed".to_string());
    wt.pr_number = Some(15);

    let report = diagnose(&[], &[wt], &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::ClosedPrWorktree);
    assert!(finding.is_some(), "should flag closed PR worktree");
}

/// Scenario: --fix does NOT delete worktrees for merged PRs.
#[test]
fn fix_does_not_auto_delete_merged_pr_worktrees() {
    let mut wt = worktree(".worktrees/issue3-tests", "issue3/tests");
    wt.pr_state = Some("merged".to_string());
    wt.pr_number = Some(12);

    let report = diagnose(&[], &[wt], &[], &[], &[]);
    let fix_results = orchard::heal::apply_fixes(&report.findings);

    // All results should be FlagForCleanup (not KillSession or DeleteFile).
    for r in &fix_results {
        assert!(
            r.message.starts_with("Flagged for manual cleanup"),
            "merged PR worktree should only be flagged, not deleted: {}",
            r.message
        );
    }
}

// ---------------------------------------------------------------------------
// Closed issue worktrees
// ---------------------------------------------------------------------------

/// Scenario: Worktree whose linked issue is closed (and no PR) is flagged.
#[test]
fn closed_issue_worktree_flagged_when_no_pr() {
    let mut wt = worktree(".worktrees/issue8-refactor", "issue8/refactor");
    wt.issue_state = Some("closed".to_string());

    let report = diagnose(&[], &[wt], &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::ClosedIssueWorktree);
    assert!(
        finding.is_some(),
        "should flag closed-issue worktree: {:?}",
        report.findings
    );
}

// ---------------------------------------------------------------------------
// Session naming mismatch
// ---------------------------------------------------------------------------

/// Scenario: Session whose name doesn't match the expected convention is warned.
#[test]
fn session_naming_mismatch_detected() {
    let sessions = vec![session("wrong-name", "/workspace/feature-login")];
    let mut wt = worktree("/workspace/feature-login", "feature/login");
    wt.expected_session_name = Some("myrepo_feature-login".to_string());

    let report = diagnose(&sessions, &[wt], &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::SessionNamingMismatch);
    assert!(finding.is_some(), "should detect naming mismatch");
    assert!(finding.unwrap().message.contains("myrepo_feature-login"));
    assert!(finding.unwrap().message.contains("wrong-name"));
}

// ---------------------------------------------------------------------------
// Multiple sessions per worktree
// ---------------------------------------------------------------------------

/// Scenario: Two sessions pointing to the same worktree path triggers a warning.
#[test]
fn multiple_sessions_per_worktree_detected() {
    let sessions = vec![
        session("myrepo_issue10-api", "/workspace/issue10-api"),
        session("extra-session", "/workspace/issue10-api"),
    ];
    let wt = worktree("/workspace/issue10-api", "issue10/api");

    let report = diagnose(&sessions, &[wt], &[], &[], &[]);

    let finding = report
        .findings
        .iter()
        .find(|f| f.category == HealCategory::MultipleSessionsPerWorktree);
    assert!(finding.is_some(), "should detect multiple sessions");
    assert!(finding.unwrap().message.contains("myrepo_issue10-api"));
    assert!(finding.unwrap().message.contains("extra-session"));
}

// ---------------------------------------------------------------------------
// Report format
// ---------------------------------------------------------------------------

/// Scenario: Report uses structured output with icons.
#[test]
fn report_format_includes_icons() {
    let sessions = vec![session("orphan", "/tmp")]; // /tmp is a real path but not a worktree
    let worktrees = vec![];

    let report = diagnose(&sessions, &worktrees, &[], &[], &[]);
    let text = format_report(&report, None);

    // Should contain at least one icon character.
    let has_icon =
        text.contains('\u{2713}') || text.contains('\u{26a0}') || text.contains('\u{2716}');
    assert!(has_icon, "report should contain icon characters: {text}");
}

/// Scenario: JSON output contains a "findings" array with required fields.
#[test]
fn json_output_contains_findings_array() {
    let sessions = vec![session("orphan", "/tmp")];
    let worktrees = vec![];

    let report = diagnose(&sessions, &worktrees, &[], &[], &[]);
    let json_str = serde_json::to_string(&report).expect("serialize report");
    let json: serde_json::Value = serde_json::from_str(&json_str).expect("parse json");

    let findings = json.get("findings").expect("should have findings field");
    assert!(findings.is_array(), "findings should be an array");

    let arr = findings.as_array().unwrap();
    if !arr.is_empty() {
        let first = &arr[0];
        assert!(
            first.get("category").is_some(),
            "finding should have category"
        );
        assert!(
            first.get("severity").is_some(),
            "finding should have severity"
        );
        assert!(
            first.get("message").is_some(),
            "finding should have message"
        );
        assert!(first.get("action").is_some(), "finding should have action");
    }
}
