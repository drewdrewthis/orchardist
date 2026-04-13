//! Centralized theme definition for TUI styling.
//!
//! Every semantic color role used across the TUI is defined as a named field
//! on [`Theme`]. Rendering functions receive `&Theme` (or access it via
//! `self.theme` on `App`) instead of hardcoding `Color::*` literals.
//!
//! This module also provides [`display_group_color`], a free function that
//! maps a [`DisplayGroup`] to a themed color without modifying `DisplayGroup`'s
//! own API.
//!
//! It also provides [`REPO_COLORS`] and [`repo_color`] for assigning a stable
//! per-repo color by config index throughout the TUI (tab bar, row indicator).

use ratatui::style::Color;

use crate::derive::DisplayGroup;

/// Fixed palette of per-repo colors, cycled by config index.
///
/// Vibrant fruit-inspired colors that pop against the dark forest base theme.
/// Used by [`repo_color`] to assign a consistent color to each configured
/// repository in the tab bar and the row indicator column.
pub const REPO_COLORS: [Color; 6] = [
    Color::Rgb(255, 120, 120), // strawberry — bright enough on dark green bg
    Color::Rgb(255, 180, 40),  // tangerine — warm orange, high contrast
    Color::Rgb(255, 230, 80),  // lemon — vivid yellow
    Color::Rgb(200, 140, 255), // plum — lighter purple for readability
    Color::Rgb(255, 150, 200), // dragonfruit — soft pink
    Color::Rgb(130, 210, 255), // blueberry — brighter blue
];

/// Returns the color for a repository at the given config index.
///
/// Indexes into [`REPO_COLORS`] with modulo wrapping, so repos beyond the
/// palette size cycle back to the start.
///
/// # Examples
///
/// ```
/// use orchard::tui::theme::repo_color;
/// use ratatui::style::Color;
/// assert_eq!(repo_color(0), Color::Rgb(255, 120, 120));
/// assert_eq!(repo_color(6), Color::Rgb(255, 120, 120)); // wraps around
/// ```
pub fn repo_color(index: usize) -> Color {
    REPO_COLORS[index % REPO_COLORS.len()]
}

/// All semantic color roles for the TUI.
///
/// Each field maps to a specific visual purpose (e.g. "error text",
/// "selected-row background", "accent highlight"). `Theme::default()`
/// returns the exact colors previously hardcoded throughout the TUI, so
/// adopting it is a pure refactor with zero visual change.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Theme {
    /// Primary accent color for interactive elements (selected items, key
    /// hints, table borders, loading spinners).
    pub accent: Color,

    /// Color for error messages, failed operations, and destructive states
    /// (CI failing, changes requested, closed PRs, merge conflicts).
    pub error: Color,

    /// Color for warnings, pending states, and advisory notices (unresolved
    /// threads, pending CI, attached-session caution, idle Claude, active
    /// filter/search indicators).
    pub warning: Color,

    /// Color for success states (approved PR, completed operations, active
    /// Claude sessions, reachable hosts, logo).
    pub success: Color,

    /// Subdued color for de-emphasized text (separators, empty fields,
    /// branch column, "no PR", hint descriptions, timestamps).
    pub dimmed: Color,

    /// Background color for the selected/highlighted row.
    pub selected_bg: Color,

    /// Default border color for panels and preview panes.
    pub border: Color,

    /// Color for an actively-working Claude session indicator.
    pub claude_active: Color,

    /// Color for an idle/absent Claude session indicator.
    pub claude_idle: Color,

    /// Color for a Claude session awaiting user input.
    pub claude_needs_input: Color,

    /// Base background color for popups and overlays.
    pub background: Color,

    /// Default foreground text color.
    pub text: Color,

    /// Color for the orchardist (main session) display group.
    pub orchardist: Color,

    /// Color for merge-conflict indicators.
    pub merge_conflict: Color,

    /// Color for the prioritized display group.
    pub prioritized: Color,

    /// Color for merged pull requests.
    pub pr_merged: Color,

    /// Color for hosts whose reachability status is unknown/pending.
    pub host_unknown: Color,

    /// Foreground color for the terminal preview pane content.
    pub preview_content: Color,

    /// Color for search input text and active search/filter labels.
    pub search_highlight: Color,
}

impl Default for Theme {
    fn default() -> Self {
        Self {
            accent: Color::Rgb(0, 200, 120),
            error: Color::Red,
            warning: Color::Yellow,
            success: Color::Rgb(80, 220, 100),
            dimmed: Color::Rgb(100, 110, 100),
            selected_bg: Color::Rgb(30, 50, 30),
            border: Color::Rgb(60, 75, 60),
            claude_active: Color::Green,
            claude_idle: Color::DarkGray,
            claude_needs_input: Color::Red,
            background: Color::Reset, // inherit terminal background
            text: Color::White,
            orchardist: Color::Magenta,
            merge_conflict: Color::Red,
            prioritized: Color::Yellow,
            pr_merged: Color::Magenta,
            host_unknown: Color::Magenta,
            preview_content: Color::Gray,
            search_highlight: Color::Yellow,
        }
    }
}

/// Maps a [`DisplayGroup`] to its themed color without modifying
/// `DisplayGroup`'s own API.
///
/// This keeps the color mapping in the TUI layer rather than in the
/// domain model.
pub fn display_group_color(group: DisplayGroup, theme: &Theme) -> Color {
    match group {
        DisplayGroup::RepoMain => theme.orchardist,
        DisplayGroup::Prioritized => theme.prioritized,
        DisplayGroup::NeedsAttention => theme.error,
        DisplayGroup::ClaudeWorking => theme.claude_active,
        DisplayGroup::ReadyToMerge => theme.accent,
        DisplayGroup::Other => theme.dimmed,
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_accent_is_bright_emerald_green() {
        assert_eq!(Theme::default().accent, Color::Rgb(0, 200, 120));
    }

    #[test]
    fn default_error_is_red() {
        assert_eq!(Theme::default().error, Color::Red);
    }

    #[test]
    fn default_warning_is_yellow() {
        assert_eq!(Theme::default().warning, Color::Yellow);
    }

    #[test]
    fn default_success_is_lush_green() {
        assert_eq!(Theme::default().success, Color::Rgb(80, 220, 100));
    }

    #[test]
    fn default_dimmed_is_mossy_gray_green() {
        assert_eq!(Theme::default().dimmed, Color::Rgb(100, 110, 100));
    }

    #[test]
    fn default_selected_bg_is_deep_forest_floor() {
        assert_eq!(Theme::default().selected_bg, Color::Rgb(30, 50, 30));
    }

    #[test]
    fn default_border_is_dark_forest() {
        assert_eq!(Theme::default().border, Color::Rgb(60, 75, 60));
    }

    #[test]
    fn default_claude_active_is_green() {
        assert_eq!(Theme::default().claude_active, Color::Green);
    }

    #[test]
    fn default_claude_idle_is_dark_gray() {
        assert_eq!(Theme::default().claude_idle, Color::DarkGray);
    }

    #[test]
    fn default_claude_needs_input_is_red() {
        assert_eq!(Theme::default().claude_needs_input, Color::Red);
    }

    #[test]
    fn default_background_inherits_terminal() {
        assert_eq!(Theme::default().background, Color::Reset);
    }

    #[test]
    fn default_text_is_white() {
        assert_eq!(Theme::default().text, Color::White);
    }

    #[test]
    fn default_orchardist_is_magenta() {
        assert_eq!(Theme::default().orchardist, Color::Magenta);
    }

    #[test]
    fn default_merge_conflict_is_red() {
        assert_eq!(Theme::default().merge_conflict, Color::Red);
    }

    #[test]
    fn default_prioritized_is_yellow() {
        assert_eq!(Theme::default().prioritized, Color::Yellow);
    }

    #[test]
    fn default_pr_merged_is_magenta() {
        assert_eq!(Theme::default().pr_merged, Color::Magenta);
    }

    #[test]
    fn default_host_unknown_is_magenta() {
        assert_eq!(Theme::default().host_unknown, Color::Magenta);
    }

    #[test]
    fn default_preview_content_is_gray() {
        assert_eq!(Theme::default().preview_content, Color::Gray);
    }

    #[test]
    fn default_search_highlight_is_yellow() {
        assert_eq!(Theme::default().search_highlight, Color::Yellow);
    }

    #[test]
    fn theme_is_copy() {
        let t = Theme::default();
        let t2 = t;
        assert_eq!(t, t2);
    }

    #[test]
    #[allow(clippy::clone_on_copy)]
    fn theme_is_clone() {
        let t = Theme::default();
        let t2 = t.clone();
        assert_eq!(t, t2);
    }

    #[test]
    fn display_group_repo_main_returns_theme_orchardist() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::RepoMain, &theme),
            theme.orchardist
        );
    }

    #[test]
    fn display_group_needs_attention_returns_theme_error() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::NeedsAttention, &theme),
            theme.error
        );
    }

    #[test]
    fn display_group_claude_working_returns_theme_claude_active() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::ClaudeWorking, &theme),
            theme.claude_active
        );
    }

    #[test]
    fn display_group_ready_to_merge_returns_theme_accent() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::ReadyToMerge, &theme),
            theme.accent
        );
    }

    #[test]
    fn display_group_prioritized_returns_theme_prioritized() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::Prioritized, &theme),
            theme.prioritized
        );
    }

    #[test]
    fn display_group_other_returns_theme_dimmed() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::Other, &theme),
            theme.dimmed
        );
    }

    // -----------------------------------------------------------------------
    // REPO_COLORS / repo_color
    // -----------------------------------------------------------------------

    #[test]
    fn repo_colors_has_six_entries() {
        assert_eq!(REPO_COLORS.len(), 6);
    }

    #[test]
    fn repo_color_index_0_is_strawberry_red() {
        assert_eq!(repo_color(0), Color::Rgb(255, 120, 120));
    }

    #[test]
    fn repo_color_index_1_is_tangerine() {
        assert_eq!(repo_color(1), Color::Rgb(255, 180, 40));
    }

    #[test]
    fn repo_color_index_2_is_lemon_yellow() {
        assert_eq!(repo_color(2), Color::Rgb(255, 230, 80));
    }

    #[test]
    fn repo_color_index_3_is_plum_purple() {
        assert_eq!(repo_color(3), Color::Rgb(200, 140, 255));
    }

    #[test]
    fn repo_color_index_4_is_dragonfruit_pink() {
        assert_eq!(repo_color(4), Color::Rgb(255, 150, 200));
    }

    #[test]
    fn repo_color_index_5_is_blueberry() {
        assert_eq!(repo_color(5), Color::Rgb(130, 210, 255));
    }

    #[test]
    fn repo_color_wraps_at_palette_size() {
        // Index 6 wraps back to strawberry red (same as index 0).
        assert_eq!(repo_color(6), Color::Rgb(255, 120, 120));
        // Index 7 wraps to tangerine (same as index 1).
        assert_eq!(repo_color(7), Color::Rgb(255, 180, 40));
    }

    #[test]
    fn repo_color_large_index_wraps_correctly() {
        // 14 % 6 == 2 → lemon yellow
        assert_eq!(repo_color(14), Color::Rgb(255, 230, 80));
    }
}
