use crossterm::event::{KeyCode, KeyEvent};
use ratatui::prelude::*;
use ratatui::widgets::*;

use std::collections::HashSet;
use std::time::Instant;

use crate::derive::{DisplayGroup, TaskRow};
use crate::navigation;
use crate::paths;
use crate::tui::state::{
    CleanupState, DeleteState, FilterMode, NewSessionState, Phase, TransferState, ViewState,
};
use crate::tui::widgets::{claude_badge, status_badge};
use crate::tui::{filter_stale, App, SPINNER_FRAMES, WARNING_DURATION_SECS};
use crate::remote;
use crate::tmux;
use crate::types::Worktree;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

/// Number of lines to capture from tmux panes for preview.
const PANE_CAPTURE_LINES: u32 = 100;

/// Minimum terminal height for the full header/logo display.
const FULL_HEADER_MIN_HEIGHT: u16 = 30;

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
    },
    CreateSession {
        worktree_path: String,
        branch: Option<String>,
        host: Option<String>,
    },
}

impl DisplayGroup {
    fn label(self) -> &'static str {
        match self {
            Self::Shepherd => "shepherd",
            Self::NeedsAttention => "needs attention",
            Self::ClaudeWorking => "claude working",
            Self::ReadyToMerge => "ready to merge",
            Self::Other => "other",
        }
    }

    fn color(self) -> Color {
        match self {
            Self::Shepherd => Color::Magenta,
            Self::NeedsAttention => Color::Red,
            Self::ClaudeWorking => Color::Green,
            Self::ReadyToMerge => Color::Cyan,
            Self::Other => Color::DarkGray,
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
    /// Sequential display number (1-based).
    pub num: usize,
    pub row: &'a TaskRow,
    pub group: DisplayGroup,
}

/// Returns the visible tasks from the pre-sorted task_rows.
///
/// When `backlog_expanded` is false, all DisplayGroup::Other rows are excluded.
/// `filter_mode` and `search_text` further narrow results, but shepherd rows always bypass both.
/// Returns `(visible_tasks, total_other_count)` where the second value is used to display
/// the backlog summary row.
pub(crate) fn visible_tasks<'a>(
    task_rows: &'a [TaskRow],
    backlog_expanded: bool,
    filter_mode: &FilterMode,
    search_text: &str,
) -> (Vec<VisibleTask<'a>>, usize) {
    let total_other = task_rows.iter().filter(|r| r.display_group == DisplayGroup::Other).count();

    let search_lower = search_text.to_lowercase();

    let mut result = Vec::new();
    let mut num = 1usize;

    for row in task_rows {
        let is_other = row.display_group == DisplayGroup::Other;

        // Backlog collapse: exclude Other rows when collapsed.
        if is_other && !backlog_expanded {
            continue;
        }

        // Shepherd rows always pass filter and search.
        if !row.is_shepherd {
            // Apply filter_mode.
            let passes_filter = match filter_mode {
                FilterMode::All => true,
                FilterMode::HasSession => !row.sessions.is_empty(),
                FilterMode::HasClaude => row.sessions.iter().any(|s| {
                    s.claude_state != crate::claude_state::ClaudeState::None
                }),
                FilterMode::HasPR => row.pr.is_some(),
            };
            if !passes_filter {
                continue;
            }

            // Apply search text.
            if !search_lower.is_empty() {
                let matches = row.repo_slug.to_lowercase().contains(&search_lower)
                    || row.branch.to_lowercase().contains(&search_lower);
                if !matches {
                    continue;
                }
            }
        }

        result.push(VisibleTask { num, row, group: row.display_group });
        num += 1;
    }

    (result, total_other)
}

/// Returns a single PR status string for the task row.
///
/// When a PR exists its number is prepended: e.g. `#123 ✓ approved`.
fn pr_status_text(row: &TaskRow) -> (String, Style) {
    let Some(ref pr) = row.pr else {
        return ("no PR".to_string(), Style::default().fg(Color::DarkGray));
    };

    let prefix = format!("#{} ", pr.number);

    if pr.review_decision.as_deref() == Some("approved") {
        return (format!("{}\u{2713} approved", prefix), Style::default().fg(Color::Green));
    }
    if pr.review_decision.as_deref() == Some("changes_requested") {
        return (format!("{}\u{2716} changes req", prefix), Style::default().fg(Color::Red));
    }
    if pr.has_conflicts {
        return (format!("{}\u{2716} conflict", prefix), Style::default().fg(Color::Red));
    }
    if pr.unresolved_threads > 0 {
        return (
            format!("{}\u{25cb} unresolved ({})", prefix, pr.unresolved_threads),
            Style::default().fg(Color::Yellow),
        );
    }
    if pr.checks_state.as_deref() == Some("failing") {
        return (format!("{}\u{2716} failing", prefix), Style::default().fg(Color::Red));
    }
    if pr.checks_state.as_deref() == Some("pending") {
        return (format!("{}\u{25d0} pending CI", prefix), Style::default().fg(Color::Yellow));
    }
    // Default for open PR with no special state
    (format!("{}\u{25cb} needs review", prefix), Style::default().fg(Color::DarkGray))
}

/// Returns a Claude activity indicator for the task row.
///
/// When hook state files are available, shows richer info including context
/// window percentage. Falls back to boolean flags from terminal scraping.
fn claude_status_text(row: &TaskRow) -> (String, Style) {
    if row.sessions.is_empty() {
        return ("\u{25cb} none".to_string(), Style::default().fg(Color::DarkGray));
    }

    let count = row.sessions.len();
    let count_suffix = if count > 1 { format!(" {}", count) } else { String::new() };

    // Find the most "urgent" structured state across sessions.
    let has_input = row.sessions.iter().any(|s| s.claude_state == crate::claude_state::ClaudeState::Input);
    let has_working = row.sessions.iter().any(|s| s.claude_state == crate::claude_state::ClaudeState::Working);
    let has_idle = row.sessions.iter().any(|s| s.claude_state == crate::claude_state::ClaudeState::Idle);

    // Get context % from any session that has it.
    let ctx_pct = row.sessions.iter().find_map(|s| s.context_window_pct);
    let ctx_suffix = ctx_pct.map(|p| format!(" {}%", p as u32)).unwrap_or_default();

    if has_input {
        return (format!("\u{2757} input{}{}", count_suffix, ctx_suffix), Style::default().fg(Color::Red));
    }
    if has_working {
        return (format!("\u{26a1} active{}{}", count_suffix, ctx_suffix), Style::default().fg(Color::Green));
    }
    if has_idle {
        return (format!("\u{25cf} idle{}{}", count_suffix, ctx_suffix), Style::default().fg(Color::Yellow));
    }

    // Fallback to boolean checks for sessions without hook data.
    if row.sessions.iter().any(|s| s.claude_needs_input) {
        return (format!("\u{2757} input{}", count_suffix), Style::default().fg(Color::Red));
    }
    if row.sessions.iter().any(|s| s.claude_is_working) {
        return (format!("\u{26a1} active{}", count_suffix), Style::default().fg(Color::Green));
    }
    if row.sessions.iter().any(|s| s.has_claude_active) {
        return (format!("\u{25cf} idle{}", count_suffix), Style::default().fg(Color::Yellow));
    }

    ("\u{25cf} idle".to_string(), Style::default().fg(Color::Yellow))
}


impl App {
    pub(crate) fn handle_list_key(&mut self, key: KeyEvent) -> bool {
        // In task mode, navigation uses the visible task count.
        if !self.task_rows.is_empty() {
            return self.handle_task_list_key(key);
        }

        match key.code {
            // Digit jump 1-9
            KeyCode::Char(c) if c.is_ascii_digit() && c != '0' => {
                if let Some(idx) =
                    navigation::cursor_index_from_digit(c, self.worktrees.len())
                {
                    self.cursor = idx;
                    self.pane_content.clear();
                    self.fetch_pane_content();
                }
                false
            }
            KeyCode::Up | KeyCode::Char('k') => {
                if self.cursor > 0 {
                    self.cursor -= 1;
                    self.pane_content.clear();
                    self.fetch_pane_content();
                }
                false
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if !self.worktrees.is_empty() && self.cursor < self.worktrees.len() - 1 {
                    self.cursor += 1;
                    self.pane_content.clear();
                    self.fetch_pane_content();
                }
                false
            }
            KeyCode::Enter | KeyCode::Char('t') => {
                self.switch_to_tmux_session()
            }
            KeyCode::Char('o') => {
                self.open_pr_url();
                false
            }
            KeyCode::Char('i') => {
                self.open_issue_url();
                false
            }
            KeyCode::Char('p') => {
                self.start_transfer_dialog();
                false
            }
            KeyCode::Char('d') => {
                self.start_delete_dialog();
                false
            }
            KeyCode::Char('c') => {
                self.enter_cleanup_view();
                false
            }
            KeyCode::Char('n') => {
                self.view = ViewState::NewSession(NewSessionState {
                    name: String::new(),
                    cursor: 0,
                });
                false
            }
            KeyCode::Char('r') => {
                self.refreshing = true;
                self.start_refresh();
                false
            }
            KeyCode::Char('R') => {
                self.reconnect_unreachable_hosts();
                false
            }
            KeyCode::Char('q') | KeyCode::Esc => {
                // Quit without switching sessions.
                true
            }
            _ => false,
        }
    }

    fn handle_task_list_key(&mut self, key: KeyEvent) -> bool {
        // Search mode takes over all key input except Esc/Enter.
        if self.search_active {
            match key.code {
                KeyCode::Esc => {
                    self.search_active = false;
                    self.search_text.clear();
                }
                KeyCode::Enter => {
                    self.search_active = false;
                }
                KeyCode::Backspace => {
                    self.search_text.pop();
                }
                KeyCode::Char(c) => {
                    self.search_text.push(c);
                }
                _ => {}
            }
            // Clamp cursor after search text change.
            let (tasks, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
            self.cursor = self.cursor.min(tasks.len().saturating_sub(1));
            return false;
        }

        let visible_count = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text).0.len();

        match key.code {
            // Digit jump 1-9: jump to flat index
            KeyCode::Char(c) if c.is_ascii_digit() && c != '0' => {
                if let Some(idx) = navigation::cursor_index_from_digit(c, visible_count) {
                    self.cursor = idx;
                    self.fetch_task_pane_content();
                }
                false
            }
            KeyCode::Up | KeyCode::Char('k') => {
                if self.cursor > 0 {
                    self.cursor -= 1;
                    self.fetch_task_pane_content();
                }
                false
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if visible_count > 0 && self.cursor < visible_count - 1 {
                    self.cursor += 1;
                    self.fetch_task_pane_content();
                }
                false
            }
            KeyCode::Enter => {
                // Switch to the task's session, or create a worktree + session if none exist.
                // Compute tasks, extract owned data, then drop before calling &mut self methods.
                let (tasks, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
                let action = tasks.get(self.cursor).map(|vt| {
                    if let Some(session) = vt.row.sessions.first() {
                        TaskEnterAction::JoinSession {
                            session_name: session.name.clone(),
                            worktree_path: vt.row.worktree_path.clone(),
                            branch: Some(vt.row.branch.clone()),
                            host: session.host.clone(),
                        }
                    } else {
                        TaskEnterAction::CreateSession {
                            worktree_path: vt.row.worktree_path.clone(),
                            branch: Some(vt.row.branch.clone()),
                            host: vt.row.worktree_host.clone(),
                        }
                    }
                });
                // Drop tasks (and its borrow of task_rows) before calling &mut self methods.
                drop(tasks);
                match action {
                    None => false,
                    Some(TaskEnterAction::JoinSession { session_name, worktree_path, branch, host }) => {
                        // Guard: refuse to join a session on an unreachable host.
                        if let Some(ref h) = host
                            && self.host_reachable.get(h.as_str()) == Some(&false) {
                                self.warning = Some((format!("@{} is unreachable", h), Instant::now()));
                                return false;
                            }
                        // Has a session — join or create it (handles both remote and local).
                        self.join_or_create_session(
                            &session_name,
                            &worktree_path,
                            branch.as_deref(),
                            host.as_deref(),
                            None,
                        )
                    }
                    Some(TaskEnterAction::CreateSession { worktree_path, branch, host }) => {
                        // Guard: refuse to create a session on an unreachable host.
                        if let Some(ref h) = host {
                            if self.host_reachable.get(h.as_str()) == Some(&false) {
                                self.warning = Some((format!("@{} is unreachable", h), Instant::now()));
                                return false;
                            }
                        }
                        // No session but worktree exists — derive session name and create.
                        let repo_name = self.repo_name.clone();
                        let session_name = tmux::derive_session_name(
                            &repo_name,
                            branch.as_deref(),
                            &worktree_path,
                        );
                        self.join_or_create_session(
                            &session_name,
                            &worktree_path,
                            branch.as_deref(),
                            host.as_deref(),
                            None,
                        )
                    }
                }
            }
            KeyCode::Char('o') => {
                // Open PR URL in browser for the selected task.
                let (visible, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
                if let Some(vt) = visible.get(self.cursor)
                    && let Some(ref pr) = vt.row.pr {
                        // Construct PR URL from repo_slug and PR number.
                        let url = format!("https://github.com/{}/pull/{}", vt.row.repo_slug, pr.number);
                        crate::browser::open_url(&url);
                    }
                false
            }
            KeyCode::Char('i') => {
                // Open issue URL in browser for the selected task.
                let (visible, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
                if let Some(vt) = visible.get(self.cursor)
                    && let Some(num) = vt.row.issue_number {
                        let url = format!("https://github.com/{}/issues/{}", vt.row.repo_slug, num);
                        crate::browser::open_url(&url);
                    }
                false
            }
            KeyCode::Char('b') => {
                self.backlog_expanded = !self.backlog_expanded;
                self.cursor = 0;
                false
            }
            KeyCode::Char('B') => {
                self.show_branch_column = !self.show_branch_column;
                false
            }
            KeyCode::Char('f') => {
                self.filter_mode = self.filter_mode.next();
                self.cursor = 0;
                false
            }
            KeyCode::Char('/') => {
                self.search_active = true;
                self.search_text.clear();
                false
            }
            KeyCode::Char('c') => {
                self.enter_cleanup_view();
                false
            }
            KeyCode::Char('r') => {
                self.refreshing = true;
                self.start_refresh();
                false
            }
            KeyCode::Char('R') => {
                self.reconnect_unreachable_hosts();
                false
            }
            KeyCode::Char('q') | KeyCode::Esc => true,
            _ => false,
        }
    }

    /// Fetches pane content for the task at the current cursor position.
    pub(crate) fn fetch_task_pane_content(&mut self) {
        self.pane_content.clear();
        let (visible, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
        if let Some(vt) = visible.get(self.cursor) {
            // Find a session to capture pane content from.
            if let Some(session) = vt.row.sessions.first() {
                let session_name = session.name.clone();
                let remote_host = session.host.clone();
                let tx = self.tx.clone();
                std::thread::spawn(move || {
                    let content = if let Some(host) = remote_host {
                        remote::capture_remote_pane_content(&host, &session_name, PANE_CAPTURE_LINES).unwrap_or_default()
                    } else {
                        tmux::capture_pane_content(&session_name, PANE_CAPTURE_LINES).unwrap_or_default()
                    };
                    let _ = tx.send(crate::tui::state::AppMsg::PaneContent(session_name, content));
                });
            }
        }
    }

    /// Attempts to reconnect all currently unreachable SSH hosts.
    ///
    /// If all hosts are reachable, displays an informational warning. Otherwise
    /// spawns a background thread to probe each unreachable host and send results
    /// back via the App message channel.
    fn reconnect_unreachable_hosts(&mut self) {
        let unreachable: Vec<String> = self.host_reachable
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

        let area = f.area();
        let width = area.width as usize;
        let hdr_height = header_height(area.height);

        // Error state
        if let Some(ref err) = self.error {
            let chunks = Layout::default()
                .direction(Direction::Vertical)
                .constraints([Constraint::Length(hdr_height), Constraint::Length(1), Constraint::Min(3)])
                .split(area);
            self.render_header(f, chunks[0]);
            let err_para = Paragraph::new(err.as_str())
                .style(Style::default().fg(Color::Red))
                .block(
                    Block::default()
                        .borders(Borders::ALL)
                        .border_style(Style::default().fg(Color::Red))
                        .border_type(BorderType::Rounded),
                )
                .wrap(Wrap { trim: true });
            f.render_widget(err_para, chunks[2]);
            return;
        }

        // Loading state
        if self.loading && self.worktrees.is_empty() {
            let chunks = Layout::default()
                .direction(Direction::Vertical)
                .constraints([Constraint::Length(hdr_height), Constraint::Length(1), Constraint::Min(3)])
                .split(area);
            self.render_header(f, chunks[0]);
            let spinner = SPINNER_FRAMES[self.spinner_frame];
            let loading_text = format!("{} Loading worktrees...", spinner);
            let para = Paragraph::new(loading_text)
                .style(Style::default().fg(Color::Cyan))
                .alignment(Alignment::Center);
            f.render_widget(para, chunks[2]);
            return;
        }

        // Empty state
        if self.worktrees.is_empty() {
            let chunks = Layout::default()
                .direction(Direction::Vertical)
                .constraints([
                    Constraint::Length(hdr_height),
                    Constraint::Length(1),
                    Constraint::Min(3),
                    Constraint::Length(1),
                ])
                .split(area);
            self.render_header(f, chunks[0]);
            let empty = Paragraph::new("No worktrees found.")
                .style(Style::default().fg(Color::Yellow))
                .block(
                    Block::default()
                        .borders(Borders::ALL)
                        .border_style(Style::default().fg(Color::Yellow))
                        .border_type(BorderType::Rounded),
                )
                .alignment(Alignment::Center);
            f.render_widget(empty, chunks[2]);
            self.render_hints(f, chunks[3]);
            return;
        }

        // Calculate preview height (+2 for borders, +1 for column header row)
        let list_height = (self.worktrees.len() as u16) + 3;
        let has_preview = !self.pane_content.is_empty()
            && self.cursor < self.worktrees.len()
            && self.worktrees[self.cursor].tmux_session.is_some();

        let has_warning = self
            .warning
            .as_ref()
            .is_some_and(|(_, t)| t.elapsed().as_secs() < WARNING_DURATION_SECS);

        let mut constraints = vec![
            Constraint::Length(hdr_height), // header
            Constraint::Length(1),          // spacer
            Constraint::Length(list_height), // worktree list
        ];

        if has_preview {
            constraints.push(Constraint::Length(1)); // spacer
            constraints.push(Constraint::Min(4));    // preview fills remaining
        }

        if has_warning {
            constraints.push(Constraint::Length(1)); // warning
        }

        constraints.push(Constraint::Length(1)); // hints

        // If no preview, add remainder absorber between list and hints
        if !has_preview {
            let hints_idx = constraints.len() - 1;
            constraints.insert(hints_idx, Constraint::Min(0));
        }

        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints(constraints)
            .split(area);

        let mut chunk_idx = 0;

        // Header
        self.render_header(f, chunks[chunk_idx]);
        chunk_idx += 1;

        // Spacer
        chunk_idx += 1;

        // Worktree list
        self.render_worktree_list(f, chunks[chunk_idx], width);
        chunk_idx += 1;

        // Preview
        if has_preview {
            chunk_idx += 1; // spacer
            self.render_preview(f, chunks[chunk_idx]);
            chunk_idx += 1;
        }

        // Warning
        if has_warning {
            if let Some((ref msg, _)) = self.warning {
                let warn = Paragraph::new(msg.as_str())
                    .style(Style::default().fg(Color::Yellow))
                    .alignment(Alignment::Center);
                f.render_widget(warn, chunks[chunk_idx]);
            }
            chunk_idx += 1;
        }

        // Hints
        self.render_hints(f, chunks[chunk_idx]);
    }

    pub(crate) fn render_header(&self, f: &mut Frame, area: Rect) {
        let green_style = Style::default().fg(Color::Green);
        let red_style = Style::default().fg(Color::Red);

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
                host_spans.push(Span::styled(" (stale)", Style::default().fg(Color::DarkGray)));
            }
        }

        // Build timestamp span.
        let timestamp_span = if self.refreshing {
            let spinner = SPINNER_FRAMES[self.spinner_frame];
            Span::styled(
                format!("  {} refreshing...", spinner),
                Style::default().fg(Color::Cyan),
            )
        } else {
            let elapsed = self.last_refresh.elapsed().as_secs();
            let ts_text = if elapsed < 60 {
                format!("  ({}s ago)", elapsed)
            } else if elapsed < 3600 {
                format!("  ({}m ago)", elapsed / 60)
            } else {
                format!("  ({}h ago)", elapsed / 3600)
            };
            Span::styled(ts_text, Style::default().fg(Color::DarkGray))
        };

        if header_height(f.area().height) == 1 {
            let mut spans = vec![Span::styled(
                "\u{1f333} Git Orchard",
                Style::default()
                    .fg(Color::Green)
                    .add_modifier(Modifier::BOLD),
            )];
            spans.extend(host_spans);
            spans.push(timestamp_span);
            if !self.refreshing {
                spans.push(Span::styled(
                    "  r:refresh",
                    Style::default().fg(Color::DarkGray),
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
                host_line_spans.push(Span::styled(" (stale)", Style::default().fg(Color::DarkGray)));
            }
        }

        let logo_style = Style::default()
            .fg(Color::Green)
            .add_modifier(Modifier::BOLD);
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
                    .fg(Color::Green)
                    .add_modifier(Modifier::BOLD),
            )
            .block(
                Block::default()
                    .borders(Borders::ALL)
                    .border_style(Style::default().fg(Color::Green))
                    .border_type(BorderType::Rounded),
            );
        f.render_widget(header, area);
    }

    pub(crate) fn render_worktree_list(&self, f: &mut Frame, area: Rect, _term_width: usize) {
        let has_remote = self.worktrees.iter().any(|wt| wt.remote.is_some());

        // Compute actual content widths for dynamic columns
        let max_path_len = self
            .worktrees
            .iter()
            .map(|wt| paths::tildify(&wt.path).len())
            .max()
            .unwrap_or(10);
        let max_branch_len = self
            .worktrees
            .iter()
            .map(|wt| wt.branch.as_deref().unwrap_or("(detached)").len())
            .max()
            .unwrap_or(10);
        let max_session_len = self
            .worktrees
            .iter()
            .filter_map(|wt| wt.tmux_session.as_ref())
            .map(|s| s.len() + 2) // +2 for icon prefix
            .max()
            .unwrap_or(10);

        // Build column constraints — use content-based sizing with Fill for the two big columns
        let mut widths = vec![
            Constraint::Length(4),                                        // cursor+index
            Constraint::Max(max_path_len as u16 + 1),                    // path: fit content, shrink if needed
            Constraint::Max(max_branch_len as u16 + 1),                  // branch: fit content, shrink if needed
            Constraint::Length(12),                                       // status
            Constraint::Length(8),                                        // claude
        ];
        if has_remote {
            widths.push(Constraint::Length(14));                          // remote
        }
        widths.push(Constraint::Max(max_session_len as u16 + 1));        // session: fit content

        // Header
        let header_style = Style::default()
            .fg(Color::DarkGray)
            .add_modifier(Modifier::BOLD);
        let mut header_cells = vec![
            Cell::from("  #"),
            Cell::from("PATH"),
            Cell::from("BRANCH"),
            Cell::from("STATUS"),
            Cell::from("CLAUDE"),
        ];
        if has_remote {
            header_cells.push(Cell::from("REMOTE"));
        }
        header_cells.push(Cell::from("SESSION"));
        let header_row = Row::new(header_cells).style(header_style);

        // Data rows
        let rows: Vec<Row> = self
            .worktrees
            .iter()
            .enumerate()
            .map(|(i, wt)| {
                let selected = i == self.cursor;
                let cursor_char = if selected { ">" } else { " " };
                let idx_cell = Cell::from(format!("{}{:>2}", cursor_char, i + 1));

                // Path
                let path_display = paths::tildify(&wt.path);
                let path_cell = Cell::from(path_display);

                // Branch
                let branch_str = wt.branch.as_deref().unwrap_or("(detached)").to_string();
                let branch_cell =
                    Cell::from(branch_str).style(Style::default().fg(Color::Yellow));

                // Status
                let badge = status_badge(wt, self.refreshing);
                let status_cell = Cell::from(badge.text).style(badge.style);

                // Claude
                let cbadge = claude_badge(wt);
                let claude_cell = Cell::from(cbadge.text).style(cbadge.style);

                // Session
                let tmux_str = if let Some(ref sess) = wt.tmux_session {
                    let icon = if wt.tmux_attached {
                        "\u{25b6}"
                    } else {
                        "\u{25fc}"
                    };
                    format!("{} {}", icon, sess)
                } else {
                    String::new()
                };
                let tmux_style = if wt.tmux_session.is_some() {
                    if wt.tmux_attached {
                        Style::default().fg(Color::Green)
                    } else {
                        Style::default().fg(Color::Blue)
                    }
                } else {
                    Style::default().fg(Color::DarkGray)
                };
                let tmux_cell = Cell::from(tmux_str).style(tmux_style);

                // Determine host reachability for this worktree.
                let host_unreachable = wt
                    .remote
                    .as_ref()
                    .and_then(|h| self.host_reachable.get(h.as_str()))
                    .copied()
                    == Some(false);

                let mut cells =
                    vec![idx_cell, path_cell, branch_cell, status_cell, claude_cell];
                if has_remote {
                    let remote_cell = if let Some(ref h) = wt.remote {
                        match self.host_reachable.get(h.as_str()) {
                            Some(&false) => Cell::from(format!("@{} \u{2717}", h))
                                .style(Style::default().fg(Color::Red)),
                            Some(&true) => Cell::from(format!("@{} \u{25cf}", h))
                                .style(Style::default().fg(Color::Green)),
                            None => Cell::from(format!("@{}", h))
                                .style(Style::default().fg(Color::Magenta)),
                        }
                    } else {
                        Cell::from("").style(Style::default().fg(Color::Magenta))
                    };
                    cells.push(remote_cell);
                }
                cells.push(tmux_cell);

                let row = Row::new(cells);
                if selected {
                    row.style(
                        Style::default()
                            .fg(Color::Cyan)
                            .add_modifier(Modifier::BOLD)
                            .add_modifier(if host_unreachable { Modifier::DIM } else { Modifier::empty() }),
                    )
                } else if host_unreachable {
                    row.style(Style::default().add_modifier(Modifier::DIM))
                } else {
                    row
                }
            })
            .collect();

        let block = Block::default()
            .title(" WORKTREES ")
            .title_style(
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            )
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Cyan))
            .border_type(BorderType::Rounded);

        let table = Table::new(rows, &widths)
            .header(header_row)
            .block(block)
            .column_spacing(1);

        f.render_widget(table, area);
    }

    pub(crate) fn render_preview(&self, f: &mut Frame, area: Rect) {
        if self.pane_content.is_empty()
            || self.worktrees.is_empty()
            || self.cursor >= self.worktrees.len()
        {
            return;
        }
        let wt = &self.worktrees[self.cursor];
        if wt.tmux_session.is_none() {
            return;
        }

        let branch_label = wt.branch.as_deref().unwrap_or("(detached)");
        let title = format!(" PREVIEW \u{2014} {} ", branch_label);

        let block = Block::default()
            .title(title)
            .title_style(
                Style::default()
                    .fg(Color::DarkGray)
                    .add_modifier(Modifier::BOLD),
            )
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::DarkGray))
            .border_type(BorderType::Double);

        // Truncate content lines to fit
        let inner_height = area.height.saturating_sub(2) as usize;
        let all_lines: Vec<&str> = self.pane_content.lines().collect();
        let display_lines = if all_lines.len() > inner_height {
            &all_lines[all_lines.len() - inner_height..]
        } else {
            &all_lines
        };
        let content = display_lines.join("\n");

        let preview = Paragraph::new(content)
            .style(Style::default().fg(Color::Gray))
            .block(block);
        f.render_widget(preview, area);
    }

    pub(crate) fn render_hints(&self, f: &mut Frame, area: Rect) {
        let sep = Span::styled(" \u{2502} ", Style::default().fg(Color::DarkGray));

        let mut spans: Vec<Span> = vec![
            Span::styled(
                "1-9",
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::raw(" jump"),
            sep.clone(),
            Span::styled(
                "enter",
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::raw(" tmux"),
        ];

        // PR link hint
        let has_pr_url = !self.worktrees.is_empty()
            && self.cursor < self.worktrees.len()
            && self.worktrees[self.cursor]
                .pr
                .as_ref()
                .is_some_and(|pr| !pr.url.is_empty());
        spans.push(sep.clone());
        if has_pr_url {
            spans.push(Span::styled(
                "o",
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            ));
            spans.push(Span::raw(" pr"));
        } else {
            spans.push(Span::styled(
                "o pr",
                Style::default().fg(Color::DarkGray),
            ));
        }

        // Transfer hint
        if self.global_config.repos.iter().any(|r| !r.remotes.is_empty()) {
            spans.push(sep.clone());
            let is_remote = !self.worktrees.is_empty()
                && self.cursor < self.worktrees.len()
                && self.worktrees[self.cursor].remote.is_some();
            if is_remote {
                spans.push(Span::styled(
                    "p",
                    Style::default()
                        .fg(Color::Cyan)
                        .add_modifier(Modifier::BOLD),
                ));
                spans.push(Span::raw(" pull"));
            } else {
                spans.push(Span::styled(
                    "p",
                    Style::default()
                        .fg(Color::Cyan)
                        .add_modifier(Modifier::BOLD),
                ));
                spans.push(Span::raw(" push"));
            }
        }

        spans.push(sep.clone());
        spans.push(Span::styled(
            "d",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" delete"));

        spans.push(sep.clone());
        spans.push(Span::styled(
            "c",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" cleanup"));

        spans.push(sep.clone());
        spans.push(Span::styled(
            "n",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" new"));

        let key_style = Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD);
        spans.push(sep.clone());
        self.append_common_hints(&mut spans, &sep, key_style, "back");

        let hints = Paragraph::new(Line::from(spans)).alignment(Alignment::Center);
        f.render_widget(hints, area);
    }

    // -------------------------------------------------------------------
    // Actions triggered from list view
    // -------------------------------------------------------------------

    fn selected_worktree(&self) -> Option<&Worktree> {
        self.worktrees.get(self.cursor)
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

    /// Ensures the tmux session for the selected worktree exists (creating it if needed),
    /// stores the session name in `switch_target`, and returns `true` so the event loop
    /// breaks and the TUI exits.
    fn switch_to_tmux_session(&mut self) -> bool {
        let wt = match self.selected_worktree() {
            Some(wt) => wt.clone(),
            None => return false,
        };

        // Guard: refuse to switch to a session on an unreachable host.
        if let Some(ref host) = wt.remote
            && self.host_reachable.get(host.as_str()) == Some(&false) {
                self.warning = Some((format!("@{} is unreachable", host), Instant::now()));
                return false;
            }

        let repo_name = self.repo_name.clone();

        let session_name = wt
            .tmux_session
            .clone()
            .or_else(|| {
                wt.branch.as_ref().map(|b| {
                    tmux::derive_session_name(&repo_name, Some(b), &wt.path)
                })
            })
            .unwrap_or_default();

        if session_name.is_empty() {
            return false;
        }

        self.join_or_create_session(
            &session_name,
            &wt.path,
            wt.branch.as_deref(),
            wt.remote.as_deref(),
            wt.pr.as_ref(),
        )
    }

    fn open_pr_url(&self) {
        let wt = match self.selected_worktree() {
            Some(wt) => wt,
            None => return,
        };
        if let Some(ref pr) = wt.pr
            && !pr.url.is_empty() {
                crate::browser::open_url(&pr.url);
            }
    }

    fn open_issue_url(&self) {
        let wt = match self.selected_worktree() {
            Some(wt) => wt,
            None => return,
        };
        if let Some(num) = wt.issue_number {
            // Find repo slug from global config: match by worktree path prefix.
            let slug = self.global_config.repos.iter().find_map(|repo| {
                if wt.path.starts_with(&repo.path) {
                    Some(repo.slug.as_str())
                } else {
                    None
                }
            });
            if let Some(slug) = slug {
                let url = format!("https://github.com/{}/issues/{}", slug, num);
                crate::browser::open_url(&url);
            }
        }
    }

    fn start_delete_dialog(&mut self) {
        let wt = match self.selected_worktree().cloned() {
            Some(wt) => wt,
            None => return,
        };
        if wt.is_bare {
            self.warning = Some((
                "Cannot delete the bare worktree.".to_string(),
                Instant::now(),
            ));
            return;
        }
        self.view = ViewState::ConfirmDelete(DeleteState {
            target: wt,
            phase: Phase::Confirm,
            error: None,
        });
    }

    fn start_transfer_dialog(&mut self) {
        let has_any_remote = self.global_config.repos.iter().any(|r| !r.remotes.is_empty());
        if self.worktrees.is_empty()
            || self.cursor >= self.worktrees.len()
            || !has_any_remote
        {
            return;
        }
        let wt = &self.worktrees[self.cursor];
        if wt.is_bare || wt.branch.is_none() {
            self.warning = Some((
                "Cannot transfer: no branch.".to_string(),
                Instant::now(),
            ));
            return;
        }
        self.view = ViewState::Transfer(TransferState {
            target: wt.clone(),
            phase: Phase::Confirm,
            error: None,
        });
    }

    fn enter_cleanup_view(&mut self) {
        let stale = filter_stale(&self.worktrees);
        let selected = stale.iter().map(|wt| wt.path.clone()).collect::<HashSet<_>>();
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

impl App {
    /// Renders the task-grouped view. Called by `render_list` when tasks are present.
    pub(crate) fn render_task_list(&self, f: &mut Frame) {
        let area = f.area();
        let hdr_height = header_height(area.height);

        let (tasks, total_backlog) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);

        // Only show HOST column when at least one task has a remote session or remote worktree.
        let has_remote = self.task_rows.iter().any(|r| {
            r.sessions.iter().any(|s| s.host.is_some()) || r.worktree_host.is_some()
        });

        let show_branch = self.show_branch_column;

        // Compute available width for the TITLE column.
        // Fixed columns: # (3) + spacing(1) + ISSUE (6) + spacing(1) + TITLE (flex) + spacing(1)
        //                + STATUS (22) + spacing(1) + CLAUDE (10) + borders (2)
        // Optional: + BRANCH (20) + spacing(1)
        // With HOST: + spacing(1) + HOST (12)
        let fixed = 3 + 1 + 6 + 1 + 1 + 22 + 1 + 10 + 2;
        let branch_extra = if show_branch { 20 + 1 } else { 0 };
        let host_extra = if has_remote { 1 + 12 } else { 0 };
        let title_width = (area.width as usize).saturating_sub(fixed + branch_extra + host_extra);

        // Column widths — BRANCH column optional, HOST column included only when remotes exist.
        let mut widths: Vec<Constraint> = vec![
            Constraint::Length(3),   // #
            Constraint::Length(6),   // ISSUE
            Constraint::Min(20),     // TITLE (flexible)
        ];
        if show_branch {
            widths.push(Constraint::Length(20)); // BRANCH (left-truncated)
        }
        if has_remote {
            widths.push(Constraint::Length(12)); // HOST
        }
        widths.push(Constraint::Length(22)); // STATUS
        widths.push(Constraint::Length(10)); // CLAUDE

        // Build rows for the table, including section header rows.
        let num_columns = widths.len();
        let (rows, row_heights) = self.build_task_table_rows(&tasks, show_branch, total_backlog, self.backlog_expanded, has_remote, title_width, num_columns);

        let has_warning = self
            .warning
            .as_ref()
            .is_some_and(|(_, t)| t.elapsed().as_secs() < WARNING_DURATION_SECS);

        // Calculate total table body height from individual row heights
        let body_height: u16 = row_heights.iter().sum::<u16>();
        let table_height = body_height.saturating_add(3); // +2 borders +1 header row

        // Check if selected task has a preview
        let selected_task = tasks.get(self.cursor);
        let has_preview = selected_task.is_some_and(|vt| {
            !self.pane_content.is_empty() && !vt.row.sessions.is_empty()
        });

        let mut constraints = vec![
            Constraint::Length(hdr_height),
            Constraint::Length(1), // spacer
            Constraint::Length(table_height),
        ];

        if has_preview {
            constraints.push(Constraint::Length(1)); // spacer
            constraints.push(Constraint::Min(4));    // preview fills remaining
        }

        if has_warning {
            constraints.push(Constraint::Length(1));
        }

        // If no preview, add remainder absorber between table and hints
        if !has_preview {
            constraints.push(Constraint::Min(0));
        }

        constraints.push(Constraint::Length(1)); // hints

        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints(constraints)
            .split(area);

        let mut idx = 0;
        self.render_header(f, chunks[idx]);
        idx += 1;
        idx += 1; // spacer

        // Header row
        let header_style = Style::default()
            .fg(Color::DarkGray)
            .add_modifier(Modifier::BOLD);
        let mut header_cells = vec![
            Cell::from(" #"),
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
        header_cells.push(Cell::from("CLAUDE"));
        let header_row = Row::new(header_cells).style(header_style);

        let block = Block::default()
            .title(" TASKS ")
            .title_style(Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Cyan))
            .border_type(BorderType::Rounded);

        let table = Table::new(rows, &widths)
            .header(header_row)
            .block(block)
            .column_spacing(1);

        f.render_widget(table, chunks[idx]);
        idx += 1;

        // Preview
        if has_preview {
            idx += 1; // spacer
            self.render_task_preview(f, chunks[idx], selected_task);
            idx += 1;
        }

        if has_warning {
            if let Some((ref msg, _)) = self.warning {
                let warn = Paragraph::new(msg.as_str())
                    .style(Style::default().fg(Color::Yellow))
                    .alignment(Alignment::Center);
                f.render_widget(warn, chunks[idx]);
            }
            idx += 1;
        }

        if !has_preview {
            idx += 1; // absorber
        }

        self.render_hints_task(f, chunks[idx]);
    }

    fn build_task_table_rows(
        &self,
        tasks: &[VisibleTask],
        show_branch: bool,
        total_backlog: usize,
        backlog_expanded: bool,
        has_remote: bool,
        title_width: usize,
        num_columns: usize,
    ) -> (Vec<Row<'static>>, Vec<u16>) {
        let mut rows: Vec<Row<'static>> = Vec::new();
        let mut row_heights: Vec<u16> = Vec::new();
        let mut last_group: Option<DisplayGroup> = None;

        for (flat_idx, vt) in tasks.iter().enumerate() {
            let selected = flat_idx == self.cursor;

            // Section header when display group changes
            if last_group != Some(vt.group) {
                last_group = Some(vt.group);
                let header_row = group_header_row(vt.group, num_columns, total_backlog, backlog_expanded);
                rows.push(header_row);
                row_heights.push(1);
            }

            let (pr_text, pr_style) = pr_status_text(vt.row);
            let (claude_text, claude_style) = claude_status_text(vt.row);

            let title_raw = if vt.row.is_shepherd {
                // Shepherd rows show the repo name, not the branch.
                vt.row.repo_slug.split('/').nth(1).unwrap_or(&vt.row.repo_slug)
            } else {
                match vt.row.issue_title.as_deref() {
                    Some(title) if !title.is_empty() => title,
                    _ => branch_tail(&vt.row.branch),
                }
            };
            let title_display = crate::paths::truncate_left(title_raw, title_width);

            // Determine host name for reachability lookup: prefer session host, fall back to worktree host.
            let task_host: Option<&str> = vt.row.sessions.iter().find_map(|s| s.host.as_deref())
                .or(vt.row.worktree_host.as_deref());
            let host_unreachable = task_host
                .and_then(|h| self.host_reachable.get(h))
                .copied()
                == Some(false);

            let row_style = if selected {
                let base = Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD);
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
                Cell::from("").style(Style::default().fg(Color::DarkGray))
            };

            let mut cells = vec![
                Cell::from(format!("{:>2}", vt.num)),
                issue_cell,
                Cell::from(title_display),
            ];

            if show_branch {
                let branch_display = crate::paths::truncate_left(branch_tail(&vt.row.branch), 20);
                let branch_cell = Cell::from(branch_display).style(Style::default().fg(Color::DarkGray));
                cells.push(branch_cell);
            }

            if has_remote {
                let host_cell = if let Some(h) = task_host {
                    match self.host_reachable.get(h) {
                        Some(&false) => Cell::from(format!("@{} \u{2717}", h))
                            .style(Style::default().fg(Color::Red)),
                        Some(&true) => Cell::from(format!("@{} \u{25cf}", h))
                            .style(Style::default().fg(Color::Green)),
                        None => Cell::from(format!("@{}", h))
                            .style(Style::default().fg(Color::Magenta)),
                    }
                } else {
                    Cell::from("")
                };
                cells.push(host_cell);
            }

            cells.push(Cell::from(pr_text).style(pr_style));
            cells.push(Cell::from(claude_text).style(claude_style));

            rows.push(Row::new(cells).style(row_style));
            row_heights.push(1);
        }

        // When backlog is collapsed and there are backlog items, add summary row.
        if !backlog_expanded && total_backlog > 0 {
            let summary_text = format!("{} backlog items -- press b to expand", total_backlog);
            let mut summary_cells = vec![
                Cell::from(summary_text).style(Style::default().fg(Color::DarkGray)),
            ];
            // Fill remaining columns with empty cells to match num_columns.
            for _ in 1..num_columns {
                summary_cells.push(Cell::from(""));
            }
            rows.push(Row::new(summary_cells));
            row_heights.push(1);
        }

        (rows, row_heights)
    }

    /// Renders the preview pane with task metadata in the border title.
    fn render_task_preview(&self, f: &mut Frame, area: Rect, selected_task: Option<&VisibleTask>) {
        if self.pane_content.is_empty() {
            return;
        }
        let Some(vt) = selected_task else { return };

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
        let pr_part = vt.row.pr.as_ref().map(|p| format!(" \u{2502} pr: #{}", p.number)).unwrap_or_default();

        let title = format!("\u{2500}\u{2500} {}{}{}{} \u{2500}\u{2500}",
            issue_part, title_part, wt_part, pr_part);

        let block = Block::default()
            .title(title)
            .title_style(
                Style::default()
                    .fg(Color::DarkGray)
                    .add_modifier(Modifier::BOLD),
            )
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::DarkGray))
            .border_type(BorderType::Double);

        // Truncate content lines to fit
        let inner_height = area.height.saturating_sub(2) as usize;
        let all_lines: Vec<&str> = self.pane_content.lines().collect();
        let display_lines = if all_lines.len() > inner_height {
            &all_lines[all_lines.len() - inner_height..]
        } else {
            &all_lines
        };
        let content = display_lines.join("\n");

        let preview = Paragraph::new(content)
            .style(Style::default().fg(Color::Gray))
            .block(block);
        f.render_widget(preview, area);
    }

    /// Appends the common trailing hint keys: refresh, reconnect, quit, help.
    fn append_common_hints(&self, spans: &mut Vec<Span<'static>>, sep: &Span<'static>, key_style: Style, quit_label: &'static str) {
        if self.refreshing {
            let spinner = SPINNER_FRAMES[self.spinner_frame];
            spans.push(Span::styled(
                format!("{} refreshing...", spinner),
                Style::default().fg(Color::Cyan),
            ));
        } else {
            spans.push(Span::styled("r", key_style));
            spans.push(Span::raw(" refresh"));
        }

        let has_unreachable = self.host_reachable.values().any(|&v| !v);
        if has_unreachable {
            spans.push(sep.clone());
            spans.push(Span::styled("R", key_style));
            spans.push(Span::raw(" reconnect"));
        }

        spans.push(sep.clone());
        spans.push(Span::styled("q", key_style));
        spans.push(Span::raw(format!(" {}", quit_label)));

        spans.push(sep.clone());
        spans.push(Span::styled("?", key_style));
        spans.push(Span::raw(" help"));
    }

    /// Renders the hint bar for task mode.
    pub(crate) fn render_hints_task(&self, f: &mut Frame, area: Rect) {
        let sep = Span::styled(" \u{2502} ", Style::default().fg(Color::DarkGray));
        let key_style = Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD);

        // When search is active, show the search input indicator.
        if self.search_active {
            let search_display = format!("/ {}_", self.search_text);
            let hints = Paragraph::new(Line::from(vec![
                Span::styled(search_display, Style::default().fg(Color::Yellow)),
                Span::styled("  esc:cancel  enter:apply", Style::default().fg(Color::DarkGray)),
            ])).alignment(Alignment::Center);
            f.render_widget(hints, area);
            return;
        }

        let mut spans: Vec<Span> = vec![
            Span::styled("enter", key_style),
            Span::raw(" switch"),
            sep.clone(),
        ];

        // PR link hint — dim when selected task has no PR.
        let has_pr = !self.task_rows.is_empty() && {
            let (visible, _) = visible_tasks(&self.task_rows, self.backlog_expanded, &self.filter_mode, &self.search_text);
            visible.get(self.cursor).is_some_and(|vt| vt.row.pr.is_some())
        };
        if has_pr {
            spans.push(Span::styled("o", key_style));
            spans.push(Span::raw(" pr"));
        } else {
            spans.push(Span::styled("o pr", Style::default().fg(Color::DarkGray)));
        }
        spans.push(sep.clone());

        spans.push(Span::styled("b", key_style));
        spans.push(Span::raw(":backlog"));
        spans.push(sep.clone());

        spans.push(Span::styled("B", key_style));
        spans.push(Span::raw(":branch"));
        spans.push(sep.clone());

        spans.push(Span::styled("f", key_style));
        spans.push(Span::raw(":filter"));
        spans.push(sep.clone());

        spans.push(Span::styled("/", key_style));
        spans.push(Span::raw(":search"));

        // Active filter label.
        if self.filter_mode != crate::tui::state::FilterMode::All {
            spans.push(sep.clone());
            spans.push(Span::styled(
                format!("[{}]", self.filter_mode),
                Style::default().fg(Color::Yellow),
            ));
        }

        // Active search text label.
        if !self.search_text.is_empty() {
            spans.push(sep.clone());
            spans.push(Span::styled(
                format!("[/{}]", self.search_text),
                Style::default().fg(Color::Yellow),
            ));
        }

        spans.push(sep.clone());

        spans.push(Span::styled("c", key_style));
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

/// Creates a section header row spanning all columns for a display group.
///
/// `num_columns` is the total number of columns in the table (must match the data rows).
/// For the Other group when expanded, the label includes the backlog count.
/// The Shepherd header uses bold + Cyan styling.
fn group_header_row(
    group: DisplayGroup,
    num_columns: usize,
    total_backlog: usize,
    backlog_expanded: bool,
) -> Row<'static> {
    let label = if group == DisplayGroup::Other && backlog_expanded {
        format!("backlog ({})", total_backlog)
    } else {
        group.label().to_string()
    };

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

    let (color, bold) = if group == DisplayGroup::Shepherd {
        (Color::Cyan, true)
    } else {
        (group.color(), false)
    };

    let title_style = if bold {
        Style::default().fg(color).add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(color)
    };

    // Header text goes in the TITLE column (index 2). Fill remaining columns with empty cells.
    let mut cells: Vec<Cell> = vec![
        Cell::from(""),
        Cell::from(""),
        Cell::from(text).style(title_style),
    ];
    for _ in 3..num_columns {
        cells.push(Cell::from(""));
    }
    Row::new(cells)
}

/// Returns the height (in terminal rows) to allocate for the header.
///
/// When the terminal is tall enough (>= 30 rows), the full header is
/// shown in a bordered block (7 rows). On shorter terminals a single compact
/// line is used instead so the task list gets as much vertical space as possible.
pub(crate) fn header_height(terminal_height: u16) -> u16 {
    if terminal_height >= FULL_HEADER_MIN_HEIGHT { 9 } else { 1 }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo, SessionInfo, TaskRow};

    fn make_task_row(issue_number: u32, group: DisplayGroup) -> TaskRow {
        TaskRow {
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
            is_shepherd: false,
        }
    }

    fn make_session_info(name: &str) -> SessionInfo {
        SessionInfo {
            name: name.to_string(),
            host: None,
            has_claude_active: false,
            claude_is_working: false,
            claude_needs_input: false,
            claude_state: crate::claude_state::ClaudeState::None,
            context_window_pct: None,
            cost_usd: None,
            model: None,
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
    fn visible_tasks_returns_all_non_backlog() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
            make_task_row(3, DisplayGroup::Other),
        ];
        // With backlog collapsed (default), Other rows are excluded.
        let (visible, total_backlog) = visible_tasks(&rows, false, &FilterMode::All, "");
        assert_eq!(visible.len(), 2);
        assert_eq!(total_backlog, 1);
    }

    #[test]
    fn backlog_collapsed_excludes_other() {
        let rows: Vec<TaskRow> = (1u32..=5)
            .map(|i| make_task_row(i, DisplayGroup::Other))
            .collect();
        let (visible, total) = visible_tasks(&rows, false, &FilterMode::All, "");
        assert_eq!(total, 5);
        assert_eq!(visible.len(), 0, "collapsed backlog should have no visible rows");
    }

    #[test]
    fn backlog_expanded_includes_other() {
        let rows: Vec<TaskRow> = (1u32..=5)
            .map(|i| make_task_row(i, DisplayGroup::Other))
            .collect();
        let (visible, total) = visible_tasks(&rows, true, &FilterMode::All, "");
        assert_eq!(total, 5);
        assert_eq!(visible.len(), 5, "expanded backlog should show all Other rows");
    }

    #[test]
    fn sequential_numbering_across_groups() {
        let rows = vec![
            make_task_row(10, DisplayGroup::NeedsAttention),
            make_task_row(20, DisplayGroup::ClaudeWorking),
            make_task_row(30, DisplayGroup::Other),
        ];
        let (visible, _) = visible_tasks(&rows, true, &FilterMode::All, "");
        assert_eq!(visible.len(), 3);
        assert_eq!(visible[0].num, 1);
        assert_eq!(visible[1].num, 2);
        assert_eq!(visible[2].num, 3);
    }

    #[test]
    fn filter_has_session() {
        let row_no_session = make_task_row(1, DisplayGroup::NeedsAttention);
        let row_with_session = TaskRow {
            sessions: vec![make_session_info("sess")],
            ..make_task_row(2, DisplayGroup::ClaudeWorking)
        };
        let shepherd = TaskRow { is_shepherd: true, ..make_task_row(3, DisplayGroup::Shepherd) };
        let rows = vec![shepherd, row_no_session, row_with_session];
        let (visible, _) = visible_tasks(&rows, false, &FilterMode::HasSession, "");
        // shepherd always passes + row with session
        assert_eq!(visible.len(), 2);
        assert!(visible.iter().any(|v| v.row.is_shepherd));
        assert!(visible.iter().any(|v| !v.row.sessions.is_empty()));
    }

    #[test]
    fn filter_has_pr() {
        use crate::derive::PrInfo as DPrInfo;
        let row_no_pr = make_task_row(1, DisplayGroup::NeedsAttention);
        let row_with_pr = TaskRow {
            pr: Some(DPrInfo {
                number: 10,
                branch: "feat/pr".to_string(),
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(2, DisplayGroup::ReadyToMerge)
        };
        let shepherd = TaskRow { is_shepherd: true, ..make_task_row(3, DisplayGroup::Shepherd) };
        let rows = vec![shepherd, row_no_pr, row_with_pr];
        let (visible, _) = visible_tasks(&rows, false, &FilterMode::HasPR, "");
        assert_eq!(visible.len(), 2);
        assert!(visible.iter().any(|v| v.row.is_shepherd));
        assert!(visible.iter().any(|v| v.row.pr.is_some()));
    }

    #[test]
    fn filter_has_claude() {
        let row_no_claude = make_task_row(1, DisplayGroup::NeedsAttention);
        let row_with_claude = TaskRow {
            sessions: vec![SessionInfo {
                claude_state: crate::claude_state::ClaudeState::Working,
                has_claude_active: true,
                claude_is_working: true,
                ..make_session_info("sess")
            }],
            ..make_task_row(2, DisplayGroup::ClaudeWorking)
        };
        let shepherd = TaskRow { is_shepherd: true, ..make_task_row(3, DisplayGroup::Shepherd) };
        let rows = vec![shepherd, row_no_claude, row_with_claude];
        let (visible, _) = visible_tasks(&rows, false, &FilterMode::HasClaude, "");
        assert_eq!(visible.len(), 2);
        assert!(visible.iter().any(|v| v.row.is_shepherd));
        assert!(visible.iter().any(|v| v.row.sessions.iter().any(|s| s.claude_state != crate::claude_state::ClaudeState::None)));
    }

    #[test]
    fn search_filters_by_text() {
        let row_match = TaskRow {
            branch: "feat/my-feature".to_string(),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let row_no_match = TaskRow {
            branch: "feat/other-thing".to_string(),
            ..make_task_row(2, DisplayGroup::ClaudeWorking)
        };
        let rows = vec![row_match, row_no_match];
        let (visible, _) = visible_tasks(&rows, false, &FilterMode::All, "my-feature");
        assert_eq!(visible.len(), 1);
        assert_eq!(visible[0].row.branch, "feat/my-feature");
    }

    #[test]
    fn shepherd_always_visible() {
        let shepherd = TaskRow {
            is_shepherd: true,
            branch: "main".to_string(),
            ..make_task_row(1, DisplayGroup::Shepherd)
        };
        let other = make_task_row(2, DisplayGroup::NeedsAttention);
        let rows = vec![shepherd, other];
        // HasPR filter would exclude both, but shepherd bypasses it.
        let (visible, _) = visible_tasks(&rows, false, &FilterMode::HasPR, "nomatch");
        assert_eq!(visible.len(), 1);
        assert!(visible[0].row.is_shepherd);
    }

    #[test]
    fn pr_status_approved_text() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 42,
                branch: "feat/branch".to_string(),
                review_decision: Some("approved".to_string()),
                checks_state: Some("passing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::ReadyToMerge)
        };
        let (text, _) = pr_status_text(&row);
        assert!(text.starts_with("#42 "), "expected '#42 ' prefix in: {}", text);
        assert!(text.contains("approved"), "expected 'approved' in: {}", text);
    }

    #[test]
    fn pr_status_no_pr() {
        let row = make_task_row(1, DisplayGroup::Other);
        let (text, _) = pr_status_text(&row);
        assert_eq!(text, "no PR");
    }

    #[test]
    fn claude_status_active() {
        let row = TaskRow {
            sessions: vec![SessionInfo {
                name: "sess".to_string(),
                host: None,
                has_claude_active: true,
                claude_is_working: true,
                claude_needs_input: false,
                claude_state: crate::claude_state::ClaudeState::None,
                context_window_pct: None,
                cost_usd: None,
                model: None,
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("active"), "expected 'active' in: {}", text);
    }

    #[test]
    fn claude_status_idle() {
        let row = TaskRow {
            sessions: vec![SessionInfo {
                name: "sess".to_string(),
                host: None,
                has_claude_active: false,
                claude_is_working: false,
                claude_needs_input: false,
                claude_state: crate::claude_state::ClaudeState::None,
                context_window_pct: None,
                cost_usd: None,
                model: None,
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("idle"), "expected 'idle' in: {}", text);
    }

    #[test]
    fn claude_status_needs_input() {
        let row = TaskRow {
            sessions: vec![SessionInfo {
                name: "sess".to_string(),
                host: None,
                has_claude_active: true,
                claude_is_working: false,
                claude_needs_input: true,
                claude_state: crate::claude_state::ClaudeState::Input,
                context_window_pct: None,
                cost_usd: None,
                model: None,
            }],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("input"), "expected 'input' in: {}", text);
    }

        #[test]
    fn claude_status_none_when_no_session() {
        let row = make_task_row(1, DisplayGroup::Other);
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("none"), "expected 'none' in: {}", text);
    }

    #[test]
    fn pr_status_changes_requested() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: Some("changes_requested".to_string()),
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row);
        assert!(text.contains("changes req"), "expected 'changes req' in: {}", text);
    }

    #[test]
    fn pr_status_conflict() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: None,
                checks_state: None,
                has_conflicts: true,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row);
        assert!(text.contains("conflict"), "expected 'conflict' in: {}", text);
    }

    #[test]
    fn pr_status_unresolved_threads() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 3,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row);
        assert!(text.contains("unresolved"), "expected 'unresolved' in: {}", text);
        assert!(text.contains("3"), "expected count 3 in: {}", text);
    }

    #[test]
    fn pr_status_failing_ci() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: None,
                checks_state: Some("failing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = pr_status_text(&row);
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
        assert_eq!(branch_tail("user/feat/issue-456-refactor"), "issue-456-refactor");
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

    fn session_with_hook_state(state: crate::claude_state::ClaudeState, ctx_pct: Option<f64>) -> SessionInfo {
        let (has_active, is_working, needs_input) = match state {
            crate::claude_state::ClaudeState::Working => (true, true, false),
            crate::claude_state::ClaudeState::Idle => (true, false, false),
            crate::claude_state::ClaudeState::Input => (true, false, true),
            crate::claude_state::ClaudeState::None => (false, false, false),
        };
        SessionInfo {
            name: "sess".to_string(),
            host: None,
            has_claude_active: has_active,
            claude_is_working: is_working,
            claude_needs_input: needs_input,
            claude_state: state,
            context_window_pct: ctx_pct,
            cost_usd: None,
            model: None,
        }
    }

    #[test]
    fn claude_status_working_with_context_shows_percentage() {
        let row = TaskRow {
            sessions: vec![session_with_hook_state(crate::claude_state::ClaudeState::Working, Some(73.0))],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("active"), "expected 'active' in: {}", text);
        assert!(text.contains("73%"), "expected '73%' in: {}", text);
    }

    #[test]
    fn claude_status_idle_from_hook_state() {
        let row = TaskRow {
            sessions: vec![session_with_hook_state(crate::claude_state::ClaudeState::Idle, None)],
            ..make_task_row(1, DisplayGroup::Other)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("idle"), "expected 'idle' in: {}", text);
    }

    #[test]
    fn claude_status_input_from_hook_state() {
        let row = TaskRow {
            sessions: vec![session_with_hook_state(crate::claude_state::ClaudeState::Input, None)],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("input"), "expected 'input' in: {}", text);
    }

    #[test]
    fn claude_status_input_with_context_shows_percentage() {
        let row = TaskRow {
            sessions: vec![session_with_hook_state(crate::claude_state::ClaudeState::Input, Some(95.0))],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row);
        assert!(text.contains("input"), "expected 'input' in: {}", text);
        assert!(text.contains("95%"), "expected '95%' in: {}", text);
    }

    #[test]
    fn claude_status_no_context_pct_when_none() {
        let row = TaskRow {
            sessions: vec![session_with_hook_state(crate::claude_state::ClaudeState::Working, None)],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let (text, _) = claude_status_text(&row);
        assert!(!text.contains('%'), "expected no % when context_window_pct is None: {}", text);
    }

    #[test]
    fn pr_status_pending_ci() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: None,
                checks_state: Some("pending".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::Other)
        };
        let (text, _) = pr_status_text(&row);
        assert!(text.contains("pending"), "expected 'pending' in: {}", text);
    }

    #[test]
    fn claude_status_multiple_sessions() {
        let row = TaskRow {
            sessions: vec![
                SessionInfo {
                    name: "sess1".to_string(),
                    host: None,
                    has_claude_active: true,
                    claude_is_working: true,
                    claude_needs_input: false,
                    claude_state: crate::claude_state::ClaudeState::Working,
                    context_window_pct: None,
                    cost_usd: None,
                    model: None,
                },
                SessionInfo {
                    name: "sess2".to_string(),
                    host: None,
                    has_claude_active: true,
                    claude_is_working: false,
                    claude_needs_input: true,
                    claude_state: crate::claude_state::ClaudeState::Input,
                    context_window_pct: Some(45.0),
                    cost_usd: None,
                    model: None,
                },
            ],
            ..make_task_row(1, DisplayGroup::NeedsAttention)
        };
        let (text, _) = claude_status_text(&row);
        // Input takes priority over working; count suffix should show " 2"
        assert!(text.contains("input"), "expected 'input' in: {}", text);
        assert!(text.contains("2"), "expected session count '2' in: {}", text);
    }

    #[test]
    fn search_is_case_insensitive() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
        ];
        // Search with uppercase should match lowercase branch "feat/issue-1"
        let (visible, _) = visible_tasks(&rows, true, &FilterMode::All, "FEAT/ISSUE");
        assert_eq!(visible.len(), 1);
    }

    #[test]
    fn combined_filter_and_search() {
        let mut row_with_session = make_task_row(1, DisplayGroup::NeedsAttention);
        row_with_session.sessions = vec![make_session_info("sess1")];
        row_with_session.branch = "feat/target-branch".to_string();

        let mut row_with_session_no_match = make_task_row(2, DisplayGroup::NeedsAttention);
        row_with_session_no_match.sessions = vec![make_session_info("sess2")];
        row_with_session_no_match.branch = "feat/other-branch".to_string();

        let row_no_session = make_task_row(3, DisplayGroup::NeedsAttention);

        let rows = vec![row_with_session, row_with_session_no_match, row_no_session];

        // HasSession filter + search "target" should only match the first row
        let (visible, _) = visible_tasks(&rows, true, &FilterMode::HasSession, "target");
        assert_eq!(visible.len(), 1);
        assert_eq!(visible[0].row.issue_number, Some(1));
    }
}
