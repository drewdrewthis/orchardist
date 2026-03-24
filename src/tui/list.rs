use crossterm::event::{KeyCode, KeyEvent};
use ratatui::prelude::*;
use ratatui::widgets::*;

use std::collections::HashSet;
use std::time::Instant;

use crate::derive::{DisplayGroup, TaskRow};
use crate::navigation;
use crate::paths;
use crate::tui::state::{
    CleanupState, DeleteState, NewSessionState, Phase, TransferState, ViewState,
};
use crate::tui::widgets::{claude_badge, status_badge};
use crate::tui::{filter_stale, App, SPINNER_FRAMES, WARNING_DURATION_SECS};
use crate::remote;
use crate::tmux;
use crate::types::Worktree;

// ---------------------------------------------------------------------------
// Task view helpers
// ---------------------------------------------------------------------------

const BACKLOG_PAGE_SIZE: usize = 10;

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

/// A task entry prepared for rendering in the task-centric view.
#[derive(Debug)]
pub(crate) struct VisibleTask<'a> {
    /// Sequential display number (1-based).
    pub num: usize,
    pub row: &'a TaskRow,
    pub group: DisplayGroup,
}

/// Returns the visible tasks from the pre-sorted task_rows, paginating backlog.
///
/// `task_rows` is already sorted by display group then issue number (from derive module).
/// This function paginates the backlog section and assigns sequential display numbers.
pub(crate) fn visible_tasks<'a>(
    task_rows: &'a [TaskRow],
    backlog_page: usize,
) -> (Vec<VisibleTask<'a>>, usize) {
    let total_backlog = task_rows.iter().filter(|r| r.display_group == DisplayGroup::Other).count();

    let non_backlog: Vec<&TaskRow> = task_rows
        .iter()
        .filter(|r| r.display_group != DisplayGroup::Other)
        .collect();

    let backlog_all: Vec<&TaskRow> = task_rows
        .iter()
        .filter(|r| r.display_group == DisplayGroup::Other)
        .collect();

    let backlog_start = backlog_page * BACKLOG_PAGE_SIZE;
    let backlog_page_slice = if backlog_start < backlog_all.len() {
        &backlog_all[backlog_start..(backlog_start + BACKLOG_PAGE_SIZE).min(backlog_all.len())]
    } else {
        &[][..]
    };

    let mut result = Vec::new();
    let mut num = 1usize;

    for row in &non_backlog {
        result.push(VisibleTask { num, row, group: row.display_group });
        num += 1;
    }
    for row in backlog_page_slice {
        result.push(VisibleTask { num, row, group: row.display_group });
        num += 1;
    }

    (result, total_backlog)
}

/// Returns a single PR status string for the task row.
fn pr_status_text(row: &TaskRow) -> (String, Style) {
    let Some(ref pr) = row.pr else {
        return ("no PR".to_string(), Style::default().fg(Color::DarkGray));
    };

    if pr.review_decision.as_deref() == Some("approved") {
        return ("\u{2713} approved".to_string(), Style::default().fg(Color::Green));
    }
    if pr.review_decision.as_deref() == Some("changes_requested") {
        return ("\u{2716} changes req".to_string(), Style::default().fg(Color::Red));
    }
    if pr.has_conflicts {
        return ("\u{2716} conflict".to_string(), Style::default().fg(Color::Red));
    }
    if pr.unresolved_threads > 0 {
        return (
            format!("\u{25cb} unresolved ({})", pr.unresolved_threads),
            Style::default().fg(Color::Yellow),
        );
    }
    if pr.checks_state.as_deref() == Some("failing") {
        return ("\u{2716} failing".to_string(), Style::default().fg(Color::Red));
    }
    if pr.checks_state.as_deref() == Some("pending") {
        return ("\u{25d0} pending CI".to_string(), Style::default().fg(Color::Yellow));
    }
    // Default for open PR with no special state
    ("\u{25cb} needs review".to_string(), Style::default().fg(Color::DarkGray))
}

/// Returns a Claude activity indicator for the task row.
fn claude_status_text(row: &TaskRow) -> (String, Style) {
    if row.sessions.is_empty() {
        return ("\u{25cb} none".to_string(), Style::default().fg(Color::DarkGray));
    }

    let count = row.sessions.len();
    let count_suffix = if count > 1 { format!(" {}", count) } else { String::new() };

    // Check for needs-input first (most urgent).
    if row.sessions.iter().any(|s| s.claude_needs_input) {
        return (format!("\u{2757} input{}", count_suffix), Style::default().fg(Color::Red));
    }

    if row.sessions.iter().any(|s| s.has_claude_active) {
        return (format!("\u{26a1} active{}", count_suffix), Style::default().fg(Color::Green));
    }

    (format!("\u{25cf} idle{}", count_suffix), Style::default().fg(Color::Yellow))
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
        let (tasks, total_backlog) = visible_tasks(&self.task_rows, self.backlog_page);
        let visible_count = tasks.len();

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
                } else if self.backlog_page > 0 {
                    self.backlog_page -= 1;
                    let (new_tasks, _) = visible_tasks(&self.task_rows, self.backlog_page);
                    self.cursor = new_tasks.len().saturating_sub(1);
                    self.fetch_task_pane_content();
                }
                false
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if visible_count > 0 && self.cursor < visible_count - 1 {
                    self.cursor += 1;
                    self.fetch_task_pane_content();
                } else if self.cursor == visible_count.saturating_sub(1) {
                    let next_start = (self.backlog_page + 1) * BACKLOG_PAGE_SIZE;
                    if next_start < total_backlog {
                        self.backlog_page += 1;
                        let non_backlog = tasks.iter().filter(|t| t.group != DisplayGroup::Other).count();
                        self.cursor = non_backlog;
                        self.fetch_task_pane_content();
                    }
                }
                false
            }
            KeyCode::Enter => {
                // Switch to the task's session, or create a worktree + session if none exist.
                // Extract owned data from the borrow of task_rows before taking &mut self.
                let action = {
                    let (visible, _) = visible_tasks(&self.task_rows, self.backlog_page);
                    visible.get(self.cursor).map(|vt| {
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
                    })
                };
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
                let (visible, _) = visible_tasks(&self.task_rows, self.backlog_page);
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
                let (visible, _) = visible_tasks(&self.task_rows, self.backlog_page);
                if let Some(vt) = visible.get(self.cursor)
                    && let Some(num) = vt.row.issue_number {
                        let url = format!("https://github.com/{}/issues/{}", vt.row.repo_slug, num);
                        crate::browser::open_url(&url);
                    }
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
                false
            }
            KeyCode::Char('q') | KeyCode::Esc => true,
            _ => false,
        }
    }

    /// Fetches pane content for the task at the current cursor position.
    pub(crate) fn fetch_task_pane_content(&mut self) {
        self.pane_content.clear();
        let (visible, _) = visible_tasks(&self.task_rows, self.backlog_page);
        if let Some(vt) = visible.get(self.cursor) {
            // Find a session to capture pane content from.
            if let Some(session) = vt.row.sessions.first() {
                let session_name = session.name.clone();
                let remote_host = session.host.clone();
                let tx = self.tx.clone();
                std::thread::spawn(move || {
                    let content = if let Some(host) = remote_host {
                        remote::capture_remote_pane_content(&host, &session_name, 100).unwrap_or_default()
                    } else {
                        tmux::capture_pane_content(&session_name, 100).unwrap_or_default()
                    };
                    let _ = tx.send(crate::tui::state::AppMsg::PaneContent(session_name, content));
                });
            }
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
            Line::from("\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}"),
            Line::from(Span::styled("\u{250c}\u{2500}\u{2510}\u{252c}\u{250c}\u{252c}\u{2510}\u{2554}\u{2550}\u{2557}\u{2566}\u{2550}\u{2557}\u{2554}\u{2550}\u{2557}\u{2566} \u{2566}\u{2554}\u{2550}\u{2557}\u{2566}\u{2550}\u{2557}\u{2554}\u{2566}\u{2557}", logo_style)),
            Line::from(Span::styled("\u{2502} \u{252c}\u{2502} \u{2502} \u{2551} \u{2551}\u{2560}\u{2566}\u{255d}\u{2551}  \u{2560}\u{2550}\u{2569}\u{2560}\u{2550}\u{2557}\u{2560}\u{2566}\u{255d} \u{2551}\u{2551}", logo_style)),
            Line::from(Span::styled("\u{2514}\u{2500}\u{2518}\u{2534} \u{2534} \u{255a}\u{2550}\u{255d}\u{2569}\u{255a}\u{2550}\u{255a}\u{2550}\u{255d}\u{2569} \u{2569}\u{2569} \u{2569}\u{2569}\u{255a}\u{2550}\u{2550}\u{2569}\u{255d}", logo_style)),
            Line::from("\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}\u{1f332}\u{1f333}\u{1f334}"),
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

        spans.push(sep.clone());
        if self.refreshing {
            let spinner = SPINNER_FRAMES[self.spinner_frame];
            spans.push(Span::styled(
                format!("{} refreshing...", spinner),
                Style::default().fg(Color::Cyan),
            ));
        } else {
            spans.push(Span::styled(
                "r",
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            ));
            spans.push(Span::raw(" refresh"));
        }

        let has_unreachable = self.host_reachable.values().any(|&v| !v);
        if has_unreachable {
            spans.push(sep.clone());
            spans.push(Span::styled(
                "R",
                Style::default()
                    .fg(Color::Cyan)
                    .add_modifier(Modifier::BOLD),
            ));
            spans.push(Span::raw(" reconnect"));
        }

        spans.push(sep.clone());
        spans.push(Span::styled(
            "q",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" back"));

        spans.push(sep);
        spans.push(Span::styled(
            "?",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ));
        spans.push(Span::raw(" help"));

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

        let (tasks, total_backlog) = visible_tasks(&self.task_rows, self.backlog_page);

        // Only show HOST column when at least one task has a remote session or remote worktree.
        let has_remote = self.task_rows.iter().any(|r| {
            r.sessions.iter().any(|s| s.host.is_some()) || r.worktree_host.is_some()
        });

        // Build rows for the table, including section header rows.
        let (rows, row_heights) = self.build_task_table_rows(&tasks, total_backlog, has_remote);

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

        // Column widths — HOST column included only when remotes exist.
        let mut widths: Vec<Constraint> = vec![
            Constraint::Length(3),   // #
            Constraint::Length(7),   // ISSUE
            Constraint::Length(7),   // PR
            Constraint::Min(20),     // TITLE (flexible)
        ];
        if has_remote {
            widths.push(Constraint::Length(12)); // HOST
        }
        widths.push(Constraint::Length(18)); // STATUS
        widths.push(Constraint::Length(10)); // CLAUDE

        // Header row
        let header_style = Style::default()
            .fg(Color::DarkGray)
            .add_modifier(Modifier::BOLD);
        let mut header_cells = vec![
            Cell::from(" #"),
            Cell::from("ISSUE"),
            Cell::from("PR"),
            Cell::from("TITLE"),
        ];
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
        total_backlog: usize,
        has_remote: bool,
    ) -> (Vec<Row<'static>>, Vec<u16>) {
        let mut rows: Vec<Row<'static>> = Vec::new();
        let mut row_heights: Vec<u16> = Vec::new();
        let mut last_group: Option<DisplayGroup> = None;

        let backlog_count = tasks.iter().filter(|t| t.group == DisplayGroup::Other).count();
        let backlog_has_more = total_backlog > backlog_count + self.backlog_page * BACKLOG_PAGE_SIZE;

        for (flat_idx, vt) in tasks.iter().enumerate() {
            let selected = flat_idx == self.cursor;

            // Section header when display group changes
            if last_group != Some(vt.group) {
                last_group = Some(vt.group);
                let header_row = group_header_row(vt.group, backlog_count, total_backlog, backlog_has_more, has_remote);
                rows.push(header_row);
                row_heights.push(1);
            }

            let (pr_text, pr_style) = pr_status_text(vt.row);
            let (claude_text, claude_style) = claude_status_text(vt.row);

            let title_display = match vt.row.issue_title.as_deref() {
                Some(title) if !title.is_empty() => title.to_string(),
                _ => vt.row.branch.clone(),
            };

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
            let pr_cell = if let Some(ref pr) = vt.row.pr {
                Cell::from(format!("#{}", pr.number))
            } else {
                Cell::from("").style(Style::default().fg(Color::DarkGray))
            };

            let mut cells = vec![
                Cell::from(format!("{:>2}", vt.num)),
                issue_cell,
                pr_cell,
                Cell::from(title_display),
            ];

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

        // Edge case: empty visible list but backlog exists
        if last_group.is_none() && total_backlog > 0 {
            let header_row = group_header_row(DisplayGroup::Other, 0, total_backlog, backlog_has_more, has_remote);
            rows.push(header_row);
            row_heights.push(1);
            let mut empty_cells = vec![
                Cell::from(""),
                Cell::from(""),
                Cell::from(""),
                Cell::from("(no tasks on this page)").style(Style::default().fg(Color::DarkGray)),
            ];
            if has_remote {
                empty_cells.push(Cell::from(""));
            }
            empty_cells.push(Cell::from(""));
            empty_cells.push(Cell::from(""));
            rows.push(Row::new(empty_cells));
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
            None => vt.row.branch.clone(),
        };
        let title_part = match vt.row.issue_title.as_deref() {
            Some(t) if !t.is_empty() => format!(" {}", truncate_str(t, 30)),
            _ => String::new(),
        };
        let wt_part = {
            let short = paths::tildify(&vt.row.worktree_path);
            format!(" \u{2502} wt: {}", truncate_str(&short, 25))
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

    /// Renders the hint bar for task mode.
    pub(crate) fn render_hints_task(&self, f: &mut Frame, area: Rect) {
        let sep = Span::styled(" \u{2502} ", Style::default().fg(Color::DarkGray));
        let key_style = Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD);

        let mut spans: Vec<Span> = vec![
            Span::styled("enter", key_style),
            Span::raw(" switch"),
            sep.clone(),
        ];

        // PR link hint — dim when selected task has no PR.
        let has_pr = !self.task_rows.is_empty() && {
            let (visible, _) = visible_tasks(&self.task_rows, self.backlog_page);
            visible.get(self.cursor).is_some_and(|vt| vt.row.pr.is_some())
        };
        if has_pr {
            spans.push(Span::styled("o", key_style));
            spans.push(Span::raw(" pr"));
        } else {
            spans.push(Span::styled("o pr", Style::default().fg(Color::DarkGray)));
        }
        spans.push(sep.clone());

        spans.push(Span::styled("c", key_style));
        spans.push(Span::raw(" cleanup"));
        spans.push(sep.clone());

        if self.refreshing {
            let spinner = SPINNER_FRAMES[self.spinner_frame];
            spans.push(Span::styled(
                format!("{} refreshing...", spinner),
                Style::default().fg(Color::Cyan),
            ));
            spans.push(sep.clone());
        } else {
            spans.push(Span::styled("r", key_style));
            spans.push(Span::raw(" refresh"));
            spans.push(sep.clone());
        }

        let has_unreachable = self.host_reachable.values().any(|&v| !v);
        if has_unreachable {
            spans.push(Span::styled("R", key_style));
            spans.push(Span::raw(" reconnect"));
            spans.push(sep.clone());
        }

        spans.push(Span::styled("q", key_style));
        spans.push(Span::raw(" quit"));

        spans.push(sep.clone());
        spans.push(Span::styled("?", key_style));
        spans.push(Span::raw(" help"));

        let hints = Paragraph::new(Line::from(spans)).alignment(Alignment::Center);
        f.render_widget(hints, area);
    }
}

// ---------------------------------------------------------------------------
// Task view helpers (free functions)
// ---------------------------------------------------------------------------

/// Creates a section header row spanning all columns for a display group.
fn group_header_row(
    group: DisplayGroup,
    visible_backlog: usize,
    total_backlog: usize,
    has_more: bool,
    has_remote: bool,
) -> Row<'static> {
    let label = if group == DisplayGroup::Other {
        let count_info = if visible_backlog < total_backlog {
            format!("{} of {}", visible_backlog, total_backlog)
        } else {
            format!("{}", total_backlog)
        };
        let hint = if has_more { "  j/k \u{25bc}\u{25b2}" } else { "" };
        format!("{} ({}){}", group.label(), count_info, hint)
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

    let color = group.color();
    // 6 cells when no HOST column (#, ISSUE, PR, TITLE, STATUS, CLAUDE),
    // 7 when HOST is present (adds HOST between TITLE and STATUS).
    if has_remote {
        Row::new(vec![
            Cell::from(""),
            Cell::from(""),
            Cell::from(""),
            Cell::from(text).style(Style::default().fg(color)),
            Cell::from(""),
            Cell::from(""),
            Cell::from(""),
        ])
    } else {
        Row::new(vec![
            Cell::from(""),
            Cell::from(""),
            Cell::from(""),
            Cell::from(text).style(Style::default().fg(color)),
            Cell::from(""),
            Cell::from(""),
        ])
    }
}

fn truncate_str(s: &str, max: usize) -> String {
    let chars: Vec<char> = s.chars().collect();
    if chars.len() <= max {
        s.to_string()
    } else {
        let truncated: String = chars[..max.saturating_sub(1)].iter().collect();
        format!("{}...", truncated)
    }
}

/// Returns the height (in terminal rows) to allocate for the header.
///
/// When the terminal is tall enough (>= 30 rows), the full header is
/// shown in a bordered block (7 rows). On shorter terminals a single compact
/// line is used instead so the task list gets as much vertical space as possible.
pub(crate) fn header_height(terminal_height: u16) -> u16 {
    if terminal_height >= 30 { 9 } else { 1 }
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
            pr: None,
            sessions: vec![],
            display_group: group,
            is_shepherd: false,
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
        let (visible, total_backlog) = visible_tasks(&rows, 0);
        assert_eq!(visible.len(), 3);
        assert_eq!(total_backlog, 1);
    }

    #[test]
    fn backlog_pagination_first_page() {
        let rows: Vec<TaskRow> = (1u32..=25)
            .map(|i| make_task_row(i, DisplayGroup::Other))
            .collect();
        let (visible, total) = visible_tasks(&rows, 0);
        assert_eq!(total, 25);
        assert_eq!(visible.len(), BACKLOG_PAGE_SIZE);
    }

    #[test]
    fn backlog_pagination_second_page() {
        let rows: Vec<TaskRow> = (1u32..=25)
            .map(|i| make_task_row(i, DisplayGroup::Other))
            .collect();
        let (visible, total) = visible_tasks(&rows, 1);
        assert_eq!(total, 25);
        assert_eq!(visible.len(), BACKLOG_PAGE_SIZE);
    }

    #[test]
    fn sequential_numbering_across_groups() {
        let rows = vec![
            make_task_row(10, DisplayGroup::NeedsAttention),
            make_task_row(20, DisplayGroup::ClaudeWorking),
            make_task_row(30, DisplayGroup::Other),
        ];
        let (visible, _) = visible_tasks(&rows, 0);
        assert_eq!(visible.len(), 3);
        assert_eq!(visible[0].num, 1);
        assert_eq!(visible[1].num, 2);
        assert_eq!(visible[2].num, 3);
    }

    #[test]
    fn pr_status_approved_text() {
        let row = TaskRow {
            pr: Some(PrInfo {
                number: 1,
                branch: "feat/branch".to_string(),
                review_decision: Some("approved".to_string()),
                checks_state: Some("passing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::ReadyToMerge)
        };
        let (text, _) = pr_status_text(&row);
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
                claude_needs_input: false,
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
                claude_needs_input: false,
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
                claude_needs_input: true,
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
}
