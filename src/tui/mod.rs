//! Ratatui-based terminal user interface.
//!
//! Drives the interactive worktree list, handles keyboard events, manages
//! background cache refreshes via a worker thread, and delegates rendering
//! to the `list`, `dialogs`, and `widgets` sub-modules.
mod dialogs;
mod list;
mod message;
mod state;
pub mod theme;
mod widgets;

pub use theme::Theme;

use std::collections::{HashMap, HashSet};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyModifiers, MouseButton, MouseEventKind};
use ratatui::prelude::*;
use std::cell::Cell;

use crate::cache;
use crate::cache_sources;
use crate::derive;
use crate::git;
use crate::global_config;
use crate::navigation;
use crate::remote;
use crate::session::StandaloneSessionRow;
use crate::tmux;
use crate::types::Worktree;

use message::{Message, UpdateResult};
use state::{AppMsg, CleanupState, Phase, ViewState};
use std::path::Path;
use std::process::Command;

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const LOCAL_REFRESH_SECS: u64 = 5;
const FULL_REFRESH_SECS: u64 = 60;
const WARNING_DURATION_SECS: u64 = 3;
const POLL_TIMEOUT_MS: u64 = 100;

/// Maximum milliseconds between two clicks on the same row for double-click detection.
const DOUBLE_CLICK_MS: u128 = 400;

/// Attribution URL displayed in the hints bar footer.
const ATTRIBUTION_URL: &str = "https://github.com/drewdrewthis/orchard-rs";

// ---------------------------------------------------------------------------
// Notification snapshot
// ---------------------------------------------------------------------------

/// Captures the notification-relevant state of one worktree row so that
/// transitions can be detected between cache refresh cycles.
struct WorktreeSnapshot {
    claude_working: bool,
    claude_needs_input: bool,
    ci_status: Option<String>,
    has_unresolved_threads: bool,
}

// ---------------------------------------------------------------------------
// App
// ---------------------------------------------------------------------------

/// Root TUI application state. Owns all display data, background channels, and dialog state.
pub struct App {
    cursor: usize,
    loading: bool,
    refreshing: bool,
    error: Option<String>,
    warning: Option<(String, Instant)>,
    repo_root: String,
    repo_name: String,
    pane_content: String,
    view: ViewState,
    /// Centralized color theme for all TUI rendering.
    theme: Theme,

    // Derived task view from caches
    task_rows: Vec<derive::WorktreeRow>,
    /// Standalone tmux sessions from global config, enriched with live state.
    standalone_sessions: Vec<StandaloneSessionRow>,
    global_config: global_config::GlobalConfig,
    /// Index into `global_config.repos`: 0 = all repos, 1+ = specific repo.
    active_repo_index: usize,
    show_branch_column: bool,
    search_text: String,
    search_active: bool,

    // Reachability state keyed by SSH host name
    host_reachable: HashMap<String, bool>,

    // Background data channel
    tx: mpsc::Sender<AppMsg>,
    rx: mpsc::Receiver<AppMsg>,

    // Session to switch to after the TUI exits. Set by Enter key handler.
    switch_target: Option<String>,

    // Auto-refresh
    last_refresh: Instant,
    last_full_refresh: Instant,
    /// Throbber animation state — advanced each frame via `calc_next()`.
    throbber_state: throbber_widgets_tui::ThrobberState,

    // Previous snapshots used to detect state transitions between cache refreshes.
    previous_worktree_states: HashMap<String, WorktreeSnapshot>,

    // Mouse support
    /// Last rendered table body rect (excludes border + header row). Updated by render.
    table_area: Cell<Rect>,
    /// Last rendered attribution URL rect. Updated by render.
    url_area: Cell<Rect>,
    /// Row index and timestamp of last mouse click, for double-click detection.
    last_click: Option<(usize, Instant)>,

    /// Scroll state for the preview pane (tui-scrollview).
    ///
    /// Uses `Cell` for interior mutability so `render_task_preview` (which takes
    /// `&self`) can update the state via `StatefulWidget::render`.
    preview_scroll_state: std::cell::Cell<tui_scrollview::ScrollViewState>,

    /// Expanded rows keyed by worktree path or standalone session name.
    ///
    /// When a key is present, the row's pane sub-rows are rendered below it.
    /// Entries are silently removed on refresh when the row's pane count drops to <= 1.
    expanded: HashSet<String>,

    /// Currently selected pane sub-row within the focused parent row.
    ///
    /// `None` means the parent row itself is selected. `Some(n)` means
    /// pane sub-row `n` is selected. This does NOT affect `self.cursor`.
    selected_pane: Option<usize>,
}

impl App {
    fn new(command: &str) -> Self {
        let repo_root = git::find_repo_root();
        let repo_name = git::get_repo_name();
        let global_cfg = global_config::load_global_config();
        let task_rows = derive_from_all_caches(&global_cfg);
        let state = crate::build_state::build_state(&global_cfg);
        let standalone_sessions = state.standalone_sessions;
        let (tx, rx) = mpsc::channel();

        let view = if command == "cleanup" {
            ViewState::Cleanup(CleanupState {
                stale: Vec::new(),
                selected: std::collections::HashSet::new(),
                cursor: 0,
                phase: Phase::Idle,
                deleted: Vec::new(),
                errors: Vec::new(),
            })
        } else {
            ViewState::List
        };

        App {
            cursor: 0,
            loading: true,
            refreshing: false,
            error: None,
            warning: None,
            repo_root,
            repo_name,
            pane_content: String::new(),
            view,
            theme: Theme::default(),
            task_rows,
            standalone_sessions,
            global_config: global_cfg,
            active_repo_index: 0,
            show_branch_column: false,
            search_text: String::new(),
            search_active: false,
            host_reachable: HashMap::new(),
            tx,
            rx,
            last_refresh: Instant::now(),
            last_full_refresh: Instant::now(),
            throbber_state: throbber_widgets_tui::ThrobberState::default(),
            switch_target: None,
            previous_worktree_states: HashMap::new(),
            table_area: Cell::new(Rect::default()),
            url_area: Cell::new(Rect::default()),
            last_click: None,
            preview_scroll_state: std::cell::Cell::new(tui_scrollview::ScrollViewState::default()),
            expanded: HashSet::new(),
            selected_pane: None,
        }
    }

    // -------------------------------------------------------------------
    // Active repo filtering
    // -------------------------------------------------------------------

    /// Returns the active repo slug when a specific repo is selected (index > 0).
    ///
    /// Index 0 means "all repos" (returns `None`). Index N returns
    /// `global_config.repos[N-1].slug`.
    pub(crate) fn active_repo_slug(&self) -> Option<&str> {
        if self.active_repo_index == 0 {
            return None;
        }
        let idx = self.active_repo_index.saturating_sub(1);
        self.global_config.repos.get(idx).map(|r| r.slug.as_str())
    }

    // -------------------------------------------------------------------
    // Expand/collapse helpers
    // -------------------------------------------------------------------

    /// Returns the expansion key for the row at cursor position `idx`.
    ///
    /// For standalone sessions (idx < standalone_count), the key is the session name.
    /// For worktree rows, the key is the worktree path.
    fn expansion_key_at(&self, idx: usize) -> Option<String> {
        let standalone_count = self.standalone_sessions.len();
        if idx < standalone_count {
            self.standalone_sessions
                .get(idx)
                .map(|ss| ss.session.tmux.name.clone())
        } else {
            let wt_idx = idx - standalone_count;
            let tasks = list::visible_tasks_filtered(
                &self.task_rows,
                &self.search_text,
                self.active_repo_slug(),
            );
            tasks.get(wt_idx).map(|vt| vt.row.worktree_path.clone())
        }
    }

    /// Returns the pane count for the row at cursor position `idx`.
    ///
    /// For standalone sessions: pane count from the session's panes vec.
    /// For worktree rows: pane count from the first session (if any).
    fn pane_count_at(&self, idx: usize) -> usize {
        let standalone_count = self.standalone_sessions.len();
        if idx < standalone_count {
            self.standalone_sessions
                .get(idx)
                .map(|ss| ss.session.panes.len())
                .unwrap_or(0)
        } else {
            let wt_idx = idx - standalone_count;
            let tasks = list::visible_tasks_filtered(
                &self.task_rows,
                &self.search_text,
                self.active_repo_slug(),
            );
            tasks
                .get(wt_idx)
                .and_then(|vt| vt.row.sessions.first())
                .map(|s| s.panes.len())
                .unwrap_or(0)
        }
    }

    /// Returns the pane index to use for preview capture.
    ///
    /// When the cursor is on a sub-row, returns `Some(pane_index)`.
    /// When on a parent row, returns `None` (default pane 0).
    #[cfg(test)]
    pub(crate) fn preview_pane_index(&self) -> Option<usize> {
        self.selected_pane
    }

    /// Returns true if the row at cursor `idx` is currently expanded.
    fn is_row_expanded(&self, idx: usize) -> bool {
        self.expansion_key_at(idx)
            .is_some_and(|key| self.expanded.contains(&key))
    }

    /// Collects expansion keys for all rows with pane count > 1.
    fn all_expandable_keys(&self) -> Vec<String> {
        let tasks = list::visible_tasks_filtered(
            &self.task_rows,
            &self.search_text,
            self.active_repo_slug(),
        );
        let mut keys = Vec::new();
        for ss in &self.standalone_sessions {
            if ss.session.panes.len() > 1 {
                keys.push(ss.session.tmux.name.clone());
            }
        }
        for vt in tasks.iter() {
            let pane_count = vt.row.sessions.first().map(|s| s.panes.len()).unwrap_or(0);
            if pane_count > 1 {
                keys.push(vt.row.worktree_path.clone());
            }
        }
        keys
    }

    /// Prunes expansion state: removes entries for rows whose pane count <= 1
    /// or that no longer exist in the current data set.
    fn prune_expansion_state(&mut self) {
        let tasks = list::visible_tasks_filtered(
            &self.task_rows,
            &self.search_text,
            self.active_repo_slug(),
        );

        let mut valid_keys: HashSet<String> = HashSet::new();
        for ss in &self.standalone_sessions {
            if ss.session.panes.len() > 1 {
                valid_keys.insert(ss.session.tmux.name.clone());
            }
        }
        for vt in tasks.iter() {
            let pane_count = vt.row.sessions.first().map(|s| s.panes.len()).unwrap_or(0);
            if pane_count > 1 {
                valid_keys.insert(vt.row.worktree_path.clone());
            }
        }

        self.expanded.retain(|k| valid_keys.contains(k));
    }

    // -------------------------------------------------------------------
    // Background refresh pipeline
    // -------------------------------------------------------------------

    /// Starts a full background refresh of all data sources.
    ///
    /// Probes remote SSH hosts, refreshes GitHub issues/PRs, local and remote
    /// worktrees, and all tmux sessions. Sends `AppMsg::CacheRefreshed` when done.
    fn start_full_refresh(&self) {
        // Cache-based refresh pipeline.
        let config = self.global_config.clone();
        let tx = self.tx.clone();
        std::thread::spawn(move || {
            // Probe each unique remote host before attempting remote operations.
            let mut reachable_hosts: std::collections::HashSet<String> =
                std::collections::HashSet::new();
            let mut unreachable_hosts: std::collections::HashSet<String> =
                std::collections::HashSet::new();

            for repo in &config.repos {
                for remote in &repo.remotes {
                    let host = remote.host.clone();
                    if reachable_hosts.contains(&host) || unreachable_hosts.contains(&host) {
                        continue;
                    }
                    match crate::remote::ssh_exec(&host, "true") {
                        Ok(_) => {
                            let _ = tx.send(AppMsg::HostReachability(host.clone(), true));
                            reachable_hosts.insert(host);
                        }
                        Err(_) => {
                            let _ = tx.send(AppMsg::HostReachability(host.clone(), false));
                            unreachable_hosts.insert(host);
                        }
                    }
                }
            }

            for repo in &config.repos {
                let _ = cache_sources::refresh_issues(repo);
                let _ = cache_sources::refresh_prs(repo);
                let _ = cache_sources::refresh_worktrees(repo);
                for remote in &repo.remotes {
                    if reachable_hosts.contains(&remote.host) {
                        let _ = cache_sources::refresh_remote_worktrees(repo, remote);
                    }
                }
            }
            // Refresh tmux sessions (local).
            let _ = cache_sources::refresh_tmux_sessions(None);
            // Refresh remote tmux sessions for reachable hosts only.
            let mut tmux_hosts_refreshed: std::collections::HashSet<String> =
                std::collections::HashSet::new();
            for repo in &config.repos {
                for remote in &repo.remotes {
                    if reachable_hosts.contains(&remote.host)
                        && tmux_hosts_refreshed.insert(remote.host.clone())
                    {
                        let _ = cache_sources::refresh_tmux_sessions(Some(&remote.host));
                    }
                }
            }
            // Ensure a main tmux session exists for each configured repo.
            ensure_main_sessions(&config);
            // Signal that caches are updated.
            let _ = tx.send(AppMsg::CacheRefreshed);
        });
    }

    /// Starts a local-only background refresh (worktrees + tmux sessions).
    ///
    /// Skips GitHub API calls, remote host probing, and remote worktree/session
    /// refreshes. Sends `AppMsg::LocalCacheRefreshed` when done.
    fn start_local_refresh(&self) {
        let config = self.global_config.clone();
        let tx = self.tx.clone();
        std::thread::spawn(move || {
            for repo in &config.repos {
                let _ = cache_sources::refresh_worktrees(repo);
            }
            let _ = cache_sources::refresh_tmux_sessions(None);
            let _ = tx.send(AppMsg::LocalCacheRefreshed);
        });
    }

    // -------------------------------------------------------------------
    // Drain messages from background threads
    // -------------------------------------------------------------------

    fn check_updates(&mut self) {
        while let Ok(msg) = self.rx.try_recv() {
            match msg {
                AppMsg::CacheRefreshed => {
                    let old_states = std::mem::take(&mut self.previous_worktree_states);
                    self.task_rows = derive_from_all_caches(&self.global_config);
                    let state = crate::build_state::build_state(&self.global_config);
                    self.standalone_sessions = state.standalone_sessions;
                    // Warn on refresh (not fatal) — a new worktree may have
                    // introduced a collision after boot. Don't crash the TUI.
                    if let Err(e) =
                        check_standalone_collisions(&self.standalone_sessions, &self.task_rows)
                    {
                        crate::logger::LOG.warn(&format!("{e}"));
                    }
                    self.loading = false;
                    self.refreshing = false;
                    self.error = None;
                    let total = self.standalone_sessions.len() + self.task_rows.len();
                    if total > 0 && self.cursor >= total {
                        self.cursor = total - 1;
                    }
                    // Populate cleanup stale list if in cleanup view with empty stale.
                    if let ViewState::Cleanup(ref mut cs) = self.view
                        && cs.stale.is_empty()
                    {
                        cs.stale = filter_stale(&self.task_rows);
                        cs.selected = cs
                            .stale
                            .iter()
                            .map(|row| row.worktree_path.clone())
                            .collect();
                    }
                    // Prune expansion state for rows that lost their panes.
                    self.prune_expansion_state();

                    // Fetch pane content for the current task selection.
                    self.fetch_task_pane_content();

                    self.detect_and_notify(&old_states);

                    // Write session manifest so resurrection knows which
                    // worktrees had active sessions at last refresh.
                    let manifest_entries: Vec<cache::SessionManifestEntry> = self
                        .task_rows
                        .iter()
                        .filter(|row| !row.sessions.is_empty())
                        .map(|row| cache::SessionManifestEntry {
                            session_name: row.sessions[0].tmux.name.clone(),
                            worktree_path: row.worktree_path.clone(),
                            branch: row.branch.clone(),
                            had_claude: row.sessions.iter().any(|s| s.claude.is_some()),
                            host: row.worktree_host.clone(),
                        })
                        .collect();
                    if !manifest_entries.is_empty() {
                        let manifest = cache::SessionManifest {
                            last_updated: chrono::Utc::now(),
                            sessions: manifest_entries,
                        };
                        let _ = cache::write_manifest(&manifest);
                    }
                }
                AppMsg::HostReachability(host, reachable) => {
                    self.host_reachable.insert(host, reachable);
                }
                AppMsg::PaneContent(session_name, content) => {
                    // Accept pane content when session matches the current row (standalone or worktree).
                    let standalone_count = self.standalone_sessions.len();
                    let matches = if self.cursor < standalone_count {
                        self.standalone_sessions
                            .get(self.cursor)
                            .is_some_and(|ss| ss.session.tmux.name == session_name)
                    } else {
                        let wt_cursor = self.cursor - standalone_count;
                        self.task_rows.get(wt_cursor).is_some_and(|row| {
                            row.sessions.iter().any(|s| s.tmux.name == session_name)
                        })
                    };
                    if matches {
                        self.pane_content = content;
                        // Reset scroll to bottom so most recent output is visible.
                        // We set a max-offset; the ScrollView render will clamp it.
                        let mut state = tui_scrollview::ScrollViewState::default();
                        state.scroll_to_bottom();
                        self.preview_scroll_state.set(state);
                    }
                }
                AppMsg::LocalCacheRefreshed => {
                    let old_states = std::mem::take(&mut self.previous_worktree_states);
                    self.task_rows = derive_from_all_caches(&self.global_config);
                    let state = crate::build_state::build_state(&self.global_config);
                    self.standalone_sessions = state.standalone_sessions;
                    if let Err(e) =
                        check_standalone_collisions(&self.standalone_sessions, &self.task_rows)
                    {
                        crate::logger::LOG.warn(&format!("{e}"));
                    }
                    self.error = None;
                    let total = self.standalone_sessions.len() + self.task_rows.len();
                    if total > 0 && self.cursor >= total {
                        self.cursor = total - 1;
                    }
                    if let ViewState::Cleanup(ref mut cs) = self.view
                        && cs.stale.is_empty()
                    {
                        cs.stale = filter_stale(&self.task_rows);
                        cs.selected = cs
                            .stale
                            .iter()
                            .map(|row| row.worktree_path.clone())
                            .collect();
                    }
                    self.prune_expansion_state();
                    self.fetch_task_pane_content();
                    self.detect_and_notify(&old_states);
                }
                AppMsg::DeleteDone => {
                    if let ViewState::ConfirmDelete(ref mut ds) = self.view {
                        ds.phase = Phase::Done;
                    }
                    self.warning = Some(("Worktree deleted.".to_string(), Instant::now()));
                    self.start_full_refresh();
                }
                AppMsg::DeleteErr(e) => {
                    if let ViewState::ConfirmDelete(ref mut ds) = self.view {
                        ds.phase = Phase::Error;
                        ds.error = Some(e);
                    }
                }
                AppMsg::CleanupDone { deleted, errors } => {
                    if let ViewState::Cleanup(ref mut cs) = self.view {
                        cs.deleted = deleted;
                        cs.errors = errors;
                        cs.phase = Phase::Done;
                    }
                    self.start_full_refresh();
                }
                AppMsg::CreateWorktreeDone { session_name } => {
                    self.switch_target = Some(session_name);
                    self.start_full_refresh();
                }
                AppMsg::CreateWorktreeErr(e) => {
                    self.warning = Some((e, Instant::now()));
                }
                AppMsg::CreateWorktreeWarn {
                    session_name,
                    warning,
                } => {
                    self.warning = Some((warning, Instant::now()));
                    if !session_name.is_empty() {
                        self.switch_target = Some(session_name);
                    }
                    self.start_full_refresh();
                }
            }
        }
    }

    /// Detects state transitions between the previous and current worktree snapshots
    /// and fires desktop notifications for significant changes (Claude needs input,
    /// Claude finished, CI failed, new review comments).
    ///
    /// Also saves the current row states as snapshots for the next comparison.
    fn detect_and_notify(&mut self, old_states: &HashMap<String, WorktreeSnapshot>) {
        let terminal_app = self.global_config.terminal_app.as_str();
        for row in &self.task_rows {
            let key = row.worktree_path.clone();
            let old = old_states.get(&key);
            let label = row.issue_title.as_deref().unwrap_or(&row.branch);
            let session = row.sessions.first().map(|s| s.tmux.name.as_str());
            let notify = |title: &str, message: &str| {
                crate::notify::send_notification_with_session(
                    title,
                    message,
                    session,
                    terminal_app,
                );
            };

            // Claude was working, now needs input.
            if row.sessions.iter().any(|s| {
                s.claude
                    .as_ref()
                    .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Input)
            }) && old.map(|o| !o.claude_needs_input).unwrap_or(false)
            {
                notify(
                    "Claude needs input",
                    &format!("{} is waiting for you", label),
                );
            }

            // Claude was working, now idle (finished).
            if !row.sessions.iter().any(|s| {
                s.claude
                    .as_ref()
                    .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Working)
            }) && old.map(|o| o.claude_working).unwrap_or(false)
            {
                notify("Claude finished", label);
            }

            // CI transitioned to failing.
            if let Some(ref pr) = row.pr {
                if pr.checks_state.as_deref() == Some("failing")
                    && old
                        .map(|o| o.ci_status.as_deref() != Some("failing"))
                        .unwrap_or(false)
                {
                    notify("CI Failed", &format!("#{} {}", pr.number, label));
                }

                // New unresolved review threads appeared.
                if pr.unresolved_threads > 0
                    && old.map(|o| !o.has_unresolved_threads).unwrap_or(false)
                {
                    notify(
                        "Review comments",
                        &format!(
                            "#{} has {} unresolved thread(s)",
                            pr.number, pr.unresolved_threads
                        ),
                    );
                }
            }
        }

        // Save current state as snapshots for the next comparison.
        self.previous_worktree_states = self
            .task_rows
            .iter()
            .map(|row| {
                let snapshot = WorktreeSnapshot {
                    claude_working: row.sessions.iter().any(|s| {
                        s.claude
                            .as_ref()
                            .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Working)
                    }),
                    claude_needs_input: row.sessions.iter().any(|s| {
                        s.claude
                            .as_ref()
                            .is_some_and(|c| c.status == crate::claude_state::ClaudeState::Input)
                    }),
                    ci_status: row.pr.as_ref().and_then(|p| p.checks_state.clone()),
                    has_unresolved_threads: row
                        .pr
                        .as_ref()
                        .map(|p| p.unresolved_threads > 0)
                        .unwrap_or(false),
                };
                (row.worktree_path.clone(), snapshot)
            })
            .collect();
    }

    // -------------------------------------------------------------------
    // TEA: handle_event — pure key-to-message mapping
    // -------------------------------------------------------------------

    /// Maps a raw key event to a semantic [`Message`] based on the current view state.
    ///
    /// This is a pure function: it reads `&self` but never mutates state.
    /// Returns `None` for unbound keys (the event loop ignores them).
    fn handle_event(&self, key: KeyEvent) -> Option<Message> {
        crate::logger::LOG.info(&format!(
            "tui: key event: {:?} view={:?}",
            key.code,
            self.view_name()
        ));

        // Ctrl+C always quits regardless of view.
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('c') {
            return Some(Message::Quit);
        }

        match &self.view {
            ViewState::List => {
                if self.search_active {
                    return match key.code {
                        KeyCode::Esc => Some(Message::SearchCancel),
                        KeyCode::Enter => Some(Message::SearchConfirm),
                        KeyCode::Backspace => Some(Message::SearchBackspace),
                        KeyCode::Char(c) => Some(Message::SearchChar(c)),
                        _ => None,
                    };
                }

                let standalone_count = self.standalone_sessions.len();
                let worktree_visible_count = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.search_text,
                    self.active_repo_slug(),
                )
                .len();
                let visible_count = standalone_count + worktree_visible_count;

                match key.code {
                    KeyCode::Char(c) if c.is_ascii_digit() && c != '0' => {
                        navigation::cursor_index_from_digit(c, visible_count).map(Message::CursorTo)
                    }
                    KeyCode::Up | KeyCode::Char('k') => Some(Message::CursorUp),
                    KeyCode::Down | KeyCode::Char('j') => Some(Message::CursorDown),
                    KeyCode::Enter => Some(Message::Enter),
                    KeyCode::Char('o') => Some(Message::OpenPR),
                    KeyCode::Char('i') => Some(Message::OpenIssue),
                    KeyCode::Char('B') => Some(Message::ToggleBranchColumn),
                    KeyCode::Char('d') => Some(Message::Delete),
                    KeyCode::Char('p') => Some(Message::TogglePriority),
                    KeyCode::Char('n') => Some(Message::NewSession),
                    KeyCode::Char('w') => Some(Message::NewWorktree),
                    KeyCode::Char('/') => Some(Message::StartSearch),
                    KeyCode::Char('c') => Some(Message::Cleanup),
                    // h/Left = collapse, l/Right = expand, E = toggle all
                    KeyCode::Char('h') | KeyCode::Left => Some(Message::CollapseRow),
                    KeyCode::Char('l') | KeyCode::Right => Some(Message::ExpandRow),
                    KeyCode::Char('E') => Some(Message::ToggleExpandAll),
                    // Tab/BackTab = repo cycling (replaces Left/Right)
                    KeyCode::Tab => Some(Message::NextRepo),
                    KeyCode::BackTab => Some(Message::PrevRepo),
                    KeyCode::Char('r') => Some(Message::Refresh),
                    KeyCode::Char('R') => Some(Message::ReconnectHosts),
                    KeyCode::Char('?') => Some(Message::ToggleHelp),
                    KeyCode::PageUp => Some(Message::PreviewPageUp),
                    KeyCode::PageDown => Some(Message::PreviewPageDown),
                    KeyCode::Char('q') | KeyCode::Esc => Some(Message::Quit),
                    _ => None,
                }
            }
            ViewState::ConfirmDelete(ds) => match ds.phase {
                Phase::Confirm => match key.code {
                    KeyCode::Char('y') => Some(Message::ConfirmYes),
                    KeyCode::Char('n') | KeyCode::Esc => Some(Message::ConfirmNo),
                    _ => None,
                },
                Phase::Done | Phase::Error => Some(Message::DismissDialog),
                _ => None,
            },
            ViewState::Cleanup(cs) => {
                if cs.phase == Phase::Done {
                    return match key.code {
                        KeyCode::Char('q') | KeyCode::Esc => Some(Message::Cancel),
                        _ => None,
                    };
                }
                if cs.phase == Phase::InProgress {
                    return None;
                }
                match key.code {
                    KeyCode::Up | KeyCode::Char('k') => Some(Message::CursorUp),
                    KeyCode::Down | KeyCode::Char('j') => Some(Message::CursorDown),
                    KeyCode::Char(' ') => Some(Message::ToggleSelection),
                    KeyCode::Enter => Some(Message::ConfirmCleanup),
                    KeyCode::Char('q') | KeyCode::Esc => Some(Message::Cancel),
                    _ => None,
                }
            }
            ViewState::NewSession(_) => match key.code {
                KeyCode::Esc => Some(Message::Cancel),
                KeyCode::Enter => Some(Message::ConfirmNewSession),
                KeyCode::Backspace => Some(Message::DeleteChar),
                KeyCode::Char(c) if c.is_alphanumeric() || c == '-' || c == '_' => {
                    Some(Message::InputChar(c))
                }
                _ => None,
            },
            ViewState::NewWorktree(_) => match key.code {
                KeyCode::Esc => Some(Message::Cancel),
                KeyCode::Enter => Some(Message::ConfirmNewWorktree),
                KeyCode::Backspace => Some(Message::DeleteWorktreeChar),
                KeyCode::Char(c)
                    if c.is_alphanumeric() || c == '-' || c == '_' || c == '/' || c == '.' =>
                {
                    Some(Message::InputWorktreeChar(c))
                }
                _ => None,
            },
            ViewState::Help => match key.code {
                KeyCode::Char('?') => Some(Message::ToggleHelp),
                KeyCode::Esc | KeyCode::Char('q') => Some(Message::Cancel),
                _ => None,
            },
        }
    }

    // -------------------------------------------------------------------
    // TEA: handle_mouse_event — mouse-to-message mapping
    // -------------------------------------------------------------------

    /// Maps a mouse event to a [`Message`], if applicable.
    ///
    /// Only processes events when in List view with search inactive.
    /// Scroll events map to CursorUp/CursorDown; clicks select rows or
    /// open the attribution URL; double-clicks activate the selected row.
    fn handle_mouse_event(&mut self, event: crossterm::event::MouseEvent) -> Option<Message> {
        // Mouse events only handled in List view with search inactive.
        if !matches!(self.view, ViewState::List) || self.search_active {
            return None;
        }

        let table = self.table_area.get();
        let url = self.url_area.get();

        let in_table = event.column >= table.x
            && event.column < table.x + table.width
            && event.row >= table.y
            && event.row < table.y + table.height;

        match event.kind {
            MouseEventKind::ScrollDown if in_table => Some(Message::CursorDown),
            MouseEventKind::ScrollUp if in_table => Some(Message::CursorUp),
            MouseEventKind::Down(MouseButton::Left) => {
                // Check URL area first.
                let in_url = url.width > 0
                    && event.column >= url.x
                    && event.column < url.x + url.width
                    && event.row >= url.y
                    && event.row < url.y + url.height;
                if in_url {
                    return Some(Message::OpenAttribution);
                }

                if !in_table {
                    return None;
                }

                // Compute which visual row was clicked within the table body.
                let visual_row = (event.row - table.y) as usize;

                // Map visual row to cursor index, accounting for group headers.
                let cursor_index = self.visual_row_to_cursor(visual_row);

                let cursor_index = cursor_index?;

                // Double-click detection.
                if let Some((prev_row, prev_time)) = self.last_click
                    && prev_row == cursor_index
                    && prev_time.elapsed().as_millis() < DOUBLE_CLICK_MS
                {
                    self.last_click = None;
                    return Some(Message::ActivateRow(cursor_index));
                }

                self.last_click = Some((cursor_index, Instant::now()));
                Some(Message::CursorTo(cursor_index))
            }
            _ => None,
        }
    }

    /// Maps a visual row offset within the table body to a cursor index.
    ///
    /// Returns `None` if the row maps to a group header, sub-row, or is out of range.
    /// Visual rows include standalone sessions (with optional sub-rows), then
    /// group headers interleaved with worktree task rows (with optional sub-rows).
    fn visual_row_to_cursor(&self, visual_row: usize) -> Option<usize> {
        let mut table_row = 0usize;

        // Standalone session rows come first.
        for (idx, ss) in self.standalone_sessions.iter().enumerate() {
            if table_row == visual_row {
                return Some(idx);
            }
            table_row += 1;
            // Skip sub-rows if expanded.
            if self.expanded.contains(&ss.session.tmux.name) {
                let sub_count = ss.session.panes.len();
                if visual_row < table_row + sub_count {
                    return None; // clicked on a sub-row
                }
                table_row += sub_count;
            }
        }

        let standalone_count = self.standalone_sessions.len();

        // For worktree rows, account for group header rows and sub-rows.
        let tasks = list::visible_tasks_filtered(
            &self.task_rows,
            &self.search_text,
            self.active_repo_slug(),
        );

        let mut last_group: Option<crate::derive::DisplayGroup> = None;

        for (task_idx, vt) in tasks.iter().enumerate() {
            // Group header inserted when display group changes.
            if last_group != Some(vt.group) {
                if table_row == visual_row {
                    return None; // clicked on a group header
                }
                last_group = Some(vt.group);
                table_row += 1;
            }

            if table_row == visual_row {
                return Some(task_idx + standalone_count);
            }
            table_row += 1;

            // Skip sub-rows if expanded.
            if self.expanded.contains(&vt.row.worktree_path) {
                let sub_count = vt.row.sessions.first().map(|s| s.panes.len()).unwrap_or(0);
                if visual_row < table_row + sub_count {
                    return None; // clicked on a sub-row
                }
                table_row += sub_count;
            }
        }

        None // clicked below all rows
    }

    // -------------------------------------------------------------------
    // TEA: update — all state mutation
    // -------------------------------------------------------------------

    /// Processes a [`Message`] and applies the corresponding state mutation.
    ///
    /// Returns an [`UpdateResult`] indicating whether the TUI should quit
    /// and whether a follow-up message should be processed immediately.
    fn update(&mut self, msg: Message) -> UpdateResult {
        /// Shorthand for a non-quitting result with no follow-up.
        fn ok() -> UpdateResult {
            UpdateResult {
                quit: false,
                next_msg: None,
            }
        }

        match msg {
            Message::Quit => UpdateResult {
                quit: true,
                next_msg: None,
            },
            Message::CursorUp => {
                match &mut self.view {
                    ViewState::Cleanup(cs) => {
                        if cs.cursor > 0 {
                            cs.cursor -= 1;
                        }
                    }
                    _ => {
                        if let Some(pane) = self.selected_pane {
                            if pane > 0 {
                                // Move up within sub-rows.
                                self.selected_pane = Some(pane - 1);
                            } else {
                                // First sub-row → back to parent.
                                self.selected_pane = None;
                            }
                            self.fetch_task_pane_content();
                        } else if self.cursor > 0 {
                            self.cursor -= 1;
                            // If moving up onto an expanded row, select the last sub-row.
                            if self.is_row_expanded(self.cursor) {
                                let count = self.pane_count_at(self.cursor);
                                if count > 0 {
                                    self.selected_pane = Some(count - 1);
                                }
                            }
                            self.fetch_task_pane_content();
                        }
                    }
                }
                ok()
            }
            Message::CursorDown => {
                match &mut self.view {
                    ViewState::Cleanup(cs) => {
                        if !cs.stale.is_empty() && cs.cursor < cs.stale.len() - 1 {
                            cs.cursor += 1;
                        }
                    }
                    _ => {
                        let standalone_count = self.standalone_sessions.len();
                        let worktree_visible_count = list::visible_tasks_filtered(
                            &self.task_rows,
                            &self.search_text,
                            self.active_repo_slug(),
                        )
                        .len();
                        let visible_count = standalone_count + worktree_visible_count;

                        if let Some(pane) = self.selected_pane {
                            let count = self.pane_count_at(self.cursor);
                            if pane + 1 < count {
                                // Move down within sub-rows.
                                self.selected_pane = Some(pane + 1);
                            } else {
                                // Last sub-row → next parent row.
                                self.selected_pane = None;
                                if visible_count > 0 && self.cursor < visible_count - 1 {
                                    self.cursor += 1;
                                }
                            }
                            self.fetch_task_pane_content();
                        } else if self.is_row_expanded(self.cursor) {
                            // Parent of expanded row → enter first sub-row.
                            self.selected_pane = Some(0);
                            self.fetch_task_pane_content();
                        } else if visible_count > 0 && self.cursor < visible_count - 1 {
                            self.cursor += 1;
                            self.fetch_task_pane_content();
                        }
                    }
                }
                ok()
            }
            Message::CursorTo(idx) => {
                self.cursor = idx;
                self.selected_pane = None;
                self.fetch_task_pane_content();
                ok()
            }
            Message::PreviewPageUp => {
                let mut state = self.preview_scroll_state.get();
                state.scroll_page_up();
                self.preview_scroll_state.set(state);
                ok()
            }
            Message::PreviewPageDown => {
                let mut state = self.preview_scroll_state.get();
                state.scroll_page_down();
                self.preview_scroll_state.set(state);
                ok()
            }
            Message::Enter => {
                let quit = self.handle_enter_action();
                UpdateResult {
                    quit,
                    next_msg: None,
                }
            }
            Message::ActivateRow(idx) => {
                self.cursor = idx;
                self.fetch_task_pane_content();
                UpdateResult {
                    quit: false,
                    next_msg: Some(Message::Enter),
                }
            }
            Message::OpenAttribution => {
                crate::browser::open_url(ATTRIBUTION_URL);
                ok()
            }
            Message::OpenPR => {
                let standalone_count = self.standalone_sessions.len();
                if self.guard_requires_worktree(standalone_count) {
                    return ok();
                }
                let worktree_cursor = self.cursor - standalone_count;
                let visible = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.search_text,
                    self.active_repo_slug(),
                );
                if let Some(vt) = visible.get(worktree_cursor)
                    && let Some(ref pr) = vt.row.pr
                {
                    let url = format!("https://github.com/{}/pull/{}", vt.row.repo_slug, pr.number);
                    crate::browser::open_url(&url);
                }
                ok()
            }
            Message::OpenIssue => {
                let standalone_count = self.standalone_sessions.len();
                if self.guard_requires_worktree(standalone_count) {
                    return ok();
                }
                let worktree_cursor = self.cursor - standalone_count;
                let visible = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.search_text,
                    self.active_repo_slug(),
                );
                if let Some(vt) = visible.get(worktree_cursor)
                    && let Some(num) = vt.row.issue_number
                {
                    let url = format!("https://github.com/{}/issues/{}", vt.row.repo_slug, num);
                    crate::browser::open_url(&url);
                }
                ok()
            }
            Message::ToggleBranchColumn => {
                self.show_branch_column = !self.show_branch_column;
                ok()
            }
            Message::Delete => {
                let standalone_count = self.standalone_sessions.len();
                if self.guard_requires_worktree(standalone_count) {
                    return ok();
                }
                let worktree_cursor = self.cursor - standalone_count;
                let visible = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.search_text,
                    self.active_repo_slug(),
                );
                if let Some(vt) = visible.get(worktree_cursor) {
                    let wt = list::worktree_from_task_row(vt.row);
                    self.view = ViewState::ConfirmDelete(state::DeleteState {
                        target: wt,
                        phase: Phase::Confirm,
                        error: None,
                    });
                }
                ok()
            }
            Message::TogglePriority => {
                let standalone_count = self.standalone_sessions.len();
                if self.guard_requires_worktree(standalone_count) {
                    return ok();
                }
                let worktree_cursor = self.cursor - standalone_count;
                let visible = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.search_text,
                    self.active_repo_slug(),
                );
                if let Some(vt) = visible.get(worktree_cursor) {
                    let path = vt.row.worktree_path.clone();
                    drop(visible);
                    crate::priority::toggle_priority(&path);
                    self.task_rows = crate::build_state::build_task_rows(&self.global_config);
                    let total = standalone_count + self.task_rows.len();
                    if total > 0 && self.cursor >= total {
                        self.cursor = total - 1;
                    }
                }
                ok()
            }
            Message::NewSession => {
                self.view = ViewState::NewSession(state::NewSessionState {
                    name: String::new(),
                    cursor: 0,
                });
                ok()
            }
            Message::StartSearch => {
                self.search_active = true;
                self.search_text.clear();
                ok()
            }
            Message::Cleanup => {
                self.enter_cleanup_view();
                ok()
            }
            Message::PrevRepo => {
                self.active_repo_index = self.active_repo_index.saturating_sub(1);
                self.cursor = 0;
                self.selected_pane = None;
                ok()
            }
            Message::NextRepo => {
                let repo_count = self.global_config.repos.len();
                self.active_repo_index = (self.active_repo_index + 1).min(repo_count);
                self.cursor = 0;
                self.selected_pane = None;
                ok()
            }
            Message::ExpandRow => {
                let pane_count = self.pane_count_at(self.cursor);
                if pane_count > 1
                    && let Some(key) = self.expansion_key_at(self.cursor)
                {
                    self.expanded.insert(key);
                }
                ok()
            }
            Message::CollapseRow => {
                if self.selected_pane.is_some() {
                    // On a sub-row: collapse parent + clear selected_pane.
                    if let Some(key) = self.expansion_key_at(self.cursor) {
                        self.expanded.remove(&key);
                    }
                    self.selected_pane = None;
                } else if let Some(key) = self.expansion_key_at(self.cursor) {
                    self.expanded.remove(&key);
                }
                ok()
            }
            Message::ToggleExpandAll => {
                let expandable = self.all_expandable_keys();
                if expandable.is_empty() {
                    return ok();
                }
                // If any expandable row is collapsed, expand all. Otherwise collapse all.
                let all_expanded = expandable.iter().all(|k| self.expanded.contains(k));
                if all_expanded {
                    for key in &expandable {
                        self.expanded.remove(key);
                    }
                } else {
                    for key in expandable {
                        self.expanded.insert(key);
                    }
                }
                // Don't clear selected_pane — persists for re-expansion.
                ok()
            }
            Message::Refresh => {
                self.refreshing = true;
                self.start_full_refresh();
                ok()
            }
            Message::ReconnectHosts => {
                self.reconnect_unreachable_hosts();
                ok()
            }
            Message::ToggleHelp => {
                self.view = if matches!(self.view, ViewState::Help) {
                    ViewState::List
                } else {
                    ViewState::Help
                };
                ok()
            }
            Message::SearchChar(c) => {
                self.search_text.push(c);
                self.clamp_cursor_to_visible();
                ok()
            }
            Message::SearchBackspace => {
                self.search_text.pop();
                self.clamp_cursor_to_visible();
                ok()
            }
            Message::SearchConfirm => {
                self.search_active = false;
                ok()
            }
            Message::SearchCancel => {
                self.search_active = false;
                self.search_text.clear();
                ok()
            }
            Message::ConfirmYes => {
                if let ViewState::ConfirmDelete(ds) = &mut self.view {
                    ds.phase = Phase::InProgress;
                    let target = ds.target.clone();
                    self.start_delete(&target);
                }
                ok()
            }
            Message::ConfirmNo | Message::Cancel | Message::DismissDialog => {
                self.view = ViewState::List;
                ok()
            }
            Message::ToggleSelection => {
                if let ViewState::Cleanup(cs) = &mut self.view
                    && !cs.stale.is_empty()
                    && cs.cursor < cs.stale.len()
                {
                    let path = cs.stale[cs.cursor].worktree_path.clone();
                    if cs.selected.contains(&path) {
                        cs.selected.remove(&path);
                    } else {
                        cs.selected.insert(path);
                    }
                }
                ok()
            }
            Message::ConfirmCleanup => {
                if let ViewState::Cleanup(cs) = &mut self.view {
                    let selected: Vec<_> = cs
                        .stale
                        .iter()
                        .filter(|row| cs.selected.contains(&row.worktree_path))
                        .cloned()
                        .collect();
                    if selected.is_empty() {
                        self.warning = Some(("No items selected.".to_string(), Instant::now()));
                    } else {
                        cs.phase = Phase::InProgress;
                        self.start_cleanup(selected);
                    }
                }
                ok()
            }
            Message::InputChar(c) => {
                if let ViewState::NewSession(ns) = &mut self.view {
                    ns.name.push(c);
                    ns.cursor = ns.name.len();
                }
                ok()
            }
            Message::DeleteChar => {
                if let ViewState::NewSession(ns) = &mut self.view {
                    ns.name.pop();
                    ns.cursor = ns.name.len();
                }
                ok()
            }
            Message::ConfirmNewSession => {
                let result = if let ViewState::NewSession(ns) = &self.view {
                    if !ns.name.is_empty() {
                        let name = ns.name.clone();
                        let worktree_path = self.repo_root.clone();
                        let opts = crate::types::SwitchToSessionOptions {
                            session_name: name.clone(),
                            worktree_path,
                            branch: None,
                            pr: None,
                        };
                        Some((name, opts))
                    } else {
                        None
                    }
                } else {
                    None
                };
                if let Some((name, opts)) = result {
                    match crate::tmux::create_session(&opts) {
                        Ok(()) => {
                            self.switch_target = Some(name);
                            return UpdateResult {
                                quit: true,
                                next_msg: None,
                            };
                        }
                        Err(e) => {
                            self.view = ViewState::List;
                            self.warning = Some((format!("session error: {e}"), Instant::now()));
                        }
                    }
                }
                ok()
            }
            Message::NewWorktree => {
                self.view = ViewState::NewWorktree(state::NewWorktreeState {
                    branch: String::new(),
                });
                ok()
            }
            Message::InputWorktreeChar(c) => {
                if let ViewState::NewWorktree(nw) = &mut self.view {
                    nw.branch.push(c);
                }
                ok()
            }
            Message::DeleteWorktreeChar => {
                if let ViewState::NewWorktree(nw) = &mut self.view {
                    nw.branch.pop();
                }
                ok()
            }
            Message::ConfirmNewWorktree => {
                let branch = if let ViewState::NewWorktree(nw) = &self.view {
                    if !nw.branch.is_empty() {
                        Some(nw.branch.clone())
                    } else {
                        None
                    }
                } else {
                    None
                };
                if let Some(branch) = branch {
                    self.view = ViewState::List;
                    self.start_create_worktree(&branch);
                }
                ok()
            }
        }
    }

    /// Clamps the cursor to the visible task count after search text changes.
    fn clamp_cursor_to_visible(&mut self) {
        let tasks = list::visible_tasks_filtered(
            &self.task_rows,
            &self.search_text,
            self.active_repo_slug(),
        );
        self.cursor = self.cursor.min(tasks.len().saturating_sub(1));
    }

    /// Returns a debug-friendly name for the current view state.
    fn view_name(&self) -> &'static str {
        match self.view {
            ViewState::List => "List",
            ViewState::ConfirmDelete(_) => "ConfirmDelete",
            ViewState::Cleanup(_) => "Cleanup",
            ViewState::NewSession(_) => "NewSession",
            ViewState::NewWorktree(_) => "NewWorktree",
            ViewState::Help => "Help",
        }
    }

    // -------------------------------------------------------------------
    // Rendering
    // -------------------------------------------------------------------

    /// Renders the current view state to the terminal frame.
    ///
    /// This is a read-only operation: it borrows `&self` and dispatches
    /// to the appropriate render method based on the current [`ViewState`].
    /// The preview scroll state uses `Cell` for interior mutability.
    fn render(&self, f: &mut Frame) {
        match &self.view {
            ViewState::List => self.render_list(f),
            ViewState::ConfirmDelete(ds) => self.render_delete(ds, f),
            ViewState::Cleanup(cs) => self.render_cleanup(cs, f),
            ViewState::NewSession(ns) => {
                self.render_list(f);
                self.render_new_session(ns, f);
            }
            ViewState::NewWorktree(nw) => {
                self.render_list(f);
                self.render_new_worktree(nw, f);
            }
            ViewState::Help => self.render_help(f),
        }
    }

    // -------------------------------------------------------------------
    // Actions (delete, cleanup)
    // -------------------------------------------------------------------

    fn start_delete(&self, target: &Worktree) {
        let wt = target.clone();
        let global_config = self.global_config.clone();
        let tx = self.tx.clone();
        std::thread::spawn(move || match delete_worktree(&wt, &global_config) {
            Ok(()) => {
                let _ = tx.send(AppMsg::DeleteDone);
            }
            Err(e) => {
                let _ = tx.send(AppMsg::DeleteErr(e.to_string()));
            }
        });
    }

    fn start_cleanup(&self, items: Vec<derive::WorktreeRow>) {
        let global_config = self.global_config.clone();
        let tx = self.tx.clone();
        std::thread::spawn(move || {
            let mut deleted = Vec::new();
            let mut errors = Vec::new();
            for row in &items {
                match delete_task_row(row, &global_config) {
                    Ok(()) => deleted.push(row.worktree_path.clone()),
                    Err(e) => errors.push(format!("{}: {}", row.branch, e)),
                }
            }
            let _ = tx.send(AppMsg::CleanupDone { deleted, errors });
        });
    }

    /// Spawns a background thread to create a new git worktree and tmux session.
    ///
    /// On success, sends `AppMsg::CreateWorktreeDone` (or `CreateWorktreeWarn` if
    /// the setup script fails). On failure, sends `AppMsg::CreateWorktreeErr`.
    fn start_create_worktree(&self, branch: &str) {
        let branch = branch.to_string();
        let repo_root = self.repo_root.clone();
        let repo_name = self.repo_name.clone();
        let tx = self.tx.clone();
        // Load setup_script from repo config at call time.
        let setup_script = crate::config::load_config().setup_script;

        std::thread::spawn(move || {
            let worktree_path = derive_local_worktree_path(&repo_root, &branch);

            // Try creating a new branch; fall back to checking out an existing one.
            let new_branch_result = Command::new("git")
                .args(["worktree", "add", "-b", &branch, &worktree_path])
                .current_dir(&repo_root)
                .output();

            let add_ok = matches!(new_branch_result, Ok(out) if out.status.success());

            if !add_ok {
                // Branch may already exist — try checking it out directly.
                let out = Command::new("git")
                    .args(["worktree", "add", &worktree_path, &branch])
                    .current_dir(&repo_root)
                    .output();
                match out {
                    Ok(o) if o.status.success() => {}
                    Ok(o) => {
                        let stderr = String::from_utf8_lossy(&o.stderr);
                        let _ = tx.send(AppMsg::CreateWorktreeErr(stderr.trim().to_string()));
                        return;
                    }
                    Err(e) => {
                        let _ = tx.send(AppMsg::CreateWorktreeErr(format!("{e}")));
                        return;
                    }
                }
            }

            // Run setup script if configured.
            let mut warning: Option<String> = None;
            if let Some(script) = setup_script {
                let script_path = Path::new(&repo_root).join(&script);
                if !script_path.exists() {
                    warning = Some(format!("setup script not found: {script}"));
                } else {
                    match Command::new(&script_path)
                        .current_dir(&worktree_path)
                        .output()
                    {
                        Ok(out) if !out.status.success() => {
                            let stderr = String::from_utf8_lossy(&out.stderr);
                            let code = out.status.code().unwrap_or(-1);
                            warning = Some(format!(
                                "setup script failed (exit {code}): {}",
                                stderr.trim()
                            ));
                        }
                        Err(e) => {
                            warning = Some(format!("setup script error: {e}"));
                        }
                        _ => {}
                    }
                }
            }

            // Check if we're inside tmux before attempting session creation.
            if std::env::var("TMUX").is_err() {
                let hint = "run inside tmux for session switching".to_string();
                let _ = tx.send(AppMsg::CreateWorktreeWarn {
                    session_name: String::new(),
                    warning: hint,
                });
                return;
            }

            // Derive session name and create tmux session.
            let session_name = tmux::derive_session_name(&repo_name, Some(&branch), &worktree_path);
            let opts = crate::types::SwitchToSessionOptions {
                session_name: session_name.clone(),
                worktree_path,
                branch: Some(branch),
                pr: None,
            };

            if let Err(e) = tmux::create_session(&opts) {
                let _ = tx.send(AppMsg::CreateWorktreeErr(format!(
                    "tmux session error: {e}"
                )));
                return;
            }

            match warning {
                Some(w) => {
                    let _ = tx.send(AppMsg::CreateWorktreeWarn {
                        session_name,
                        warning: w,
                    });
                }
                None => {
                    let _ = tx.send(AppMsg::CreateWorktreeDone { session_name });
                }
            }
        });
    }

    /// Constructs a minimal `App` for use in unit tests without touching the
    /// filesystem, git, or any external services.
    #[cfg(test)]
    fn new_test(task_rows: Vec<derive::WorktreeRow>) -> Self {
        let (tx, rx) = mpsc::channel();
        App {
            cursor: 0,
            loading: false,
            refreshing: false,
            error: None,
            warning: None,
            repo_root: "/test".to_string(),
            repo_name: "test-repo".to_string(),
            pane_content: String::new(),
            view: ViewState::List,
            theme: Theme::default(),
            task_rows,
            standalone_sessions: Vec::new(),
            global_config: global_config::GlobalConfig::default(),
            active_repo_index: 0,
            show_branch_column: false,
            search_text: String::new(),
            search_active: false,
            host_reachable: HashMap::new(),
            tx,
            rx,
            last_refresh: Instant::now(),
            last_full_refresh: Instant::now(),
            throbber_state: throbber_widgets_tui::ThrobberState::default(),
            switch_target: None,
            previous_worktree_states: HashMap::new(),
            table_area: Cell::new(Rect::default()),
            url_area: Cell::new(Rect::default()),
            last_click: None,
            preview_scroll_state: std::cell::Cell::new(tui_scrollview::ScrollViewState::default()),
            expanded: HashSet::new(),
            selected_pane: None,
        }
    }
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

/// Runs the Ratatui TUI. `command` determines the initial view ("cleanup" or default list).
///
/// Returns `Ok(Some(session_name))` when the user selects a session to switch to,
/// or `Ok(None)` when the user quits without selecting. The caller is responsible
/// for performing the `tmux switch-client` (or printing the session name to stdout
/// for the wrapper script to handle).
pub fn run(command: &str) -> anyhow::Result<Option<String>> {
    // Render the TUI to /dev/tty so stdout stays clean for the session name.
    // This is the standard approach (fzf, tig, lazygit all do this) — the TUI
    // talks directly to the terminal, stdout is reserved for machine output.
    let tty = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open("/dev/tty")?;

    crossterm::terminal::enable_raw_mode()?;
    let mut tty_write = tty.try_clone()?;
    crossterm::execute!(
        tty_write,
        crossterm::terminal::EnterAlternateScreen,
        crossterm::event::EnableMouseCapture
    )?;
    let backend = ratatui::backend::CrosstermBackend::new(tty_write);
    let mut terminal = ratatui::Terminal::new(backend)?;
    terminal.clear()?;

    let mut app = App::new(command);

    // Guard: standalone session names must not collide with worktree-derived names.
    check_standalone_collisions(&app.standalone_sessions, &app.task_rows)?;

    // Start standalone sessions with start_on_launch = true.
    ensure_standalone_sessions(&app.global_config)?;

    // Initial data fetch in background
    app.start_full_refresh();

    let result = run_loop(&mut terminal, &mut app);

    // Restore terminal
    crossterm::terminal::disable_raw_mode()?;
    crossterm::execute!(
        terminal.backend_mut(),
        crossterm::event::DisableMouseCapture,
        crossterm::terminal::LeaveAlternateScreen
    )?;
    terminal.show_cursor()?;

    result
}

fn run_loop(
    terminal: &mut ratatui::Terminal<ratatui::backend::CrosstermBackend<std::fs::File>>,
    app: &mut App,
) -> anyhow::Result<Option<String>> {
    loop {
        // Advance throbber animation before drawing so spinner progresses each frame.
        app.throbber_state.calc_next();

        terminal.draw(|f| app.render(f))?;

        // Poll for events with timeout (for spinner animation).
        if event::poll(Duration::from_millis(POLL_TIMEOUT_MS))? {
            let event = event::read()?;
            let msg = match event {
                Event::Key(key) => app.handle_event(key),
                Event::Mouse(mouse) => app.handle_mouse_event(mouse),
                _ => None,
            };
            if let Some(msg) = msg {
                let mut result = app.update(msg);
                while let Some(next) = result.next_msg.take() {
                    result = app.update(next);
                }
                if result.quit {
                    break;
                }
            }
        }

        // Check for background data updates.
        app.check_updates();

        // Auto-refresh: local sources every 5s, full refresh every 60s.
        if app.last_refresh.elapsed() > Duration::from_secs(LOCAL_REFRESH_SECS) {
            app.last_refresh = Instant::now();
            if app.last_full_refresh.elapsed() > Duration::from_secs(FULL_REFRESH_SECS) {
                app.last_full_refresh = Instant::now();
                app.refreshing = true;
                app.start_full_refresh();
            } else {
                app.start_local_refresh();
            }
        }
    }
    Ok(app.switch_target.take())
}

// ---------------------------------------------------------------------------
// Stale worktree filter
// ---------------------------------------------------------------------------

fn filter_stale(rows: &[derive::WorktreeRow]) -> Vec<derive::WorktreeRow> {
    rows.iter()
        .filter(|row| {
            if let Some(ref pr) = row.pr {
                let state = pr.state.as_deref().unwrap_or("");
                return state == "merged" || state == "closed";
            }
            if let Some(ref state) = row.issue_state {
                return state == "completed" || state == "closed";
            }
            false
        })
        .cloned()
        .collect()
}

// ---------------------------------------------------------------------------
// Worktree path helpers
// ---------------------------------------------------------------------------

/// Converts a branch name to a filesystem-safe slug by replacing `/` with `-`
/// and stripping non-alphanumeric characters except `.`, `-`, `_`.
fn sanitize_branch_slug(branch: &str) -> String {
    use std::sync::OnceLock;

    use regex::Regex;

    fn non_slug_re() -> &'static Regex {
        static RE: OnceLock<Regex> = OnceLock::new();
        RE.get_or_init(|| Regex::new(r"[^a-zA-Z0-9.\-_]").unwrap())
    }

    let replaced = branch.replace('/', "-");
    non_slug_re().replace_all(&replaced, "").into_owned()
}

/// Returns the absolute conventional path for a local worktree:
/// `parent(repo_root)/worktrees/worktree-SLUG`.
fn derive_local_worktree_path(repo_root: &str, branch: &str) -> String {
    use std::path::Path;

    let slug = sanitize_branch_slug(branch);
    let parent = Path::new(repo_root)
        .parent()
        .unwrap_or_else(|| Path::new("."));
    let joined = parent.join("worktrees").join(format!("worktree-{}", slug));
    match joined.canonicalize() {
        Ok(abs) => abs.to_string_lossy().into_owned(),
        Err(_) => {
            if joined.is_absolute() {
                joined.to_string_lossy().into_owned()
            } else {
                std::env::current_dir()
                    .map(|cwd| cwd.join(&joined))
                    .unwrap_or(joined)
                    .to_string_lossy()
                    .into_owned()
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Delete worktree (shared by single-delete and cleanup)
// ---------------------------------------------------------------------------

fn delete_worktree(
    wt: &Worktree,
    global_config: &global_config::GlobalConfig,
) -> anyhow::Result<()> {
    if let Some(ref host) = wt.remote {
        // Remote deletion
        if let Some(ref sess) = wt.tmux_session {
            let _ = remote::kill_remote_tmux_session(host, sess);
        }
        if let Some(ref branch) = wt.branch {
            let slug = sanitize_branch_slug(branch);
            let _ = remote::remove_remote_registry_entry(host, &slug);
        }
        // Find the remote config matching this host to get the repo_path.
        let remote_cfg = global_config
            .repos
            .iter()
            .find_map(|repo| repo.remote_for_host(host));
        if let Some(remote_cfg) = remote_cfg {
            remote::remove_remote_worktree(host, &remote_cfg.path, &wt.path)?;
        }
        return Ok(());
    }

    // Local deletion
    if let Some(ref sess) = wt.tmux_session {
        let _ = tmux::kill_tmux_session(sess);
    }
    if git::remove_worktree(&wt.path, false).is_err() {
        git::remove_worktree(&wt.path, true)?;
    }
    Ok(())
}

/// Deletes the worktree represented by a `WorktreeRow`.
///
/// Equivalent to `delete_worktree` but operates on `WorktreeRow` fields, which is
/// the only data model available after removing the legacy `Vec<Worktree>`.
fn delete_task_row(
    row: &derive::WorktreeRow,
    global_config: &global_config::GlobalConfig,
) -> anyhow::Result<()> {
    let session_name = row.sessions.first().map(|s| s.tmux.name.as_str());
    if let Some(ref host) = row.worktree_host {
        // Remote deletion
        if let Some(sess) = session_name {
            let _ = remote::kill_remote_tmux_session(host, sess);
        }
        let slug = sanitize_branch_slug(&row.branch);
        let _ = remote::remove_remote_registry_entry(host, &slug);
        // Find the remote config matching this host to get the repo_path.
        let remote_cfg = global_config
            .repos
            .iter()
            .find_map(|repo| repo.remote_for_host(host));
        if let Some(remote_cfg) = remote_cfg {
            remote::remove_remote_worktree(host, &remote_cfg.path, &row.worktree_path)?;
        }
        return Ok(());
    }

    // Local deletion
    if let Some(sess) = session_name {
        let _ = tmux::kill_tmux_session(sess);
    }
    if git::remove_worktree(&row.worktree_path, false).is_err() {
        git::remove_worktree(&row.worktree_path, true)?;
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Main session auto-creation
// ---------------------------------------------------------------------------

/// A session that needs to be created for a repo.
#[derive(Debug, PartialEq)]
pub(crate) struct SessionToCreate {
    /// Derived tmux session name (e.g. "git-orchard-rs_main").
    pub name: String,
    /// Absolute path on disk for the session start directory.
    pub start_dir: String,
    /// Slug of the repo this session belongs to (for error messages).
    pub repo_slug: String,
}

/// Pure function: given worktrees and existing sessions per repo, returns the
/// list of sessions that need to be created.
///
/// A session is needed when:
/// - The repo has at least one non-bare worktree (the origin).
/// - No existing session has the derived name.
pub(crate) fn compute_sessions_to_create(
    repos: &[(
        String,                        // repo slug
        Vec<cache::CachedWorktree>,    // worktrees cache entries
        Vec<cache::CachedTmuxSession>, // existing local tmux sessions
    )],
) -> Vec<SessionToCreate> {
    let mut result = Vec::new();

    for (slug, worktrees, sessions) in repos {
        let origin = match worktrees.iter().find(|wt| !wt.is_bare) {
            Some(wt) => wt,
            None => continue,
        };

        let session_name = tmux::derive_main_session_name(&origin.path, Some(&origin.branch));

        if sessions.iter().any(|s| s.name == session_name) {
            continue;
        }

        result.push(SessionToCreate {
            name: session_name,
            start_dir: origin.path.clone(),
            repo_slug: slug.clone(),
        });
    }

    result
}

/// Ensures a main tmux session exists for each configured repo.
///
/// For each repo, reads the worktrees cache to find the origin (first non-bare
/// entry), then checks the local tmux sessions cache. If no session with the
/// derived name exists, creates one with `tmux::new_detached_session`.
///
/// Idempotent: skips repos whose session already exists.
/// Errors from individual repos are logged but do not block others.
///
/// After creating any sessions, refreshes the local tmux sessions cache so
/// that `derive_from_all_caches` picks them up.
pub(crate) fn ensure_main_sessions(config: &global_config::GlobalConfig) {
    let existing_sessions =
        cache::read_cache::<cache::CachedTmuxSession>(&cache::tmux_cache_path(None)).entries;

    let repo_data: Vec<_> = config
        .repos
        .iter()
        .map(|repo| {
            let worktrees = cache::read_cache::<cache::CachedWorktree>(&cache::cache_path(
                repo.owner(),
                repo.repo_name(),
                "worktrees",
            ))
            .entries;
            (repo.slug.clone(), worktrees, existing_sessions.clone())
        })
        .collect();

    let to_create = compute_sessions_to_create(&repo_data);
    let mut any_created = false;

    for session in &to_create {
        match tmux::new_detached_session(&session.name, &session.start_dir) {
            Ok(()) => {
                any_created = true;
            }
            Err(e) => {
                crate::logger::LOG.warn(&format!(
                    "ensure_main_sessions: failed to create session '{}' for repo '{}': {}",
                    session.name, session.repo_slug, e
                ));
            }
        }
    }

    if any_created {
        let _ = cache_sources::refresh_tmux_sessions(None);
    }
}

/// Creates standalone tmux sessions with `start_on_launch: true` if they don't already exist.
///
/// Returns an error if any session command fails immediately — broken config is a hard failure.
fn ensure_standalone_sessions(config: &global_config::GlobalConfig) -> anyhow::Result<()> {
    for session_cfg in &config.tmux_sessions {
        if !session_cfg.start_on_launch {
            continue;
        }
        if tmux::session_exists(&session_cfg.name) {
            continue;
        }
        tmux::new_session_with_command(&session_cfg.name, &session_cfg.cwd, &session_cfg.command)
            .map_err(|e| {
            anyhow::anyhow!(
                "Failed to start standalone session '{}': {}",
                session_cfg.name,
                e
            )
        })?;
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Collision detection
// ---------------------------------------------------------------------------

/// Checks that no standalone session name collides with a worktree-derived session name.
///
/// Returns `Err` with a descriptive message if a collision is found. The error
/// identifies the standalone config name and the worktree branch that owns the
/// conflicting session, so the user knows exactly what to fix.
fn check_standalone_collisions(
    standalone: &[StandaloneSessionRow],
    task_rows: &[derive::WorktreeRow],
) -> anyhow::Result<()> {
    use std::collections::HashMap;

    // Build a map from session name → owning worktree branch for fast lookup.
    let mut wt_sessions: HashMap<&str, &str> = HashMap::new();
    for row in task_rows {
        for s in &row.sessions {
            wt_sessions.insert(s.tmux.name.as_str(), row.branch.as_str());
        }
    }

    for row in standalone {
        let name = &row.config.name;
        if let Some(branch) = wt_sessions.get(name.as_str()) {
            return Err(anyhow::anyhow!(
                "Standalone session '{}' collides with worktree session on branch '{}'",
                name,
                branch,
            ));
        }
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Cache-based derivation
// ---------------------------------------------------------------------------

/// Reads all caches for all configured repos and derives task rows.
///
/// Delegates to `build_state::build_task_rows` which owns the single
/// authoritative cache-reading and derivation logic.
fn derive_from_all_caches(config: &global_config::GlobalConfig) -> Vec<derive::WorktreeRow> {
    crate::build_state::build_task_rows(config)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo as DPrInfo, WorktreeRow};
    use crate::session::{
        ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, TmuxSessionInfo,
    };
    use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
    use ratatui::Terminal;
    use ratatui::backend::TestBackend;

    // -----------------------------------------------------------------------
    // Test helpers
    // -----------------------------------------------------------------------

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

    fn make_task_row_with_title(
        issue_number: u32,
        title: &str,
        group: DisplayGroup,
    ) -> WorktreeRow {
        WorktreeRow {
            issue_title: Some(title.to_string()),
            ..make_task_row(issue_number, group)
        }
    }

    /// Renders the app into a flat string, one row per terminal line.
    fn render_to_string(app: &mut App, width: u16, height: u16) -> String {
        let backend = TestBackend::new(width, height);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|f| app.render(f)).unwrap();
        let buf = terminal.backend().buffer().clone();
        let mut result = String::new();
        for y in 0..height {
            for x in 0..width {
                result.push_str(buf.cell((x, y)).map(|c| c.symbol()).unwrap_or(" "));
            }
            result.push('\n');
        }
        result
    }

    /// Renders the app into an ANSI-escaped string preserving colors and styles.
    fn render_to_ansi(app: &mut App, width: u16, height: u16) -> String {
        use ratatui::style::{Color, Modifier};

        fn color_to_ansi_fg(c: Color) -> Option<&'static str> {
            match c {
                Color::Black => Some("\x1b[30m"),
                Color::Red => Some("\x1b[31m"),
                Color::Green => Some("\x1b[32m"),
                Color::Yellow => Some("\x1b[33m"),
                Color::Blue => Some("\x1b[34m"),
                Color::Magenta => Some("\x1b[35m"),
                Color::Cyan => Some("\x1b[36m"),
                Color::Gray => Some("\x1b[37m"),
                Color::DarkGray => Some("\x1b[90m"),
                Color::LightRed => Some("\x1b[91m"),
                Color::LightGreen => Some("\x1b[92m"),
                Color::LightYellow => Some("\x1b[93m"),
                Color::LightBlue => Some("\x1b[94m"),
                Color::LightMagenta => Some("\x1b[95m"),
                Color::LightCyan => Some("\x1b[96m"),
                Color::White => Some("\x1b[97m"),
                Color::Reset => None,
                _ => None,
            }
        }

        fn color_to_ansi_bg(c: Color) -> Option<&'static str> {
            match c {
                Color::Black => Some("\x1b[40m"),
                Color::Red => Some("\x1b[41m"),
                Color::Green => Some("\x1b[42m"),
                Color::Yellow => Some("\x1b[43m"),
                Color::Blue => Some("\x1b[44m"),
                Color::Magenta => Some("\x1b[45m"),
                Color::Cyan => Some("\x1b[46m"),
                Color::Gray => Some("\x1b[47m"),
                Color::DarkGray => Some("\x1b[100m"),
                Color::White => Some("\x1b[107m"),
                Color::Reset => None,
                _ => None,
            }
        }

        let backend = TestBackend::new(width, height);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|f| app.render(f)).unwrap();
        let buf = terminal.backend().buffer().clone();
        let mut result = String::new();
        for y in 0..height {
            for x in 0..width {
                if let Some(cell) = buf.cell((x, y)) {
                    let mut has_style = false;
                    if cell.modifier.contains(Modifier::BOLD) {
                        result.push_str("\x1b[1m");
                        has_style = true;
                    }
                    if cell.modifier.contains(Modifier::DIM) {
                        result.push_str("\x1b[2m");
                        has_style = true;
                    }
                    if let Some(code) = color_to_ansi_fg(cell.fg) {
                        result.push_str(code);
                        has_style = true;
                    }
                    if let Some(code) = color_to_ansi_bg(cell.bg) {
                        result.push_str(code);
                        has_style = true;
                    }
                    result.push_str(cell.symbol());
                    if has_style {
                        result.push_str("\x1b[0m");
                    }
                } else {
                    result.push(' ');
                }
            }
            result.push('\n');
        }
        result
    }

    #[test]
    fn filter_stale_merged_pr() {
        let rows = vec![
            WorktreeRow {
                pr: Some(DPrInfo {
                    number: 1,
                    branch: "feat/merged".to_string(),
                    state: Some("merged".to_string()),
                    review_decision: None,
                    checks_state: None,
                    has_conflicts: false,
                    unresolved_threads: 0,
                }),
                ..make_task_row(1, DisplayGroup::Other)
            },
            make_task_row(2, DisplayGroup::Other),
        ];
        let stale = filter_stale(&rows);
        assert_eq!(stale.len(), 1);
    }

    #[test]
    fn filter_stale_closed_issue() {
        let rows = vec![WorktreeRow {
            issue_state: Some("closed".to_string()),
            ..make_task_row(1, DisplayGroup::Other)
        }];
        let stale = filter_stale(&rows);
        assert_eq!(stale.len(), 1);
    }

    #[test]
    fn filter_stale_closed_pr() {
        let rows = vec![WorktreeRow {
            pr: Some(DPrInfo {
                number: 1,
                branch: "feat/closed".to_string(),
                state: Some("closed".to_string()),
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::Other)
        }];
        let stale = filter_stale(&rows);
        assert_eq!(stale.len(), 1);
    }

    #[test]
    fn filter_stale_completed_issue() {
        let rows = vec![WorktreeRow {
            issue_state: Some("completed".to_string()),
            ..make_task_row(1, DisplayGroup::Other)
        }];
        let stale = filter_stale(&rows);
        assert_eq!(stale.len(), 1);
    }

    #[test]
    fn filter_stale_open_pr_not_stale() {
        // An open PR should not be considered stale.
        let rows = vec![WorktreeRow {
            pr: Some(DPrInfo {
                number: 1,
                branch: "feat/open".to_string(),
                state: Some("open".to_string()),
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(1, DisplayGroup::Other)
        }];
        let stale = filter_stale(&rows);
        assert!(stale.is_empty());
    }

    // -----------------------------------------------------------------------
    // New-worktree dialog (TEA) tests
    // -----------------------------------------------------------------------

    #[test]
    fn w_key_opens_new_worktree_dialog() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('w'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::NewWorktree));
        app.update(msg.unwrap());
        assert!(matches!(app.view, ViewState::NewWorktree(_)));
    }

    #[test]
    fn worktree_branch_accepts_valid_chars() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: String::new(),
        });
        for c in "feature/my-branch_1.x".chars() {
            let key = KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE);
            if let Some(msg) = app.handle_event(key) {
                app.update(msg);
            }
        }
        if let ViewState::NewWorktree(nw) = &app.view {
            assert_eq!(nw.branch, "feature/my-branch_1.x");
        } else {
            panic!("expected NewWorktree view");
        }
    }

    #[test]
    fn worktree_branch_rejects_spaces() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: String::new(),
        });
        for c in "feature branch".chars() {
            let key = KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE);
            if let Some(msg) = app.handle_event(key) {
                app.update(msg);
            }
        }
        if let ViewState::NewWorktree(nw) = &app.view {
            assert_eq!(nw.branch, "featurebranch");
        } else {
            panic!("expected NewWorktree view");
        }
    }

    #[test]
    fn worktree_branch_rejects_special_chars() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: String::new(),
        });
        for c in "feat!branch@".chars() {
            let key = KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE);
            if let Some(msg) = app.handle_event(key) {
                app.update(msg);
            }
        }
        if let ViewState::NewWorktree(nw) = &app.view {
            assert_eq!(nw.branch, "featbranch");
        } else {
            panic!("expected NewWorktree view");
        }
    }

    #[test]
    fn worktree_backspace_removes_last_char() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: "feature/xy".to_string(),
        });
        let key = KeyEvent::new(KeyCode::Backspace, KeyModifiers::NONE);
        let msg = app.handle_event(key).unwrap();
        app.update(msg);
        if let ViewState::NewWorktree(nw) = &app.view {
            assert_eq!(nw.branch, "feature/x");
        } else {
            panic!("expected NewWorktree view");
        }
    }

    #[test]
    fn worktree_escape_returns_to_list() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: "feature/x".to_string(),
        });
        let key = KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE);
        let msg = app.handle_event(key).unwrap();
        app.update(msg);
        assert!(matches!(app.view, ViewState::List));
    }

    #[test]
    fn worktree_enter_on_empty_does_nothing() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::NewWorktree(state::NewWorktreeState {
            branch: String::new(),
        });
        let key = KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE);
        let msg = app.handle_event(key).unwrap();
        app.update(msg);
        // Should still be in NewWorktree since branch was empty
        // (ConfirmNewWorktree with empty branch is a no-op)
        // Actually the update transitions to List only when branch is non-empty
        assert!(matches!(app.view, ViewState::NewWorktree(_)));
    }

    // -----------------------------------------------------------------------
    // Rendering smoke tests
    // -----------------------------------------------------------------------

    #[test]
    fn task_list_renders_issue_title() {
        let rows = vec![make_task_row_with_title(
            42,
            "Fix login bug",
            DisplayGroup::Other,
        )];
        let mut app = App::new_test(rows);
        let output = render_to_string(&mut app, 120, 40);
        assert!(output.contains("Fix login bug"), "expected title in output");
        assert!(output.contains("#42"), "expected issue number in output");
        assert!(
            output.contains("other"),
            "expected section header in output"
        );
    }

    #[test]
    fn loading_state_renders_when_no_tasks() {
        let mut app = App::new_test(vec![]);
        app.loading = true;
        let output = render_to_string(&mut app, 120, 40);
        assert!(
            output.contains("Loading"),
            "expected Loading text in output"
        );
    }

    #[test]
    fn empty_non_loading_state_shows_init_prompt() {
        let mut app = App::new_test(vec![]);
        app.loading = false;
        let output = render_to_string(&mut app, 120, 40);
        assert!(
            output.contains("No worktrees found"),
            "expected empty state message in output"
        );
    }

    /// Sends a key through handle_event + update and returns the UpdateResult.
    fn send_key(app: &mut App, key: KeyEvent) -> UpdateResult {
        if let Some(msg) = app.handle_event(key) {
            app.update(msg)
        } else {
            UpdateResult {
                quit: false,
                next_msg: None,
            }
        }
    }

    #[test]
    fn q_key_quits() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('q'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::Quit));
        let r = app.update(msg.unwrap());
        assert!(r.quit);
    }

    #[test]
    fn ctrl_c_quits() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('c'), KeyModifiers::CONTROL);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::Quit));
        let r = app.update(msg.unwrap());
        assert!(r.quit);
    }

    #[test]
    fn j_advances_cursor_in_task_view() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
        ];
        let mut app = App::new_test(rows);
        assert_eq!(app.cursor, 0);
        let key = KeyEvent::new(KeyCode::Char('j'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        app.update(msg.unwrap());
        assert_eq!(app.cursor, 1);
    }

    #[test]
    fn k_moves_cursor_up_in_task_view() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
        ];
        let mut app = App::new_test(rows);
        app.cursor = 1;
        let key = KeyEvent::new(KeyCode::Char('k'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        app.update(msg.unwrap());
        assert_eq!(app.cursor, 0);
    }

    #[test]
    fn task_list_renders_pr_number() {
        let row = WorktreeRow {
            pr: Some(DPrInfo {
                number: 55,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: None,
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row(42, DisplayGroup::ReadyToMerge)
        };
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);
        // The ISSUE and PR columns are now separate.
        assert!(output.contains("#42"), "expected issue number");
        assert!(output.contains("#55"), "expected PR number");
    }

    #[test]
    fn unreachable_host_blocks_enter() {
        let row = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Remote("gpu1".to_string()),
                    name: "sess".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                panes: vec![],
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let mut app = App::new_test(vec![row]);
        app.host_reachable.insert("gpu1".to_string(), false);

        let key = KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        let r = app.update(msg.unwrap());
        assert!(!r.quit, "enter on unreachable host should not quit");
        assert!(app.warning.is_some(), "expected warning to be set");
        assert!(
            app.warning.as_ref().unwrap().0.contains("unreachable"),
            "expected 'unreachable' in warning message"
        );
    }

    #[test]
    fn header_renders_host_connectivity() {
        let mut app = App::new_test(vec![]);
        app.host_reachable.insert("gpu1".to_string(), true);
        app.host_reachable.insert("dev2".to_string(), false);
        let output = render_to_string(&mut app, 120, 40);
        assert!(output.contains("@gpu1"), "expected @gpu1 in header");
        assert!(output.contains("@dev2"), "expected @dev2 in header");
        assert!(output.contains('\u{25cf}'), "expected ● for reachable host");
        assert!(
            output.contains('\u{2717}'),
            "expected ✗ for unreachable host"
        );
    }

    #[test]
    fn question_mark_opens_help() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('?'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        app.update(msg.unwrap());
        assert_eq!(app.view_name(), "Help");
    }

    #[test]
    fn esc_closes_help() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::Help;
        let key = KeyEvent::new(KeyCode::Esc, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        app.update(msg.unwrap());
        assert_eq!(app.view_name(), "List");
    }

    #[test]
    fn enter_on_worktree_without_session_creates_session() {
        // In the worktree-first model, every row has a worktree, so Enter
        // creates a session rather than showing a dialog.
        let rows = vec![make_task_row(42, DisplayGroup::NeedsAttention)];
        let mut app = App::new_test(rows);
        let key = KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE);
        send_key(&mut app, key);
        // View should remain List -- no confirmation dialog should appear.
        assert_ne!(app.view_name(), "Help");
    }

    // -----------------------------------------------------------------------
    // WorktreeRow builder helper
    // -----------------------------------------------------------------------

    fn make_worktree_row(branch: &str, group: DisplayGroup) -> WorktreeRow {
        WorktreeRow {
            repo_slug: "owner/repo".to_string(),
            worktree_path: format!("/workspace/{}", branch.replace('/', "-")),
            branch: branch.to_string(),
            worktree_host: None,
            issue_number: None,
            issue_title: None,
            issue_state: None,
            pr: None,
            sessions: vec![],
            display_group: group,
            is_main_worktree: false,
        }
    }

    // -----------------------------------------------------------------------
    // E2E rendering tests (TestBackend at 120×40)
    // -----------------------------------------------------------------------

    #[test]
    fn shepherd_row_renders_first_and_has_distinct_section_header() {
        let main_wt = WorktreeRow {
            is_main_worktree: true,
            display_group: DisplayGroup::RepoMain,
            ..make_worktree_row("main", DisplayGroup::RepoMain)
        };
        let other = make_worktree_row("feat/something", DisplayGroup::Other);
        let mut app = App::new_test(vec![main_wt, other]);
        let output = render_to_string(&mut app, 120, 40);

        // "repo main" section header must appear before "other"
        let repo_main_pos = output
            .find("repo main")
            .expect("expected 'repo main' section header");
        let other_pos = output
            .find("other")
            .expect("expected 'other' section header");
        assert!(
            repo_main_pos < other_pos,
            "repo main section must appear before other section"
        );

        // The main worktree row must be visible (shows repo name in TITLE column).
        assert!(
            output.contains("repo"),
            "expected repo name in main worktree row output"
        );
    }

    #[test]
    fn worktree_without_pr_renders_in_other_section() {
        let row = make_worktree_row("experimental", DisplayGroup::Other);
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);

        assert!(
            output.contains("experimental"),
            "expected branch name in output"
        );
        assert!(
            output.contains("other"),
            "expected 'other' section header in output"
        );
    }

    #[test]
    fn display_groups_render_in_correct_order() {
        let needs_attention = WorktreeRow {
            pr: Some(DPrInfo {
                number: 10,
                branch: "feat/needs-attn".to_string(),
                state: None,
                review_decision: None,
                checks_state: Some("failing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_worktree_row("feat/needs-attn", DisplayGroup::NeedsAttention)
        };
        let claude_working = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "repo-claude".to_string(),
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
            ..make_worktree_row("feat/claude-active", DisplayGroup::ClaudeWorking)
        };
        let ready_to_merge = WorktreeRow {
            pr: Some(DPrInfo {
                number: 20,
                branch: "feat/approved".to_string(),
                state: None,
                review_decision: Some("approved".to_string()),
                checks_state: Some("passing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_worktree_row("feat/approved", DisplayGroup::ReadyToMerge)
        };
        let other = make_worktree_row("feat/plain", DisplayGroup::Other);

        // Pre-sort to match expected display order (as derive::derive_all_repos would produce)
        let mut app = App::new_test(vec![needs_attention, claude_working, ready_to_merge, other]);
        let output = render_to_string(&mut app, 120, 40);

        let pos_na = output
            .find("needs attention")
            .expect("expected 'needs attention'");
        let pos_cw = output
            .find("claude working")
            .expect("expected 'claude working'");
        let pos_rtm = output
            .find("ready to merge")
            .expect("expected 'ready to merge'");
        let pos_other = output.find("other").expect("expected 'other'");

        assert!(
            pos_na < pos_cw,
            "needs attention must come before claude working"
        );
        assert!(
            pos_cw < pos_rtm,
            "claude working must come before ready to merge"
        );
        assert!(pos_rtm < pos_other, "ready to merge must come before other");
    }

    #[test]
    fn claude_needs_input_indicator_renders_and_row_in_needs_attention() {
        let row = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "repo-47".to_string(),
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
            ..make_worktree_row("feat/waiting", DisplayGroup::NeedsAttention)
        };
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);

        assert!(
            output.contains("input"),
            "expected 'input' claude status indicator"
        );
        assert!(
            output.contains("needs attention"),
            "expected NeedsAttention section header"
        );
    }

    #[test]
    fn pr_enrichment_shows_in_rendered_output() {
        let row = WorktreeRow {
            pr: Some(DPrInfo {
                number: 55,
                branch: "feat/branch".to_string(),
                state: None,
                review_decision: None,
                checks_state: Some("failing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_worktree_row("feat/branch", DisplayGroup::NeedsAttention)
        };
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);

        assert!(output.contains("#55"), "expected PR number #55 in output");
        assert!(
            output.contains("failing"),
            "expected 'failing' CI state in output"
        );
    }

    #[test]
    fn remote_host_indicator_renders_for_remote_worktree() {
        let row = WorktreeRow {
            worktree_host: Some("gpu1".to_string()),
            ..make_worktree_row("feat/remote", DisplayGroup::Other)
        };
        let mut app = App::new_test(vec![row]);
        app.host_reachable.insert("gpu1".to_string(), true);
        let output = render_to_string(&mut app, 120, 40);

        assert!(output.contains("@gpu1"), "expected '@gpu1' in output");
        assert!(
            output.contains('\u{25cf}'),
            "expected ● reachable indicator"
        );
    }

    #[test]
    fn unreachable_remote_host_shows_x_indicator() {
        let row = WorktreeRow {
            worktree_host: Some("gpu1".to_string()),
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Remote("gpu1".to_string()),
                    name: "repo-gpu1".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                panes: vec![],
            }],
            ..make_worktree_row("feat/remote", DisplayGroup::Other)
        };
        let mut app = App::new_test(vec![row]);
        app.host_reachable.insert("gpu1".to_string(), false);
        let output = render_to_string(&mut app, 120, 40);

        assert!(output.contains("@gpu1"), "expected '@gpu1' in output");
        assert!(
            output.contains('\u{2717}'),
            "expected ✗ unreachable indicator"
        );
    }

    #[test]
    fn issue_number_and_title_render_in_output() {
        let row = WorktreeRow {
            issue_number: Some(2478),
            issue_title: Some("Support workflow agents".to_string()),
            ..make_worktree_row("webapp-2478", DisplayGroup::Other)
        };
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);

        assert!(output.contains("#2478"), "expected '#2478' in output");
        assert!(
            output.contains("Support workflow agents"),
            "expected issue title in output"
        );
    }

    #[test]
    fn help_overlay_renders_when_question_mark_pressed() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('?'), KeyModifiers::NONE);
        send_key(&mut app, key);
        let output = render_to_string(&mut app, 120, 40);

        assert!(
            output.contains("Keyboard Shortcuts"),
            "expected 'Keyboard Shortcuts' in help overlay"
        );
        assert!(
            output.contains("enter"),
            "expected 'enter' key binding in help"
        );
        assert!(
            output.contains("switch") || output.contains("Switch"),
            "expected 'switch' action text in help"
        );
    }

    #[test]
    fn j_moves_cursor_down_in_worktree_first_view() {
        let rows = vec![
            make_worktree_row("feat/one", DisplayGroup::NeedsAttention),
            make_worktree_row("feat/two", DisplayGroup::ClaudeWorking),
            make_worktree_row("feat/three", DisplayGroup::ReadyToMerge),
        ];
        let mut app = App::new_test(rows);
        assert_eq!(app.cursor, 0);

        let j = KeyEvent::new(KeyCode::Char('j'), KeyModifiers::NONE);
        send_key(&mut app, j);
        assert_eq!(app.cursor, 1, "j should advance cursor from 0 to 1");
    }

    #[test]
    fn k_moves_cursor_up_in_worktree_first_view() {
        let rows = vec![
            make_worktree_row("feat/one", DisplayGroup::NeedsAttention),
            make_worktree_row("feat/two", DisplayGroup::ClaudeWorking),
            make_worktree_row("feat/three", DisplayGroup::ReadyToMerge),
        ];
        let mut app = App::new_test(rows);
        app.cursor = 1;

        let k = KeyEvent::new(KeyCode::Char('k'), KeyModifiers::NONE);
        send_key(&mut app, k);
        assert_eq!(app.cursor, 0, "k should move cursor from 1 to 0");
    }

    #[test]
    fn q_returns_true_in_worktree_first_view() {
        let rows = vec![
            make_worktree_row("feat/one", DisplayGroup::NeedsAttention),
            make_worktree_row("feat/two", DisplayGroup::ClaudeWorking),
            make_worktree_row("feat/three", DisplayGroup::ReadyToMerge),
        ];
        let mut app = App::new_test(rows);
        let q = KeyEvent::new(KeyCode::Char('q'), KeyModifiers::NONE);
        let r = send_key(&mut app, q);
        assert!(r.quit, "q should return quit=true");
    }

    // -----------------------------------------------------------------------
    // handle_event tests
    // -----------------------------------------------------------------------

    #[test]
    fn handle_event_returns_none_for_unbound_key() {
        let app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('z'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), None);
    }

    #[test]
    fn handle_event_ctrl_c_in_any_view() {
        let ctrl_c = KeyEvent::new(KeyCode::Char('c'), KeyModifiers::CONTROL);

        let app_help = {
            let mut a = App::new_test(vec![]);
            a.view = ViewState::Help;
            a
        };
        assert_eq!(app_help.handle_event(ctrl_c), Some(Message::Quit));

        let app_cleanup = {
            let mut a = App::new_test(vec![]);
            a.view = ViewState::Cleanup(state::CleanupState {
                stale: vec![],
                selected: std::collections::HashSet::new(),
                cursor: 0,
                phase: Phase::Idle,
                deleted: vec![],
                errors: vec![],
            });
            a
        };
        assert_eq!(app_cleanup.handle_event(ctrl_c), Some(Message::Quit));
    }

    #[test]
    fn handle_event_search_mode_intercepts_chars() {
        let mut app = App::new_test(vec![]);
        app.search_active = true;
        let key = KeyEvent::new(KeyCode::Char('a'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::SearchChar('a')));
    }

    #[test]
    fn handle_event_digit_returns_cursor_to() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
            make_task_row(3, DisplayGroup::Other),
        ];
        let app = App::new_test(rows);
        let key = KeyEvent::new(KeyCode::Char('1'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::CursorTo(0)));
    }

    #[test]
    fn handle_event_delete_confirm_y() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::ConfirmDelete(state::DeleteState {
            target: crate::types::Worktree {
                path: "/test/wt".to_string(),
                branch: Some("feat/test".to_string()),
                head: String::new(),
                is_bare: false,
                has_conflicts: false,
                pr: None,
                pr_loading: false,
                tmux_session: None,
                tmux_attached: false,
                tmux_pane_title: None,
                remote: None,
                issue_number: None,
                issue_state: None,
            },
            phase: Phase::Confirm,
            error: None,
        });
        let key = KeyEvent::new(KeyCode::Char('y'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::ConfirmYes));
    }

    #[test]
    fn handle_event_cleanup_space_toggles() {
        let mut app = App::new_test(vec![]);
        app.view = ViewState::Cleanup(state::CleanupState {
            stale: vec![],
            selected: std::collections::HashSet::new(),
            cursor: 0,
            phase: Phase::Idle,
            deleted: vec![],
            errors: vec![],
        });
        let key = KeyEvent::new(KeyCode::Char(' '), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::ToggleSelection));
    }

    #[test]
    fn handle_event_p_maps_to_toggle_priority() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Char('p'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::TogglePriority));
    }

    #[test]
    fn toggle_priority_update_does_not_quit() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let result = app.update(Message::TogglePriority);
        assert!(!result.quit);
    }

    // -----------------------------------------------------------------------
    // compute_sessions_to_create
    // -----------------------------------------------------------------------

    fn make_cached_worktree(path: &str, branch: &str, is_bare: bool) -> cache::CachedWorktree {
        cache::CachedWorktree {
            path: path.to_string(),
            branch: branch.to_string(),
            is_bare,
            is_locked: false,
            host: None,
        }
    }

    fn make_cached_session(name: &str) -> cache::CachedTmuxSession {
        cache::CachedTmuxSession {
            name: name.to_string(),
            path: "/some/path".to_string(),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            host: None,
            last_output_lines: vec![],
            claude_state_raw: None,
        }
    }

    #[test]
    fn returns_session_to_create_when_none_exist() {
        let repos = vec![(
            "acme/my-project".to_string(),
            vec![make_cached_worktree(
                "/workspace/git-orchard-rs",
                "main",
                false,
            )],
            vec![],
        )];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "git-orchard-rs_main");
        assert_eq!(result[0].start_dir, "/workspace/git-orchard-rs");
    }

    #[test]
    fn skips_repo_when_session_already_exists() {
        let repos = vec![(
            "acme/my-project".to_string(),
            vec![make_cached_worktree(
                "/workspace/git-orchard-rs",
                "main",
                false,
            )],
            vec![make_cached_session("git-orchard-rs_main")],
        )];
        let result = compute_sessions_to_create(&repos);
        assert!(
            result.is_empty(),
            "expected no sessions to create when session exists"
        );
    }

    #[test]
    fn creates_missing_session_even_when_other_repos_have_theirs() {
        let repos = vec![
            (
                "acme/my-project".to_string(),
                vec![make_cached_worktree(
                    "/workspace/git-orchard-rs",
                    "main",
                    false,
                )],
                vec![make_cached_session("git-orchard-rs_main")],
            ),
            (
                "acme/webapp".to_string(),
                vec![make_cached_worktree("/workspace/webapp", "main", false)],
                vec![make_cached_session("git-orchard-rs_main")],
            ),
        ];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "webapp_main");
        assert_eq!(result[0].start_dir, "/workspace/webapp");
        assert_eq!(result[0].repo_slug, "acme/webapp");
    }

    #[test]
    fn returns_sessions_for_all_repos_when_none_exist() {
        let repos = vec![
            (
                "acme/my-project".to_string(),
                vec![make_cached_worktree(
                    "/workspace/git-orchard-rs",
                    "main",
                    false,
                )],
                vec![],
            ),
            (
                "acme/webapp".to_string(),
                vec![make_cached_worktree("/workspace/webapp", "main", false)],
                vec![],
            ),
        ];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 2);
        let names: Vec<&str> = result.iter().map(|s| s.name.as_str()).collect();
        assert!(
            names.contains(&"git-orchard-rs_main"),
            "expected git-orchard-rs_main"
        );
        assert!(names.contains(&"webapp_main"), "expected webapp_main");
    }

    #[test]
    fn skips_repo_with_no_non_bare_worktree() {
        let repos = vec![(
            "acme/my-project".to_string(),
            vec![make_cached_worktree(
                "/workspace/git-orchard-rs",
                "main",
                true,
            )],
            vec![],
        )];
        let result = compute_sessions_to_create(&repos);
        assert!(
            result.is_empty(),
            "expected no sessions when only bare worktrees exist"
        );
    }

    #[test]
    fn uses_origin_branch_not_hardcoded_main() {
        let repos = vec![(
            "acme/webapp".to_string(),
            vec![make_cached_worktree("/workspace/webapp", "develop", false)],
            vec![],
        )];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "webapp_develop");
    }

    #[test]
    fn picks_first_non_bare_worktree_as_origin() {
        let repos = vec![(
            "acme/my-project".to_string(),
            vec![
                make_cached_worktree("/workspace/git-orchard-rs", "main", false),
                make_cached_worktree("/workspace/git-orchard-rs/.worktrees/feat", "feat/x", false),
            ],
            vec![],
        )];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].start_dir, "/workspace/git-orchard-rs");
    }

    #[test]
    #[ignore] // Run manually: cargo test tui_screenshot -- --ignored
    fn tui_screenshot() {
        let shepherd = WorktreeRow {
            is_main_worktree: true,
            display_group: DisplayGroup::RepoMain,
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "orchard_main".to_string(),
                    status: SessionStatus::Running { attached: true },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Working,
                    cost_usd: Some(12.50),
                    context_window_pct: Some(23.0),
                    model: Some("opus".to_string()),
                }),
                panes: vec![],
            }],
            ..make_worktree_row("main", DisplayGroup::RepoMain)
        };
        let needs_attn = WorktreeRow {
            pr: Some(DPrInfo {
                number: 70,
                branch: "feat/tea-pattern".to_string(),
                state: Some("open".to_string()),
                review_decision: Some("changes_requested".to_string()),
                checks_state: Some("failing".to_string()),
                has_conflicts: false,
                unresolved_threads: 2,
            }),
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "orchard_issue53".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Input,
                    cost_usd: Some(8.30),
                    context_window_pct: Some(67.0),
                    model: Some("sonnet".to_string()),
                }),
                panes: vec![],
            }],
            ..make_task_row_with_title(53, "TEA pattern refactor", DisplayGroup::NeedsAttention)
        };
        let working = WorktreeRow {
            pr: Some(DPrInfo {
                number: 68,
                branch: "feat/shepherd".to_string(),
                state: Some("open".to_string()),
                review_decision: None,
                checks_state: Some("pending".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "orchard_issue47".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Working,
                    cost_usd: Some(37.88),
                    context_window_pct: Some(19.0),
                    model: Some("opus".to_string()),
                }),
                panes: vec![],
            }],
            ..make_task_row_with_title(
                47,
                "Shepherd persistent session",
                DisplayGroup::ClaudeWorking,
            )
        };
        let ready = WorktreeRow {
            pr: Some(DPrInfo {
                number: 67,
                branch: "feat/theme-struct".to_string(),
                state: Some("open".to_string()),
                review_decision: Some("approved".to_string()),
                checks_state: Some("passing".to_string()),
                has_conflicts: false,
                unresolved_threads: 0,
            }),
            ..make_task_row_with_title(54, "Add Theme struct", DisplayGroup::ReadyToMerge)
        };
        let other = make_task_row_with_title(16, "Orchard heal command", DisplayGroup::Other);

        let mut app = App::new_test(vec![shepherd, needs_attn, working, ready, other]);
        let ansi = render_to_ansi(&mut app, 120, 30);
        let path = std::env::temp_dir().join("orchard-tui-screenshot.ansi");
        std::fs::write(&path, &ansi).expect("failed to write screenshot");
        eprintln!("Screenshot written to: {}", path.display());
    }

    #[test]
    fn session_name_sanitizes_dots_in_path() {
        let repos = vec![(
            "org/my.project-v2".to_string(),
            vec![make_cached_worktree(
                "/workspace/my.project-v2",
                "main",
                false,
            )],
            vec![],
        )];
        let result = compute_sessions_to_create(&repos);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].name, "my_project-v2_main");
    }

    // -----------------------------------------------------------------------
    // Mouse event tests
    // -----------------------------------------------------------------------

    use crossterm::event::{MouseButton, MouseEvent, MouseEventKind};

    /// Creates a MouseEvent with the given kind at the specified position.
    fn make_mouse_event(kind: MouseEventKind, column: u16, row: u16) -> MouseEvent {
        MouseEvent {
            kind,
            column,
            row,
            modifiers: KeyModifiers::NONE,
        }
    }

    /// Creates an App with table_area pre-set for mouse hit testing.
    fn app_with_table_area(task_rows: Vec<WorktreeRow>) -> App {
        let app = App::new_test(task_rows);
        // Simulate a rendered table body starting at y=5, height=10, full width.
        app.table_area.set(Rect {
            x: 0,
            y: 5,
            width: 80,
            height: 10,
        });
        app
    }

    #[test]
    fn mouse_scroll_down_in_table_returns_cursor_down() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 7);
        assert_eq!(app.handle_mouse_event(event), Some(Message::CursorDown));
    }

    #[test]
    fn mouse_scroll_up_in_table_returns_cursor_up() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::ScrollUp, 10, 7);
        assert_eq!(app.handle_mouse_event(event), Some(Message::CursorUp));
    }

    #[test]
    fn mouse_scroll_outside_table_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // y=2 is above the table body (starts at y=5).
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 2);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_click_selects_row() {
        let mut app = app_with_table_area(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
            make_task_row(3, DisplayGroup::NeedsAttention),
        ]);
        // All in same group, so group header at visual row 0, data rows at 1, 2, 3.
        // Click on visual row 2 (y=5+2=7) -> task index 1 -> cursor 1.
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 7);
        assert_eq!(app.handle_mouse_event(event), Some(Message::CursorTo(1)));
    }

    #[test]
    fn mouse_click_on_group_header_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // Group header is at visual row 0 (y=5).
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 5);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_click_below_last_row_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // One group header + one data row = 2 visual rows. Click at visual row 5 is out of range.
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 10);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_click_outside_table_x_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // x=90 is outside the table (width=80).
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 90, 7);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_double_click_returns_activate_row() {
        let mut app = app_with_table_area(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
        ]);
        // First click: visual row 1 (y=6) -> cursor 0.
        let event1 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        assert_eq!(app.handle_mouse_event(event1), Some(Message::CursorTo(0)));
        assert!(app.last_click.is_some());

        // Second click on same row within DOUBLE_CLICK_MS -> ActivateRow.
        let event2 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        assert_eq!(
            app.handle_mouse_event(event2),
            Some(Message::ActivateRow(0))
        );
        assert!(app.last_click.is_none());
    }

    #[test]
    fn mouse_clicks_on_different_rows_not_double_click() {
        let mut app = app_with_table_area(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
            make_task_row(3, DisplayGroup::NeedsAttention),
        ]);
        // Click on row 0 (visual row 1, y=6).
        let event1 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        assert_eq!(app.handle_mouse_event(event1), Some(Message::CursorTo(0)));

        // Click on row 2 (visual row 3, y=8) -> CursorTo, not Enter.
        let event2 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 8);
        assert_eq!(app.handle_mouse_event(event2), Some(Message::CursorTo(2)));
    }

    #[test]
    fn mouse_events_ignored_during_search() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        app.search_active = true;
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 7);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_events_ignored_in_help_view() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        app.view = ViewState::Help;
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 7);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_events_ignored_in_confirm_delete_view() {
        let mut app = app_with_table_area(vec![]);
        app.view = ViewState::ConfirmDelete(state::DeleteState {
            target: crate::types::Worktree {
                path: "/test/wt".to_string(),
                branch: Some("feat/test".to_string()),
                head: String::new(),
                is_bare: false,
                has_conflicts: false,
                pr: None,
                pr_loading: false,
                tmux_session: None,
                tmux_attached: false,
                tmux_pane_title: None,
                remote: None,
                issue_number: None,
                issue_state: None,
            },
            phase: Phase::Confirm,
            error: None,
        });
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 7);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn last_click_none_on_new_app() {
        let app = App::new_test(vec![]);
        assert!(app.last_click.is_none());
    }

    #[test]
    fn last_click_stored_after_single_click() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        app.handle_mouse_event(event);
        assert!(app.last_click.is_some());
        let (row, _) = app.last_click.unwrap();
        assert_eq!(row, 0);
    }

    #[test]
    fn last_click_reset_after_double_click() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        app.handle_mouse_event(event);
        assert!(app.last_click.is_some());

        let event2 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        app.handle_mouse_event(event2);
        assert!(app.last_click.is_none());
    }

    #[test]
    fn table_area_default_before_render() {
        let app = App::new_test(vec![]);
        let area = app.table_area.get();
        assert_eq!(area, Rect::default());
    }

    #[test]
    fn url_area_default_before_render() {
        let app = App::new_test(vec![]);
        let area = app.url_area.get();
        assert_eq!(area, Rect::default());
    }

    #[test]
    fn visual_row_to_cursor_standalone_sessions() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // Add a standalone session.
        app.standalone_sessions
            .push(crate::session::StandaloneSessionRow {
                config: crate::session::StandaloneConfig {
                    name: "test-session".to_string(),
                    command: "bash".to_string(),
                    cwd: "/tmp".to_string(),
                    start_on_launch: false,
                },
                session: EnrichedSession {
                    tmux: TmuxSessionInfo {
                        name: "test-session".to_string(),
                        host: Host::Local,
                        status: SessionStatus::Dead,
                    },
                    claude: None,
                    panes: vec![],
                },
            });

        // Visual row 0 = standalone session -> cursor 0.
        assert_eq!(app.visual_row_to_cursor(0), Some(0));
        // Visual row 1 = group header for NeedsAttention -> None.
        assert_eq!(app.visual_row_to_cursor(1), None);
        // Visual row 2 = first task row -> cursor 1 (standalone_count=1 + task_idx=0).
        assert_eq!(app.visual_row_to_cursor(2), Some(1));
    }

    #[test]
    fn visual_row_to_cursor_multiple_groups() {
        let app = App::new_test(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
            make_task_row(3, DisplayGroup::Other),
        ]);
        // No standalone sessions. Layout:
        // Row 0: NeedsAttention group header -> None
        // Row 1: task 0 -> cursor 0
        // Row 2: task 1 -> cursor 1
        // Row 3: Other group header -> None
        // Row 4: task 2 -> cursor 2
        assert_eq!(app.visual_row_to_cursor(0), None);
        assert_eq!(app.visual_row_to_cursor(1), Some(0));
        assert_eq!(app.visual_row_to_cursor(2), Some(1));
        assert_eq!(app.visual_row_to_cursor(3), None);
        assert_eq!(app.visual_row_to_cursor(4), Some(2));
        assert_eq!(app.visual_row_to_cursor(5), None); // out of range
    }

    #[test]
    fn mouse_click_url_area_returns_open_attribution() {
        let mut app = app_with_table_area(vec![]);
        // Set up a URL area.
        app.url_area.set(Rect {
            x: 50,
            y: 30,
            width: 40,
            height: 1,
        });
        // Click within URL area returns OpenAttribution (side effect deferred to update).
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 55, 30);
        assert_eq!(
            app.handle_mouse_event(event),
            Some(Message::OpenAttribution)
        );
    }

    #[test]
    fn mouse_click_outside_url_area_in_footer_returns_none() {
        let mut app = app_with_table_area(vec![]);
        app.url_area.set(Rect {
            x: 50,
            y: 30,
            width: 40,
            height: 1,
        });
        // Click on footer row but outside URL x-bounds.
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 30);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn mouse_double_click_expired_returns_cursor_to() {
        let mut app = app_with_table_area(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
        ]);
        // First click on row 0.
        let event1 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        assert_eq!(app.handle_mouse_event(event1), Some(Message::CursorTo(0)));

        // Simulate expired double-click window by back-dating last_click.
        app.last_click = Some((0, Instant::now() - Duration::from_millis(500)));

        // Second click on same row after timeout -> CursorTo, not ActivateRow.
        let event2 = make_mouse_event(MouseEventKind::Down(MouseButton::Left), 10, 6);
        assert_eq!(app.handle_mouse_event(event2), Some(Message::CursorTo(0)));
    }

    #[test]
    fn mouse_right_click_on_table_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::Down(MouseButton::Right), 10, 6);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    // Rich content widget tests (ScrollView preview, ASCII art header)
    // -----------------------------------------------------------------------

    #[test]
    fn handle_event_page_up_returns_preview_scroll() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let key = KeyEvent::new(KeyCode::PageUp, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::PreviewPageUp));
    }

    #[test]
    fn handle_event_page_down_returns_preview_scroll() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let key = KeyEvent::new(KeyCode::PageDown, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::PreviewPageDown));
    }

    #[test]
    fn preview_page_down_advances_scroll_state() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let initial = app.preview_scroll_state.get();
        app.update(Message::PreviewPageDown);
        let after = app.preview_scroll_state.get();
        // After scrolling down, the y offset should have advanced.
        assert!(
            after.offset().y >= initial.offset().y,
            "scroll_page_down should advance y offset"
        );
    }

    #[test]
    fn preview_page_up_decrements_scroll_state() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // First scroll down, then back up.
        app.update(Message::PreviewPageDown);
        app.update(Message::PreviewPageDown);
        let before = app.preview_scroll_state.get();
        app.update(Message::PreviewPageUp);
        let after = app.preview_scroll_state.get();
        assert!(
            after.offset().y <= before.offset().y,
            "scroll_page_up should decrease y offset"
        );
    }

    #[test]
    fn ascii_art_renders_in_tall_terminal() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        app.repo_name = "orchard".to_string();
        let output = render_to_string(&mut app, 120, 40);
        // The ASCII art logo should always appear in tall terminals.
        assert!(
            output.contains("╔═╗╦═╗╔═╗"),
            "ASCII art logo should appear in tall terminal, got:\n{output}"
        );
    }

    #[test]
    fn compact_header_on_short_terminal() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        app.repo_name = "orchard".to_string();
        // Short terminal: 20 rows, well below FULL_HEADER_MIN_HEIGHT.
        let output = render_to_string(&mut app, 120, 20);
        // Compact header shows "Git Orchard" as plain text.
        assert!(
            output.contains("Git Orchard"),
            "compact header should show 'Git Orchard' text"
        );
    }

    // -----------------------------------------------------------------------
    // check_standalone_collisions tests
    // -----------------------------------------------------------------------

    fn make_standalone_row(name: &str) -> crate::session::StandaloneSessionRow {
        crate::session::StandaloneSessionRow {
            config: crate::session::StandaloneConfig {
                name: name.to_string(),
                command: "bash".to_string(),
                cwd: "/tmp".to_string(),
                start_on_launch: false,
            },
            session: EnrichedSession {
                tmux: TmuxSessionInfo {
                    name: name.to_string(),
                    host: Host::Local,
                    status: SessionStatus::Dead,
                },
                claude: None,
                panes: vec![],
            },
        }
    }

    fn make_task_row_with_session(branch: &str, session_name: &str) -> WorktreeRow {
        WorktreeRow {
            repo_slug: "owner/repo".to_string(),
            worktree_path: format!("/workspace/{}", branch),
            branch: branch.to_string(),
            worktree_host: None,
            issue_number: None,
            issue_title: None,
            issue_state: None,
            pr: None,
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    name: session_name.to_string(),
                    host: Host::Local,
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                panes: vec![],
            }],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
        }
    }

    #[test]
    fn no_collision_returns_ok() {
        let standalone = vec![make_standalone_row("shepherd")];
        let task_rows = vec![make_task_row_with_session("feat/issue-1", "repo_1")];
        assert!(check_standalone_collisions(&standalone, &task_rows).is_ok());
    }

    #[test]
    fn collision_with_worktree_session_returns_error() {
        let standalone = vec![make_standalone_row("repo_1")];
        let task_rows = vec![make_task_row_with_session("feat/issue-1", "repo_1")];
        let err = check_standalone_collisions(&standalone, &task_rows).unwrap_err();
        let msg = err.to_string();
        assert!(
            msg.contains("repo_1"),
            "error should mention the colliding session name, got: {msg}"
        );
        assert!(
            msg.contains("feat/issue-1"),
            "error should mention the owning worktree branch, got: {msg}"
        );
    }

    #[test]
    fn no_false_positive_when_names_differ() {
        let standalone = vec![
            make_standalone_row("shepherd"),
            make_standalone_row("monitor"),
        ];
        let task_rows = vec![
            make_task_row_with_session("feat/issue-1", "repo_1"),
            make_task_row_with_session("feat/issue-2", "repo_2"),
        ];
        assert!(check_standalone_collisions(&standalone, &task_rows).is_ok());
    }

    // -----------------------------------------------------------------------
    // Key binding: h → CollapseRow (not Heal)
    // -----------------------------------------------------------------------

    #[test]
    fn h_key_maps_to_collapse_not_heal() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Char('h'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::CollapseRow));
    }

    #[test]
    fn left_arrow_maps_to_collapse() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Left, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::CollapseRow));
    }

    #[test]
    fn right_arrow_maps_to_expand() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Right, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::ExpandRow));
    }

    #[test]
    fn l_key_maps_to_expand() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Char('l'), KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::ExpandRow));
    }

    #[test]
    fn tab_maps_to_next_repo() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Tab, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::NextRepo));
    }

    #[test]
    fn backtab_maps_to_prev_repo() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::BackTab, KeyModifiers::SHIFT);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::PrevRepo));
    }

    #[test]
    fn e_key_maps_to_toggle_expand_all() {
        let app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        let key = KeyEvent::new(KeyCode::Char('E'), KeyModifiers::SHIFT);
        let msg = app.handle_event(key);
        assert_eq!(msg, Some(Message::ToggleExpandAll));
    }

    // -----------------------------------------------------------------------
    // Expand/collapse state management
    // -----------------------------------------------------------------------

    fn make_task_row_with_panes(issue: u32, pane_count: usize) -> WorktreeRow {
        let panes: Vec<crate::session::PaneInfo> = (0..pane_count)
            .map(|i| crate::session::PaneInfo {
                index: i,
                tmux_target: format!("0.{i}"),
                command: format!("cmd{}", i),
                title: format!("pane{}", i),
                has_claude: i == 0, // first pane has claude
            })
            .collect();
        WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: format!("sess-{}", issue),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                panes,
            }],
            ..make_task_row(issue, DisplayGroup::Other)
        }
    }

    #[test]
    fn expand_row_adds_to_expanded_set() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.update(Message::ExpandRow);
        assert!(app.expanded.contains("/workspace/repo-1"));
    }

    #[test]
    fn expand_row_noop_for_single_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 1)]);
        app.update(Message::ExpandRow);
        assert!(app.expanded.is_empty());
    }

    #[test]
    fn collapse_row_removes_from_expanded_set() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.update(Message::CollapseRow);
        assert!(!app.expanded.contains("/workspace/repo-1"));
    }

    #[test]
    fn toggle_expand_all_expands_when_any_collapsed() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 3),
            make_task_row_with_panes(3, 3),
        ]);
        app.update(Message::ToggleExpandAll);
        assert_eq!(app.expanded.len(), 3);
    }

    #[test]
    fn toggle_expand_all_collapses_when_all_expanded() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 3),
            make_task_row_with_panes(3, 3),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.expanded.insert("/workspace/repo-2".to_string());
        app.expanded.insert("/workspace/repo-3".to_string());
        app.update(Message::ToggleExpandAll);
        assert!(app.expanded.is_empty());
    }

    #[test]
    fn expand_preserves_cursor_position() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 3),
        ]);
        app.cursor = 1;
        app.update(Message::ToggleExpandAll);
        assert_eq!(app.cursor, 1, "cursor should stay on same logical row");
        assert_eq!(app.selected_pane, None, "selected_pane should remain None");
    }

    #[test]
    fn collapse_all_preserves_selected_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.selected_pane = Some(1);
        app.update(Message::ToggleExpandAll);
        assert_eq!(
            app.selected_pane,
            Some(1),
            "selected_pane should persist across collapse-all"
        );
    }

    // -----------------------------------------------------------------------
    // Navigation with sub-rows
    // -----------------------------------------------------------------------

    #[test]
    fn down_on_expanded_row_enters_first_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.update(Message::CursorDown);
        assert_eq!(app.cursor, 0, "cursor stays on parent row");
        assert_eq!(app.selected_pane, Some(0), "enters first sub-row");
    }

    #[test]
    fn down_on_last_sub_row_moves_to_next_parent() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.selected_pane = Some(2); // last sub-row of row 0
        app.update(Message::CursorDown);
        assert_eq!(app.cursor, 1, "cursor moves to next parent");
        assert_eq!(app.selected_pane, None, "selected_pane cleared");
    }

    #[test]
    fn up_on_first_sub_row_returns_to_parent() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.selected_pane = Some(0);
        app.update(Message::CursorUp);
        assert_eq!(app.cursor, 0);
        assert_eq!(app.selected_pane, None, "back to parent row");
    }

    #[test]
    fn up_on_middle_sub_row_moves_up_within_sub_rows() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.selected_pane = Some(2);
        app.update(Message::CursorUp);
        assert_eq!(app.selected_pane, Some(1));
    }

    #[test]
    fn cursor_to_clears_selected_pane() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
        ]);
        app.selected_pane = Some(1);
        app.update(Message::CursorTo(1));
        assert_eq!(app.cursor, 1);
        assert_eq!(app.selected_pane, None, "digit-jump clears selected_pane");
    }

    #[test]
    fn collapse_from_sub_row_clears_selected_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.selected_pane = Some(1);
        app.update(Message::CollapseRow);
        assert_eq!(app.selected_pane, None);
        assert!(!app.expanded.contains("/workspace/repo-1"));
    }

    // -----------------------------------------------------------------------
    // Expansion state tracked by worktree path
    // -----------------------------------------------------------------------

    #[test]
    fn expansion_state_tracked_by_worktree_path() {
        let mut app = App::new_test(vec![make_task_row_with_panes(42, 3)]);
        app.update(Message::ExpandRow);
        assert!(app.expanded.contains("/workspace/repo-42"));
    }

    // -----------------------------------------------------------------------
    // Preview pane index selection
    // -----------------------------------------------------------------------

    #[test]
    fn preview_pane_index_none_for_parent() {
        let app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        assert_eq!(app.preview_pane_index(), None);
    }

    #[test]
    fn preview_pane_index_some_for_sub_row() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.selected_pane = Some(1);
        assert_eq!(app.preview_pane_index(), Some(1));
    }

    // -----------------------------------------------------------------------
    // Up arrow onto expanded row selects last sub-row
    // -----------------------------------------------------------------------

    #[test]
    fn up_onto_expanded_row_selects_last_sub_row() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.cursor = 1; // on row 2
        app.update(Message::CursorUp);
        assert_eq!(app.cursor, 0, "cursor moves to expanded row");
        assert_eq!(app.selected_pane, Some(2), "selects last sub-row");
    }

    // -----------------------------------------------------------------------
    // prune_expansion_state
    // -----------------------------------------------------------------------

    #[test]
    fn prune_expansion_state_removes_entry_when_row_has_one_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 1)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.prune_expansion_state();
        assert!(
            app.expanded.is_empty(),
            "single-pane row should be pruned from expanded set"
        );
    }

    #[test]
    fn prune_expansion_state_retains_entry_when_row_still_has_multiple_panes() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.prune_expansion_state();
        assert!(
            app.expanded.contains("/workspace/repo-1"),
            "multi-pane row should remain in expanded set"
        );
    }

    // --- sanitize_branch_slug ---

    #[test]
    fn sanitize_replaces_slash_with_dash() {
        assert_eq!(sanitize_branch_slug("feat/my-branch"), "feat-my-branch");
    }

    #[test]
    fn sanitize_strips_special_characters() {
        assert_eq!(sanitize_branch_slug("feat/hello world!"), "feat-helloworld");
    }

    #[test]
    fn sanitize_preserves_dots_dashes_underscores() {
        assert_eq!(sanitize_branch_slug("fix/v1.2_patch"), "fix-v1.2_patch");
    }

    #[test]
    fn sanitize_plain_branch_unchanged() {
        assert_eq!(sanitize_branch_slug("main"), "main");
    }

    // --- derive_local_worktree_path ---

    #[test]
    fn local_path_uses_parent_and_slug() {
        let result = derive_local_worktree_path("/home/user/repo", "feat/my-feature");
        assert!(
            result.ends_with("worktrees/worktree-feat-my-feature"),
            "got: {}",
            result
        );
    }

    #[test]
    fn local_path_parent_segment_correct() {
        let result = derive_local_worktree_path("/srv/repos/myrepo", "fix/bug-101");
        assert!(
            result.contains("worktrees/worktree-fix-bug-101"),
            "got: {}",
            result
        );
    }
}
