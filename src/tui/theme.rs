//! Centralized theme definition for TUI styling.
//!
//! Every semantic color role used across the TUI is defined as a named field
//! on [`Theme`]. Rendering functions receive `&Theme` (or access it via
//! `self.theme` on `App`) instead of hardcoding `Color::*` literals.
//!
//! This module also provides [`display_group_color`], a free function that
//! maps a [`DisplayGroup`] to a themed color without modifying `DisplayGroup`'s
//! own API.

use ratatui::style::Color;

use crate::derive::DisplayGroup;

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

    /// Color for the shepherd (main session) display group.
    pub shepherd: Color,

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
            accent: Color::Cyan,
            error: Color::Red,
            warning: Color::Yellow,
            success: Color::Green,
            dimmed: Color::DarkGray,
            selected_bg: Color::DarkGray,
            border: Color::DarkGray,
            claude_active: Color::Green,
            claude_idle: Color::DarkGray,
            claude_needs_input: Color::Red,
            background: Color::Black,
            text: Color::White,
            shepherd: Color::Magenta,
            merge_conflict: Color::Red,
            prioritized: Color::White,
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
        DisplayGroup::RepoMain => theme.shepherd,
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
    fn default_accent_is_cyan() {
        assert_eq!(Theme::default().accent, Color::Cyan);
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
    fn default_success_is_green() {
        assert_eq!(Theme::default().success, Color::Green);
    }

    #[test]
    fn default_dimmed_is_dark_gray() {
        assert_eq!(Theme::default().dimmed, Color::DarkGray);
    }

    #[test]
    fn default_selected_bg_is_dark_gray() {
        assert_eq!(Theme::default().selected_bg, Color::DarkGray);
    }

    #[test]
    fn default_border_is_dark_gray() {
        assert_eq!(Theme::default().border, Color::DarkGray);
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
    fn default_background_is_black() {
        assert_eq!(Theme::default().background, Color::Black);
    }

    #[test]
    fn default_text_is_white() {
        assert_eq!(Theme::default().text, Color::White);
    }

    #[test]
    fn default_shepherd_is_magenta() {
        assert_eq!(Theme::default().shepherd, Color::Magenta);
    }

    #[test]
    fn default_merge_conflict_is_red() {
        assert_eq!(Theme::default().merge_conflict, Color::Red);
    }

    #[test]
    fn default_prioritized_is_white() {
        assert_eq!(Theme::default().prioritized, Color::White);
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
    fn display_group_shepherd_returns_theme_shepherd() {
        let theme = Theme::default();
        assert_eq!(
            display_group_color(DisplayGroup::RepoMain, &theme),
            theme.shepherd
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
}
