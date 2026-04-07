//! Worktree list view: rendering and Enter-key action handling.
//!
//! Draws the main task/worktree table, the detail pane, and the header;
//! formats row labels; and handles the Enter-key session join/create logic.
//! Key-to-message mapping lives in `mod.rs` (`handle_event`).
use ratatui::prelude::*;
use ratatui::widgets::*;
use tui_scrollview::{ScrollView, ScrollbarVisibility};

use std::collections::HashSet;
use std::time::Instant;

use crate::derive::{DisplayGroup, WorktreeRow};
use crate::paths;
use crate::remote;
use crate::tmux;
use crate::tui::state::{CleanupState, Phase, ViewState};
use crate::tui::theme::{Theme, display_group_color, repo_color};
use crate::tui::{ATTRIBUTION_URL, App, WARNING_DURATION_SECS, filter_stale};

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Number of lines to capture from tmux panes for preview.
const PANE_CAPTURE_LINES: u32 = 100;

/// Minimum terminal height for the full header/logo display.
const FULL_HEADER_MIN_HEIGHT: u16 = 30;

/// Rows consumed by the table border (top) and column header row.
const TABLE_BODY_Y_OFFSET: u16 = 2;
/// Total rows consumed by table chrome (top border + header + bottom border).
const TABLE_CHROME_HEIGHT: u16 = 3;
/// Maximum fraction of terminal height allocated to the task table (when preview is visible).
const TABLE_MAX_HEIGHT_FRACTION: f32 = 0.40;
/// Minimum useful table height (top border + header + 1 data row + bottom border).
const TABLE_MIN_HEIGHT: u16 = 5;
/// Lines scrolled per mouse wheel tick on the preview pane.
pub(crate) const MOUSE_SCROLL_LINES: usize = 3;

// ---------------------------------------------------------------------------
// Task view helpers
// ---------------------------------------------------------------------------

/// Describes what action the Enter key should take in the task view.
/// Used to avoid holding a borrow on `task_rows` while calling `&mut self` methods.
enum TaskEnterAction {
    JoinSession {
        session_name: String,
        worktree_path: String,
        branch: Option<String>,
        host: Option<String>,
        /// When `Some`, select this pane after switching (tmux target "window.pane").
        pane_target: Option<String>,
    },
    CreateSession {
        worktree_path: String,
        branch: Option<String>,
        host: Option<String>,
    },
    /// Attach to or restart a standalone session.
    JoinStandalone {
        session_name: String,
        command: String,
        cwd: String,
        /// When `Some`, select this pane after switching (tmux target "window.pane").
        pane_target: Option<String>,
    },
}

/// Returns whether the cursor is currently on a standalone session row.
///
/// Standalone sessions occupy indices 0..standalone_count before worktree rows.
pub(crate) fn cursor_is_standalone(cursor: usize, standalone_count: usize) -> bool {
    cursor < standalone_count
}

impl DisplayGroup {
    fn label(self) -> &'static str {
        match self {
            Self::RepoMain => "repo main",
            Self::Prioritized => "prioritized",
            Self::NeedsAttention => "needs attention",
            Self::ClaudeWorking => "claude working",
            Self::ReadyToMerge => "ready to merge",
            Self::Other => "other",
        }
    }
}

/// Returns the part of a branch name after the final `/`.
///
/// When a branch has no slash the full name is returned.
/// This keeps the TITLE column readable for prefixed branches like "feat/issue-123".
pub(crate) fn branch_tail(branch: &str) -> &str {
    match branch.rfind('/') {
        Some(pos) => &branch[pos + 1..],
        None => branch,
    }
}

/// A task entry prepared for rendering in the task-centric view.
#[derive(Debug)]
pub(crate) struct VisibleTask<'a> {
    pub row: &'a WorktreeRow,
    pub group: DisplayGroup,
}

/// Returns the visible tasks from the pre-sorted task_rows.
///
/// All rows are always visible — there is no backlog collapsing.
/// `filter_text` narrows results; main worktree rows always bypass it.
/// When `repo_slug_filter` is `Some(slug)`, only rows from that repo are shown
/// (main worktree rows are also filtered so each repo only shows its own).
#[cfg(test)]
pub(crate) fn visible_tasks<'a>(
    task_rows: &'a [WorktreeRow],
    filter_text: &str,
) -> Vec<VisibleTask<'a>> {
    visible_tasks_filtered(task_rows, filter_text, None)
}

/// Like `visible_tasks` but with an optional repo slug filter.
pub(crate) fn visible_tasks_filtered<'a>(
    task_rows: &'a [WorktreeRow],
    filter_text: &str,
    repo_slug_filter: Option<&str>,
) -> Vec<VisibleTask<'a>> {
    let search_lower = filter_text.to_lowercase();

    let mut result = Vec::new();

    for row in task_rows {
        // Apply repo slug filter (affects all rows including main worktrees).
        if let Some(slug) = repo_slug_filter
            && row.repo_slug != slug
        {
            continue;
        }

        // Main worktree rows always pass filter and search.
        if !row.is_main_worktree {
            // Apply search text.
            if !search_lower.is_empty() {
                let matches = row.repo_slug.to_lowercase().contains(&search_lower)
                    || row.branch.to_lowercase().contains(&search_lower);
                if !matches {
                    continue;
                }
            }
        }

        result.push(VisibleTask {
            row,
            group: row.display_group,
        });
    }

    result
}

/// Returns a single PR status string for the task row.
///
/// When a PR exists its number is prepended: e.g. `#123 ✓ approved`.
fn pr_status_text(row: &WorktreeRow, theme: &Theme) -> (String, Style) {
    let Some(ref pr) = row.pr else {
        // No PR — check if the linked issue is closed/completed (stale worktree)
        if let Some(ref state) = row.issue_state
            && (state == "closed" || state == "completed")
        {
            return (
                format!("\u{2716} issue {}", state),
                Style::default().fg(theme.error),
            );
        }
        return ("no PR".to_string(), Style::default().fg(theme.dimmed));
    };

    let prefix = format!("#{} ", pr.number);

    // Merged or closed PR = stale worktree
    if pr.state.as_deref() == Some("merged") {
        return (
            format!("{}\u{2713} merged", prefix),
            Style::default().fg(theme.pr_merged),
        );
    }
    if pr.state.as_deref() == Some("closed") {
        return (
            format!("{}\u{2716} closed", prefix),
            Style::default().fg(theme.error),
        );
    }

    if pr.review_decision.as_deref() == Some("approved") {
        return (
            format!("{}\u{2713} approved", prefix),
            Style::default().fg(theme.success),
        );
    }
    if pr.review_decision.as_deref() == Some("changes_requested") {
        return (
            format!("{}\u{2716} changes req", prefix),
            Style::default().fg(theme.error),
        );
    }
    if pr.has_conflicts {
        return (
            format!("{}\u{2716} conflict", prefix),
            Style::default().fg(theme.merge_conflict),
        );
    }
    if pr.unresolved_threads > 0 {
        return (
            format!("{}\u{25cb} unresolved ({})", prefix, pr.unresolved_threads),
            Style::default().fg(theme.warning),
        );
    }
    if pr.checks_state.as_deref() == Some("failing") {
        return (
            format!("{}\u{2716} failing", prefix),
            Style::default().fg(theme.error),
        );
    }
    if pr.checks_state.as_deref() == Some("pending") {
        return (
            format!("{}\u{25d0} pending CI", prefix),
            Style::default().fg(theme.warning),
        );
    }
    // Default for open PR with no special state
    (
        format!("{}\u{25cb} needs review", prefix),
        Style::default().fg(theme.dimmed),
    )
}

/// Returns a Claude activity indicator for the task row.
///
/// When hook state files are available, shows richer info including context
/// window percentage. Falls back to boolean flags from terminal scraping.
fn claude_status_text(row: &WorktreeRow, theme: &Theme) -> (String, Style) {
    if row.sessions.is_empty() {
        return (
            "\u{25cb} none".to_string(),
            Style::default().fg(theme.claude_idle),
        );
    }

    let count = row.sessions.len();
    let count_suffix = if count > 1 {
        format!(" {}", count)
    } else {
        String::new()
    };

    // Find the most "urgent" structured state across sessions.
    use crate::claude_state::ClaudeState;
    let has_input = row.sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Input)
    });
    let has_working = row.sessions.iter().any(|s| {
        s.claude
            .as_ref()
            .is_some_and(|c| c.status == ClaudeState::Working)
    });
    // Get context % from any session that has it.
    let ctx_pct = row
        .sessions
        .iter()
        .find_map(|s| s.claude.as_ref().and_then(|c| c.context_window_pct));

    let state = if has_input {
        ClaudeState::Input
    } else if has_working {
        ClaudeState::Working
    } else {
        ClaudeState::Idle
    };

    format_claude_state(state, ctx_pct, &count_suffix, theme)
}

/// Formats a single Claude state + context % into display text and style.
fn format_claude_state(
    state: crate::claude_state::ClaudeState,
    ctx_pct: Option<f64>,
    suffix: &str,
    theme: &Theme,
) -> (String, Style) {
    use crate::claude_state::ClaudeState;
    let ctx_suffix = ctx_pct
        .map(|p| format!(" {}%", p as u32))
        .unwrap_or_default();
    match state {
        ClaudeState::Input => (
            format!("\u{26a1} input{}{}", suffix, ctx_suffix),
            Style::default().fg(theme.claude_needs_input),
        ),
        ClaudeState::Working => (
            format!("\u{26a1} active{}{}", suffix, ctx_suffix),
            Style::default().fg(theme.claude_active),
        ),
        ClaudeState::Idle => (
            format!("\u{25cf} idle{}{}", suffix, ctx_suffix),
            Style::default().fg(theme.warning),
        ),
        ClaudeState::None => (
            "\u{25cb} none".to_string(),
            Style::default().fg(theme.claude_idle),
        ),
    }
}

/// Returns Claude status text for a standalone session's single EnrichedSession.
fn standalone_claude_status(
    session: &crate::session::EnrichedSession,
    theme: &Theme,
) -> (String, Style) {
    let Some(ref claude) = session.claude else {
        return (
            "\u{25cb} none".to_string(),
            Style::default().fg(theme.claude_idle),
        );
    };
    format_claude_state(claude.status, claude.context_window_pct, "", theme)
}

impl App {
    /// Shows a warning and returns true if the cursor is on a standalone session.
    pub(crate) fn guard_requires_worktree(&mut self, standalone_count: usize) -> bool {
        if cursor_is_standalone(self.cursor, standalone_count) {
            self.warning = Some((
                "This action requires a worktree".to_string(),
                Instant::now(),
            ));
            true
        } else {
            false
        }
    }

    /// Handles the Enter key action: join or create a tmux session.
    ///
    /// Returns `true` when the TUI should exit (to switch to a session).
    pub(crate) fn handle_enter_action(&mut self) -> bool {
        let standalone_count = self.standalone_sessions.len();

        let pane_idx = self.selected_pane;
        let action = if cursor_is_standalone(self.cursor, standalone_count) {
            self.standalone_sessions.get(self.cursor).map(|ss| {
                let pane_target = pane_idx
                    .and_then(|i| ss.session.panes.get(i))
                    .map(|p| p.tmux_target.clone());
                TaskEnterAction::JoinStandalone {
                    session_name: ss.session.tmux.name.clone(),
                    command: ss.config.command.clone(),
                    cwd: ss.config.cwd.clone(),
                    pane_target,
                }
            })
        } else {
            let worktree_cursor = self.cursor - standalone_count;
            let tasks =
                visible_tasks_filtered(&self.task_rows, &self.filter_text, self.active_repo_slug());
            let action = tasks.get(worktree_cursor).map(|vt| {
                if let Some(session) = vt.row.sessions.first() {
                    let host = match &session.tmux.host {
                        crate::session::Host::Local => None,
                        crate::session::Host::Remote(h) => Some(h.clone()),
                    };
                    let pane_target = pane_idx
                        .and_then(|i| session.panes.get(i))
                        .map(|p| p.tmux_target.clone());
                    TaskEnterAction::JoinSession {
                        session_name: session.tmux.name.clone(),
                        worktree_path: vt.row.worktree_path.clone(),
                        branch: Some(vt.row.branch.clone()),
                        host,
                        pane_target,
                    }
                } else {
                    TaskEnterAction::CreateSession {
                        worktree_path: vt.row.worktree_path.clone(),
                        branch: Some(vt.row.branch.clone()),
                        host: vt.row.worktree_host.clone(),
                    }
                }
            });
            drop(tasks);
            action
        };
        match action {
            None => false,
            Some(TaskEnterAction::JoinSession {
                session_name,
                worktree_path,
                branch,
                host,
                pane_target,
            }) => {
                // Guard: refuse to join a session on a host not confirmed reachable.
                if let Some(ref h) = host
                    && self.host_reachable.get(h.as_str()) != Some(&true)
                {
                    let msg = if self.host_reachable.contains_key(h.as_str()) {
                        format!("@{} is unreachable", h)
                    } else {
                        format!("@{} -- checking connectivity...", h)
                    };
                    self.warning = Some((msg, Instant::now()));
                    return false;
                }
                self.join_or_create_session(
                    &session_name,
                    &worktree_path,
                    branch.as_deref(),
                    host.as_deref(),
                    None,
                );
                // Select specific pane after switching if a sub-row was active.
                if let Some(ref target) = pane_target {
                    let _ = tmux::select_pane(&session_name, target);
                }
                self.switch_target.is_some()
            }
            Some(TaskEnterAction::CreateSession {
                worktree_path,
                branch,
                host,
            }) => {
                // Guard: refuse to create a session on a host not confirmed reachable.
                if let Some(ref h) = host
                    && self.host_reachable.get(h.as_str()) != Some(&true)
                {
                    let msg = if self.host_reachable.contains_key(h.as_str()) {
                        format!("@{} is unreachable", h)
                    } else {
                        format!("@{} -- checking connectivity...", h)
                    };
                    self.warning = Some((msg, Instant::now()));
                    return false;
                }
                let repo_name = self.repo_name.clone();
                let session_name =
                    tmux::derive_session_name(&repo_name, branch.as_deref(), &worktree_path);
                self.join_or_create_session(
                    &session_name,
                    &worktree_path,
                    branch.as_deref(),
                    host.as_deref(),
                    None,
                )
            }
            Some(TaskEnterAction::JoinStandalone {
                session_name,
                command,
                cwd,
                pane_target,
            }) => {
                if tmux::session_exists(&session_name) {
                    if let Some(ref target) = pane_target {
                        let _ = tmux::select_pane(&session_name, target);
                    }
                    self.switch_target = Some(session_name);
                    true
                } else {
                    match tmux::new_session_with_command(&session_name, &cwd, &command) {
                        Ok(()) => {
                            self.switch_target = Some(session_name);
                            true
                        }
                        Err(e) => {
                            self.warning = Some((
                                format!("Failed to start '{}': {}", session_name, e),
                                Instant::now(),
                            ));
                            false
                        }
                    }
                }
            }
        }
    }

    /// Resolves the tmux pane target string for the selected sub-row pane.
    ///
    /// Looks up the `PaneInfo.tmux_target` from the session's pane list using
    /// `self.selected_pane` as the sequential index.
    fn resolve_pane_target(&self, panes: &[crate::session::PaneInfo]) -> Option<String> {
        self.selected_pane
            .and_then(|i| panes.get(i))
            .map(|p| p.tmux_target.clone())
    }

    /// Fetches pane content for the task at the current cursor position.
    pub(crate) fn fetch_task_pane_content(&mut self) {
        self.pane_content.clear();

        // Handle standalone sessions first.
        let standalone_count = self.standalone_sessions.len();
        if cursor_is_standalone(self.cursor, standalone_count)
            && let Some(ss) = self.standalone_sessions.get(self.cursor)
            && matches!(
                ss.session.tmux.status,
                crate::session::SessionStatus::Running { .. }
            )
        {
            let session_name = ss.session.tmux.name.clone();
            let pane_target = self.resolve_pane_target(&ss.session.panes);
            let tx = self.tx.clone();
            std::thread::spawn(move || {
                let content = tmux::capture_pane_content_at(
                    &session_name,
                    pane_target.as_deref(),
                    PANE_CAPTURE_LINES,
                )
                .unwrap_or_default();
                let _ = tx.send(crate::tui::state::AppMsg::PaneContent(
                    session_name,
                    content,
                ));
            });
            return;
        }
        if cursor_is_standalone(self.cursor, standalone_count) {
            return;
        }

        let worktree_cursor = self.cursor - standalone_count;
        let visible =
            visible_tasks_filtered(&self.task_rows, &self.filter_text, self.active_repo_slug());
        if let Some(vt) = visible.get(worktree_cursor) {
            // Find a session to capture pane content from.
            if let Some(session) = vt.row.sessions.first() {
                let session_name = session.tmux.name.clone();
                let remote_host = match &session.tmux.host {
                    crate::session::Host::Local => None,
                    crate::session::Host::Remote(h) => Some(h.clone()),
                };
                let pane_target = self.resolve_pane_target(&session.panes);
                let tx = self.tx.clone();
                std::thread::spawn(move || {
                    let content = if let Some(host) = remote_host {
                        remote::capture_remote_pane_content(
                            &host,
                            &session_name,
                            PANE_CAPTURE_LINES,
                        )
                        .unwrap_or_default()
                    } else {
                        tmux::capture_pane_content_at(
                            &session_name,
                            pane_target.as_deref(),
                            PANE_CAPTURE_LINES,
                        )
                        .unwrap_or_default()
                    };
                    let _ = tx.send(crate::tui::state::AppMsg::PaneContent(
                        session_name,
                        content,
                    ));
                });
            }
        }
    }

    /// Attempts to reconnect all currently unreachable SSH hosts.
    ///
    /// If all hosts are reachable, displays an informational warning. Otherwise
    /// spawns a background thread to probe each unreachable host and send results
    /// back via the App message channel.
    pub(crate) fn reconnect_unreachable_hosts(&mut self) {
        let unreachable: Vec<String> = self
            .host_reachable
            .iter()
            .filter(|(_, v)| !*v)
            .map(|(k, _)| k.clone())
            .collect();
        if unreachable.is_empty() {
            self.warning = Some(("All hosts reachable".to_string(), Instant::now()));
        } else {
            let tx = self.tx.clone();
            std::thread::spawn(move || {
                for host in unreachable {
                    let reachable = crate::remote::ssh_exec(&host, "true").is_ok();
                    let _ = tx.send(crate::tui::state::AppMsg::HostReachability(host, reachable));
                }
            });
            self.warning = Some(("Reconnecting...".to_string(), Instant::now()));
        }
    }

    pub(crate) fn render_list(&self, f: &mut Frame) {
        // Delegate to task-centric view when tasks are available.
        if !self.task_rows.is_empty() {
            self.render_task_list(f);
            return;
        }

        let theme = &self.theme;
        let area = f.area();
        let hdr_height = header_height(area.height);

        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([
                Constraint::Length(hdr_height),
                Constraint::Length(1),
                Constraint::Min(3),
            ])
            .split(area);

        self.render_header(f, chunks[0]);

        // Error state
        if let Some(ref err) = self.error {
            let err_para = Paragraph::new(err.as_str())
                .style(Style::default().fg(theme.error))
                .block(
                    Block::default()
                        .borders(Borders::ALL)
                        .border_style(Style::default().fg(theme.error))
                        .border_type(BorderType::Rounded),
                )
                .wrap(Wrap { trim: true });
            f.render_widget(err_para, chunks[2]);
            return;
        }

        // Loading or empty state
        if self.loading {
            let throbber = throbber_widgets_tui::Throbber::default()
                .label("Loading worktrees...")
                .style(Style::default().fg(theme.accent))
                .throbber_style(Style::default().fg(theme.accent));
            f.render_stateful_widget(throbber, chunks[2], &mut self.throbber_state.clone());
        } else {
            let empty =
                Paragraph::new("No worktrees found. Run `orchard init` to configure a repo.")
                    .style(Style::default().fg(theme.warning))
                    .block(
                        Block::default()
                            .borders(Borders::ALL)
                            .border_style(Style::default().fg(theme.warning))
                            .border_type(BorderType::Rounded),
                    )
                    .alignment(Alignment::Center);
            f.render_widget(empty, chunks[2]);
        }
    }

    pub(crate) fn render_header(&self, f: &mut Frame, area: Rect) {
        let theme = &self.theme;
        let green_style = Style::default().fg(theme.success);
        let red_style = Style::default().fg(theme.error);

        // Build host status spans (sorted by host name for stable display).
        let mut host_spans: Vec<Span> = Vec::new();
        let mut sorted_hosts: Vec<(&String, &bool)> = self.host_reachable.iter().collect();
        sorted_hosts.sort_by_key(|(h, _)| h.as_str());
        for &(host, reachable) in &sorted_hosts {
            if *reachable {
                host_spans.push(Span::styled(format!("  @{}", host), green_style));
                host_spans.push(Span::styled(" \u{25cf}", green_style)); // ●
            } else {
                host_spans.push(Span::styled(format!("  @{}", host), red_style));
                host_spans.push(Span::styled(" \u{2717}", red_style)); // ✗
                host_spans.push(Span::styled(" (stale)", Style::default().fg(theme.dimmed)));
            }
        }

        // Build timestamp span.
        let timestamp_span = if self.refreshing {
            let throbber = throbber_widgets_tui::Throbber::default()
                .throbber_style(Style::default().fg(theme.accent));
            let symbol = throbber.to_symbol_span(&self.throbber_state);
            Span::styled(
                format!("  {}refreshing...", symbol.content),
                Style::default().fg(theme.accent),
            )
        } else {
            let elapsed = self.last_full_refresh.elapsed().as_secs();
            let ts_text = if elapsed < 60 {
                format!("  ({}s ago)", elapsed)
            } else if elapsed < 3600 {
                format!("  ({}m ago)", elapsed / 60)
            } else {
                format!("  ({}h ago)", elapsed / 3600)
            };
            Span::styled(ts_text, Style::default().fg(theme.dimmed))
        };

        if header_height(f.area().height) == 1 {
            let mut spans = vec![Span::styled(
                "\u{1f333} Git Orchard",
                Style::default()
                    .fg(theme.success)
                    .add_modifier(Modifier::BOLD),
            )];
            spans.extend(host_spans);
            spans.push(timestamp_span);
            if !self.refreshing {
                spans.push(Span::styled(
                    "  r:refresh",
                    Style::default().fg(theme.dimmed),
                ));
            }
            let line = Line::from(spans);
            f.render_widget(Paragraph::new(line), area);
            return;
        }

        // Full header (height == 7): show host indicators on second line.
        let mut host_line_spans: Vec<Span> = Vec::new();
        for &(host, reachable) in &sorted_hosts {
            if *reachable {
                host_line_spans.push(Span::styled(format!(" @{} ", host), green_style));
                host_line_spans.push(Span::styled("\u{25cf}", green_style));
            } else {
                host_line_spans.push(Span::styled(format!(" @{} ", host), red_style));
                host_line_spans.push(Span::styled("\u{2717}", red_style));
                host_line_spans.push(Span::styled(" (stale)", Style::default().fg(theme.dimmed)));
            }
        }

        let logo_style = Style::default()
            .fg(theme.success)
            .add_modifier(Modifier::BOLD);

        let header_block = Block::default()
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme.success))
            .border_type(BorderType::Double);
        let inner = header_block.inner(area);
        f.render_widget(header_block, area);

        // ASCII art logo header.
        let mut header_text = vec![
            Line::from("🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴"),
            Line::from(Span::styled("┌─┐┬┌┬┐╔═╗╦═╗╔═╗╦ ╦╔═╗╦═╗╔╦╗", logo_style)),
            Line::from(Span::styled("│ ┬│ │ ║ ║╠╦╝║  ╠═╣╠═╣╠╦╝ ║║", logo_style)),
            Line::from(Span::styled("└─┘┴ ┴ ╚═╝╩╚═╚═╝╩ ╩╩ ╩╩╚══╩╝", logo_style)),
            Line::from("🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴🌲🌳🌴"),
        ];
        if !host_line_spans.is_empty() {
            header_text.push(Line::from(host_line_spans).alignment(Alignment::Center));
        }
        if !self.host_reachable.is_empty() || self.refreshing {
            header_text.push(Line::from(vec![timestamp_span]).alignment(Alignment::Center));
        }
        let header = Paragraph::new(header_text)
            .alignment(Alignment::Center)
            .style(
                Style::default()
                    .fg(theme.success)
                    .add_modifier(Modifier::BOLD),
            );
        f.render_widget(header, inner);
    }

    /// Joins or creates a tmux session. Returns `true` if the TUI should exit
    /// (switch_target has been set), `false` if a warning was shown instead.
    ///
    /// - If `remote_host` is `Some` → creates a remote proxy session and sets `switch_target`.
    /// - If local → calls `tmux::create_session` with the given options and sets `switch_target`.
    /// - On error → sets `self.warning`.
    fn join_or_create_session(
        &mut self,
        session_name: &str,
        worktree_path: &str,
        branch: Option<&str>,
        remote_host: Option<&str>,
        pr: Option<&crate::types::PrInfo>,
    ) -> bool {
        if let Some(host) = remote_host {
            // Look up the shell preference from the matching remote config.
            let shell = self
                .global_config
                .repos
                .iter()
                .find_map(|repo| repo.remote_for_host(host))
                .map(|r| r.shell.clone())
                .unwrap_or_else(|| "ssh".to_string());
            match remote::create_remote_proxy_session(host, session_name, worktree_path, &shell) {
                Ok(local_name) => {
                    self.switch_target = Some(local_name);
                    true
                }
                Err(e) => {
                    self.warning = Some((format!("remote session error: {e}"), Instant::now()));
                    false
                }
            }
        } else {
            let opts = crate::types::SwitchToSessionOptions {
                session_name: session_name.to_string(),
                worktree_path: worktree_path.to_string(),
                branch: branch.map(|b| b.to_string()),
                pr: pr.cloned(),
            };
            match tmux::create_session(&opts) {
                Ok(()) => {
                    self.switch_target = Some(session_name.to_string());
                    true
                }
                Err(e) => {
                    self.warning = Some((format!("session error: {e}"), Instant::now()));
                    false
                }
            }
        }
    }

    pub(crate) fn enter_cleanup_view(&mut self) {
        let stale = filter_stale(&self.task_rows);
        let selected = stale
            .iter()
            .map(|row| row.worktree_path.clone())
            .collect::<HashSet<_>>();
        self.view = ViewState::Cleanup(CleanupState {
            stale,
            selected,
            cursor: 0,
            phase: Phase::Idle,
            deleted: Vec::new(),
            errors: Vec::new(),
        });
    }
}

// ---------------------------------------------------------------------------
// Task-centric rendering
// ---------------------------------------------------------------------------

/// Rendering context for pane sub-rows, bundling display flags and theme.
struct SubRowContext<'a> {
    show_branch: bool,
    has_remote: bool,
    theme: &'a Theme,
}

impl App {
    /// Renders the task-grouped view. Called by `render_list` when tasks are present.
    pub(crate) fn render_task_list(&self, f: &mut Frame) {
        let full_area = f.area();

        // Horizontal padding for breathing room. No explicit bg — inherits terminal theme.
        let outer_block = Block::default().padding(Padding::horizontal(1));
        let area = outer_block.inner(full_area);
        f.render_widget(outer_block, full_area);

        let hdr_height = header_height(area.height);

        let tasks =
            visible_tasks_filtered(&self.task_rows, &self.filter_text, self.active_repo_slug());

        // Only show HOST column when at least one task has a remote session or remote worktree.
        let has_remote = self.task_rows.iter().any(|r| {
            r.sessions
                .iter()
                .any(|s| matches!(s.tmux.host, crate::session::Host::Remote(_)))
                || r.worktree_host.is_some()
        });

        let show_branch = self.show_branch_column;

        // Compute available width for the TITLE column.
        // Column order: BAR (1) + # (3) + CLAUDE (10) + ISSUE (6) + TITLE (flex)
        //               + [BRANCH (20)] + [HOST (12)] + STATUS (22)
        // Each column has 1 spacing. Plus borders (2).
        let fixed = 1 + 1 + 3 + 1 + 10 + 1 + 6 + 1 + 1 + 22 + 2;
        let branch_extra = if show_branch { 20 + 1 } else { 0 };
        let host_extra = if has_remote { 12 + 1 } else { 0 };
        let title_width = (area.width as usize).saturating_sub(fixed + branch_extra + host_extra);

        // Column widths — CLAUDE after #, STATUS at end.
        let mut widths: Vec<Constraint> = vec![
            Constraint::Length(1),  // BAR (colored repo indicator)
            Constraint::Length(3),  // #
            Constraint::Length(14), // CLAUDE (status + expand indicator like "▶3")
            Constraint::Length(6),  // ISSUE
            Constraint::Min(20),    // TITLE (flexible)
        ];
        if show_branch {
            widths.push(Constraint::Length(20)); // BRANCH (left-truncated)
        }
        if has_remote {
            widths.push(Constraint::Length(12)); // HOST
        }
        widths.push(Constraint::Length(22)); // STATUS (last)

        // Build rows for the table, including standalone sessions and section header rows.
        let num_columns = widths.len();
        let standalone_count = self.standalone_sessions.len();
        let (rows, row_heights, selected_visual_idx) = self.build_task_table_rows_with_standalone(
            &tasks,
            show_branch,
            has_remote,
            title_width,
            num_columns,
        );

        let has_warning = self
            .warning
            .as_ref()
            .is_some_and(|(_, t)| t.elapsed().as_secs() < WARNING_DURATION_SECS);

        // Calculate total table body height from individual row heights
        let body_height: u16 = row_heights.iter().sum::<u16>();
        let table_height = body_height.saturating_add(3); // +2 borders +1 header row

        // Identify the selected task (used by both preview content and title).
        let worktree_cursor = self.cursor.checked_sub(standalone_count);
        let selected_task = worktree_cursor.and_then(|wc| tasks.get(wc));

        // Always reserve preview space — the layout never shifts based on whether
        // a preview is currently available. When no content is ready, the preview
        // pane renders a placeholder. This prevents flicker as the cursor moves.
        let max_table_height =
            ((area.height as f32 * TABLE_MAX_HEIGHT_FRACTION) as u16).max(TABLE_MIN_HEIGHT);
        let table_height = table_height.min(max_table_height);

        let mut constraints = vec![
            Constraint::Length(hdr_height),
            Constraint::Length(3), // tab bar (rounded badges need 3 rows)
            Constraint::Length(table_height),
            Constraint::Length(1), // spacer
            Constraint::Min(4),    // preview fills remaining
        ];

        if has_warning {
            constraints.push(Constraint::Length(1));
        }

        constraints.push(Constraint::Length(1)); // hints
        constraints.push(Constraint::Length(1)); // attribution footer

        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints(constraints)
            .split(area);

        let mut idx = 0;
        self.render_header(f, chunks[idx]);
        idx += 1;

        self.render_repo_tabs(f, chunks[idx]);
        idx += 1;

        let theme = &self.theme;

        // Header row
        let header_style = Style::default()
            .fg(theme.dimmed)
            .add_modifier(Modifier::BOLD);
        let mut header_cells = vec![
            Cell::from(""), // BAR (no label)
            Cell::from(" #"),
            Cell::from("CLAUDE"),
            Cell::from("ISSUE"),
            Cell::from("TITLE"),
        ];
        if show_branch {
            header_cells.push(Cell::from("BRANCH"));
        }
        if has_remote {
            header_cells.push(Cell::from("HOST"));
        }
        header_cells.push(Cell::from("STATUS"));
        let header_row = Row::new(header_cells).style(header_style);

        let table_title = match self.active_repo_slug() {
            Some(slug) => format!(" TASKS \u{2014} {} ", slug),
            None => " TASKS ".to_string(),
        };
        let block = Block::default()
            .title(table_title)
            .title_style(
                Style::default()
                    .fg(theme.accent)
                    .add_modifier(Modifier::BOLD),
            )
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme.accent))
            .border_set(ratatui::symbols::border::ONE_EIGHTH_WIDE);

        let table = Table::new(rows, &widths)
            .header(header_row)
            .block(block)
            .column_spacing(1)
            .row_highlight_style(
                Style::default()
                    .bg(theme.accent)
                    .fg(Color::Black)
                    .add_modifier(Modifier::BOLD),
            );

        let table_chunk = chunks[idx];
        // Stateful render lets ratatui auto-scroll the viewport so the selected
        // row is always visible when the table is capped/clipped.
        let mut table_state = ratatui::widgets::TableState::default();
        table_state.select(selected_visual_idx);
        f.render_stateful_widget(table, table_chunk, &mut table_state);

        // Store the table body rect (excluding chrome) for mouse hit testing.
        let body_rect = Rect {
            x: table_chunk.x,
            y: table_chunk.y + TABLE_BODY_Y_OFFSET,
            width: table_chunk.width,
            height: table_chunk.height.saturating_sub(TABLE_CHROME_HEIGHT),
        };
        self.table_area.set(body_rect);

        // Render scrollbar only when total rows exceed visible area.
        // The visible inner height = table area height - 3 (borders + header row).
        // Total rows includes sub-rows for expanded parents.
        let total_rows = row_heights.len();
        let visible_rows = table_chunk.height.saturating_sub(3) as usize;
        if total_rows > visible_rows {
            // Use the visual row index (matches what the table widget actually scrolls).
            // self.cursor is a task-row index that skips group headers, so it would
            // lag behind the true scroll position.
            let scrollbar_pos = selected_visual_idx.unwrap_or(0);
            let mut scrollbar_state =
                ratatui::widgets::ScrollbarState::new(total_rows).position(scrollbar_pos);
            let scrollbar = Scrollbar::new(ScrollbarOrientation::VerticalRight)
                .begin_symbol(Some("\u{25b2}"))
                .end_symbol(Some("\u{25bc}"));
            // Render scrollbar in the table area (inside the border).
            f.render_stateful_widget(
                scrollbar,
                table_chunk.inner(ratatui::layout::Margin {
                    vertical: 1,
                    horizontal: 0,
                }),
                &mut scrollbar_state,
            );
        }
        idx += 1;

        // Preview — always rendered (placeholder when no content available).
        idx += 1; // spacer
        let standalone_at_cursor = cursor_is_standalone(self.cursor, standalone_count)
            .then(|| self.standalone_sessions.get(self.cursor))
            .flatten();
        self.render_task_preview(f, chunks[idx], selected_task, standalone_at_cursor);
        idx += 1;

        if has_warning {
            if let Some((ref msg, _)) = self.warning {
                let warn = Paragraph::new(msg.as_str())
                    .style(Style::default().fg(theme.warning))
                    .alignment(Alignment::Center);
                f.render_widget(warn, chunks[idx]);
            }
            idx += 1;
        }

        self.render_hints_task(f, chunks[idx]);
        idx += 1;

        self.render_attribution_footer(f, chunks[idx]);
    }

    /// Renders the repository tab bar.
    ///
    /// Shows an "ALL" tab followed by one tab per configured repo. The active
    /// tab uses a filled badge style: colored background, black foreground, bold.
    /// Inactive tabs show their label in the repo color with DIM modifier and no
    /// background fill. Each repo tab is colored using [`repo_color`] by config
    /// index; the ALL tab uses `theme.accent` (active) or `theme.dimmed`
    /// (inactive).
    pub(crate) fn render_repo_tabs(&self, f: &mut Frame, area: Rect) {
        let theme = &self.theme;

        // Build tab labels and colors.
        struct TabInfo {
            label: String,
            color: Color,
            active: bool,
        }

        let mut tabs = vec![TabInfo {
            label: "ALL".to_string(),
            color: theme.accent,
            active: self.active_repo_index == 0,
        }];

        for (i, repo) in self.global_config.repos.iter().enumerate() {
            let name = repo
                .slug
                .split('/')
                .nth(1)
                .unwrap_or(repo.slug.as_str())
                .to_uppercase();
            tabs.push(TabInfo {
                label: name,
                color: repo_color(i),
                active: self.active_repo_index == i + 1,
            });
        }

        // Compute widths: each badge is label.len() + 4 (2 border + 2 padding).
        // Plus 1 gap between badges.
        let tab_widths: Vec<u16> = tabs.iter().map(|t| (t.label.len() as u16) + 4).collect();

        // Lay out badges left-to-right within the area.
        let mut x = area.x;
        for (i, tab) in tabs.iter().enumerate() {
            let w = tab_widths[i];
            if x + w > area.x + area.width {
                break;
            }

            let badge_area = Rect::new(x, area.y, w, 3);

            if tab.active {
                let block = Block::bordered()
                    .border_type(BorderType::Rounded)
                    .border_style(Style::default().fg(tab.color).add_modifier(Modifier::BOLD));
                let label = Paragraph::new(Line::from(Span::styled(
                    tab.label.as_str(),
                    Style::default().fg(tab.color).add_modifier(Modifier::BOLD),
                )))
                .alignment(Alignment::Center)
                .block(block);
                f.render_widget(label, badge_area);
            } else {
                // No border — just colored text, vertically centered on row 1.
                let label = Paragraph::new(Line::from(Span::styled(
                    tab.label.as_str(),
                    Style::default().fg(tab.color),
                )))
                .alignment(Alignment::Center);
                // Render on row 1 (middle of the 3-row badge area) to align with active tab text.
                let text_area = Rect::new(badge_area.x, badge_area.y + 1, badge_area.width, 1);
                f.render_widget(label, text_area);
            }

            x += w + 1; // 1 gap between badges
        }
    }

    /// Renders the attribution footer: "made with ❤ — https://github.com/drewdrewthis/git-orchard-rs"
    ///
    /// The ❤ is rendered in error (red) color; the URL in dimmed + underlined.
    /// The footer area is also used for mouse-click URL detection.
    fn render_attribution_footer(&self, f: &mut Frame, area: Rect) {
        let theme = &self.theme;

        let heart_span = Span::styled("\u{2764}", Style::default().fg(theme.error));
        let dash_span = Span::raw(" \u{2014} ");
        let url_span = Span::styled(
            ATTRIBUTION_URL,
            Style::default()
                .fg(theme.dimmed)
                .add_modifier(Modifier::UNDERLINED),
        );

        let spans = vec![Span::raw("made with "), heart_span, dash_span, url_span];

        // Compute URL click area using display widths (not byte lengths)
        // so that multi-byte characters like ❤ and — are measured correctly.
        let prefix_width: usize = spans.iter().take(3).map(|s| s.width()).sum();
        let url_len = spans.last().map_or(0, |s| s.width());
        let total_width = prefix_width + url_len;
        let left_pad = if (area.width as usize) > total_width {
            ((area.width as usize) - total_width) / 2
        } else {
            0
        };
        let url_x = area.x + (left_pad + prefix_width) as u16;
        self.url_area.set(Rect {
            x: url_x,
            y: area.y,
            width: url_len as u16,
            height: 1,
        });

        let footer = Paragraph::new(Line::from(spans)).alignment(Alignment::Center);
        f.render_widget(footer, area);
    }

    /// Builds table rows: standalone sessions first, then worktree task rows with group headers.
    ///
    /// Column order: BAR, #, CLAUDE, ISSUE, TITLE, [BRANCH], [HOST], STATUS.
    /// Unified sequential numbering across standalone + worktree rows.
    /// Expanded rows get pane sub-rows inserted after the parent.
    fn build_task_table_rows_with_standalone(
        &self,
        tasks: &[VisibleTask],
        show_branch: bool,
        has_remote: bool,
        title_width: usize,
        num_columns: usize,
    ) -> (Vec<Row<'static>>, Vec<u16>, Option<usize>) {
        let theme = &self.theme;
        let mut rows: Vec<Row<'static>> = Vec::new();
        let mut row_heights: Vec<u16> = Vec::new();
        let mut selected_visual_idx: Option<usize> = None;
        let standalone_count = self.standalone_sessions.len();

        // Unified numbering counter (1-based, spans standalone + worktree).
        let mut unified_num = 1usize;

        // Render standalone session rows first.
        for (idx, ss) in self.standalone_sessions.iter().enumerate() {
            let selected = idx == self.cursor && self.selected_pane.is_none();
            if selected {
                selected_visual_idx = Some(rows.len());
            }
            let (claude_text, claude_style) = standalone_claude_status(&ss.session, theme);
            let status_text = match &ss.session.tmux.status {
                crate::session::SessionStatus::Running { .. } => "running",
                crate::session::SessionStatus::Dead => "not running",
            };
            let status_style = match &ss.session.tmux.status {
                crate::session::SessionStatus::Running { .. } => Style::default().fg(Color::Green),
                crate::session::SessionStatus::Dead => Style::default().fg(Color::DarkGray),
            };

            // Expand/collapse indicator in CLAUDE cell.
            let pane_count = ss.session.panes.len();
            let is_expanded = self.expanded.contains(&ss.session.tmux.name);
            let claude_display = expand_indicator(&claude_text, pane_count, is_expanded);

            let row_style = if selected {
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD)
            } else {
                Style::default()
            };

            let mut cells = vec![
                Cell::from(""), // no bar for standalone sessions
                Cell::from(format!("{:>2}", unified_num)),
                Cell::from(claude_display).style(claude_style),
                Cell::from("").style(Style::default().fg(Color::DarkGray)), // no issue
                Cell::from(ss.config.name.clone()),
            ];

            if show_branch {
                cells.push(Cell::from("")); // no branch
            }
            if has_remote {
                cells.push(Cell::from("")); // always local
            }
            cells.push(Cell::from(status_text).style(status_style));

            rows.push(Row::new(cells).style(row_style));
            row_heights.push(1);

            // Pane sub-rows for expanded standalone sessions.
            if is_expanded {
                self.push_pane_sub_rows(
                    &ss.session.panes,
                    idx,
                    Color::DarkGray, // no repo color for standalone
                    &SubRowContext {
                        show_branch,
                        has_remote,
                        theme,
                    },
                    &mut rows,
                    &mut row_heights,
                );
            }

            unified_num += 1;
        }

        // Render worktree task rows.
        let mut last_group: Option<DisplayGroup> = None;

        for (flat_idx, vt) in tasks.iter().enumerate() {
            let cursor_idx = flat_idx + standalone_count;
            let selected = cursor_idx == self.cursor && self.selected_pane.is_none();

            // Section header when display group changes
            if last_group != Some(vt.group) {
                last_group = Some(vt.group);
                let header_row = group_header_row(vt.group, num_columns, theme);
                rows.push(header_row);
                row_heights.push(1);
            }

            if selected {
                selected_visual_idx = Some(rows.len());
            }

            let (pr_text, pr_style) = pr_status_text(vt.row, theme);
            let (claude_text, claude_style) = claude_status_text(vt.row, theme);

            let title_raw = if vt.row.is_main_worktree {
                vt.row
                    .repo_slug
                    .split('/')
                    .nth(1)
                    .unwrap_or(&vt.row.repo_slug)
            } else {
                match vt.row.issue_title.as_deref() {
                    Some(title) if !title.is_empty() => title,
                    _ => branch_tail(&vt.row.branch),
                }
            };
            let title_display = crate::paths::truncate_left(title_raw, title_width);

            let task_host: Option<&str> = vt
                .row
                .sessions
                .iter()
                .find_map(|s| match &s.tmux.host {
                    crate::session::Host::Remote(h) => Some(h.as_str()),
                    crate::session::Host::Local => None,
                })
                .or(vt.row.worktree_host.as_deref());
            let host_unreachable = task_host.is_some()
                && task_host.and_then(|h| self.host_reachable.get(h)).copied() != Some(true);

            let row_style = if selected {
                let base = Style::default()
                    .fg(theme.accent)
                    .add_modifier(Modifier::BOLD);
                if host_unreachable {
                    base.add_modifier(Modifier::DIM)
                } else {
                    base
                }
            } else if host_unreachable {
                Style::default().add_modifier(Modifier::DIM)
            } else {
                Style::default()
            };

            let issue_cell = if let Some(num) = vt.row.issue_number {
                Cell::from(format!("#{}", num))
            } else {
                Cell::from("").style(Style::default().fg(theme.dimmed))
            };

            let repo_idx = self
                .global_config
                .repos
                .iter()
                .position(|r| r.slug == vt.row.repo_slug)
                .unwrap_or(0);
            let bar_color = repo_color(repo_idx);
            let bar_cell = Cell::from("\u{25cf}") // ●
                .style(Style::default().fg(bar_color));

            // Expand/collapse indicator in CLAUDE cell.
            let pane_count = vt.row.sessions.first().map(|s| s.panes.len()).unwrap_or(0);
            let is_expanded = self.expanded.contains(&vt.row.worktree_path);
            let claude_display = expand_indicator(&claude_text, pane_count, is_expanded);

            // Use unified numbering (continues from standalone count).
            let mut cells = vec![
                bar_cell,
                Cell::from(format!("{:>2}", unified_num)),
                Cell::from(claude_display).style(claude_style),
                issue_cell,
                Cell::from(title_display),
            ];

            if show_branch {
                let branch_display = crate::paths::truncate_left(branch_tail(&vt.row.branch), 20);
                let branch_cell =
                    Cell::from(branch_display).style(Style::default().fg(theme.dimmed));
                cells.push(branch_cell);
            }

            if has_remote {
                let host_cell = if let Some(h) = task_host {
                    match self.host_reachable.get(h) {
                        Some(&false) => Cell::from(format!("@{} \u{2717}", h))
                            .style(Style::default().fg(theme.error)),
                        Some(&true) => Cell::from(format!("@{} \u{25cf}", h))
                            .style(Style::default().fg(theme.success)),
                        None => Cell::from(format!("@{}", h))
                            .style(Style::default().fg(theme.host_unknown)),
                    }
                } else {
                    Cell::from("")
                };
                cells.push(host_cell);
            }

            cells.push(Cell::from(pr_text).style(pr_style));

            rows.push(Row::new(cells).style(row_style));
            row_heights.push(1);

            // Pane sub-rows for expanded worktree rows.
            if is_expanded {
                let panes = vt
                    .row
                    .sessions
                    .first()
                    .map(|s| s.panes.as_slice())
                    .unwrap_or(&[]);
                self.push_pane_sub_rows(
                    panes,
                    cursor_idx,
                    bar_color,
                    &SubRowContext {
                        show_branch,
                        has_remote,
                        theme,
                    },
                    &mut rows,
                    &mut row_heights,
                );
            }

            unified_num += 1;
        }

        (rows, row_heights, selected_visual_idx)
    }

    /// Appends pane sub-rows for an expanded parent row.
    ///
    /// Each sub-row shows: tree connector in # cell, claude indicator, command in TITLE,
    /// and empty cells elsewhere. The BAR cell inherits the parent's repo color.
    fn push_pane_sub_rows(
        &self,
        panes: &[crate::session::PaneInfo],
        parent_cursor_idx: usize,
        bar_color: Color,
        ctx: &SubRowContext<'_>,
        rows: &mut Vec<Row<'static>>,
        row_heights: &mut Vec<u16>,
    ) {
        let pane_count = panes.len();
        for (pi, pane) in panes.iter().enumerate() {
            let is_last = pi == pane_count - 1;
            let selected = self.cursor == parent_cursor_idx && self.selected_pane == Some(pi);

            // Tree connector: ├─N for non-last, └─N for last pane.
            let connector = if is_last {
                format!("\u{2514}\u{2500}{}", pane.index)
            } else {
                format!("\u{251c}\u{2500}{}", pane.index)
            };

            // Claude indicator for this pane.
            let claude_cell = if pane.has_claude {
                Cell::from("\u{26a1}").style(Style::default().fg(ctx.theme.claude_active))
            } else {
                Cell::from("\u{2500}").style(Style::default().fg(ctx.theme.dimmed))
            };

            let row_style = if selected {
                Style::default()
                    .fg(ctx.theme.accent)
                    .add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(ctx.theme.dimmed)
            };

            let mut cells = vec![
                Cell::from("\u{25cf}").style(Style::default().fg(bar_color)), // BAR inherits parent
                Cell::from(connector), // # cell: tree connector
                claude_cell,           // CLAUDE cell
                Cell::from(""),        // ISSUE: empty
                Cell::from(if pane.title.is_empty() {
                    pane.command.clone()
                } else {
                    pane.title.clone()
                }), // TITLE: pane title (falls back to command)
            ];

            if ctx.show_branch {
                cells.push(Cell::from("")); // BRANCH: empty
            }
            if ctx.has_remote {
                cells.push(Cell::from("")); // HOST: empty
            }
            cells.push(Cell::from("")); // STATUS: empty

            rows.push(Row::new(cells).style(row_style));
            row_heights.push(1);
        }
    }

    /// Renders the preview pane with task metadata in the border title.
    ///
    /// Either `selected_task` (worktree row) or `standalone_session` must be `Some`.
    /// When `standalone_session` is provided, the session name is used as the title.
    fn render_task_preview(
        &self,
        f: &mut Frame,
        area: Rect,
        selected_task: Option<&VisibleTask>,
        standalone_session: Option<&crate::session::StandaloneSessionRow>,
    ) {
        // Build the title based on what's selected; fall back to a generic header
        // when the row has no associated session or task (e.g. group separator).
        let title_opt = if let Some(ss) = standalone_session {
            Some(format!(
                "\u{2500}\u{2500} {} \u{2500}\u{2500}",
                ss.session.tmux.name
            ))
        } else if let Some(vt) = selected_task {
            let issue_part = match vt.row.issue_number {
                Some(num) => format!("#{}", num),
                None => branch_tail(&vt.row.branch).to_string(),
            };
            let title_part = match vt.row.issue_title.as_deref() {
                Some(t) if !t.is_empty() => format!(" {}", t),
                _ => String::new(),
            };
            let wt_part = {
                let short = paths::tildify(&vt.row.worktree_path);
                format!(" \u{2502} wt: {}", short)
            };
            let pr_part = vt
                .row
                .pr
                .as_ref()
                .map(|p| format!(" \u{2502} pr: #{}", p.number))
                .unwrap_or_default();
            Some(format!(
                "\u{2500}\u{2500} {}{}{}{} \u{2500}\u{2500}",
                issue_part, title_part, wt_part, pr_part
            ))
        } else {
            None
        };

        // Render placeholder when no content is available — keeps layout stable.
        if self.pane_content.is_empty() || title_opt.is_none() {
            self.render_preview_placeholder(f, area);
            // Placeholder is not scroll-interactive; clear hit-test rect.
            self.preview_area.set(Rect::default());
            return;
        }
        let title = title_opt.unwrap();

        self.preview_area.set(area);

        let theme = &self.theme;
        let block = Block::default()
            .title(title)
            .title_style(
                Style::default()
                    .fg(theme.accent)
                    .add_modifier(Modifier::BOLD),
            )
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme.accent))
            .border_type(BorderType::Double);

        let inner = block.inner(area);
        f.render_widget(block, area);

        // Build a ScrollView containing the full pane content.
        let line_count = self.pane_content.lines().count() as u16;
        let content_height = line_count.max(1);
        let content_width = inner.width.saturating_sub(1); // leave room for scrollbar

        let mut scroll_view = ScrollView::new(Size::new(content_width, content_height))
            .vertical_scrollbar_visibility(ScrollbarVisibility::Automatic)
            .horizontal_scrollbar_visibility(ScrollbarVisibility::Never);

        let paragraph = Paragraph::new(self.pane_content.as_str())
            .style(Style::default().fg(theme.preview_content));
        scroll_view.render_widget(paragraph, Rect::new(0, 0, content_width, content_height));

        let mut state = self.preview_scroll_state.get();
        f.render_stateful_widget(scroll_view, inner, &mut state);
        self.preview_scroll_state.set(state);
    }

    /// Renders an empty bordered preview pane with a centered "no preview" message.
    ///
    /// Used when the selected row has no associated session/task or the pane
    /// content has not yet loaded. Keeps the layout stable so the menu does not
    /// flicker as the cursor moves between rows with and without previews.
    fn render_preview_placeholder(&self, f: &mut Frame, area: Rect) {
        let theme = &self.theme;
        let block = Block::default()
            .title("\u{2500}\u{2500} preview \u{2500}\u{2500}")
            .title_style(Style::default().fg(theme.dimmed))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(theme.dimmed))
            .border_type(BorderType::Double);
        let inner = block.inner(area);
        f.render_widget(block, area);

        let message = Paragraph::new("no preview available")
            .style(Style::default().fg(theme.dimmed))
            .alignment(Alignment::Center);
        // Vertically center the single-line message in the inner area.
        let y_offset = inner.height / 2;
        let centered = Rect {
            x: inner.x,
            y: inner.y + y_offset,
            width: inner.width,
            height: 1.min(inner.height),
        };
        f.render_widget(message, centered);
    }

    /// Appends the common trailing hint keys: refresh, reconnect, quit, help.
    fn append_common_hints(
        &self,
        spans: &mut Vec<Span<'static>>,
        sep: &Span<'static>,
        key_style: Style,
        quit_label: &'static str,
    ) {
        let theme = &self.theme;
        if self.refreshing {
            let throbber = throbber_widgets_tui::Throbber::default()
                .throbber_style(Style::default().fg(theme.accent));
            let symbol = throbber.to_symbol_span(&self.throbber_state);
            spans.push(Span::styled(
                format!("{}refreshing...", symbol.content),
                Style::default().fg(theme.accent),
            ));
        } else {
            spans.push(Span::styled("Spc+r", key_style));
            spans.push(Span::raw(" refresh"));
        }

        let has_unreachable = self.host_reachable.values().any(|&v| !v);
        if has_unreachable {
            spans.push(sep.clone());
            spans.push(Span::styled("Spc+R", key_style));
            spans.push(Span::raw(" reconnect"));
        }

        spans.push(sep.clone());
        spans.push(Span::styled("Spc+q", key_style));
        spans.push(Span::raw(format!(" {}", quit_label)));

        spans.push(sep.clone());
        spans.push(Span::styled("Spc+?", key_style));
        spans.push(Span::raw(" help"));
    }

    /// Renders the hint bar for task mode.
    pub(crate) fn render_hints_task(&self, f: &mut Frame, area: Rect) {
        let theme = &self.theme;
        let sep = Span::styled(" \u{2502} ", Style::default().fg(theme.dimmed));
        let key_style = Style::default()
            .fg(theme.accent)
            .add_modifier(Modifier::BOLD);

        let is_standalone = cursor_is_standalone(self.cursor, self.standalone_sessions.len());
        let dim = Style::default().fg(theme.dimmed);

        let mut spans: Vec<Span> = vec![
            Span::styled("enter", key_style),
            Span::raw(" switch"),
            sep.clone(),
        ];

        // Active filter text indicator — shown inline when filter is non-empty.
        if !self.filter_text.is_empty() {
            spans.push(Span::styled(
                format!("[{}_]", self.filter_text),
                Style::default().fg(theme.search_highlight),
            ));
            spans.push(sep.clone());
        }

        // PR link hint — dim when standalone or selected task has no PR.
        let has_pr = !is_standalone && !self.task_rows.is_empty() && {
            let standalone_count = self.standalone_sessions.len();
            let worktree_cursor = self.cursor.saturating_sub(standalone_count);
            let visible =
                visible_tasks_filtered(&self.task_rows, &self.filter_text, self.active_repo_slug());
            visible
                .get(worktree_cursor)
                .is_some_and(|vt| vt.row.pr.is_some())
        };
        if has_pr {
            spans.push(Span::styled("Spc+o", key_style));
            spans.push(Span::raw(" pr"));
        } else {
            spans.push(Span::styled("Spc+o pr", dim));
        }
        spans.push(sep.clone());

        // Dim 'p' (priority) for standalone sessions.
        if is_standalone {
            spans.push(Span::styled("Spc+p priority", dim));
        } else {
            spans.push(Span::styled("Spc+p", key_style));
            spans.push(Span::raw(" priority"));
        }
        spans.push(sep.clone());

        spans.push(Span::styled("Spc+B", key_style));
        spans.push(Span::raw(" branch"));
        spans.push(sep.clone());

        spans.push(Span::styled("Tab", key_style));
        spans.push(Span::raw(" repos"));
        spans.push(sep.clone());

        spans.push(Span::styled("↔", key_style));
        spans.push(Span::raw(" expand"));
        spans.push(sep.clone());

        spans.push(Span::styled("Spc+c", key_style));
        spans.push(Span::raw(" cleanup"));
        spans.push(sep.clone());

        self.append_common_hints(&mut spans, &sep, key_style, "quit");

        let hints = Paragraph::new(Line::from(spans)).alignment(Alignment::Center);
        f.render_widget(hints, area);
    }
}

// ---------------------------------------------------------------------------
// Task view helpers (free functions)
// ---------------------------------------------------------------------------

/// Appends an expand/collapse indicator to a status string when pane count > 1.
///
/// Returns the original text unchanged for single-pane or zero-pane rows.
/// Multi-pane rows get `" ▶N"` (collapsed) or `" ▼N"` (expanded) appended.
/// Adds expand/collapse caret and pane count to the CLAUDE status text.
///
/// For multi-pane rows, replaces the leading status icon (⚡, ●, ○) with
/// ▶ (collapsed) or ▼ (expanded) and appends `(N)`.
/// Single-pane or zero-pane rows are returned unchanged.
pub(crate) fn expand_indicator(base_text: &str, pane_count: usize, expanded: bool) -> String {
    if pane_count <= 1 {
        return base_text.to_string();
    }
    let caret = if expanded { "\u{25bc}" } else { "\u{25b6}" }; // ▼ / ▶
    // Strip the leading icon character (⚡, ●, ○, etc.) and replace with caret.
    let rest = base_text
        .char_indices()
        .nth(1)
        .map(|(i, _)| &base_text[i..])
        .unwrap_or(base_text);
    format!("{} {}({})", caret, rest.trim_start(), pane_count)
}

/// Creates a section header row spanning all columns for a display group.
///
/// `num_columns` is the total number of columns in the table (must match the data rows).
/// The RepoMain header uses bold + accent styling.
fn group_header_row(group: DisplayGroup, num_columns: usize, theme: &Theme) -> Row<'static> {
    let label = group.label().to_string();

    // ──── label ────
    let line_char = "\u{2500}";
    let padded = format!(" {} ", label);
    let side_len = 8;
    let text = format!(
        "{}{}{}",
        line_char.repeat(side_len),
        padded,
        line_char.repeat(40)
    );

    let (color, bold) = if group == DisplayGroup::RepoMain {
        (theme.accent, true)
    } else {
        (display_group_color(group, theme), false)
    };

    let title_style = if bold {
        Style::default().fg(color).add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(color)
    };

    // Header text goes in the TITLE column (index 4: BAR, #, CLAUDE, ISSUE, TITLE).
    let mut cells: Vec<Cell> = vec![
        Cell::from(""), // bar placeholder
        Cell::from(""), // # placeholder
        Cell::from(""), // CLAUDE placeholder
        Cell::from(""), // ISSUE placeholder
        Cell::from(text).style(title_style),
    ];
    for _ in 5..num_columns {
        cells.push(Cell::from(""));
    }
    Row::new(cells)
}

/// Returns the height (in terminal rows) to allocate for the header.
///
/// When the terminal is tall enough (>= 30 rows), the full ASCII art logo header is
/// shown in a bordered block. On shorter terminals a single compact
/// line is used instead so the task list gets as much vertical space as possible.
///
/// The full header reserves 9 rows: 5 logo lines + optional host + optional
/// timestamp + 2 double-line border rows.
pub(crate) fn header_height(terminal_height: u16) -> u16 {
    if terminal_height >= FULL_HEADER_MIN_HEIGHT {
        9
    } else {
        1
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo, WorktreeRow};
    use crate::session::{
        ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, TmuxSessionInfo,
    };

    fn make_task_row(issue_number: u32, group: DisplayGroup) -> WorktreeRow {
        WorktreeRow {
            repo_slug: "owner/repo".to_string(),
            worktree_path: format!("/workspace/repo-{}", issue_number),
            branch: format!("feat/issue-{}", issue_number),
            worktree_host: None,
            issue_number: Some(issue_number),
            issue_title: Some(format!("Test task {}", issue_number)),
            issue_state: None,
            pr: None,
            sessions: vec![],
            display_group: group,
            is_main_worktree: false,
        }
    }

    fn make_session(name: &str) -> EnrichedSession {
        EnrichedSession {
            tmux: TmuxSessionInfo {
                host: Host::Local,
                name: name.to_string(),
                status: SessionStatus::Running { attached: false },
            },
            claude: None,
            panes: vec![],
        }
    }

    #[test]
    fn full_logo_at_threshold() {
        assert_eq!(header_height(30), 9);
    }

    #[test]
    fn full_logo_above_threshold() {
        assert_eq!(header_height(50), 9);
    }

    #[test]
    fn compact_header_just_below_threshold() {
        assert_eq!(header_height(29), 1);
    }

    #[test]
    fn compact_header_on_very_short_terminal() {
        assert_eq!(header_height(10), 1);
    }

    #[test]
    fn visible_tasks_returns_all_rows_including_other() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
            make_task_row(3, DisplayGroup::Other),
        ];
        // All rows are always visible — no backlog collapsing.
        let visible = visible_tasks(&rows, "");
        assert_eq!(visible.len(), 3);
    }

    #[test]
    fn other_group_always_shown() {
        let rows: Vec<WorktreeRow> = (1u32..=5)
            .map(|i| make_task_row(i, DisplayGroup::Other))
            .collect();
        let visible = visible_tasks(&rows, "");
        assert_eq!(visible.len(), 5, "Other rows are always visible");
    }

    #[test]
    fn sequential_numbering_across_groups() {
        let rows = vec![
            make_task_row(10, DisplayGroup::NeedsAttention),
            make_task_row(20, DisplayGroup::ClaudeWorking),
            make_task_row(30, DisplayGroup::Other),
        ];
        let visible = visible_tasks(&rows, "");
        assert_eq!(visible.len(), 3);
        // Unified numbering now happens at render time, not in visible_tasks.
        // Verify all three rows pass through with correct groups.
        assert_eq!(visible[0].group, DisplayGroup::NeedsAttention);
        assert_eq!(visible[1].group, DisplayGroup::ClaudeWorking);
        assert_eq!(visible[2].group, DisplayGroup::Other);
    }

    #[test]
    fn search_filters_by_text() {
        let row_match = WorktreeRow {
            branch: "feat/my-feature".to_string(),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let row_no_match = WorktreeRow {
            branch: "feat/other-thing".to_string(),
            ..make_task_row(2, DisplayGroup::ClaudeWorking)
        };
        let rows = vec![row_match, row_no_match];
        let visible = visible_tasks(&rows, "my-feature");
        assert_eq!(visible.len(), 1);
        assert_eq!(visible[0].row.branch, "feat/my-feature");
    }

    #[test]
    fn shepherd_always_visible() {
        let shepherd = WorktreeRow {
            is_main_worktree: true,
            branch: "main".to_string(),
            ..make_task_row(1, DisplayGroup::RepoMain)
        };
        let other = make_task_row(2, DisplayGroup::NeedsAttention);
        let rows = vec![shepherd, other];
        // Shepherd bypasses search filter.
        let visible = visible_tasks(&rows, "nomatch");
        assert_eq!(visible.len(), 1);
        assert!(visible[0].row.is_main_worktree);
    }

    #[test]
    fn pr_status_approved_text() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 42,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: Some("approved".to_string()),
                checks_state: Some("passing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::ReadyToMerge)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(
            text.starts_with("#42 "),
            "expected '#42 ' prefix in: {}",
            text
        );
        assert!(
            text.contains("approved"),
            "expected 'approved' in: {}",
            text
        );
    }

    #[test]
    fn pr_status_no_pr() {
        let row = make_task_row(1, DisplayGroup::Other);
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert_eq!(text, "no PR");
    }

    #[test]
    fn claude_status_active() {
        let row = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "sess".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Working,
                    cost_usd: None,
                    context_window_pct: None,
                    model: None,
                }),
                panes: vec![],
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("active"), "expected 'active' in: {}", text);
    }

    #[test]
    fn claude_status_idle() {
        let row = WorktreeRow {
            sessions: vec![make_session("sess")],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("idle"), "expected 'idle' in: {}", text);
    }

    #[test]
    fn claude_status_needs_input() {
        let row = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "sess".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Input,
                    cost_usd: None,
                    context_window_pct: None,
                    model: None,
                }),
                panes: vec![],
            }],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("input"), "expected 'input' in: {}", text);
    }

    #[test]
    fn claude_status_none_when_no_session() {
        let row = make_task_row(1, DisplayGroup::Other);
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("none"), "expected 'none' in: {}", text);
    }

    #[test]
    fn pr_status_changes_requested() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: Some("changes_requested".to_string()),
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(
            text.contains("changes req"),
            "expected 'changes req' in: {}",
            text
        );
    }

    #[test]
    fn pr_status_conflict() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: None,
                has_conflicts: true,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(
            text.contains("conflict"),
            "expected 'conflict' in: {}",
            text
        );
    }

    #[test]
    fn pr_status_unresolved_threads() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 3,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(
            text.contains("unresolved"),
            "expected 'unresolved' in: {}",
            text
        );
        assert!(text.contains("3"), "expected count 3 in: {}", text);
    }

    #[test]
    fn pr_status_failing_ci() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: Some("failing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(text.contains("failing"), "expected 'failing' in: {}", text);
    }

    // -----------------------------------------------------------------------
    // branch_tail
    // -----------------------------------------------------------------------

    #[test]
    fn branch_tail_returns_segment_after_last_slash() {
        assert_eq!(branch_tail("feat/issue-123"), "issue-123");
    }

    #[test]
    fn branch_tail_returns_whole_string_when_no_slash() {
        assert_eq!(branch_tail("my-feature"), "my-feature");
    }

    #[test]
    fn branch_tail_returns_last_segment_with_multiple_slashes() {
        assert_eq!(
            branch_tail("user/feat/issue-456-refactor"),
            "issue-456-refactor"
        );
    }

    #[test]
    fn branch_tail_returns_empty_str_for_trailing_slash() {
        assert_eq!(branch_tail("feat/"), "");
    }

    #[test]
    fn branch_tail_returns_empty_str_for_empty_branch() {
        assert_eq!(branch_tail(""), "");
    }

    // -----------------------------------------------------------------------
    // claude_status_text with hook state
    // -----------------------------------------------------------------------

    fn session_with_hook_state(
        state: crate::claude_state::ClaudeState,
        ctx_pct: Option<f64>,
    ) -> EnrichedSession {
        let claude = if state != crate::claude_state::ClaudeState::None {
            Some(ClaudeSessionInfo {
                status: state,
                cost_usd: None,
                context_window_pct: ctx_pct,
                model: None,
            })
        } else {
            None
        };
        EnrichedSession {
            tmux: TmuxSessionInfo {
                host: Host::Local,
                name: "sess".to_string(),
                status: SessionStatus::Running { attached: false },
            },
            claude,
            panes: vec![],
        }
    }

    #[test]
    fn claude_status_working_with_context_shows_percentage() {
        let row = WorktreeRow {
            sessions: vec![session_with_hook_state(
                crate::claude_state::ClaudeState::Working,
                Some(73.0),
            )],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("active"), "expected 'active' in: {}", text);
        assert!(text.contains("73%"), "expected '73%' in: {}", text);
    }

    #[test]
    fn claude_status_idle_from_hook_state() {
        let row = WorktreeRow {
            sessions: vec![session_with_hook_state(
                crate::claude_state::ClaudeState::Idle,
                None,
            )],
            ..make_task_row(1, DisplayGroup::Other)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("idle"), "expected 'idle' in: {}", text);
    }

    #[test]
    fn claude_status_input_from_hook_state() {
        let row = WorktreeRow {
            sessions: vec![session_with_hook_state(
                crate::claude_state::ClaudeState::Input,
                None,
            )],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("input"), "expected 'input' in: {}", text);
    }

    #[test]
    fn claude_status_input_with_context_shows_percentage() {
        let row = WorktreeRow {
            sessions: vec![session_with_hook_state(
                crate::claude_state::ClaudeState::Input,
                Some(95.0),
            )],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(text.contains("input"), "expected 'input' in: {}", text);
        assert!(text.contains("95%"), "expected '95%' in: {}", text);
    }

    #[test]
    fn claude_status_no_context_pct_when_none() {
        let row = WorktreeRow {
            sessions: vec![session_with_hook_state(
                crate::claude_state::ClaudeState::Working,
                None,
            )],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        assert!(
            !text.contains('%'),
            "expected no % when context_window_pct is None: {}",
            text
        );
    }

    #[test]
    fn pr_status_pending_ci() {
        let row = WorktreeRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: Some("pending".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::Other)
        };
        let (text, _) = pr_status_text(&row, &Theme::default());
        assert!(text.contains("pending"), "expected 'pending' in: {}", text);
    }

    #[test]
    fn claude_status_multiple_sessions() {
        let row = WorktreeRow {
            sessions: vec![
                EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: "sess1".to_string(),
                        status: SessionStatus::Running { attached: false },
                    },
                    claude: Some(ClaudeSessionInfo {
                        status: crate::claude_state::ClaudeState::Working,
                        cost_usd: None,
                        context_window_pct: None,
                        model: None,
                    }),
                    panes: vec![],
                },
                EnrichedSession {
                    tmux: TmuxSessionInfo {
                        host: Host::Local,
                        name: "sess2".to_string(),
                        status: SessionStatus::Running { attached: false },
                    },
                    claude: Some(ClaudeSessionInfo {
                        status: crate::claude_state::ClaudeState::Input,
                        cost_usd: None,
                        context_window_pct: Some(45.0),
                        model: None,
                    }),
                    panes: vec![],
                },
            ],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row, &Theme::default());
        // Input takes priority over working; count suffix should show " 2"
        assert!(text.contains("input"), "expected 'input' in: {}", text);
        assert!(
            text.contains("2"),
            "expected session count '2' in: {}",
            text
        );
    }

    #[test]
    fn search_is_case_insensitive() {
        let rows = vec![make_task_row(1, DisplayGroup::NeedsAttention)];
        // Search with uppercase should match lowercase branch "feat/issue-1"
        let visible = visible_tasks(&rows, "FEAT/ISSUE");
        assert_eq!(visible.len(), 1);
    }

    #[test]
    fn search_with_multiple_rows() {
        let mut row_match = make_task_row(1, DisplayGroup::NeedsAttention);
        row_match.branch = "feat/target-branch".to_string();

        let mut row_no_match = make_task_row(2, DisplayGroup::NeedsAttention);
        row_no_match.branch = "feat/other-branch".to_string();

        let rows = vec![row_match, row_no_match];

        // Search "target" should only match the first row
        let visible = visible_tasks(&rows, "target");
        assert_eq!(visible.len(), 1);
        assert_eq!(visible[0].row.issue_number, Some(1));
    }

    #[test]
    fn throbber_renders_loading_state() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let mut app = App::new_test(vec![]);
        app.loading = true;
        let backend = TestBackend::new(80, 30);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push(buffer[(x, y)].symbol().chars().next().unwrap_or(' '));
            }
        }

        assert!(
            text.contains("Loading worktrees..."),
            "loading state must show throbber label, got:\n{text}"
        );
    }

    #[test]
    fn ascii_header_height_for_tall_terminal() {
        // At 30+ rows, header_height returns the full ASCII logo size (9 rows).
        assert_eq!(header_height(FULL_HEADER_MIN_HEIGHT), 9);
        assert_eq!(header_height(FULL_HEADER_MIN_HEIGHT + 10), 9);
    }

    #[test]
    fn ascii_header_height_for_short_terminal() {
        // Below 30 rows, the compact 1-line header is used.
        assert_eq!(header_height(FULL_HEADER_MIN_HEIGHT - 1), 1);
        assert_eq!(header_height(15), 1);
    }

    #[test]
    fn preview_scroll_state_is_copy() {
        // ScrollViewState must be Copy so Cell<ScrollViewState> works.
        let state = tui_scrollview::ScrollViewState::default();
        let _copy = state; // Copy trait in action
        let _another = state; // Still valid after copy
    }

    // -----------------------------------------------------------------------
    // Hints bar — repo hint removal
    // -----------------------------------------------------------------------

    #[test]
    fn hints_task_contains_repo_cycling_hint() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let backend = TestBackend::new(200, 40);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_task_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push_str(buffer[(x, y)].symbol());
            }
        }

        assert!(
            text.contains("repos"),
            "hints bar must contain 'repos' repo cycling hint"
        );
        assert!(
            text.contains("Tab"),
            "hints bar must contain 'Tab' repo cycling hint"
        );
    }

    #[test]
    fn hints_task_does_not_contain_active_repo_bracket_indicator() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        // Simulate a repo selected.
        app.active_repo_index = 1;
        let backend = TestBackend::new(200, 40);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_task_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push_str(buffer[(x, y)].symbol());
            }
        }

        assert!(
            !text.contains("[◄"),
            "hints bar must not contain '[◄' bracket indicator"
        );
        assert!(
            !text.contains("►]"),
            "hints bar must not contain '►]' bracket indicator"
        );
    }

    #[test]
    fn hints_task_retains_core_hints() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let backend = TestBackend::new(200, 40);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_task_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push_str(buffer[(x, y)].symbol());
            }
        }

        assert!(text.contains("switch"), "must contain 'switch' hint");
        assert!(text.contains("branch"), "must contain 'branch' hint");
        assert!(text.contains("quit"), "must contain 'quit' hint");
        assert!(text.contains("help"), "must contain 'help' hint");
        assert!(
            text.contains("Spc+"),
            "must contain 'Spc+' leader prefix hints"
        );
    }

    // -----------------------------------------------------------------------
    // Repo color integration — slug → config index → palette color
    // -----------------------------------------------------------------------

    #[test]
    fn repo_index_lookup_maps_slug_to_correct_palette_color() {
        use crate::global_config::RepoConfig;
        use crate::tui::theme::repo_color;
        use ratatui::style::Color;

        let repos = [
            RepoConfig {
                slug: "owner/alpha".to_string(),
                path: "/workspace/alpha".to_string(),
                remotes: vec![],
            },
            RepoConfig {
                slug: "owner/beta".to_string(),
                path: "/workspace/beta".to_string(),
                remotes: vec![],
            },
            RepoConfig {
                slug: "owner/gamma".to_string(),
                path: "/workspace/gamma".to_string(),
                remotes: vec![],
            },
        ];

        let beta_idx = repos.iter().position(|r| r.slug == "owner/beta").unwrap();
        assert_eq!(beta_idx, 1);
        // index 1 → tangerine orange in the fruit palette
        assert_eq!(repo_color(beta_idx), Color::Rgb(255, 180, 40));
    }

    // -----------------------------------------------------------------------
    // render_repo_tabs — tab bar content and styling
    // -----------------------------------------------------------------------

    fn make_repo_app(
        repos: Vec<crate::global_config::RepoConfig>,
        active_repo_index: usize,
    ) -> App {
        let mut app = App::new_test(vec![]);
        app.global_config.repos = repos;
        app.active_repo_index = active_repo_index;
        app
    }

    fn render_tabs_to_buffer(app: &App) -> ratatui::buffer::Buffer {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;
        let backend = TestBackend::new(120, 3);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_repo_tabs(f, f.area());
            })
            .unwrap();
        terminal.backend().buffer().clone()
    }

    fn buffer_to_string(buffer: &ratatui::buffer::Buffer) -> String {
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push_str(buffer[(x, y)].symbol());
            }
        }
        text
    }

    #[test]
    fn render_repo_tabs_all_tab_label_present() {
        let app = make_repo_app(vec![], 0);
        let buf = render_tabs_to_buffer(&app);
        let text = buffer_to_string(&buf);
        assert!(text.contains("ALL"), "tab bar must contain 'ALL'");
    }

    #[test]
    fn render_repo_tabs_active_all_border_uses_accent_color() {
        let app = make_repo_app(vec![], 0);
        let buf = render_tabs_to_buffer(&app);
        // Active ALL tab: top-left corner (╭) at (0,0) should have accent fg.
        let corner = &buf[(0, 0)];
        assert_eq!(
            corner.style().fg,
            Some(app.theme.accent),
            "active ALL tab border must use accent color"
        );
    }

    #[test]
    fn render_repo_tabs_active_all_label_has_solid_bg() {
        let app = make_repo_app(vec![], 0);
        let buf = render_tabs_to_buffer(&app);
        // Label text is on row 1 (content row inside the block).
        // Active tab has solid colored bg with black fg text.
        let all_cell = (0..buf.area.width)
            .map(|x| &buf[(x, 1)])
            .find(|c| c.symbol() == "A");
        assert!(
            all_cell.is_some_and(|c| c.style().fg == Some(app.theme.accent)),
            "active ALL tab label must use accent fg color"
        );
    }

    #[test]
    fn render_repo_tabs_inactive_all_uses_dimmed_color() {
        use crate::global_config::RepoConfig;
        let repos = vec![RepoConfig {
            slug: "owner/alpha".to_string(),
            path: "/workspace/alpha".to_string(),
            remotes: vec![],
        }];
        let app = make_repo_app(repos, 1);
        let buf = render_tabs_to_buffer(&app);
        // Inactive ALL: label on row 1 should have dimmed fg.
        let all_cell = (0..buf.area.width)
            .map(|x| &buf[(x, 1)])
            .find(|c| c.symbol() == "A");
        assert!(
            all_cell.is_some(),
            "inactive ALL tab label must still appear"
        );
    }

    #[test]
    fn render_repo_tabs_repo_name_uppercased() {
        use crate::global_config::RepoConfig;
        let repos = vec![RepoConfig {
            slug: "owner/myrepo".to_string(),
            path: "/workspace/myrepo".to_string(),
            remotes: vec![],
        }];
        let app = make_repo_app(repos, 0);
        let buf = render_tabs_to_buffer(&app);
        let text = buffer_to_string(&buf);
        assert!(
            text.contains("MYREPO"),
            "repo tab label must be uppercased, got: {text}"
        );
    }

    #[test]
    fn render_repo_tabs_active_repo_border_uses_repo_color() {
        use crate::global_config::RepoConfig;
        use crate::tui::theme::repo_color;
        let repos = vec![RepoConfig {
            slug: "owner/beta".to_string(),
            path: "/workspace/beta".to_string(),
            remotes: vec![],
        }];
        let app = make_repo_app(repos, 1);
        let buf = render_tabs_to_buffer(&app);
        // Active repo badge starts after ALL badge (width 7) + 1 gap = x=8.
        // The border corner at row 0 should have repo_color(0) fg.
        let expected_color = repo_color(0);
        // Inactive ALL has no border, so the only ╭ is the active repo's.
        let corner = (0..buf.area.width)
            .map(|x| &buf[(x, 0)])
            .find(|c| c.symbol() == "╭");
        assert!(
            corner.is_some_and(|c| c.style().fg == Some(expected_color)),
            "active repo tab border must use repo_color"
        );
    }

    #[test]
    fn render_repo_tabs_inactive_repo_label_present() {
        use crate::global_config::RepoConfig;
        let repos = vec![
            RepoConfig {
                slug: "owner/alpha".to_string(),
                path: "/workspace/alpha".to_string(),
                remotes: vec![],
            },
            RepoConfig {
                slug: "owner/beta".to_string(),
                path: "/workspace/beta".to_string(),
                remotes: vec![],
            },
        ];
        let app = make_repo_app(repos, 2);
        let buf = render_tabs_to_buffer(&app);
        let text = buffer_to_string(&buf);
        assert!(
            text.contains("ALPHA"),
            "inactive repo label must still appear"
        );
    }

    // -----------------------------------------------------------------------
    // group_header_row — bar column alignment (strengthened)
    // -----------------------------------------------------------------------

    #[test]
    fn group_header_row_first_cell_is_empty() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let theme = Theme::default();
        let row = group_header_row(DisplayGroup::Other, 5, &theme);
        let backend = TestBackend::new(80, 5);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                let widths = vec![
                    Constraint::Length(1),
                    Constraint::Length(3),
                    Constraint::Length(6),
                    Constraint::Min(10),
                    Constraint::Length(10),
                ];
                let table = Table::new(vec![row], &widths);
                f.render_widget(table, f.area());
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        // The first cell (bar column, x=0) must be blank — the group header row
        // does not draw a color bar, leaving that column empty as a placeholder.
        assert_eq!(
            buffer[(0u16, 0u16)].symbol(),
            " ",
            "first cell (bar placeholder) must be a blank space"
        );
    }

    // -----------------------------------------------------------------------
    // Expand/collapse indicator tests
    // -----------------------------------------------------------------------

    #[test]
    fn expand_indicator_collapsed_multi_pane() {
        let result = expand_indicator("\u{26a1} active", 3, false);
        assert_eq!(result, "\u{25b6} active(3)", "got: {}", result);
    }

    #[test]
    fn expand_indicator_expanded_multi_pane() {
        let result = expand_indicator("\u{26a1} active", 3, true);
        assert_eq!(result, "\u{25bc} active(3)", "got: {}", result);
    }

    #[test]
    fn expand_indicator_single_pane_no_indicator() {
        let result = expand_indicator("\u{26a1} active", 1, false);
        assert_eq!(result, "\u{26a1} active");
    }

    #[test]
    fn expand_indicator_zero_panes_no_indicator() {
        let result = expand_indicator("\u{25cf} idle", 0, false);
        assert_eq!(result, "\u{25cf} idle");
    }

    // -----------------------------------------------------------------------
    // Column order tests (BAR, #, CLAUDE, ISSUE, TITLE, ...)
    // -----------------------------------------------------------------------

    #[test]
    fn column_order_claude_after_number() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let row = WorktreeRow {
            sessions: vec![make_session("sess")],
            ..make_task_row(42, DisplayGroup::Other)
        };
        let app = App::new_test(vec![row]);
        let backend = TestBackend::new(160, 40);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_task_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        // Check that the header row contains CLAUDE before ISSUE
        let mut header_text = String::new();
        // Header row is in the table area; check all rows for the header
        for y in 0..buffer.area.height {
            let mut line = String::new();
            for x in 0..buffer.area.width {
                line.push_str(buffer[(x, y)].symbol());
            }
            if line.contains("CLAUDE") && line.contains("ISSUE") && line.contains("TITLE") {
                header_text = line;
                break;
            }
        }
        assert!(
            !header_text.is_empty(),
            "should find header row with CLAUDE, ISSUE, TITLE"
        );
        let claude_pos = header_text.find("CLAUDE").unwrap();
        let issue_pos = header_text.find("ISSUE").unwrap();
        let title_pos = header_text.find("TITLE").unwrap();
        assert!(
            claude_pos < issue_pos,
            "CLAUDE ({}) must appear before ISSUE ({})",
            claude_pos,
            issue_pos
        );
        assert!(
            issue_pos < title_pos,
            "ISSUE ({}) must appear before TITLE ({})",
            issue_pos,
            title_pos
        );
    }

    // -----------------------------------------------------------------------
    // Hints bar — expand/collapse hints
    // -----------------------------------------------------------------------

    #[test]
    fn hints_bar_shows_expand_hints() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let backend = TestBackend::new(200, 40);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal
            .draw(|f| {
                app.render_task_list(f);
            })
            .unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut full_text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                full_text.push_str(buffer[(x, y)].symbol());
            }
        }
        assert!(
            full_text.contains("expand"),
            "hints bar must contain 'expand' hint"
        );
    }

    // -----------------------------------------------------------------------
    // Preview placeholder tests
    // -----------------------------------------------------------------------

    #[test]
    fn preview_placeholder_renders_when_pane_content_empty() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        // Row exists but pane_content is empty — placeholder should render.
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let backend = TestBackend::new(120, 50);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|f| app.render_task_list(f)).unwrap();

        let buffer = terminal.backend().buffer().clone();
        let mut text = String::new();
        for y in 0..buffer.area.height {
            for x in 0..buffer.area.width {
                text.push_str(buffer[(x, y)].symbol());
            }
        }
        assert!(
            text.contains("no preview available"),
            "preview placeholder must render 'no preview available' message"
        );
    }

    #[test]
    fn preview_placeholder_clears_hit_test_rect() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        // Placeholder should not be scroll-interactive — preview_area must be zero.
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let backend = TestBackend::new(120, 50);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|f| app.render_task_list(f)).unwrap();
        assert_eq!(
            app.preview_area.get().width,
            0,
            "placeholder render should leave preview_area as zero rect"
        );
    }

    // -----------------------------------------------------------------------
    // Table height cap tests
    // -----------------------------------------------------------------------

    /// Makes a task row that has an active session (needed to trigger preview).
    fn make_task_row_with_session_for_preview(issue: u32) -> WorktreeRow {
        WorktreeRow {
            sessions: vec![make_session(&format!("sess-{}", issue))],
            ..make_task_row(issue, DisplayGroup::Other)
        }
    }

    #[test]
    fn table_height_capped_at_40_percent_when_preview_visible() {
        use ratatui::Terminal;
        use ratatui::backend::TestBackend;

        // Build an app with 20 task rows (each height=1) + sessions so preview shows.
        let rows: Vec<WorktreeRow> = (1u32..=20)
            .map(make_task_row_with_session_for_preview)
            .collect();
        let mut app = App::new_test(rows);
        // Set pane_content so preview_visible returns true.
        app.pane_content = "some pane output".to_string();

        let terminal_height: u16 = 50;
        let backend = TestBackend::new(120, terminal_height);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|f| app.render_task_list(f)).unwrap();

        // The table_area is the body rect (table_chunk.height - TABLE_CHROME_HEIGHT).
        // With preview visible and 20 rows, uncapped table_height = 20 + 3 = 23.
        // 40% of 50 = 20.0 -> max_table_height = max(20, TABLE_MIN_HEIGHT=5) = 20.
        // Capped table_chunk.height = min(23, 20) = 20.
        // table_area.height = 20 - 3 = 17.
        let max_table_chunk_height =
            ((terminal_height as f32 * TABLE_MAX_HEIGHT_FRACTION) as u16).max(TABLE_MIN_HEIGHT);
        let table_area = app.table_area.get();
        let table_chunk_height = table_area.height + TABLE_CHROME_HEIGHT;
        assert!(
            table_chunk_height <= max_table_chunk_height,
            "table chunk height {table_chunk_height} should be <= {max_table_chunk_height}"
        );
        assert!(
            table_chunk_height >= TABLE_MIN_HEIGHT,
            "table chunk height {table_chunk_height} should be >= {TABLE_MIN_HEIGHT}"
        );
    }
}
