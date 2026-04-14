//! Fuzzy filtering and match-highlighting for the worktree task list.
//!
//! Uses [`nucleo-matcher`] (the matcher behind Helix/Zed) to fuzzy-search
//! a single concatenated "haystack" built from all visible row fields.
//! Match indices are returned so callers can highlight matched characters
//! inside each rendered cell.

use nucleo_matcher::{
    Config, Matcher, Utf32Str,
    pattern::{AtomKind, CaseMatching, Normalization, Pattern},
};
use ratatui::prelude::*;

use crate::derive::{DisplayGroup, WorktreeRow};
use crate::session::SessionStatus;

// ---------------------------------------------------------------------------
// Haystack construction
// ---------------------------------------------------------------------------

/// All visible field contributions to a row's searchable haystack.
///
/// Fields are separated by spaces. Keeping this as a struct lets us record
/// the byte offset of each field so that match indices can be mapped back
/// to individual cells for per-field highlighting.
#[derive(Debug, Clone)]
pub struct RowHaystack {
    /// The complete concatenated string searched by nucleo.
    pub text: String,
    /// Byte offset where each field begins within `text`.
    /// Fields appear in the same order as [`row_haystack_fields`].
    pub field_offsets: Vec<usize>,
}

/// Returns all visible text fields for a row in a predictable order.
///
/// This is the single source of truth for which fields are searched.
/// Callers that need the full haystack string should use [`row_haystack`];
/// callers that need per-field offsets for highlighting should use [`RowHaystack`].
///
/// Fields (in order):
/// 1. `repo_slug`
/// 2. `branch`
/// 3. Issue number (e.g. "#42")
/// 4. Issue title
/// 5. PR status text (mirrors `pr_status_text` output without Theme)
/// 6. Session status text
/// 7. Display group label
pub fn row_haystack_fields(row: &WorktreeRow) -> Vec<String> {
    vec![
        row.repo_slug.clone(),
        row.branch.clone(),
        row.issue_number
            .map(|n| format!("#{}", n))
            .unwrap_or_default(),
        row.issue_title.clone().unwrap_or_default(),
        pr_status_haystack(row),
        session_status_haystack(row),
        display_group_label(row.display_group),
    ]
}

/// Builds the full searchable haystack string for a row.
///
/// All visible fields are joined with a space so a single fuzzy query
/// can match across any combination of fields.
pub fn row_haystack(row: &WorktreeRow) -> RowHaystack {
    let fields = row_haystack_fields(row);
    let mut text = String::new();
    let mut field_offsets = Vec::with_capacity(fields.len());

    for field in &fields {
        field_offsets.push(text.len());
        text.push_str(field);
        text.push(' ');
    }

    // Remove trailing space.
    if text.ends_with(' ') {
        text.pop();
    }

    RowHaystack {
        text,
        field_offsets,
    }
}

/// Returns PR status text without a Theme dependency.
///
/// Mirrors the logic of `pr_status_text` in `list.rs` exactly — including the
/// same Unicode symbols — so that fuzzy match byte offsets align with the
/// displayed text in each cell.
fn pr_status_haystack(row: &WorktreeRow) -> String {
    let Some(ref pr) = row.pr else {
        if let Some(ref state) = row.issue_state
            && (state == "closed" || state == "completed")
        {
            return format!("\u{2716} issue {}", state);
        }
        return "no PR".to_string();
    };

    let prefix = format!("#{} ", pr.number);
    if pr.state.as_deref() == Some("merged") {
        return format!("{}\u{2713} merged", prefix);
    }
    if pr.state.as_deref() == Some("closed") {
        return format!("{}\u{2716} closed", prefix);
    }
    if pr.review_decision.as_deref() == Some("approved") {
        return format!("{}\u{2713} approved", prefix);
    }
    if pr.review_decision.as_deref() == Some("changes_requested") {
        return format!("{}\u{2716} changes req", prefix);
    }
    if pr.has_conflicts {
        return format!("{}\u{2716} conflict", prefix);
    }
    if pr.unresolved_threads > 0 {
        return format!("{}\u{25cb} unresolved ({})", prefix, pr.unresolved_threads);
    }
    // Prefer the split `ci_code_state` introduced in #218. A code-green
    // gate-blocked PR (e.g. waiting on `check-approval-or-label`) intentionally
    // does NOT surface as "failing" here — that's the regression this
    // feature fixes. A future PR will add a dedicated "gate blocked" label.
    if pr.ci_code_state.as_deref() == Some("failing") {
        return format!("{}\u{2716} failing", prefix);
    }
    if pr.ci_code_state.as_deref() == Some("pending") {
        return format!("{}\u{25d0} pending CI", prefix);
    }
    format!("{}\u{25cb} needs review", prefix)
}

/// Returns a session status text string for the haystack.
fn session_status_haystack(row: &WorktreeRow) -> String {
    if row.sessions.is_empty() {
        return "no session".to_string();
    }
    let has_running = row
        .sessions
        .iter()
        .any(|s| matches!(s.tmux.status, SessionStatus::Running { .. }));
    if has_running {
        "running".to_string()
    } else {
        "dead".to_string()
    }
}

/// Returns the display group label string.
fn display_group_label(group: DisplayGroup) -> String {
    match group {
        DisplayGroup::RepoMain => "repo main".to_string(),
        DisplayGroup::Prioritized => "prioritized".to_string(),
        DisplayGroup::NeedsAttention => "needs attention".to_string(),
        DisplayGroup::ClaudeWorking => "claude working".to_string(),
        DisplayGroup::ReadyToMerge => "ready to merge".to_string(),
        DisplayGroup::Other => "other".to_string(),
    }
}

// ---------------------------------------------------------------------------
// Fuzzy matching
// ---------------------------------------------------------------------------

/// Result of a single fuzzy match operation.
#[derive(Debug, Clone)]
pub struct FuzzyMatch {
    /// Nucleo match score — higher is a better match.
    pub score: u32,
    /// Byte indices (into the full haystack string) of the matched characters.
    pub indices: Vec<u32>,
}

/// Attempts a fuzzy match of `pattern` against `haystack`.
///
/// Returns `None` when the pattern does not match. When the pattern is empty,
/// returns `Some` with score 0 and no indices so rows are not filtered out.
pub fn fuzzy_score(pattern: &str, haystack: &str) -> Option<FuzzyMatch> {
    if pattern.is_empty() {
        return Some(FuzzyMatch {
            score: 0,
            indices: vec![],
        });
    }

    let mut matcher = Matcher::new(Config::DEFAULT);
    let pat = Pattern::new(
        pattern,
        CaseMatching::Ignore,
        Normalization::Smart,
        AtomKind::Fuzzy,
    );

    // nucleo-matcher works with UTF-32 slices; convert via Utf32Str::Ascii
    // when the haystack is ASCII (common case), falling back to a Vec<char>.
    let haystack_bytes = haystack.as_bytes();
    let haystack_chars: Vec<char>;
    let utf32 = if haystack.is_ascii() {
        Utf32Str::Ascii(haystack_bytes)
    } else {
        haystack_chars = haystack.chars().collect();
        Utf32Str::Unicode(&haystack_chars)
    };

    let mut indices: Vec<u32> = Vec::new();
    pat.indices(utf32, &mut matcher, &mut indices)?;

    // `indices` contains the *char* indices into the haystack.
    // When the string is ASCII, char index == byte index.
    // For Unicode haystacks, convert char indices to byte offsets.
    let byte_indices: Vec<u32> = if haystack.is_ascii() {
        indices
    } else {
        let char_to_byte: Vec<usize> = haystack
            .char_indices()
            .map(|(byte_offset, _)| byte_offset)
            .collect();
        indices
            .iter()
            .map(|&ci| {
                char_to_byte
                    .get(ci as usize)
                    .copied()
                    .unwrap_or(ci as usize) as u32
            })
            .collect()
    };

    // We need the score too; re-query (indices already returned Some so score will too).
    let score = pat.score(utf32, &mut matcher)?;

    Some(FuzzyMatch {
        score,
        indices: byte_indices,
    })
}

// ---------------------------------------------------------------------------
// Highlight span computation
// ---------------------------------------------------------------------------

/// Builds a `Vec<Span>` from `text` where characters at `match_byte_indices`
/// are highlighted with `highlight_style` and the rest use `base_style`.
///
/// Indices that fall outside `text` are silently ignored. The returned spans
/// can be assembled into a `Line` for ratatui rendering.
///
/// # Arguments
/// * `text` - The display string (e.g. issue title or branch name).
/// * `field_start` - Byte offset of this field within the full haystack.
/// * `match_byte_indices` - Byte indices of matched chars in the full haystack.
/// * `base_style` - Style for non-matched characters.
/// * `highlight_style` - Style applied to matched characters.
pub fn highlight_spans(
    text: &str,
    field_start: usize,
    match_byte_indices: &[u32],
    base_style: Style,
    highlight_style: Style,
) -> Vec<Span<'static>> {
    if match_byte_indices.is_empty() || text.is_empty() {
        return vec![Span::styled(text.to_owned(), base_style)];
    }

    // Collect the byte indices that fall within this field's range.
    let field_end = field_start + text.len();
    let mut local_indices: Vec<usize> = match_byte_indices
        .iter()
        .filter_map(|&bi| {
            let b = bi as usize;
            if b >= field_start && b < field_end {
                Some(b - field_start)
            } else {
                None
            }
        })
        .collect();

    if local_indices.is_empty() {
        return vec![Span::styled(text.to_owned(), base_style)];
    }

    local_indices.sort_unstable();
    local_indices.dedup();

    // Walk the text as chars, grouping consecutive chars by whether they are highlighted.
    let mut spans: Vec<Span<'static>> = Vec::new();
    let mut current_text = String::new();
    let mut current_highlighted = local_indices.contains(&0);
    let mut byte_pos = 0usize;

    for ch in text.chars() {
        let is_match = local_indices.binary_search(&byte_pos).is_ok();

        if is_match != current_highlighted && !current_text.is_empty() {
            let style = if current_highlighted {
                highlight_style
            } else {
                base_style
            };
            spans.push(Span::styled(current_text.clone(), style));
            current_text.clear();
            current_highlighted = is_match;
        } else if current_text.is_empty() {
            current_highlighted = is_match;
        }

        current_text.push(ch);
        byte_pos += ch.len_utf8();
    }

    if !current_text.is_empty() {
        let style = if current_highlighted {
            highlight_style
        } else {
            base_style
        };
        spans.push(Span::styled(current_text, style));
    }

    spans
}

// ---------------------------------------------------------------------------
// Span truncation
// ---------------------------------------------------------------------------

/// Truncates a `Vec<Span>` from the left to fit within `max_width` characters.
///
/// Mirrors the behavior of [`crate::paths::truncate_left`] but operates on
/// styled spans so that highlight styles are preserved after truncation.
///
/// If the total character width of all spans is within `max_width`, the spans
/// are returned unchanged. Otherwise, characters are removed from the left
/// until the content (plus a leading "…" ellipsis) fits.
///
/// Multi-byte Unicode characters are counted by char, not byte.
pub fn truncate_spans_left(spans: Vec<Span<'static>>, max_width: usize) -> Vec<Span<'static>> {
    let total_chars: usize = spans.iter().map(|s| s.content.chars().count()).sum();
    if total_chars <= max_width {
        return spans;
    }
    if max_width <= 1 {
        return vec![Span::raw("…".to_string())];
    }

    // We need to keep only the last (max_width - 1) chars, prefixed with "…".
    let keep = max_width - 1;
    let skip = total_chars - keep;

    let mut result: Vec<Span<'static>> = Vec::new();
    let mut skipped = 0usize;
    let mut ellipsis_prepended = false;

    for span in spans {
        let span_chars: Vec<char> = span.content.chars().collect();
        let span_len = span_chars.len();

        if skipped + span_len <= skip {
            // Skip this span entirely.
            skipped += span_len;
            continue;
        }

        // Partial or full inclusion of this span.
        let chars_to_skip_in_span = skip.saturating_sub(skipped);
        skipped += chars_to_skip_in_span;

        let remaining: String = span_chars[chars_to_skip_in_span..].iter().collect();
        if remaining.is_empty() {
            continue;
        }

        if !ellipsis_prepended {
            // Prepend "…" with the same style as the first kept span.
            result.push(Span::styled(format!("…{}", remaining), span.style));
            ellipsis_prepended = true;
        } else {
            result.push(Span::styled(remaining, span.style));
        }
    }

    if result.is_empty() {
        result.push(Span::raw("…".to_string()));
    }

    result
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
#[allow(deprecated)] // PrInfo.checks_state — fixtures still populate the legacy field for now
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};
    use crate::session::{EnrichedSession, Host, SessionStatus, TmuxSessionInfo};

    fn base_row() -> WorktreeRow {
        WorktreeRow {
            repo_slug: "owner/repo".to_string(),
            worktree_path: "/workspace/repo".to_string(),
            branch: "feat/issue-42".to_string(),
            worktree_host: None,
            issue_number: Some(42),
            issue_title: Some("Fix Azure integration bug".to_string()),
            issue_state: None,
            issue_labels: vec![],
            issue_assignees: vec![],
            issue_created_at: None,
            issue_updated_at: None,
            issue_blocked_by: vec![],
            issue_sub_issues: vec![],
            issue_parent: None,
            pr: None,
            sessions: vec![],
            display_group: DisplayGroup::NeedsAttention,
            is_main_worktree: false,
            worktree_ahead: None,
            worktree_behind: None,
            worktree_last_commit_at: None,
        }
    }

    fn make_session_running(name: &str) -> EnrichedSession {
        EnrichedSession {
            tmux: TmuxSessionInfo {
                host: Host::Local,
                name: name.to_string(),
                status: SessionStatus::Running { attached: false },
            },
            claude: None,
            windows: vec![],
            panes: vec![],
            started_at: None,
            last_activity_at: None,
        }
    }

    // -------------------------------------------------------------------------
    // Haystack coverage
    // -------------------------------------------------------------------------

    #[test]
    fn haystack_includes_repo_slug() {
        let row = base_row();
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("owner/repo"),
            "repo_slug missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_branch() {
        let row = base_row();
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("feat/issue-42"),
            "branch missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_issue_number() {
        let row = base_row();
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("#42"),
            "issue number missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_issue_title() {
        let row = base_row();
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("Fix Azure integration bug"),
            "issue title missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_pr_status_approved() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 99,
                branch: "feat/issue-42".to_string(),
                review_decision: Some("approved".to_string()),
                ..PrInfo::default()
            }),
            ..base_row()
        };
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("\u{2713} approved"),
            "pr approved status with symbol missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_pr_status_failing_with_symbol() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 12,
                branch: "feat/issue-42".to_string(),
                checks_state: Some("failing".to_string()),
                ci_code_state: Some("failing".to_string()),
                ..PrInfo::default()
            }),
            ..base_row()
        };
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("\u{2716} failing"),
            "pr failing status with symbol missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_no_pr_text() {
        let row = base_row(); // no PR
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("no PR"),
            "no PR text missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_merged_pr_text() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 7,
                branch: "feat/issue-42".to_string(),
                state: Some("merged".to_string()),
                ..PrInfo::default()
            }),
            ..base_row()
        };
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("merged"),
            "merged status missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_session_status_running() {
        let row = WorktreeRow {
            sessions: vec![make_session_running("mysession")],
            ..base_row()
        };
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("running"),
            "session running status missing from haystack: {}",
            haystack.text
        );
    }

    #[test]
    fn haystack_includes_display_group_label() {
        let row = base_row(); // NeedsAttention
        let haystack = row_haystack(&row);
        assert!(
            haystack.text.contains("needs attention"),
            "display group label missing from haystack: {}",
            haystack.text
        );
    }

    // -------------------------------------------------------------------------
    // Ranking
    // -------------------------------------------------------------------------

    #[test]
    fn better_match_ranks_higher() {
        // "azure" appears as a complete word in the first haystack (strong match).
        // In the second haystack the same characters appear but scattered further apart.
        let strong = fuzzy_score("azure", "azure storage fix").unwrap();
        // "a...z...u...r...e" as a very spread-out subsequence.
        let weak = fuzzy_score("azure", "analyzing zone updates in remote env").unwrap();
        assert!(
            strong.score > weak.score,
            "strong match score {} should exceed weak match score {}",
            strong.score,
            weak.score
        );
    }

    #[test]
    fn no_match_returns_none() {
        let result = fuzzy_score("xqzjkl", "owner/repo feat/issue-42 no PR");
        assert!(result.is_none(), "expected no match for nonsense pattern");
    }

    #[test]
    fn empty_pattern_returns_zero_score_for_any_haystack() {
        let result = fuzzy_score("", "any text here");
        assert!(result.is_some(), "empty pattern should match everything");
        assert_eq!(result.unwrap().score, 0);
    }

    // -------------------------------------------------------------------------
    // Highlight span computation
    // -------------------------------------------------------------------------

    #[test]
    fn highlight_spans_no_indices_returns_single_span() {
        let spans = highlight_spans(
            "hello world",
            0,
            &[],
            Style::default(),
            Style::default().add_modifier(Modifier::BOLD),
        );
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].content.as_ref(), "hello world");
    }

    #[test]
    fn highlight_spans_splits_on_match_indices() {
        // Match index 0 (h) from field at offset 0.
        let highlight = Style::default().add_modifier(Modifier::BOLD);
        let base = Style::default();
        let spans = highlight_spans("hello", 0, &[0], base, highlight);
        // First char 'h' highlighted, remainder 'ello' base.
        assert_eq!(spans.len(), 2, "expected 2 spans, got: {:?}", spans);
        assert_eq!(spans[0].content.as_ref(), "h");
        assert_eq!(spans[0].style, highlight);
        assert_eq!(spans[1].content.as_ref(), "ello");
        assert_eq!(spans[1].style, base);
    }

    #[test]
    fn highlight_spans_with_field_offset() {
        // Field "Azure" starts at byte 4 in the haystack "Fix Azure".
        // Match indices 4,5,6,7,8 refer to A,z,u,r,e.
        let indices: Vec<u32> = vec![4, 5, 6, 7, 8];
        let highlight = Style::default().add_modifier(Modifier::BOLD);
        let base = Style::default();
        let spans = highlight_spans("Azure", 4, &indices, base, highlight);
        // All chars match → single highlighted span.
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].content.as_ref(), "Azure");
        assert_eq!(spans[0].style, highlight);
    }

    #[test]
    fn highlight_spans_indices_outside_field_are_ignored() {
        // Field "hello" at offset 10. Indices 0..4 are in another field.
        let spans = highlight_spans(
            "hello",
            10,
            &[0, 1, 2, 3],
            Style::default(),
            Style::default().add_modifier(Modifier::BOLD),
        );
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].content.as_ref(), "hello");
    }

    // -------------------------------------------------------------------------
    // truncate_spans_left
    // -------------------------------------------------------------------------

    #[test]
    fn truncate_spans_left_within_width_returns_unchanged() {
        let spans = vec![
            Span::raw("hello".to_string()),
            Span::styled(
                " world".to_string(),
                Style::default().add_modifier(Modifier::BOLD),
            ),
        ];
        let result = truncate_spans_left(spans.clone(), 20);
        assert_eq!(result.len(), 2);
        assert_eq!(result[0].content.as_ref(), "hello");
        assert_eq!(result[1].content.as_ref(), " world");
    }

    #[test]
    fn truncate_spans_left_exceeding_width_prepends_ellipsis_and_trims_left() {
        // "hello world" = 11 chars; max_width = 7 → keep last 6 chars = "world" + 1 more
        // skip 4 chars: "hell" → keep "o world" but only 6 chars after "…"
        // Actually: keep = 6, skip = 5 ("hello"), result = "…world"
        let spans = vec![Span::raw("hello world".to_string())];
        let result = truncate_spans_left(spans, 7);
        // total=11, keep=6, skip=5 → skip "hello", keep " world" (6 chars)
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].content.as_ref(), "… world");
    }

    #[test]
    fn truncate_spans_left_preserves_style_of_kept_span() {
        let bold = Style::default().add_modifier(Modifier::BOLD);
        let spans = vec![
            Span::raw("prefix".to_string()),
            Span::styled("suffix".to_string(), bold),
        ];
        // total=12, max_width=8, keep=7, skip=5 → skip "prefi", keep "x" + "suffix"
        // First kept span fragment: "x" from "prefix", styled as raw (no style)
        // Then full "suffix" span with bold
        let result = truncate_spans_left(spans, 8);
        assert!(result.len() >= 2);
        // Last span should retain bold style.
        let last = result.last().unwrap();
        assert_eq!(last.content.as_ref(), "suffix");
        assert_eq!(last.style, bold);
    }

    #[test]
    fn truncate_spans_left_zero_width_returns_ellipsis() {
        let spans = vec![Span::raw("hello".to_string())];
        let result = truncate_spans_left(spans, 0);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].content.as_ref(), "…");
    }

    #[test]
    fn truncate_spans_left_width_one_returns_ellipsis() {
        let spans = vec![Span::raw("hello".to_string())];
        let result = truncate_spans_left(spans, 1);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].content.as_ref(), "…");
    }

    // -------------------------------------------------------------------------
    // Main worktree bypass
    // -------------------------------------------------------------------------

    #[test]
    fn fuzzy_score_is_some_for_empty_pattern() {
        // Verifies that empty pattern always matches (bypass logic in visible_tasks_filtered).
        assert!(fuzzy_score("", "anything").is_some());
    }

    #[test]
    fn row_haystack_fields_has_expected_count() {
        // 7 fields in the defined order.
        let fields = row_haystack_fields(&base_row());
        assert_eq!(
            fields.len(),
            7,
            "expected 7 haystack fields, got {}",
            fields.len()
        );
    }
}
