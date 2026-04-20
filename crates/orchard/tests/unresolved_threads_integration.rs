/// Library-level integration test for issue #320: `UnresolvedThreads` pipeline status.
///
/// Exercises the full in-process pipeline — `CachedPr` → `derive_worktree_rows`
/// → `WorktreeRow` → `WorktreeState` → `JsonWorktree` — covering the behavioral
/// contract from the @e2e scenario in `specs/features/unresolved-threads-pipeline-status.feature`
/// (lines 45–53) without the fragility of subprocess'ing the compiled binary.
///
/// A worktree whose PR is approved, CI passing, no merge conflicts, not paused,
/// not blocked, but has `unresolved_threads = 2` must surface as
/// `status = "unresolved_threads"` with glyph `"💬"` in JSON output — not
/// `"ready"` or `"awaiting_review"`.
mod common;

use common::{make_approved_pr, make_worktree};
use orchard::cache::CachedPr;
use orchard::derive::derive_worktree_rows;
use orchard::json_output::JsonWorktree;
use orchard::orchard_state::WorktreeState;

/// Integration test for issue #320 (covers AC #3 and AC #9):
/// A PR that is approved, CI passing, no merge conflicts, not paused, not blocked,
/// but has `unresolved_threads = 2` must produce a `JsonWorktree` with
/// `status == "unresolved_threads"` and `status_glyph == "💬"`.
///
/// This exercises the full pipeline:
///   `CachedPr` → `derive_worktree_rows` → `WorktreeRow` → `WorktreeState` → `JsonWorktree`
#[test]
fn orchard_json_surfaces_unresolved_threads_status_for_approved_passing_pr_with_threads() {
    // Build a fixture with a feature worktree + approved/passing PR #3298
    // that has 2 unresolved review threads.
    let pr = CachedPr {
        unresolved_threads: 2,
        unresolved_thread_comment_timestamps: vec![1_700_000_000, 1_700_100_000],
        ..make_approved_pr(3298, "feature/unresolved-threads")
    };
    let worktrees = vec![
        make_worktree("/workspace/fixture", "main"),
        make_worktree("/workspace/fixture-feat", "feature/unresolved-threads"),
    ];

    // Run the derive pipeline — pure, no I/O.
    let rows = derive_worktree_rows(&[], &[pr], &worktrees, &[], "owner/fixture", &[], &[]);

    // Row 0 is the main worktree; row 1 is the feature worktree.
    assert_eq!(rows.len(), 2);
    let feat_row = &rows[1];

    // Convert to WorktreeState (the unified data model).
    let ws = WorktreeState::from(feat_row);

    // Convert to the JSON output type.
    let json_wt = JsonWorktree::from(&ws);

    // AC #9 / AC #3: status must be the stable snake_case string, not "ready" or "awaiting_review".
    assert_eq!(
        json_wt.status, "unresolved_threads",
        "approved + CI passing + unresolved_threads=2 must surface as unresolved_threads"
    );
    assert_eq!(
        json_wt.status_glyph, "💬",
        "status_glyph must be the speech-bubble glyph (U+1F4AC)"
    );
    assert_ne!(
        json_wt.status, "ready",
        "PR with unresolved threads must NOT be surfaced as ready"
    );
    assert_ne!(
        json_wt.status, "awaiting_review",
        "PR with unresolved threads must NOT fall through to awaiting_review"
    );

    // Also verify the camelCase keys round-trip correctly through serde.
    let value = serde_json::to_value(&json_wt).expect("serialize JsonWorktree");
    assert_eq!(value["status"], "unresolved_threads");
    assert_eq!(value["statusGlyph"], "💬");
}
