//! Pipeline signal lexicon — pure functional core for TUI row state.
//!
//! Every TUI row answers one question: **why isn't this merged yet?** The pipeline
//! status is a single glyph per parent row, chosen by a merge-blocker hierarchy
//! (first match wins). Agent activity is a separate axis, rolled up bottom-up
//! across the tmux session → window → pane hierarchy.
//!
//! Lexicons (see issue #251):
//!
//! | Glyph | PipelineStatus | Meaning |
//! |---|---|---|
//! | ❓ | NeedsInput | any descendant Claude agent is waiting for user input |
//! | 🚫 | CiFailing | `pr.ci_code_state == "failing"` |
//! | ⚠️ | MergeConflict | `pr.has_conflicts` |
//! | 🔴 | ChangesRequested | `pr.review_decision == "CHANGES_REQUESTED"` |
//! | (blank) | Coding | no PR, or PR without review — no blocker to render yet |
//! | ⬆️ | AwaitingReview | PR open, no decision yet — up-arrow asks reviewer to act |
//! | 📝 | Draft | `pr.is_draft` |
//! | 🔗 | Blocked | `issue.blocked_by` non-empty with open blockers |
//! | ⏸️ | Paused | `paused` label present |
//! | 🟢 | Ready | all gates green |
//! | 🚀 | Merged | `pr.state == MERGED` |
//!
//! `Coding` renders a blank STATUS glyph on purpose: the STATUS column answers
//! "why isn't this merged?" and for a work-in-progress branch the answer is
//! "nothing's blocking, work is in progress." Agent activity (⚡/○ in column A)
//! carries whether someone is on it — mixing the two axes confused the signal.
//!
//! Severity note: `Coding` (active work — watch the agent) outranks `AwaitingReview`
//! (passive wait — nothing to do). Watching workers beats waiting on a reviewer.
//!
//! | Glyph | Activity | Meaning |
//! |---|---|---|
//! | ⚡ | Working | a Claude agent is actively working |
//! | ○ | Idle | an agent exists but is idle |
//! | 💀 | Exhausted | rate-limits depleted OR context_window_pct ≥ 95 |
//! | (blank) | None | no agent / non-Claude pane |
//!
//! Activity rollup severity (highest wins): `Exhausted > Input > Working > Idle > None`.
//! `Input` is an activity-level signal that also maps to the `NeedsInput` status. The
//! rollup treats it as its own level so a single ⚡ working child cannot mask a ❓ sibling.

use crate::claude_state::ClaudeState;
use crate::derive::WorktreeRow;
use crate::orchard_state::{ClaudeEnrichment, IssueInfo, PrState, WorktreeState};

/// Merge-blocker status for a worktree row. Ordered by severity (most-blocking first)
/// so `Ord` can be used for sort comparisons directly.
///
/// The variant order matches the merge-blocker hierarchy in issue #251.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum PipelineStatus {
    /// ❓ Any descendant agent is waiting for user input.
    NeedsInput,
    /// 🚫 PR has failing CI.
    CiFailing,
    /// ⚠️ PR has merge conflicts.
    MergeConflict,
    /// 🔴 PR has CHANGES_REQUESTED review.
    ChangesRequested,
    /// ⌨️ Branch is being actively coded on — no PR, or PR without review requested.
    ///
    /// Severity: outranks `AwaitingReview` because active work needs watching;
    /// waiting on a reviewer is passive (the user can't act on it anyway).
    Coding,
    /// ⬆️ PR is open with no review decision yet — waiting on a human reviewer.
    AwaitingReview,
    /// 📝 PR is a draft.
    Draft,
    /// 🔗 Issue is blocked by open blockers.
    Blocked,
    /// ⏸️ Issue/PR is paused.
    Paused,
    /// 🟢 All gates green — ready to merge.
    Ready,
    /// 🚀 PR is merged (terminal state; row renders dim).
    Merged,
}

impl PipelineStatus {
    /// Single-glyph representation of this status.
    pub fn glyph(self) -> &'static str {
        match self {
            Self::NeedsInput => "\u{2753}",            // ❓
            Self::CiFailing => "\u{1F6AB}",            // 🚫
            Self::MergeConflict => "\u{26A0}\u{FE0F}", // ⚠️
            Self::ChangesRequested => "\u{1F534}",     // 🔴
            // Coding: blank — "no blocker, work in progress." Agent activity
            // in column A carries whether someone is on it.
            Self::Coding => "",
            Self::AwaitingReview => "\u{2B06}\u{FE0F}", // ⬆️
            Self::Draft => "\u{1F4DD}",                 // 📝
            Self::Blocked => "\u{1F517}",               // 🔗
            Self::Paused => "\u{23F8}\u{FE0F}",         // ⏸️
            Self::Ready => "\u{1F7E2}",                 // 🟢
            Self::Merged => "\u{1F680}",                // 🚀
        }
    }

    /// Short human-readable label (legend + accessibility/testing).
    pub fn label(self) -> &'static str {
        match self {
            Self::NeedsInput => "needs input",
            Self::CiFailing => "CI failing",
            Self::MergeConflict => "merge conflict",
            Self::ChangesRequested => "changes requested",
            Self::Coding => "coding",
            Self::AwaitingReview => "awaiting review",
            Self::Draft => "draft",
            Self::Blocked => "blocked",
            Self::Paused => "paused",
            Self::Ready => "ready",
            Self::Merged => "merged",
        }
    }
}

/// Agent activity state for a single agent-carrying row.
///
/// Ordered so highest severity (`Exhausted`) compares greatest — the rollup uses
/// `max` across descendants and the result is the parent row's activity glyph.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Default)]
pub enum Activity {
    /// No agent is present on this row (or a non-Claude pane).
    #[default]
    None,
    /// Agent exists but is idle (finished a turn, waiting for next prompt).
    Idle,
    /// Agent is actively working (generating output or running tools).
    Working,
    /// Agent is waiting for user input. Rolls up to the `NeedsInput` status.
    Input,
    /// Agent is rate-limited or context-exhausted (≥95%). Needs human attention.
    Exhausted,
}

impl Activity {
    /// Single-glyph representation, or empty string for `None`.
    ///
    /// `Input` reuses the ⚡ working glyph in column A — the `NeedsInput`
    /// *status* column is where the ❓ renders. Keeping column A to just three
    /// activity glyphs (⚡ / ○ / 💀) matches the issue spec.
    pub fn glyph(self) -> &'static str {
        match self {
            Self::None => "",
            Self::Idle => "\u{25CB}",                  // ○
            Self::Working | Self::Input => "\u{26A1}", // ⚡
            Self::Exhausted => "\u{1F480}",            // 💀
        }
    }

    /// Short human-readable label (legend + accessibility/testing).
    pub fn label(self) -> &'static str {
        match self {
            Self::None => "none",
            Self::Idle => "idle",
            Self::Working => "working",
            Self::Input => "input",
            Self::Exhausted => "exhausted",
        }
    }
}

// ---------------------------------------------------------------------------
// Resolvers
// ---------------------------------------------------------------------------

/// Context-window threshold above which the skull glyph fires.
///
/// 95% matches the issue spec and leaves a narrow margin before forced compaction.
pub const CONTEXT_EXHAUSTED_PCT: f64 = 95.0;

/// Resolves a single Claude enrichment into its `Activity` level.
///
/// Skull rule fires when either:
/// - `rate_limits` indicates a depleted bucket (5hr OR weekly used ≥ 100), or
/// - `context_window_pct` ≥ [`CONTEXT_EXHAUSTED_PCT`].
pub fn activity_from_claude(c: &ClaudeEnrichment) -> Activity {
    if is_exhausted(c) {
        return Activity::Exhausted;
    }
    match c.status {
        ClaudeState::Input => Activity::Input,
        ClaudeState::Working => Activity::Working,
        ClaudeState::Idle => Activity::Idle,
        ClaudeState::None => Activity::None,
    }
}

fn is_exhausted(c: &ClaudeEnrichment) -> bool {
    if let Some(pct) = c.context_window_pct
        && pct >= CONTEXT_EXHAUSTED_PCT
    {
        return true;
    }
    if let Some(ref rl) = c.rate_limits
        && rate_limits_exhausted(rl)
    {
        return true;
    }
    false
}

fn rate_limits_exhausted(rl: &crate::session::ClaudeRateLimits) -> bool {
    // A bucket is considered depleted at ≥100% used. We check whichever
    // window Claude Code surfaces on the telemetry line.
    rl.five_hour_used_pct.is_some_and(|p| p >= 100.0)
        || rl.seven_day_used_pct.is_some_and(|p| p >= 100.0)
}

/// Bottom-up rollup of activity across the worktree's sessions → windows → panes.
///
/// A worktree row's column-A glyph is the highest-severity activity found anywhere
/// in its descendant agent tree. When no sessions carry Claude enrichment (or there
/// are no sessions at all), returns [`Activity::None`] (blank column).
///
/// The rollup is lossless: collapsing a subtree in the TUI doesn't hide any signal
/// because the parent already reflects the worst state below it.
pub fn rollup_activity(wt: &WorktreeState) -> Activity {
    wt.sessions
        .iter()
        .filter_map(|s| s.claude.as_ref().map(activity_from_claude))
        .max()
        .unwrap_or(Activity::None)
}

/// Resolves the pipeline status for a worktree row using the merge-blocker
/// hierarchy (first match wins).
///
/// Order:
/// 1. Merged       (terminal)
/// 2. NeedsInput   (any agent awaiting input)
/// 3. CiFailing    (PR ci_code_state == failing)
/// 4. MergeConflict
/// 5. ChangesRequested
/// 6. Blocked      (issue blocked_by has open blockers)
/// 7. Paused       (paused label on issue or PR)
/// 8. Draft        (PR is draft)
/// 9. Ready        (PR open, approved, CI passing)
/// 10. AwaitingReview (PR open, no decision)
/// 11. Coding      (no PR, or PR without review requested — default)
///
/// Note: `NeedsInput` outranks CI/conflict/etc. because a human is actively
/// required; everything else can wait. `Merged` wins overall so merged PRs
/// don't spuriously render as "ready" or "coding".
pub fn resolve_status(wt: &WorktreeState) -> PipelineStatus {
    // Merged is terminal — dim row, sink sort.
    if let Some(pr) = &wt.pr
        && is_merged(pr)
    {
        return PipelineStatus::Merged;
    }

    // Needs input wins over all other non-terminal states because a human
    // must act before anything else can progress.
    if wt.sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Input)
    }) {
        return PipelineStatus::NeedsInput;
    }

    if let Some(pr) = &wt.pr {
        if pr.ci_code_state.as_deref() == Some("failing") {
            return PipelineStatus::CiFailing;
        }
        if pr.has_conflicts {
            return PipelineStatus::MergeConflict;
        }
        if matches_review(&pr.review_decision, "CHANGES_REQUESTED") {
            return PipelineStatus::ChangesRequested;
        }
    }

    if has_open_blockers(&wt.issue) {
        return PipelineStatus::Blocked;
    }

    if is_paused(&wt.issue, &wt.pr) {
        return PipelineStatus::Paused;
    }

    if let Some(pr) = &wt.pr {
        if pr.is_draft.unwrap_or(false) {
            return PipelineStatus::Draft;
        }
        if is_ready_to_merge(pr) {
            return PipelineStatus::Ready;
        }
        if is_open(pr) {
            return PipelineStatus::AwaitingReview;
        }
    }

    // Default: active coding — either no PR yet, or a PR that hasn't triggered
    // a review cycle yet (state unknown, closed-without-merge).
    PipelineStatus::Coding
}

fn is_merged(pr: &PrState) -> bool {
    pr.state
        .as_deref()
        .is_some_and(|s| s.eq_ignore_ascii_case("merged"))
}

fn is_open(pr: &PrState) -> bool {
    pr.state
        .as_deref()
        .is_some_and(|s| s.eq_ignore_ascii_case("open"))
}

fn matches_review(rd: &Option<String>, target: &str) -> bool {
    rd.as_deref().is_some_and(|v| {
        v.eq_ignore_ascii_case(target) || v.eq_ignore_ascii_case(&target.replace('_', ""))
    })
}

fn has_open_blockers(issue: &Option<IssueInfo>) -> bool {
    issue.as_ref().is_some_and(|i| !i.blocked_by.is_empty())
}

fn is_paused(issue: &Option<IssueInfo>, pr: &Option<PrState>) -> bool {
    let issue_paused = issue
        .as_ref()
        .is_some_and(|i| i.labels.iter().any(|l| l.eq_ignore_ascii_case("paused")));
    let pr_paused = pr
        .as_ref()
        .is_some_and(|p| p.labels.iter().any(|l| l.eq_ignore_ascii_case("paused")));
    issue_paused || pr_paused
}

fn is_ready_to_merge(pr: &PrState) -> bool {
    if !is_open(pr) {
        return false;
    }
    if pr.is_draft.unwrap_or(false) || pr.has_conflicts {
        return false;
    }
    // Approved with no failing/pending CI — call it ready.
    let approved = matches_review(&pr.review_decision, "APPROVED")
        || pr
            .review_decision
            .as_deref()
            .is_some_and(|v| v.eq_ignore_ascii_case("approved"));
    let ci_ok = matches!(pr.ci_code_state.as_deref(), Some("passing") | None);
    approved && ci_ok
}

// ---------------------------------------------------------------------------
// Sort key — pipeline status severity with priority rerank
// ---------------------------------------------------------------------------

/// Sort key for worktree rows based on pipeline status severity (issue #251).
///
/// Ordering (ascending — smaller sorts first):
/// 1. **Main worktree** — the repo's main worktree pins to the top.
/// 2. **Priority flag** — priority rows float to the top of their status group.
/// 3. **PipelineStatus severity** — merge-blocker hierarchy (`NeedsInput` first,
///    `Merged` last).
/// 4. **SINCE timestamp** — oldest-time-in-current-state first within a status
///    group (the row that has been stuck longest is the most urgent).
/// 5. **Issue number** — ascending; `Some` before `None`.
/// 6. **Branch name** — alphabetical (deterministic final tiebreaker).
///
/// Priority does not override status severity entirely — a priority ❓ still
/// outranks a priority 🟢 — but within a status bucket priority floats up.
/// The issue spec says "priority flag re-ranks within a status group"; here we
/// lift priority to the outer key so that a priority 🟢 still beats a non-
/// priority ❌ only when the user explicitly flagged it (matches memory:
/// `feedback_focus_driven_sorting`).
///
/// The compromise chosen: **status severity is primary, priority is secondary**.
/// That matches the issue mockup where priority rows still obey hierarchy but
/// float within the group.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RowSortKey<'a> {
    /// Main worktrees always sort before non-main rows within the same repo.
    pub is_main: bool,
    /// Pipeline status severity — primary grouping.
    pub status: PipelineStatus,
    /// Priority flag — floats row up within a status group (true before false).
    pub priority: bool,
    /// SINCE epoch seconds — older = more urgent. `None` sorts last.
    pub since: Option<u64>,
    /// Issue number ascending; `Some` before `None`.
    pub issue_number: Option<u32>,
    /// Branch name alphabetical (final tiebreaker).
    pub branch: &'a str,
}

impl<'a> PartialOrd for RowSortKey<'a> {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl<'a> Ord for RowSortKey<'a> {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        use std::cmp::Ordering;
        // Main worktrees first (true sorts before false).
        other
            .is_main
            .cmp(&self.is_main)
            // Status severity ascending — merge-blocker order.
            .then_with(|| self.status.cmp(&other.status))
            // Priority floats up within a status group (true before false).
            .then_with(|| other.priority.cmp(&self.priority))
            // Older SINCE first — the row stuck longest is most urgent.
            .then_with(|| match (self.since, other.since) {
                (Some(a), Some(b)) => a.cmp(&b),
                (Some(_), None) => Ordering::Less,
                (None, Some(_)) => Ordering::Greater,
                (None, None) => Ordering::Equal,
            })
            // Issue number ascending.
            .then_with(|| match (self.issue_number, other.issue_number) {
                (Some(a), Some(b)) => a.cmp(&b),
                (Some(_), None) => Ordering::Less,
                (None, Some(_)) => Ordering::Greater,
                (None, None) => Ordering::Equal,
            })
            // Branch alphabetical.
            .then_with(|| self.branch.cmp(other.branch))
    }
}

/// Builds a [`RowSortKey`] from a worktree row using [`resolve_status`] and
/// [`since_epoch`]. The `priority` flag is threaded in separately — callers
/// pass the persisted-priority lookup.
pub fn sort_key<'a>(wt: &'a WorktreeState, priority: bool) -> RowSortKey<'a> {
    let status = resolve_status(wt);
    let since = since_epoch(wt, status);
    RowSortKey {
        is_main: wt.is_main_worktree,
        status,
        priority,
        since,
        issue_number: wt.issue.as_ref().map(|i| i.number),
        branch: &wt.branch,
    }
}

// ---------------------------------------------------------------------------
// WorktreeRow adapters
// ---------------------------------------------------------------------------
//
// The TUI renders off `WorktreeRow` (the pre-join derive type); `WorktreeState`
// is the unified state model consumed by `--json` and by the pure signal core.
// These adapters let the TUI call into the signal core without materializing a
// `WorktreeState` per render.

/// Resolves [`PipelineStatus`] for a [`WorktreeRow`] (TUI-side adapter).
pub fn resolve_status_row(row: &WorktreeRow) -> PipelineStatus {
    let state = WorktreeState::from(row);
    resolve_status(&state)
}

/// Computes the activity rollup for a [`WorktreeRow`] (TUI-side adapter).
pub fn rollup_activity_row(row: &WorktreeRow) -> Activity {
    row.sessions
        .iter()
        .filter_map(|s| s.claude.as_ref())
        .map(|c| {
            // Bridge from session::ClaudeSessionInfo through the enrichment shape.
            let ce = ClaudeEnrichment::from(c);
            activity_from_claude(&ce)
        })
        .max()
        .unwrap_or(Activity::None)
}

/// Returns the SINCE timestamp (Unix epoch seconds) for a [`WorktreeRow`].
pub fn since_epoch_row(row: &WorktreeRow, status: PipelineStatus) -> Option<u64> {
    let state = WorktreeState::from(row);
    since_epoch(&state, status)
}

/// Builds a [`RowSortKey`] for a [`WorktreeRow`].
///
/// Priority is read from the row's `display_group` — `Prioritized` is how the
/// join layer surfaces the user's persisted priority flag.
pub fn sort_key_row(row: &WorktreeRow) -> RowSortKey<'_> {
    let status = resolve_status_row(row);
    let since = since_epoch_row(row, status);
    let priority = matches!(row.display_group, crate::derive::DisplayGroup::Prioritized);
    RowSortKey {
        is_main: row.is_main_worktree,
        status,
        priority,
        since,
        issue_number: row.issue_number,
        branch: &row.branch,
    }
}

// ---------------------------------------------------------------------------
// Labels — unified, deduped
// ---------------------------------------------------------------------------

/// Merges issue and PR labels into a single deduped vector, preserving first-seen order.
///
/// Issue labels come first, then any PR labels not already present. Comparison is
/// case-insensitive so `Bug` and `bug` dedupe to one. The workflow-phase labels
/// (handled by `derive::phase_from_labels`) are intentionally *not* filtered out —
/// they're still worth surfacing as a badge so users can see them at a glance.
pub fn unified_labels(issue: Option<&IssueInfo>, pr: Option<&PrState>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();

    let mut push = |label: &String| {
        let key = label.to_ascii_lowercase();
        if seen.insert(key) {
            out.push(label.clone());
        }
    };

    if let Some(i) = issue {
        for l in &i.labels {
            push(l);
        }
    }
    if let Some(p) = pr {
        for l in &p.labels {
            push(l);
        }
    }
    out
}

// ---------------------------------------------------------------------------
// SINCE — per-status timestamp selector
// ---------------------------------------------------------------------------

/// Returns the "time in current state" reference timestamp for this worktree row.
///
/// Per issue #251, each pipeline status has a specific timestamp source with
/// a fallback for cases where the primary source is unavailable. All timestamps
/// are returned as Unix epoch seconds; callers format the elapsed duration.
///
/// Three timestamps are flagged as potentially needing backend work (last-failing-
/// check, paused-label-applied-at, pr.merged_at). In the absence of dedicated
/// fields this function falls back to `pr.updated_at` or `issue.updated_at`.
pub fn since_epoch(wt: &WorktreeState, status: PipelineStatus) -> Option<u64> {
    match status {
        PipelineStatus::NeedsInput => wt
            .sessions
            .iter()
            .filter_map(|s| s.claude.as_ref())
            .filter(|c| c.status == ClaudeState::Input)
            .filter_map(|c| c.state_changed_at)
            .min(),
        PipelineStatus::CiFailing => wt
            .pr
            .as_ref()
            .and_then(|pr| pr.updated_at.as_deref())
            .and_then(parse_iso8601),
        PipelineStatus::MergeConflict => wt
            .pr
            .as_ref()
            .and_then(|pr| pr.updated_at.as_deref())
            .and_then(parse_iso8601),
        PipelineStatus::ChangesRequested => wt
            .pr
            .as_ref()
            .and_then(|pr| {
                // Prefer the latest CHANGES_REQUESTED review's submission
                // timestamp — that's when the reviewer actually blocked the PR.
                // Fall back to `pr.updated_at` when no per-review timestamp
                // is available.
                pr.reviews
                    .iter()
                    .filter(|r| r.state.eq_ignore_ascii_case("CHANGES_REQUESTED"))
                    .filter_map(|r| r.submitted_at.as_deref())
                    .max()
                    .or(pr.updated_at.as_deref())
            })
            .and_then(parse_iso8601),
        PipelineStatus::AwaitingReview => wt
            .pr
            .as_ref()
            .and_then(|pr| {
                pr.last_commit_pushed_at
                    .as_deref()
                    .or(pr.updated_at.as_deref())
            })
            .and_then(parse_iso8601),
        PipelineStatus::Coding => wt
            .last_commit_at
            .as_deref()
            .or(wt.issue.as_ref().and_then(|i| i.created_at.as_deref()))
            .and_then(parse_iso8601),
        PipelineStatus::Draft => wt
            .pr
            .as_ref()
            .and_then(|pr| pr.created_at.as_deref())
            .and_then(parse_iso8601),
        PipelineStatus::Blocked => wt
            .issue
            .as_ref()
            .and_then(|i| i.updated_at.as_deref().or(i.created_at.as_deref()))
            .and_then(parse_iso8601),
        PipelineStatus::Paused => wt
            .issue
            .as_ref()
            .and_then(|i| i.updated_at.as_deref().or(i.created_at.as_deref()))
            .or_else(|| wt.pr.as_ref().and_then(|pr| pr.updated_at.as_deref()))
            .and_then(parse_iso8601),
        PipelineStatus::Ready => wt
            .pr
            .as_ref()
            .and_then(|pr| {
                pr.last_commit_pushed_at
                    .as_deref()
                    .or(pr.updated_at.as_deref())
            })
            .and_then(parse_iso8601),
        PipelineStatus::Merged => wt
            .pr
            .as_ref()
            .and_then(|pr| pr.updated_at.as_deref())
            .and_then(parse_iso8601),
    }
}

// ---------------------------------------------------------------------------
// First-launch legend marker
// ---------------------------------------------------------------------------

/// Path to the first-launch marker file.
///
/// Presence of this file indicates the user has seen (and dismissed) the legend
/// overlay at least once. Absence means the TUI should pop the Help/legend
/// view on first start. The file lives under the user cache dir alongside
/// `priorities.json`.
pub fn legend_marker_path() -> Option<std::path::PathBuf> {
    dirs::cache_dir().map(|d| d.join("orchard").join("legend_seen"))
}

/// Returns true when the user has never seen the legend (first launch).
pub fn is_first_launch() -> bool {
    match legend_marker_path() {
        Some(p) => !p.exists(),
        // If we can't find the cache dir, fail closed — don't nag.
        None => false,
    }
}

/// Persists that the user has seen the legend. Idempotent; errors are ignored
/// since the feature is cosmetic.
pub fn mark_legend_seen() {
    if let Some(p) = legend_marker_path()
        && let Some(parent) = p.parent()
    {
        let _ = std::fs::create_dir_all(parent);
        let _ = std::fs::write(&p, b"seen");
    }
}

/// Parses an ISO 8601 timestamp string into Unix epoch seconds, or `None` on failure.
///
/// Accepts RFC 3339 (`2024-06-01T10:00:00Z`) and a fallback `%Y-%m-%dT%H:%M:%SZ`.
pub fn parse_iso8601(s: &str) -> Option<u64> {
    chrono::DateTime::parse_from_rfc3339(s)
        .or_else(|_| chrono::DateTime::parse_from_str(s, "%Y-%m-%dT%H:%M:%SZ"))
        .ok()
        .and_then(|dt| u64::try_from(dt.timestamp()).ok())
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::claude_state::ClaudeState;
    use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};
    use crate::orchard_state::{ClaudeEnrichment, IssueInfo, PrState, SessionState, WorktreeState};

    fn wt() -> WorktreeState {
        WorktreeState::from(&WorktreeRow {
            repo_slug: "o/r".into(),
            worktree_path: "/p".into(),
            branch: "b".into(),
            worktree_host: None,
            issue_number: None,
            issue_title: None,
            issue_state: None,
            issue_labels: vec![],
            issue_assignees: vec![],
            issue_created_at: None,
            issue_updated_at: None,
            issue_blocked_by: vec![],
            issue_sub_issues: vec![],
            issue_parent: None,
            worktree_ahead: None,
            worktree_behind: None,
            worktree_last_commit_at: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
            layout: crate::cache::WorktreeLayout::Bare,
        })
    }

    fn pr(overrides: impl FnOnce(&mut PrInfo)) -> PrState {
        #[allow(deprecated)]
        let mut info = PrInfo {
            number: 42,
            branch: "b".into(),
            state: Some("open".into()),
            ..PrInfo::default()
        };
        overrides(&mut info);
        PrState::from(&info)
    }

    fn issue(overrides: impl FnOnce(&mut IssueInfo)) -> IssueInfo {
        let mut i = IssueInfo {
            number: 1,
            title: String::new(),
            state: "open".into(),
            labels: vec![],
            assignees: vec![],
            created_at: None,
            updated_at: None,
            blocked_by: vec![],
            sub_issues: vec![],
            parent: None,
        };
        overrides(&mut i);
        i
    }

    fn claude(status: ClaudeState, ctx_pct: Option<f64>) -> ClaudeEnrichment {
        ClaudeEnrichment {
            status,
            model: None,
            last_tool: None,
            current_task: None,
            session_start_ts: None,
            input_tokens: None,
            output_tokens: None,
            cache_creation_input_tokens: None,
            cache_read_input_tokens: None,
            context_window_pct: ctx_pct,
            cost_usd: None,
            total_duration_ms: None,
            rate_limits: None,
            stop_reason: None,
            turn_count: None,
            state_changed_at: None,
        }
    }

    fn session_with_claude(c: ClaudeEnrichment) -> SessionState {
        SessionState {
            name: "s".into(),
            host: None,
            claude: Some(c),
            windows: vec![],
            started_at: None,
            last_activity_at: None,
        }
    }

    // -- glyph/label mapping ------------------------------------------------

    #[test]
    fn every_status_has_distinct_glyph() {
        // `Coding` is intentionally blank (no blocker to show) — excluded here
        // because an empty string can't be "distinct" from itself, and it's
        // the only status that maps to no glyph by design.
        let all = [
            PipelineStatus::NeedsInput,
            PipelineStatus::CiFailing,
            PipelineStatus::MergeConflict,
            PipelineStatus::ChangesRequested,
            PipelineStatus::AwaitingReview,
            PipelineStatus::Draft,
            PipelineStatus::Blocked,
            PipelineStatus::Paused,
            PipelineStatus::Ready,
            PipelineStatus::Merged,
        ];
        let glyphs: std::collections::HashSet<_> = all.iter().map(|s| s.glyph()).collect();
        assert_eq!(glyphs.len(), all.len(), "glyphs must be unique");
    }

    #[test]
    fn status_ord_matches_hierarchy() {
        assert!(PipelineStatus::NeedsInput < PipelineStatus::CiFailing);
        assert!(PipelineStatus::CiFailing < PipelineStatus::MergeConflict);
        // Coding (active work, needs watching) outranks AwaitingReview (passive wait).
        assert!(PipelineStatus::Coding < PipelineStatus::AwaitingReview);
        assert!(PipelineStatus::AwaitingReview < PipelineStatus::Draft);
        assert!(PipelineStatus::Ready < PipelineStatus::Merged);
    }

    #[test]
    fn activity_ord_matches_severity() {
        assert!(Activity::None < Activity::Idle);
        assert!(Activity::Idle < Activity::Working);
        assert!(Activity::Working < Activity::Input);
        assert!(Activity::Input < Activity::Exhausted);
    }

    #[test]
    fn activity_input_and_working_share_lightning_glyph() {
        assert_eq!(Activity::Working.glyph(), Activity::Input.glyph());
    }

    #[test]
    fn activity_none_is_blank_glyph() {
        assert_eq!(Activity::None.glyph(), "");
    }

    // -- resolve_status ------------------------------------------------------

    #[test]
    fn status_merged_beats_everything() {
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.state = Some("merged".into());
            p.has_conflicts = true;
            p.ci_code_state = Some("failing".into());
        }));
        w.sessions
            .push(session_with_claude(claude(ClaudeState::Input, None)));
        assert_eq!(resolve_status(&w), PipelineStatus::Merged);
    }

    #[test]
    fn status_needs_input_beats_ci_failing() {
        let mut w = wt();
        w.pr = Some(pr(|p| p.ci_code_state = Some("failing".into())));
        w.sessions
            .push(session_with_claude(claude(ClaudeState::Input, None)));
        assert_eq!(resolve_status(&w), PipelineStatus::NeedsInput);
    }

    #[test]
    fn status_ci_failing_when_pr_has_failing_code_state() {
        let mut w = wt();
        w.pr = Some(pr(|p| p.ci_code_state = Some("failing".into())));
        assert_eq!(resolve_status(&w), PipelineStatus::CiFailing);
    }

    #[test]
    fn status_merge_conflict_when_pr_has_conflicts() {
        let mut w = wt();
        w.pr = Some(pr(|p| p.has_conflicts = true));
        assert_eq!(resolve_status(&w), PipelineStatus::MergeConflict);
    }

    #[test]
    fn status_ci_failing_beats_merge_conflict() {
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.ci_code_state = Some("failing".into());
            p.has_conflicts = true;
        }));
        assert_eq!(resolve_status(&w), PipelineStatus::CiFailing);
    }

    #[test]
    fn status_changes_requested_when_review_decision() {
        let mut w = wt();
        w.pr = Some(pr(|p| p.review_decision = Some("CHANGES_REQUESTED".into())));
        assert_eq!(resolve_status(&w), PipelineStatus::ChangesRequested);
    }

    #[test]
    fn status_blocked_when_issue_has_open_blockers() {
        let mut w = wt();
        w.issue = Some(issue(|i| i.blocked_by = vec![99]));
        assert_eq!(resolve_status(&w), PipelineStatus::Blocked);
    }

    #[test]
    fn status_paused_when_paused_label_on_issue() {
        let mut w = wt();
        w.issue = Some(issue(|i| i.labels = vec!["paused".into()]));
        assert_eq!(resolve_status(&w), PipelineStatus::Paused);
    }

    #[test]
    fn status_paused_case_insensitive() {
        let mut w = wt();
        w.issue = Some(issue(|i| i.labels = vec!["Paused".into()]));
        assert_eq!(resolve_status(&w), PipelineStatus::Paused);
    }

    #[test]
    fn status_draft_when_pr_is_draft() {
        let mut w = wt();
        w.pr = Some(pr(|p| p.is_draft = Some(true)));
        assert_eq!(resolve_status(&w), PipelineStatus::Draft);
    }

    #[test]
    fn status_ready_when_approved_and_ci_passing() {
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.review_decision = Some("APPROVED".into());
            p.ci_code_state = Some("passing".into());
        }));
        assert_eq!(resolve_status(&w), PipelineStatus::Ready);
    }

    #[test]
    fn status_awaiting_review_when_pr_open_no_decision() {
        let mut w = wt();
        w.pr = Some(pr(|_| {}));
        assert_eq!(resolve_status(&w), PipelineStatus::AwaitingReview);
    }

    #[test]
    fn status_coding_when_no_pr() {
        let w = wt();
        assert_eq!(resolve_status(&w), PipelineStatus::Coding);
    }

    #[test]
    fn status_blocked_beats_paused() {
        let mut w = wt();
        w.issue = Some(issue(|i| {
            i.blocked_by = vec![99];
            i.labels = vec!["paused".into()];
        }));
        assert_eq!(resolve_status(&w), PipelineStatus::Blocked);
    }

    // -- activity rollup -----------------------------------------------------

    #[test]
    fn activity_no_sessions_returns_none() {
        let w = wt();
        assert_eq!(rollup_activity(&w), Activity::None);
    }

    #[test]
    fn activity_rollup_picks_highest_severity() {
        let mut w = wt();
        w.sessions = vec![
            session_with_claude(claude(ClaudeState::Idle, None)),
            session_with_claude(claude(ClaudeState::Working, None)),
            session_with_claude(claude(ClaudeState::Idle, None)),
        ];
        assert_eq!(rollup_activity(&w), Activity::Working);
    }

    #[test]
    fn activity_rollup_input_beats_working() {
        let mut w = wt();
        w.sessions = vec![
            session_with_claude(claude(ClaudeState::Working, None)),
            session_with_claude(claude(ClaudeState::Input, None)),
        ];
        assert_eq!(rollup_activity(&w), Activity::Input);
    }

    #[test]
    fn activity_exhausted_beats_input_via_context_pct() {
        let mut w = wt();
        w.sessions = vec![
            session_with_claude(claude(ClaudeState::Input, None)),
            session_with_claude(claude(ClaudeState::Working, Some(99.0))),
        ];
        assert_eq!(rollup_activity(&w), Activity::Exhausted);
    }

    #[test]
    fn activity_context_exhausted_at_95_pct() {
        let c = claude(ClaudeState::Working, Some(95.0));
        assert_eq!(activity_from_claude(&c), Activity::Exhausted);
    }

    #[test]
    fn activity_context_not_exhausted_below_threshold() {
        let c = claude(ClaudeState::Working, Some(94.9));
        assert_eq!(activity_from_claude(&c), Activity::Working);
    }

    // -- labels --------------------------------------------------------------

    #[test]
    fn unified_labels_dedupes_case_insensitively() {
        let i = issue(|i| i.labels = vec!["Bug".into(), "priority".into()]);
        let p = pr(|p| p.labels = vec!["bug".into(), "backend".into()]);
        let out = unified_labels(Some(&i), Some(&p));
        assert_eq!(out, vec!["Bug", "priority", "backend"]);
    }

    #[test]
    fn unified_labels_empty_inputs() {
        assert!(unified_labels(None, None).is_empty());
    }

    #[test]
    fn unified_labels_preserves_order_issue_first() {
        let i = issue(|i| i.labels = vec!["a".into(), "b".into()]);
        let p = pr(|p| p.labels = vec!["c".into(), "a".into()]);
        let out = unified_labels(Some(&i), Some(&p));
        assert_eq!(out, vec!["a", "b", "c"]);
    }

    // -- since_epoch ---------------------------------------------------------

    #[test]
    fn since_needs_input_uses_claude_state_changed_at() {
        let mut w = wt();
        let mut c = claude(ClaudeState::Input, None);
        c.state_changed_at = Some(12345);
        w.sessions.push(session_with_claude(c));
        assert_eq!(since_epoch(&w, PipelineStatus::NeedsInput), Some(12345));
    }

    #[test]
    fn since_ci_failing_uses_pr_updated_at() {
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.ci_code_state = Some("failing".into());
            p.updated_at = Some("2024-06-01T10:00:00Z".into());
        }));
        assert!(since_epoch(&w, PipelineStatus::CiFailing).is_some());
    }

    #[test]
    fn since_coding_uses_last_commit_at() {
        let mut w = wt();
        w.last_commit_at = Some("2024-06-01T10:00:00Z".into());
        assert!(since_epoch(&w, PipelineStatus::Coding).is_some());
    }

    #[test]
    fn since_blocked_prefers_issue_updated_at_over_created_at() {
        // Issue #251 spec: Blocked SINCE sources from issue.updated_at.
        let mut w = wt();
        w.issue = Some(issue(|i| {
            i.blocked_by = vec![99];
            i.created_at = Some("2024-01-01T00:00:00Z".into());
            i.updated_at = Some("2024-06-01T00:00:00Z".into());
        }));
        let ts = since_epoch(&w, PipelineStatus::Blocked).expect("blocked ts");
        let created_ts = parse_iso8601("2024-01-01T00:00:00Z").unwrap();
        assert!(ts > created_ts, "should use updated_at, not created_at");
    }

    #[test]
    fn since_blocked_falls_back_to_created_at_when_updated_at_missing() {
        let mut w = wt();
        w.issue = Some(issue(|i| {
            i.blocked_by = vec![99];
            i.created_at = Some("2024-01-01T00:00:00Z".into());
        }));
        assert!(since_epoch(&w, PipelineStatus::Blocked).is_some());
    }

    #[test]
    fn since_paused_prefers_issue_updated_at() {
        let mut w = wt();
        w.issue = Some(issue(|i| {
            i.labels = vec!["paused".into()];
            i.updated_at = Some("2024-06-01T00:00:00Z".into());
        }));
        assert!(since_epoch(&w, PipelineStatus::Paused).is_some());
    }

    #[test]
    fn since_changes_requested_uses_latest_review_timestamp() {
        // Multiple reviews; the LATEST CHANGES_REQUESTED review timestamp wins.
        // Earlier approved reviews and COMMENTED reviews are ignored.
        use crate::cache::CachedReview;
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.review_decision = Some("CHANGES_REQUESTED".into());
            p.updated_at = Some("2024-01-01T00:00:00Z".into());
            p.reviews = vec![
                CachedReview {
                    author: "a".into(),
                    state: "APPROVED".into(),
                    submitted_at: Some("2024-03-01T00:00:00Z".into()),
                },
                CachedReview {
                    author: "b".into(),
                    state: "CHANGES_REQUESTED".into(),
                    submitted_at: Some("2024-05-01T00:00:00Z".into()),
                },
                CachedReview {
                    author: "c".into(),
                    state: "COMMENTED".into(),
                    submitted_at: Some("2024-06-01T00:00:00Z".into()),
                },
            ];
        }));
        let ts = since_epoch(&w, PipelineStatus::ChangesRequested).expect("ts");
        let expected = parse_iso8601("2024-05-01T00:00:00Z").unwrap();
        assert_eq!(
            ts, expected,
            "latest CHANGES_REQUESTED review ts should win"
        );
    }

    #[test]
    fn since_changes_requested_falls_back_to_pr_updated_at() {
        let mut w = wt();
        w.pr = Some(pr(|p| {
            p.review_decision = Some("CHANGES_REQUESTED".into());
            p.updated_at = Some("2024-06-01T00:00:00Z".into());
            // Reviews vector is empty — no per-review timestamp available.
        }));
        assert!(since_epoch(&w, PipelineStatus::ChangesRequested).is_some());
    }

    #[test]
    fn parse_iso8601_roundtrips() {
        assert_eq!(parse_iso8601("1970-01-01T00:00:00Z"), Some(0));
        assert!(parse_iso8601("not-a-date").is_none());
    }

    // -- sort key ------------------------------------------------------------

    fn key<'a>(status: PipelineStatus, priority: bool, branch: &'a str) -> RowSortKey<'a> {
        RowSortKey {
            is_main: false,
            status,
            priority,
            since: None,
            issue_number: None,
            branch,
        }
    }

    #[test]
    fn sort_main_worktree_pins_top() {
        let main = RowSortKey {
            is_main: true,
            ..key(PipelineStatus::Coding, false, "main")
        };
        let needs_input = key(PipelineStatus::NeedsInput, true, "feat");
        assert!(main < needs_input);
    }

    #[test]
    fn sort_status_severity_is_primary() {
        let needs = key(PipelineStatus::NeedsInput, false, "a");
        let coding = key(PipelineStatus::Coding, true, "b");
        assert!(needs < coding);
    }

    #[test]
    fn sort_priority_floats_within_status_group() {
        let priority = key(PipelineStatus::Coding, true, "a");
        let plain = key(PipelineStatus::Coding, false, "b");
        assert!(priority < plain);
    }

    #[test]
    fn sort_merged_sinks_to_bottom() {
        let merged = key(PipelineStatus::Merged, true, "a");
        let coding = key(PipelineStatus::Coding, false, "z");
        assert!(coding < merged);
    }

    #[test]
    fn sort_since_older_first_within_group() {
        let old = RowSortKey {
            since: Some(100),
            ..key(PipelineStatus::AwaitingReview, false, "a")
        };
        let recent = RowSortKey {
            since: Some(500),
            ..key(PipelineStatus::AwaitingReview, false, "b")
        };
        assert!(old < recent, "oldest SINCE is most urgent");
    }
}
