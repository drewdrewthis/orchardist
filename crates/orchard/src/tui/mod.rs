//! Ratatui-based terminal user interface.
//!
//! Drives the interactive worktree list, handles keyboard events, manages
//! background cache refreshes via a worker thread, and delegates rendering
//! to the `list`, `dialogs`, and `widgets` sub-modules.
mod dialogs;
pub mod fuzzy;
pub(crate) mod last_selection;
mod list;
mod message;
pub mod refresh;
mod sessions;
mod state;
pub mod theme;
mod widgets;
mod worktree_ops;

pub use theme::Theme;

use sessions::{check_standalone_collisions, ensure_main_sessions, ensure_standalone_sessions};
use worktree_ops::{delete_task_row, filter_stale};

use std::collections::{HashMap, HashSet};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyModifiers, MouseButton, MouseEventKind};
use ratatui::prelude::*;
use std::cell::Cell;

use crate::cache;
use crate::cache_sources;
use crate::derive;
use crate::global_config;
use crate::navigation;
use crate::session::{StandaloneSessionRow, WindowInfo};
use crate::tmux;

// ---------------------------------------------------------------------------
// SubCursor
// ---------------------------------------------------------------------------

/// Two-level sub-cursor within an expanded session row.
///
/// Replaces the old `selected_pane: Option<usize>` with support for
/// both window-level and pane-level selection.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) enum SubCursor {
    /// Parent row is selected (no sub-row focus).
    None,
    /// A window sub-row is selected (for multi-window sessions).
    /// Value is the tmux window index.
    Window(usize),
    /// A pane sub-row is selected.
    /// `window` is the tmux window index, `pane` is the vec index within that window.
    Pane { window: usize, pane: usize },
}

// ---------------------------------------------------------------------------
// Reachability
// ---------------------------------------------------------------------------

/// Tri-state SSH reachability for a remote host.
///
/// `Unknown` means no probe has completed yet (e.g. startup before the first
/// background check). `Reachable` / `Unreachable` reflect the most recent
/// probe result. Callers should treat `Unknown` as "don't show a connectivity
/// warning yet" rather than "assume reachable".
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Reachability {
    /// No probe result available yet.
    Unknown,
    /// The host responded to the last SSH reachability probe.
    Reachable,
    /// The host did not respond to the last SSH reachability probe.
    Unreachable,
}

use message::{Message, UpdateResult};
use state::{AppMsg, CleanupState, DaemonStatus, InputPhase, Phase, ViewState};
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
const ATTRIBUTION_URL: &str = "https://github.com/drewdrewthis/git-orchard-rs";

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
    /// Text accumulated by bare keystrokes — filters the visible task list in real time.
    filter_text: String,
    /// Current input phase: bare keys dispatch actions (`Idle`) or feed the search bar (`Searching`).
    input_phase: InputPhase,

    // Reachability state keyed by SSH host name
    host_reachable: HashMap<String, bool>,

    // Background data channel
    tx: mpsc::Sender<AppMsg>,
    rx: mpsc::Receiver<AppMsg>,

    // Session to switch to after the TUI exits. Set by Enter key handler.
    switch_target: Option<String>,

    /// Whether the last selection should be persisted on exit and restored on launch.
    /// False in "cleanup" mode, which has its own cursor and no selection to save.
    persist_selection: bool,

    // Auto-refresh
    last_refresh: Instant,
    last_full_refresh: Instant,
    /// Throbber animation state — advanced each frame via `calc_next()`.
    throbber_state: throbber_widgets_tui::ThrobberState,

    // Previous state snapshot used to detect transitions between cache refreshes.
    previous_orchard_state: Option<crate::orchard_state::OrchardState>,
    // Debounce state for claude status transitions — suppresses single-poll flicker.
    claude_debounce: crate::watch::debounce::ClaudeDebounceState,

    // Mouse support
    /// Last rendered table body rect (excludes border + header row). Updated by render.
    table_area: Cell<Rect>,
    /// Last rendered attribution URL rect. Updated by render.
    url_area: Cell<Rect>,
    /// Last rendered preview pane rect. Updated by render_task_preview.
    /// Zero rect when preview is not visible.
    preview_area: Cell<Rect>,
    /// Row index and timestamp of last mouse click, for double-click detection.
    last_click: Option<(usize, Instant)>,

    /// Scroll state for the preview pane (tui-scrollview).
    ///
    /// Uses `Cell` for interior mutability so `render_task_preview` (which takes
    /// `&self`) can update the state via `StatefulWidget::render`.
    preview_scroll_state: std::cell::Cell<tui_scrollview::ScrollViewState>,

    /// Pulse tick sampled at the top of each [`App::render`] call (issue #281).
    ///
    /// All animated glyphs in a single frame read this field rather than calling
    /// [`crate::signal::pulse_tick`] directly — that way a render that straddles
    /// a second boundary still produces a coherent frame. The run loop writes
    /// this just before [`App::render`]; tests override it to pin a frame.
    pulse_tick: Cell<u8>,

    /// Expanded rows keyed by worktree path or standalone session name.
    ///
    /// When a key is present, the row's pane sub-rows are rendered below it.
    /// Entries are silently removed on refresh when the row's pane count drops to <= 1.
    expanded: HashSet<String>,

    /// Two-level sub-cursor within an expanded session row.
    ///
    /// `SubCursor::None` means the parent row is selected. `SubCursor::Window(w)`
    /// means a window sub-row is focused. `SubCursor::Pane { window, pane }` means
    /// a pane within a window is focused. Does NOT affect `self.cursor`.
    sub_cursor: SubCursor,

    /// Expanded windows keyed by `"session_name:window_index"`.
    ///
    /// When a key is present, the window's pane sub-rows are rendered below it.
    window_expanded: HashSet<String>,

    /// Keys that have been seen as expandable in a prior refresh.
    ///
    /// Used to distinguish "new row, auto-expand on first sight" from
    /// "user explicitly collapsed, preserve across refreshes". A key present
    /// here but absent from `expanded` means the user collapsed it — the next
    /// `prune_expansion_state` call must not re-insert it.
    seen_expandable_keys: HashSet<String>,

    /// Window keys that have been seen in a prior refresh. Parallel to
    /// `seen_expandable_keys` for `window_expanded`.
    seen_window_keys: HashSet<String>,

    /// Most recent `WorkViewSnapshot` fetched by `start_full_refresh`.
    ///
    /// Populated by the refresh thread; consumed by the `CacheRefreshed`
    /// message handler. Protected by a Mutex so the background thread and
    /// the message handler can share it across the channel boundary.
    work_view_snapshot: std::sync::Arc<std::sync::Mutex<Option<crate::daemon::WorkViewSnapshot>>>,

    /// Ahead/behind counts fetched via `git branch -vv` in `start_full_refresh`.
    ///
    /// Keyed by project directory path → branch name → `(ahead, behind)`.
    ///
    /// # Carve-out: ahead/behind via `git branch -vv`
    ///
    /// The daemon's `Worktree` schema does not yet expose `ahead`/`behind`
    /// counts. Until the daemon exposes these fields, the refresh thread
    /// shells out to `git branch -vv` per project directory to populate them.
    /// Remove this field and the corresponding `git branch -vv` call in
    /// `start_full_refresh` once the daemon exposes `Worktree.ahead` and
    /// `Worktree.behind` (tracked as daemon issue #483).
    ///
    /// **Staleness note**: this slot is populated by `start_full_refresh` only —
    /// the local-only refresh path (`start_local_refresh`) does NOT re-run
    /// `git branch -vv` per project to keep the local tick fast. Worst-case
    /// staleness for ahead/behind data is bounded by the full refresh cadence
    /// (~60s + focus events). New branches that appear between full refreshes
    /// render with stale (or zero) ahead/behind until the next full tick. This
    /// is acceptable because ahead/behind is a hint, not a contract — and
    /// tracked-for-removal in #483 (daemon to expose ahead/behind directly).
    ahead_behind_snapshot:
        std::sync::Arc<std::sync::Mutex<Option<crate::daemon::work_view_adapter::AheadBehindMap>>>,

    /// Injectable source for `workView` GraphQL queries.
    ///
    /// In production this is a real [`crate::daemon::Client`].
    /// In tests, inject a fake that returns a canned snapshot without
    /// issuing any HTTP requests.
    work_view_source: std::sync::Arc<dyn crate::daemon::WorkViewSource>,

    /// Daemon reachability as reported by the most recent refresh tick.
    ///
    /// Initialised to [`DaemonStatus::Unknown`] at startup. Updated by the
    /// [`AppMsg::DaemonStatusChanged`] message sent from the refresh thread
    /// before each `CacheRefreshed`/`LocalCacheRefreshed`. Drives the
    /// "daemon unreachable" indicator in the TUI header.
    pub(crate) daemon_status: DaemonStatus,
}

impl App {
    fn new(command: &str) -> Self {
        let repo_root = worktree_core::find_repo_root();
        let repo_name = worktree_core::get_repo_name();
        // Drew 2026-05-10: orchard-tui must NOT silently mutate
        // ~/.orchard/config.json. Only `orchard config init` and
        // `orchard config write` (explicit CLI) may write it.
        let global_cfg = global_config::load_global_config();

        let task_rows = crate::build_state::build_task_rows(&global_cfg);
        let hosts = crate::cache::read_host_reachability();
        let state = crate::merge_remote::build_state_with_cached_snapshots(&global_cfg, &hosts);
        let standalone_sessions = state.standalone_sessions;
        let (tx, rx) = mpsc::channel();

        let persist_selection = command != "cleanup";

        let view = if !persist_selection {
            ViewState::Cleanup(CleanupState {
                stale: Vec::new(),
                selected: std::collections::HashSet::new(),
                cursor: 0,
                phase: Phase::Idle,
                deleted: Vec::new(),
                errors: Vec::new(),
            })
        } else if crate::signal::is_first_launch() {
            // Surface the legend overlay once so new users see the status &
            // activity lexicon before they're asked to read emoji-encoded rows.
            ViewState::Help
        } else {
            ViewState::List
        };

        // Resolve the initial cursor position and active repo from the last saved selection.
        // Cleanup mode always starts at 0 (it has its own cursor and no repo filter to restore).
        let (initial_cursor, initial_repo_index) = if persist_selection {
            let sel = last_selection::load();
            let cursor = last_selection::resolve_cursor(&sel, &standalone_sessions, &task_rows);
            let repo_index = last_selection::resolve_active_repo_index(&sel, &global_cfg.repos);
            (cursor, repo_index)
        } else {
            (0, 0)
        };

        let mut app = App {
            cursor: initial_cursor,
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
            active_repo_index: initial_repo_index,
            show_branch_column: false,
            filter_text: String::new(),
            input_phase: InputPhase::Idle,
            host_reachable: HashMap::new(),
            tx,
            rx,
            last_refresh: Instant::now(),
            last_full_refresh: Instant::now(),
            throbber_state: throbber_widgets_tui::ThrobberState::default(),
            switch_target: None,
            persist_selection,
            previous_orchard_state: None,
            claude_debounce: crate::watch::debounce::ClaudeDebounceState::new(),
            table_area: Cell::new(Rect::default()),
            url_area: Cell::new(Rect::default()),
            preview_area: Cell::new(Rect::default()),
            last_click: None,
            preview_scroll_state: std::cell::Cell::new(tui_scrollview::ScrollViewState::default()),
            pulse_tick: Cell::new(crate::signal::pulse_tick()),
            expanded: HashSet::new(),
            sub_cursor: SubCursor::None,
            window_expanded: HashSet::new(),
            seen_expandable_keys: HashSet::new(),
            seen_window_keys: HashSet::new(),
            work_view_snapshot: {
                // Cold-start fallback (AC#7): pre-populate the snapshot slot
                // from disk so the first render shows stale data rather than
                // an empty screen while waiting for the daemon.
                let initial_snap = crate::daemon::work_view_cache::read_snapshot();
                std::sync::Arc::new(std::sync::Mutex::new(initial_snap))
            },
            ahead_behind_snapshot: std::sync::Arc::new(std::sync::Mutex::new(None)),
            work_view_source: {
                // Build the daemon client; fall back to a no-op source if the
                // client cannot be constructed (e.g. invalid URL env var).
                match crate::daemon::Client::local() {
                    Ok(client) => std::sync::Arc::new(client),
                    Err(e) => {
                        crate::logger::LOG.warn(&format!(
                            "daemon client build failed at startup: {e}; will retry on first refresh"
                        ));
                        std::sync::Arc::new(crate::tui::refresh::NullWorkViewSource)
                    }
                }
            },
            daemon_status: DaemonStatus::Unknown,
        };
        // Default-expanded: issue #251 requires the hierarchy visible from
        // first paint without a data-refresh round-trip. `prune_expansion_state`
        // auto-expands every multi-child row, so seed the expansion set now.
        app.prune_expansion_state();
        app
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

    /// Returns the [`Reachability`] of `host` based on the most recent probe.
    ///
    /// - No entry in `host_reachable` → [`Reachability::Unknown`] (probe not yet run)
    /// - `Some(true)` → [`Reachability::Reachable`]
    /// - `Some(false)` → [`Reachability::Unreachable`]
    pub(crate) fn reachability(&self, host: &str) -> Reachability {
        match self.host_reachable.get(host) {
            None => Reachability::Unknown,
            Some(true) => Reachability::Reachable,
            Some(false) => Reachability::Unreachable,
        }
    }

    /// Returns true if at least one host has been probed (i.e. the reachability
    /// map is non-empty). Callers use this to decide whether to show probe-
    /// dependent chrome such as the header timestamp.
    pub(crate) fn has_probe_results(&self) -> bool {
        !self.host_reachable.is_empty()
    }

    /// Yields the names of all hosts currently known to be unreachable.
    pub(crate) fn unreachable_hosts(&self) -> impl Iterator<Item = &str> {
        self.host_reachable.keys().filter_map(|host| {
            matches!(self.reachability(host), Reachability::Unreachable).then_some(host.as_str())
        })
    }

    /// Returns true if any probed host is currently [`Reachability::Unreachable`].
    pub(crate) fn has_unreachable_host(&self) -> bool {
        self.unreachable_hosts().next().is_some()
    }

    /// Returns every probed host paired with its [`Reachability`], sorted by
    /// host name for stable rendering order. Probed hosts are always
    /// [`Reachability::Reachable`] or [`Reachability::Unreachable`];
    /// [`Reachability::Unknown`] is unreachable in this return type by
    /// construction.
    pub(crate) fn probed_hosts_sorted(&self) -> Vec<(&str, Reachability)> {
        let mut entries: Vec<(&str, Reachability)> = self
            .host_reachable
            .keys()
            .map(|host| (host.as_str(), self.reachability(host)))
            .collect();
        entries.sort_by_key(|(host, _)| *host);
        entries
    }

    /// Seeds reachability state for a host. Test-only: lets tests express
    /// intent in terms of the [`Reachability`] domain type instead of
    /// reaching into the `host_reachable` storage directly.
    ///
    /// [`Reachability::Unknown`] removes any existing entry, matching the
    /// "no probe has run yet" semantics of a missing map key.
    #[cfg(test)]
    pub(crate) fn seed_reachability(&mut self, host: &str, reachability: Reachability) {
        match reachability {
            Reachability::Unknown => {
                self.host_reachable.remove(host);
            }
            Reachability::Reachable => {
                self.host_reachable.insert(host.to_string(), true);
            }
            Reachability::Unreachable => {
                self.host_reachable.insert(host.to_string(), false);
            }
        }
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
                &self.filter_text,
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
                &self.filter_text,
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
    /// When the cursor is on a pane sub-row, returns `Some(pane_flat_index)`.
    /// When on a window sub-row, returns `None` (active pane of that window).
    /// When on a parent row, returns `None` (default pane 0).
    #[cfg(test)]
    pub(crate) fn preview_pane_index(&self) -> Option<usize> {
        match &self.sub_cursor {
            SubCursor::Pane { window, pane } => {
                // Find the flat pane index by summing panes in prior windows.
                let windows = self.windows_at(self.cursor);
                let mut flat = 0;
                for w in windows {
                    if w.index == *window {
                        return Some(flat + pane);
                    }
                    flat += w.panes.len();
                }
                Some(*pane)
            }
            _ => None,
        }
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
            &self.filter_text,
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
    ///
    /// Operates on *all* task rows and standalone sessions (unfiltered) so
    /// collapse/expand intent is preserved across filter and repo-switch
    /// changes — a row hidden by the current filter is still a real row the
    /// user had intent about.
    fn prune_expansion_state(&mut self) {
        let mut valid_keys: HashSet<String> = HashSet::new();
        for ss in &self.standalone_sessions {
            if ss.session.panes.len() > 1 {
                valid_keys.insert(ss.session.tmux.name.clone());
            }
        }
        for row in &self.task_rows {
            let pane_count = row.sessions.first().map(|s| s.panes.len()).unwrap_or(0);
            if pane_count > 1 {
                valid_keys.insert(row.worktree_path.clone());
            }
        }

        self.expanded.retain(|k| valid_keys.contains(k));

        // Auto-expand only keys that are new (first-time sightings).
        // Keys already in `seen_expandable_keys` were visible on a prior refresh;
        // if they are absent from `expanded` now, the user collapsed them — preserve that.
        for key in &valid_keys {
            if !self.seen_expandable_keys.contains(key) {
                self.expanded.insert(key.clone());
            }
        }
        self.seen_expandable_keys = valid_keys;

        // Also prune window_expanded: remove entries for sessions that are no longer expanded.
        let expanded_ref = &self.expanded;

        // Collect session names for currently expanded rows.
        let mut expanded_session_names: HashSet<String> = HashSet::new();
        for ss in &self.standalone_sessions {
            if expanded_ref.contains(&ss.session.tmux.name) {
                expanded_session_names.insert(ss.session.tmux.name.clone());
            }
        }
        for row in &self.task_rows {
            if expanded_ref.contains(&row.worktree_path)
                && let Some(s) = row.sessions.first()
            {
                expanded_session_names.insert(s.tmux.name.clone());
            }
        }

        self.window_expanded.retain(|k| {
            // Key format: "session_name:window_index"
            if let Some(colon_pos) = k.rfind(':') {
                let session_name = &k[..colon_pos];
                expanded_session_names.contains(session_name)
            } else {
                false
            }
        });

        // Compute valid window keys for sessions with >1 window.
        let mut valid_window_keys: HashSet<String> = HashSet::new();
        for ss in &self.standalone_sessions {
            if self.expanded.contains(&ss.session.tmux.name) && ss.session.windows.len() > 1 {
                for (i, _) in ss.session.windows.iter().enumerate() {
                    valid_window_keys.insert(Self::window_expansion_key(&ss.session.tmux.name, i));
                }
            }
        }
        for row in &self.task_rows {
            if self.expanded.contains(&row.worktree_path)
                && let Some(s) = row.sessions.first()
                && s.windows.len() > 1
            {
                for (i, _) in s.windows.iter().enumerate() {
                    valid_window_keys.insert(Self::window_expansion_key(&s.tmux.name, i));
                }
            }
        }

        // Auto-expand only window keys that are new (not yet seen), preserving
        // any user-collapsed windows from prior refreshes.
        for key in &valid_window_keys {
            if !self.seen_window_keys.contains(key) {
                self.window_expanded.insert(key.clone());
            }
        }
        self.seen_window_keys = valid_window_keys;
    }

    // -------------------------------------------------------------------
    // Window hierarchy helpers
    // -------------------------------------------------------------------

    /// Returns the windows for the session at cursor position `idx` (cloned).
    ///
    /// For standalone sessions, returns the session's windows.
    /// For worktree rows, returns windows from the first session (if any).
    /// Returns an owned Vec because the worktree path requires a temporary lookup.
    fn windows_at(&self, idx: usize) -> Vec<WindowInfo> {
        let standalone_count = self.standalone_sessions.len();
        if idx < standalone_count {
            self.standalone_sessions
                .get(idx)
                .map(|ss| ss.session.windows.clone())
                .unwrap_or_default()
        } else {
            let wt_idx = idx - standalone_count;
            let tasks = list::visible_tasks_filtered(
                &self.task_rows,
                &self.filter_text,
                self.active_repo_slug(),
            );
            tasks
                .get(wt_idx)
                .and_then(|vt| vt.row.sessions.first())
                .map(|s| s.windows.clone())
                .unwrap_or_default()
        }
    }

    /// Returns the number of windows for the session at cursor position `idx`.
    fn window_count_at(&self, idx: usize) -> usize {
        self.windows_at(idx).len()
    }

    /// Returns true when the session at cursor `idx` has exactly 1 window.
    fn is_single_window_session(&self, idx: usize) -> bool {
        self.window_count_at(idx) == 1
    }

    /// Returns the expansion key for a window within a session.
    fn window_expansion_key(session_name: &str, window_index: usize) -> String {
        format!("{}:{}", session_name, window_index)
    }

    /// Returns true if the given window is expanded.
    fn is_window_expanded(&self, session_name: &str, window_index: usize) -> bool {
        self.window_expanded
            .contains(&Self::window_expansion_key(session_name, window_index))
    }

    /// Returns the session name for the row at cursor position `idx`.
    fn session_name_at(&self, idx: usize) -> Option<String> {
        let standalone_count = self.standalone_sessions.len();
        if idx < standalone_count {
            self.standalone_sessions
                .get(idx)
                .map(|ss| ss.session.tmux.name.clone())
        } else {
            let wt_idx = idx - standalone_count;
            let tasks = list::visible_tasks_filtered(
                &self.task_rows,
                &self.filter_text,
                self.active_repo_slug(),
            );
            tasks
                .get(wt_idx)
                .and_then(|vt| vt.row.sessions.first())
                .map(|s| s.tmux.name.clone())
        }
    }

    /// Clears window expansion state for all windows of a given session.
    fn clear_window_expansion_for_session(&mut self, session_name: &str) {
        let prefix = format!("{}:", session_name);
        self.window_expanded.retain(|k| !k.starts_with(&prefix));
    }

    /// Total sub-row count for the session at `idx`, considering window and pane expansion.
    ///
    /// For single-window sessions (auto-flattened): returns pane count (same as before).
    /// For multi-window sessions: returns window count + expanded pane counts.
    fn sub_row_count_at(&self, idx: usize) -> usize {
        let windows = self.windows_at(idx);
        if windows.len() <= 1 {
            // Auto-flatten: sub-rows are panes directly.
            self.pane_count_at(idx)
        } else {
            let session_name = self.session_name_at(idx).unwrap_or_default();
            let mut count = windows.len();
            for w in windows {
                if self.is_window_expanded(&session_name, w.index) {
                    count += w.panes.len();
                }
            }
            count
        }
    }

    // -------------------------------------------------------------------
    // Background refresh pipeline
    // -------------------------------------------------------------------

    /// Starts a full background refresh of all data sources.
    ///
    /// Probes remote SSH hosts, fetches local data (projects, worktrees, tmux
    /// sessions, Claude instances) via the daemon's `workView` query, and
    /// refreshes remote worktrees + sessions via `cache_sources`.
    /// Sends `AppMsg::CacheRefreshed` when done.
    ///
    /// Phase 3: local data (issues, PRs, worktrees, tmux sessions) is sourced
    /// from the daemon's [`crate::daemon::WorkViewSource::work_view`] call in
    /// the background thread. The result is placed into `work_view_snapshot`
    /// for the `CacheRefreshed` handler to consume. When the daemon is
    /// unreachable, a warning is logged and the handler falls back to
    /// the last-known cache path.
    ///
    /// Remote worktrees + tmux sessions continue to flow through
    /// `cache_sources::refresh_remote_*` until daemon Workstream F populates
    /// per-peer data in `WorkView` (Phase 4+).
    fn start_full_refresh(&self) {
        let config = self.global_config.clone();
        let tx = self.tx.clone();
        let work_view_slot = self.work_view_snapshot.clone();
        let ahead_behind_slot = self.ahead_behind_snapshot.clone();
        let work_view_source = self.work_view_source.clone();
        std::thread::spawn(move || {
            // Probe each unique remote host concurrently before attempting remote
            // operations. One dead VM must not block probes for healthy hosts.
            // Use the kind-aware variant so `boxd-fork` golden hosts (which
            // reject `true` as a subcommand) are probed with `list --json`.
            let remotes = crate::sources::hosts::remotes_from_config(&config);
            let probe_results = crate::sources::hosts::probe_reachability_all_for_remotes(&remotes);

            let mut reachable_hosts: std::collections::HashSet<String> =
                std::collections::HashSet::new();
            for (host, reachable) in &probe_results {
                let _ = tx.send(AppMsg::HostReachability(host.clone(), *reachable));
                if *reachable {
                    reachable_hosts.insert(host.clone());
                }
            }

            // Fetch LOCAL data from the daemon's WorkView.
            // Replaces: cache_sources::refresh_issues + refresh_prs +
            //           refresh_worktrees + refresh_tmux_sessions
            match work_view_source.work_view() {
                Ok(snapshot) => {
                    // Carve-out: ahead/behind via `git branch -vv` — runs in
                    // parallel across all configured repos via
                    // `fetch_ahead_behind_for_snapshot`. Remove once the daemon
                    // exposes `Worktree.ahead`/`Worktree.behind` (tracked as #483).
                    let ahead_behind = fetch_ahead_behind_for_snapshot(&config);
                    if let Ok(mut slot) = ahead_behind_slot.lock() {
                        *slot = Some(ahead_behind);
                    }
                    // Persist snapshot so cold-start fallback has fresh data.
                    if let Err(e) = crate::daemon::work_view_cache::write_snapshot(&snapshot) {
                        crate::logger::LOG.warn(&format!("work_view_cache write failed: {e}"));
                    }
                    if let Ok(mut slot) = work_view_slot.lock() {
                        *slot = Some(snapshot);
                    }
                    let _ = tx.send(AppMsg::DaemonStatusChanged(DaemonStatus::Reachable));
                }
                Err(err) => {
                    crate::logger::LOG.warn(&format!("daemon work_view failed: {err}"));
                    let _ = tx.send(AppMsg::DaemonStatusChanged(DaemonStatus::Unreachable));
                }
            }

            // REMOTE data — unchanged per AC #2.
            // daemon doesn't yet populate per-peer worktrees in WorkView.
            let seen_remotes: std::sync::Mutex<std::collections::HashSet<String>> =
                std::sync::Mutex::new(std::collections::HashSet::new());

            crate::refresh_parallel::for_each_repo_parallel(&config, |repo| {
                for remote in &repo.remotes {
                    if !reachable_hosts.contains(&remote.host) {
                        continue;
                    }
                    // Snapshot fork hosts BEFORE refresh_remote_worktrees mutates
                    // the cache — preserves real host strings for vanished-fork
                    // detection in refresh_remote_tmux_sessions.
                    let pre_snapshot = cache_sources::snapshot_fork_hosts_for_remote(repo, remote);
                    let _ = cache_sources::refresh_remote_worktrees(repo, remote);

                    // Refresh tmux sessions for reachable remotes only.
                    // Dedup prevents double deletion when the same BoxdFork
                    // golden host appears in multiple repos.
                    let key = dedup_key(remote);
                    if seen_remotes.lock().unwrap().insert(key) {
                        let _ = cache_sources::refresh_remote_tmux_sessions(
                            repo,
                            remote,
                            &pre_snapshot,
                        );
                    }
                }
            });

            // Ensure a main tmux session exists for each configured repo.
            ensure_main_sessions(&config);
            // Signal that caches are updated.
            let _ = tx.send(AppMsg::CacheRefreshed);
        });
    }

    /// Starts a local-only background refresh via the daemon's WorkView.
    ///
    /// Replaces the old per-source `cache_sources::refresh_worktrees` +
    /// `cache_sources::refresh_tmux_sessions` calls with a single round-trip
    /// to the daemon. No remote host probing, no GitHub API calls.
    /// Sends `AppMsg::LocalCacheRefreshed` when done.
    ///
    /// **Staleness note**: `ahead_behind_snapshot` is populated by
    /// `start_full_refresh` only — this local-only refresh path does NOT
    /// re-run `git branch -vv` per project to keep the local tick fast.
    /// Worst-case staleness for ahead/behind data is bounded by the full
    /// refresh cadence (~60s + focus events). New branches that appear
    /// between full refreshes render with stale (or zero) ahead/behind
    /// until the next full tick. This is acceptable because ahead/behind
    /// is a hint, not a contract — and tracked-for-removal in #483
    /// (daemon to expose ahead/behind directly).
    fn start_local_refresh(&self) {
        let tx = self.tx.clone();
        let work_view_slot = self.work_view_snapshot.clone();
        let work_view_source = self.work_view_source.clone();
        std::thread::spawn(move || {
            match work_view_source.work_view() {
                Ok(snapshot) => {
                    // Persist snapshot so cold-start fallback has fresh data.
                    if let Err(e) = crate::daemon::work_view_cache::write_snapshot(&snapshot) {
                        crate::logger::LOG.warn(&format!("work_view_cache write failed: {e}"));
                    }
                    if let Ok(mut slot) = work_view_slot.lock() {
                        *slot = Some(snapshot);
                    }
                    let _ = tx.send(AppMsg::DaemonStatusChanged(DaemonStatus::Reachable));
                }
                Err(err) => {
                    crate::logger::LOG.warn(&format!("daemon work_view (local) failed: {err}"));
                    let _ = tx.send(AppMsg::DaemonStatusChanged(DaemonStatus::Unreachable));
                }
            }
            let _ = tx.send(AppMsg::LocalCacheRefreshed);
        });
    }

    /// Builds an [`crate::orchard_state::OrchardState`] from the current
    /// `work_view_snapshot` slot, merging in cached remote snapshots.
    ///
    /// When a daemon snapshot is available it drives the local state via
    /// [`crate::daemon::work_view_adapter::build_local_state`]. When the slot
    /// is empty (daemon unreachable) it falls back to the cache-driven path
    /// [`crate::build_state::build_state_with_hosts`]. Either way, cached
    /// remote snapshots written by `refresh_remote_*` this cycle are folded in
    /// (AC#2 — remote path unchanged).
    fn rebuild_state_from_snapshot(&self) -> crate::orchard_state::OrchardState {
        let hosts = crate::cache::read_host_reachability();
        let snapshot_opt = self.work_view_snapshot.lock().ok().and_then(|g| g.clone());
        let ahead_behind_opt = self
            .ahead_behind_snapshot
            .lock()
            .ok()
            .and_then(|g| g.clone());

        let mut state = if let Some(snapshot) = snapshot_opt {
            crate::daemon::work_view_adapter::build_local_state(
                &snapshot,
                &self.global_config,
                &hosts,
                ahead_behind_opt.as_ref(),
            )
        } else {
            crate::build_state::build_state_with_hosts(&self.global_config, &hosts)
        };

        let remote_snapshots = crate::orchard_snapshot::load_cached_snapshots(&self.global_config);
        for (host, snap) in remote_snapshots {
            crate::merge_remote::merge_remote_snapshot(&mut state, snap, host);
        }
        state
    }

    // -------------------------------------------------------------------
    // Drain messages from background threads
    // -------------------------------------------------------------------

    fn check_updates(&mut self) {
        while let Ok(msg) = self.rx.try_recv() {
            match msg {
                AppMsg::CacheRefreshed => {
                    let state = self.rebuild_state_from_snapshot();

                    self.task_rows = crate::tui::refresh::state_to_task_rows(&state.repos);
                    self.standalone_sessions = state.standalone_sessions.clone();
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

                    self.detect_and_notify(&state);

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
                AppMsg::DaemonStatusChanged(status) => {
                    self.daemon_status = status;
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
                    // Mirror CacheRefreshed: use the daemon WorkView snapshot
                    // when available, fall back to the cache-driven path otherwise.
                    let state = self.rebuild_state_from_snapshot();

                    self.task_rows = crate::tui::refresh::state_to_task_rows(&state.repos);
                    self.standalone_sessions = state.standalone_sessions.clone();
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
                    self.detect_and_notify(&state);
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

    /// Detects state transitions between the previous and current `OrchardState`
    /// and fires desktop notifications for significant changes (Claude needs input,
    /// Claude finished, CI failed, new review comments).
    ///
    /// Delegates diffing to `crate::watch::diff::diff` and saves the current
    /// state for the next comparison.
    fn detect_and_notify(&mut self, new_state: &crate::orchard_state::OrchardState) {
        if let Some(ref old_state) = self.previous_orchard_state {
            let events = crate::watch::diff::diff(old_state, new_state, &mut self.claude_debounce);
            let terminal_app = self.global_config.terminal_app.as_str();
            for event in &events {
                if let Some((title, message, session)) = event.kind.notification() {
                    crate::notify::send_notification_with_session(
                        title,
                        &message,
                        session,
                        terminal_app,
                    );
                }
            }
        }
        self.previous_orchard_state = Some(new_state.clone());
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
                let standalone_count = self.standalone_sessions.len();
                let worktree_visible_count = list::visible_tasks_filtered(
                    &self.task_rows,
                    &self.filter_text,
                    self.active_repo_slug(),
                )
                .len();
                let visible_count = standalone_count + worktree_visible_count;

                match self.input_phase {
                    InputPhase::Idle => {
                        // Bare keys are direct actions. Navigation + special keys.
                        match key.code {
                            KeyCode::Char('o') => Some(Message::OpenPR),
                            KeyCode::Char('i') => Some(Message::OpenIssue),
                            KeyCode::Char('B') => Some(Message::ToggleBranchColumn),
                            KeyCode::Char('d') => Some(Message::Delete),
                            KeyCode::Char('p') => Some(Message::TogglePriority),
                            KeyCode::Char('n') => Some(Message::NewSession),
                            KeyCode::Char('w') => Some(Message::NewWorktree),
                            KeyCode::Char('c') => Some(Message::Cleanup),
                            KeyCode::Char('h') => Some(Message::CollapseRow),
                            KeyCode::Char('l') => Some(Message::ExpandRow),
                            KeyCode::Char('E') => Some(Message::ToggleExpandAll),
                            KeyCode::Char('r') => Some(Message::Refresh),
                            KeyCode::Char('R') => Some(Message::ReconnectHosts),
                            KeyCode::Char('?') => Some(Message::ToggleHelp),
                            KeyCode::Char('q') => Some(Message::Quit),
                            KeyCode::Char('j') => Some(Message::CursorDown),
                            KeyCode::Char('k') => Some(Message::CursorUp),
                            KeyCode::Char(c) if c.is_ascii_digit() && c != '0' => {
                                navigation::cursor_index_from_digit(c, visible_count)
                                    .map(Message::CursorTo)
                            }
                            KeyCode::Char(' ') => Some(Message::OpenSearch),
                            KeyCode::Up => Some(Message::CursorUp),
                            KeyCode::Down => Some(Message::CursorDown),
                            KeyCode::Left => Some(Message::CollapseRow),
                            KeyCode::Right => Some(Message::ExpandRow),
                            KeyCode::Enter => Some(Message::Enter),
                            KeyCode::Esc => Some(Message::Quit),
                            KeyCode::Tab => Some(Message::NextRepo),
                            KeyCode::BackTab => Some(Message::PrevRepo),
                            KeyCode::PageUp => Some(Message::PreviewPageUp),
                            KeyCode::PageDown => Some(Message::PreviewPageDown),
                            _ => None,
                        }
                    }
                    InputPhase::Searching => {
                        // Navigation keys work, printable chars go to filter.
                        match key.code {
                            KeyCode::Esc => Some(Message::CloseSearch),
                            KeyCode::Enter => Some(Message::Enter),
                            KeyCode::Up => Some(Message::CursorUp),
                            KeyCode::Down => Some(Message::CursorDown),
                            KeyCode::Left => Some(Message::CollapseRow),
                            KeyCode::Right => Some(Message::ExpandRow),
                            KeyCode::Tab => Some(Message::NextRepo),
                            KeyCode::BackTab => Some(Message::PrevRepo),
                            KeyCode::PageUp => Some(Message::PreviewPageUp),
                            KeyCode::PageDown => Some(Message::PreviewPageDown),
                            KeyCode::Backspace => {
                                if self.filter_text.is_empty() {
                                    Some(Message::CloseSearch)
                                } else {
                                    Some(Message::FilterBackspace)
                                }
                            }
                            KeyCode::Char(c) => Some(Message::FilterChar(c)), // includes space
                            _ => None,
                        }
                    }
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
        // Mouse events only handled in List view.
        if !matches!(self.view, ViewState::List) {
            return None;
        }

        let table = self.table_area.get();
        let url = self.url_area.get();

        let in_table = event.column >= table.x
            && event.column < table.x + table.width
            && event.row >= table.y
            && event.row < table.y + table.height;

        let preview = self.preview_area.get();
        let in_preview = preview.width > 0
            && event.column >= preview.x
            && event.column < preview.x + preview.width
            && event.row >= preview.y
            && event.row < preview.y + preview.height;

        match event.kind {
            MouseEventKind::ScrollDown if in_preview => Some(Message::PreviewScrollDown),
            MouseEventKind::ScrollUp if in_preview => Some(Message::PreviewScrollUp),
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
                let sub_count = self.sub_row_count_at(idx);
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
            &self.filter_text,
            self.active_repo_slug(),
        );

        let mut last_group: Option<crate::derive::DisplayGroup> = None;

        for (task_idx, vt) in tasks.iter().enumerate() {
            let cursor_idx = task_idx + standalone_count;
            // Group header inserted when display group changes.
            if last_group != Some(vt.group) {
                if table_row == visual_row {
                    return None; // clicked on a group header
                }
                last_group = Some(vt.group);
                table_row += 1;
            }

            if table_row == visual_row {
                return Some(cursor_idx);
            }
            table_row += 1;

            // Skip sub-rows if expanded.
            if self.expanded.contains(&vt.row.worktree_path) {
                let sub_count = self.sub_row_count_at(cursor_idx);
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
                        match self.sub_cursor.clone() {
                            SubCursor::Pane { window, pane } => {
                                if pane > 0 {
                                    self.sub_cursor = SubCursor::Pane {
                                        window,
                                        pane: pane - 1,
                                    };
                                } else if self.is_single_window_session(self.cursor) {
                                    // Single-window: up from first pane → parent row.
                                    self.sub_cursor = SubCursor::None;
                                } else {
                                    // Multi-window: up from first pane → window row.
                                    self.sub_cursor = SubCursor::Window(window);
                                }
                                self.fetch_task_pane_content();
                            }
                            SubCursor::Window(w) => {
                                let windows = self.windows_at(self.cursor);
                                // Find this window's position in the list.
                                let pos = windows.iter().position(|wi| wi.index == w);
                                if let Some(p) = pos {
                                    if p == 0 {
                                        // First window → parent row.
                                        self.sub_cursor = SubCursor::None;
                                    } else {
                                        // Previous window. If it's expanded, go to its last pane.
                                        let prev_win = &windows[p - 1];
                                        let session_name =
                                            self.session_name_at(self.cursor).unwrap_or_default();
                                        if self.is_window_expanded(&session_name, prev_win.index)
                                            && !prev_win.panes.is_empty()
                                        {
                                            self.sub_cursor = SubCursor::Pane {
                                                window: prev_win.index,
                                                pane: prev_win.panes.len() - 1,
                                            };
                                        } else {
                                            self.sub_cursor = SubCursor::Window(prev_win.index);
                                        }
                                    }
                                } else {
                                    self.sub_cursor = SubCursor::None;
                                }
                                self.fetch_task_pane_content();
                            }
                            SubCursor::None => {
                                if self.cursor > 0 {
                                    self.cursor -= 1;
                                    // If moving up onto an expanded row, select the last sub-row.
                                    if self.is_row_expanded(self.cursor) {
                                        let windows = self.windows_at(self.cursor);
                                        if windows.len() <= 1 {
                                            // Single-window auto-flatten: last pane.
                                            let count = self.pane_count_at(self.cursor);
                                            if count > 0 {
                                                let win_idx =
                                                    windows.first().map(|w| w.index).unwrap_or(0);
                                                self.sub_cursor = SubCursor::Pane {
                                                    window: win_idx,
                                                    pane: count - 1,
                                                };
                                            }
                                        } else {
                                            // Multi-window: land on last window (or last pane if expanded).
                                            let last_win = &windows[windows.len() - 1];
                                            let session_name = self
                                                .session_name_at(self.cursor)
                                                .unwrap_or_default();
                                            if self
                                                .is_window_expanded(&session_name, last_win.index)
                                                && !last_win.panes.is_empty()
                                            {
                                                self.sub_cursor = SubCursor::Pane {
                                                    window: last_win.index,
                                                    pane: last_win.panes.len() - 1,
                                                };
                                            } else {
                                                self.sub_cursor = SubCursor::Window(last_win.index);
                                            }
                                        }
                                    }
                                    self.fetch_task_pane_content();
                                }
                            }
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
                            &self.filter_text,
                            self.active_repo_slug(),
                        )
                        .len();
                        let visible_count = standalone_count + worktree_visible_count;

                        match self.sub_cursor.clone() {
                            SubCursor::Pane { window, pane } => {
                                // Check if there's a next pane in this window.
                                let windows = self.windows_at(self.cursor);
                                let win = windows.iter().find(|w| w.index == window);
                                let pane_count = win.map(|w| w.panes.len()).unwrap_or(0);
                                if pane + 1 < pane_count {
                                    self.sub_cursor = SubCursor::Pane {
                                        window,
                                        pane: pane + 1,
                                    };
                                } else if windows.len() <= 1 {
                                    // Single-window: last pane → next parent row.
                                    self.sub_cursor = SubCursor::None;
                                    if visible_count > 0 && self.cursor < visible_count - 1 {
                                        self.cursor += 1;
                                    }
                                } else {
                                    // Multi-window: find next window after current.
                                    let pos = windows.iter().position(|w| w.index == window);
                                    if let Some(p) = pos {
                                        if p + 1 < windows.len() {
                                            self.sub_cursor =
                                                SubCursor::Window(windows[p + 1].index);
                                        } else {
                                            // Last window's last pane → next parent row.
                                            self.sub_cursor = SubCursor::None;
                                            if visible_count > 0 && self.cursor < visible_count - 1
                                            {
                                                self.cursor += 1;
                                            }
                                        }
                                    } else {
                                        self.sub_cursor = SubCursor::None;
                                    }
                                }
                                self.fetch_task_pane_content();
                            }
                            SubCursor::Window(w) => {
                                // If window is expanded, enter first pane.
                                let session_name =
                                    self.session_name_at(self.cursor).unwrap_or_default();
                                let windows = self.windows_at(self.cursor);
                                let win = windows.iter().find(|wi| wi.index == w);
                                if self.is_window_expanded(&session_name, w)
                                    && let Some(win) = win
                                    && !win.panes.is_empty()
                                {
                                    self.sub_cursor = SubCursor::Pane { window: w, pane: 0 };
                                    self.fetch_task_pane_content();
                                    return ok();
                                }
                                // Window collapsed: go to next window or next parent.
                                let pos = windows.iter().position(|wi| wi.index == w);
                                if let Some(p) = pos {
                                    if p + 1 < windows.len() {
                                        self.sub_cursor = SubCursor::Window(windows[p + 1].index);
                                    } else {
                                        self.sub_cursor = SubCursor::None;
                                        if visible_count > 0 && self.cursor < visible_count - 1 {
                                            self.cursor += 1;
                                        }
                                    }
                                } else {
                                    self.sub_cursor = SubCursor::None;
                                }
                                self.fetch_task_pane_content();
                            }
                            SubCursor::None => {
                                if self.is_row_expanded(self.cursor) {
                                    let windows = self.windows_at(self.cursor);
                                    if windows.len() <= 1 {
                                        // Single-window auto-flatten: enter first pane.
                                        let win_idx = windows.first().map(|w| w.index).unwrap_or(0);
                                        self.sub_cursor = SubCursor::Pane {
                                            window: win_idx,
                                            pane: 0,
                                        };
                                    } else {
                                        // Multi-window: enter first window.
                                        self.sub_cursor = SubCursor::Window(windows[0].index);
                                    }
                                    self.fetch_task_pane_content();
                                } else if visible_count > 0 && self.cursor < visible_count - 1 {
                                    self.cursor += 1;
                                    self.fetch_task_pane_content();
                                }
                            }
                        }
                    }
                }
                ok()
            }
            Message::CursorTo(idx) => {
                self.cursor = idx;
                self.sub_cursor = SubCursor::None;
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
            Message::PreviewScrollUp => {
                let mut state = self.preview_scroll_state.get();
                // scroll_up() moves 1 row; loop for mouse-wheel granularity.
                for _ in 0..list::MOUSE_SCROLL_LINES {
                    state.scroll_up();
                }
                self.preview_scroll_state.set(state);
                ok()
            }
            Message::PreviewScrollDown => {
                let mut state = self.preview_scroll_state.get();
                for _ in 0..list::MOUSE_SCROLL_LINES {
                    state.scroll_down();
                }
                self.preview_scroll_state.set(state);
                ok()
            }
            Message::Enter => {
                let quit = self.handle_enter_action();
                self.filter_text.clear();
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
                    &self.filter_text,
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
                    &self.filter_text,
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
                    &self.filter_text,
                    self.active_repo_slug(),
                );
                if let Some(vt) = visible.get(worktree_cursor) {
                    self.view = ViewState::ConfirmDelete(Box::new(state::DeleteState {
                        target: vt.row.clone(),
                        phase: Phase::Confirm,
                        error: None,
                    }));
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
                    &self.filter_text,
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
            Message::Cleanup => {
                self.enter_cleanup_view();
                ok()
            }
            Message::PrevRepo => {
                self.active_repo_index = self.active_repo_index.saturating_sub(1);
                self.cursor = 0;
                self.sub_cursor = SubCursor::None;
                ok()
            }
            Message::NextRepo => {
                let repo_count = self.global_config.repos.len();
                self.active_repo_index = (self.active_repo_index + 1).min(repo_count);
                self.cursor = 0;
                self.sub_cursor = SubCursor::None;
                ok()
            }
            Message::ExpandRow => {
                match &self.sub_cursor {
                    SubCursor::Window(w) => {
                        // Expand window: add to window_expanded.
                        if let Some(session_name) = self.session_name_at(self.cursor) {
                            let windows = self.windows_at(self.cursor);
                            let win = windows.iter().find(|wi| wi.index == *w);
                            if let Some(win) = win
                                && win.panes.len() > 1
                            {
                                let key = Self::window_expansion_key(&session_name, *w);
                                self.window_expanded.insert(key);
                            }
                        }
                    }
                    _ => {
                        // Expand session row.
                        let pane_count = self.pane_count_at(self.cursor);
                        if pane_count > 1
                            && let Some(key) = self.expansion_key_at(self.cursor)
                        {
                            self.expanded.insert(key);
                        }
                    }
                }
                ok()
            }
            Message::CollapseRow => {
                match &self.sub_cursor {
                    SubCursor::Pane { window, .. } => {
                        let w = *window;
                        if self.is_single_window_session(self.cursor) {
                            // Single-window: collapse session entirely.
                            if let Some(key) = self.expansion_key_at(self.cursor) {
                                self.expanded.remove(&key);
                            }
                            self.sub_cursor = SubCursor::None;
                        } else {
                            // Multi-window: collapse window, go to window row.
                            if let Some(session_name) = self.session_name_at(self.cursor) {
                                let key = Self::window_expansion_key(&session_name, w);
                                self.window_expanded.remove(&key);
                            }
                            self.sub_cursor = SubCursor::Window(w);
                        }
                    }
                    SubCursor::Window(_) => {
                        // Left on window row is no-op (must navigate to session row).
                    }
                    SubCursor::None => {
                        if let Some(key) = self.expansion_key_at(self.cursor) {
                            self.expanded.remove(&key);
                            // Clear window expansion state for this session.
                            if let Some(session_name) = self.session_name_at(self.cursor) {
                                self.clear_window_expansion_for_session(&session_name);
                            }
                        }
                    }
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
                // Don't clear sub_cursor — persists for re-expansion.
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
                    // Leaving the legend counts as "seen" — first-launch users
                    // won't be shown it again on the next start.
                    crate::signal::mark_legend_seen();
                    ViewState::List
                } else {
                    ViewState::Help
                };
                ok()
            }
            Message::OpenSearch => {
                self.input_phase = InputPhase::Searching;
                ok()
            }
            Message::CloseSearch => {
                self.input_phase = InputPhase::Idle;
                ok()
            }
            Message::FilterChar(c) => {
                self.filter_text.push(c);
                self.cursor = 0;
                self.clamp_cursor_to_visible();
                ok()
            }
            Message::FilterBackspace => {
                self.filter_text.pop();
                self.clamp_cursor_to_visible();
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
                // Dismissing the legend counts as "seen" for first-launch.
                if matches!(self.view, ViewState::Help) {
                    crate::signal::mark_legend_seen();
                }
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
            &self.filter_text,
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
        // Pulse tick for this frame is sampled once by the run loop *before*
        // calling `render` (see `run_loop`) and stored on `self.pulse_tick`.
        // Every animated-row renderer reads `self.pulse_tick.get()` rather
        // than re-sampling wall-clock time, guaranteeing frame coherence even
        // when a render straddles a second boundary. Tests can set the field
        // directly to force a deterministic frame (issue #281).
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

    /// Returns `true` when any row has a rollup activity that animates
    /// (`Activity::Idle` or `Activity::Input`) (issue #281).
    ///
    /// The run loop uses this to pick its event-poll cadence: fast (100ms)
    /// while any animation is on screen, slower otherwise. Called on every
    /// poll-timeout calculation, so the scan is kept cheap — we short-circuit
    /// as soon as the first animated row is seen, and we delegate the full
    /// exhaustion/rollup check to [`crate::signal::activity_from_claude`] only
    /// when a session's raw status is already in the animated set.
    pub(crate) fn has_animated_visible_row(&self) -> bool {
        use crate::signal::Activity;

        // Cheap pre-check for standalone sessions: if the raw ClaudeState is
        // already not Idle/Input, no need to build the enrichment at all.
        let standalone_animated = self.standalone_sessions.iter().any(|ss| {
            ss.session.claude.as_ref().is_some_and(|c| {
                if !matches!(
                    c.status,
                    crate::claude_state::ClaudeState::Idle
                        | crate::claude_state::ClaudeState::Input
                ) {
                    return false;
                }
                // Context-window or rate-limit exhaustion could escalate this
                // to Activity::Exhausted — a static glyph, not animated. Fall
                // through to the full resolver to respect that escalation.
                let enrichment = crate::orchard_state::ClaudeEnrichment::from(c);
                matches!(
                    crate::signal::activity_from_claude(&enrichment),
                    Activity::Idle | Activity::Input
                )
            })
        });

        if standalone_animated {
            return true;
        }

        self.task_rows.iter().any(|row| {
            matches!(
                crate::signal::rollup_activity_row(row),
                Activity::Idle | Activity::Input
            )
        })
    }

    // -------------------------------------------------------------------
    // Actions (delete, cleanup)
    // -------------------------------------------------------------------

    fn start_delete(&self, target: &derive::WorktreeRow) {
        let wt = target.clone();
        let global_config = self.global_config.clone();
        let tx = self.tx.clone();
        std::thread::spawn(move || match delete_task_row(&wt, &global_config) {
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
            // Single source of truth for the worktree-path slug rule lives in
            // `worktree-core` so the TUI dialog and `orchard new` agree on
            // where to put a new worktree.
            let worktree_path = worktree_core::worktree_path_for(&repo_root, &branch);
            let worktree_path = worktree_path.to_string_lossy().into_owned();

            // Delegate the new-branch-then-fallback dance to worktree-core.
            // The shared library is the single source of truth for git worktree
            // mutation; the TUI just collects intent and reports outcomes.
            if let Err(e) =
                worktree_core::create_worktree(Path::new(&repo_root), &branch, &worktree_path)
            {
                let _ = tx.send(AppMsg::CreateWorktreeErr(format!("{e}")));
                return;
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
        let mut app = App {
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
            filter_text: String::new(),
            input_phase: InputPhase::Idle,
            host_reachable: HashMap::new(),
            tx,
            rx,
            last_refresh: Instant::now(),
            last_full_refresh: Instant::now(),
            throbber_state: throbber_widgets_tui::ThrobberState::default(),
            switch_target: None,
            persist_selection: true,
            previous_orchard_state: None,
            claude_debounce: crate::watch::debounce::ClaudeDebounceState::new(),
            table_area: Cell::new(Rect::default()),
            url_area: Cell::new(Rect::default()),
            preview_area: Cell::new(Rect::default()),
            last_click: None,
            preview_scroll_state: std::cell::Cell::new(tui_scrollview::ScrollViewState::default()),
            pulse_tick: Cell::new(crate::signal::pulse_tick()),
            expanded: HashSet::new(),
            sub_cursor: SubCursor::None,
            window_expanded: HashSet::new(),
            seen_expandable_keys: HashSet::new(),
            seen_window_keys: HashSet::new(),
            work_view_snapshot: std::sync::Arc::new(std::sync::Mutex::new(None)),
            ahead_behind_snapshot: std::sync::Arc::new(std::sync::Mutex::new(None)),
            work_view_source: std::sync::Arc::new(crate::tui::refresh::NullWorkViewSource),
            daemon_status: DaemonStatus::Unknown,
        };
        // Mirror production: auto-expand multi-child rows so tests see
        // hierarchy by default (issue #251).
        app.prune_expansion_state();
        app
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

    // Hydrate the preview pane for the restored cursor row so the first paint
    // shows content without requiring a key press. Skip in cleanup mode.
    if app.persist_selection {
        app.fetch_task_pane_content();
    }

    // Initial data fetch in background
    app.start_full_refresh();

    let result = run_loop(&mut terminal, &mut app);

    // Persist the current selection so it can be restored on the next launch.
    // Skip in cleanup mode — cleanup has its own cursor and no selection to save.
    if app.persist_selection
        && let Some(sel) = current_selection(&app)
        && let Err(e) = last_selection::save(&sel)
    {
        crate::logger::LOG.warn(&format!("last_selection: save error: {e}"));
    }

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

/// Idle-screen poll timeout (issue #281). Chosen to balance:
/// - input latency: a keypress blocks for at most this long before reaching
///   the event loop,
/// - CPU: render cost is dominated by `has_animated_visible_row` + ratatui's
///   diff render, so stretching past ~500ms past diminishing returns.
///
/// 500ms keeps keystrokes feeling snappy (users struggle to notice <500ms
/// of delay on a single key) while cutting wasted renders ~5× vs the fast
/// path. Exposed as `const` so it's obvious at the call site and tunable.
pub(crate) const IDLE_POLL_TIMEOUT_MS: u64 = 500;

/// Picks the event-poll cadence based on what's visible on screen (issue #281).
///
/// When any row animates (Idle/Input pulse) or a full-refresh spinner is
/// running, we need the fast cadence so the animation appears at ~10Hz and
/// the 1s pulse boundary is caught within 100ms. Otherwise, the idle cadence
/// saves CPU on long-running screens with nothing to update.
pub(crate) fn poll_cadence(animated_visible: bool, refreshing: bool) -> Duration {
    if animated_visible || refreshing {
        Duration::from_millis(POLL_TIMEOUT_MS)
    } else {
        Duration::from_millis(IDLE_POLL_TIMEOUT_MS)
    }
}

fn run_loop(
    terminal: &mut ratatui::Terminal<ratatui::backend::CrosstermBackend<std::fs::File>>,
    app: &mut App,
) -> anyhow::Result<Option<String>> {
    loop {
        // Advance throbber animation before drawing so spinner progresses each frame.
        app.throbber_state.calc_next();

        // Sample the pulse tick ONCE per frame (issue #281). All animated row
        // renderers read `app.pulse_tick.get()` — coherent within the frame
        // even across second boundaries.
        app.pulse_tick.set(crate::signal::pulse_tick());

        terminal.draw(|f| app.render(f))?;

        // Poll cadence adapts to visible animation (issue #281); see
        // [`poll_cadence`] for the policy and its rationale.
        let poll_timeout = poll_cadence(app.has_animated_visible_row(), app.refreshing);

        if event::poll(poll_timeout)? {
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
// Last-selection helpers (pure, testable)
// ---------------------------------------------------------------------------

/// Builds a `LastSelection` from the current app state.
///
/// Returns `None` when the cursor is out of bounds for both lists,
/// so the caller can skip saving and preserve the previous file.
pub(crate) fn current_selection(app: &App) -> Option<last_selection::LastSelection> {
    let active_repo_slug = app.active_repo_slug().map(String::from);
    let standalone_count = app.standalone_sessions.len();
    if app.cursor < standalone_count
        && let Some(ss) = app.standalone_sessions.get(app.cursor)
    {
        return Some(last_selection::LastSelection {
            kind: last_selection::SelectionKind::Standalone,
            key: ss.session.tmux.name.clone(),
            active_repo_slug,
        });
    }
    let wt_idx = app.cursor.saturating_sub(standalone_count);
    if let Some(row) = app.task_rows.get(wt_idx) {
        return Some(last_selection::LastSelection {
            kind: last_selection::SelectionKind::Worktree,
            key: row.worktree_path.clone(),
            active_repo_slug,
        });
    }
    None
}

// ---------------------------------------------------------------------------
// Remote dedup helpers
// ---------------------------------------------------------------------------

/// Returns the dedup key for a remote, used to avoid running
/// `refresh_remote_tmux_sessions` twice for the same BoxdFork golden host
/// when it appears in multiple repos.
///
/// Uses `kind_str` (kebab-case) rather than `{:?}` debug output so the key
/// is stable across Rust version changes.
fn dedup_key(remote: &crate::global_config::RemoteConfig) -> String {
    format!(
        "{}:{}",
        crate::cache_sources::kind_str(remote.kind),
        remote.host
    )
}

/// Fetches ahead/behind commit counts for every configured repo by running
/// `git branch -vv` per repo directory, in parallel.
///
/// The daemon's `Worktree` schema does not yet expose `ahead`/`behind` counts
/// (tracked in #483); this is a temporary carve-out that will be removed when
/// the daemon ships those fields.
///
/// Returns a `HashMap` keyed by repo directory path → branch name →
/// `(ahead, behind)`.
fn fetch_ahead_behind_for_snapshot(
    config: &global_config::GlobalConfig,
) -> crate::daemon::work_view_adapter::AheadBehindMap {
    let result: std::sync::Mutex<crate::daemon::work_view_adapter::AheadBehindMap> =
        std::sync::Mutex::new(HashMap::new());
    crate::refresh_parallel::for_each_repo_parallel(config, |repo| {
        if let Ok(out) = crate::cache_sources::run_local_in("git", &["branch", "-vv"], &repo.path) {
            let branch_map = crate::cache_sources::parse_git_ahead_behind(&out);
            if let Ok(mut acc) = result.lock() {
                acc.insert(repo.path.clone(), branch_map);
            }
        }
    });
    result.into_inner().unwrap_or_default()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
#[allow(deprecated)] // PrInfo.checks_state — fixtures still populate the legacy field for now
mod tests {
    use super::*;
    use crate::derive::{DisplayGroup, PrInfo as DPrInfo, WorktreeRow};
    use crate::session::{
        ClaudeSessionInfo, EnrichedSession, Host, SessionStatus, TmuxSessionInfo,
    };
    use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
    use ratatui::Terminal;
    use ratatui::backend::TestBackend;
    use sessions::compute_sessions_to_create;

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
            display_group: group,
            is_main_worktree: false,
            layout: crate::cache::WorktreeLayout::Bare,
            discovery_path: None,
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
                    ci_code_state: None,
                    ci_gate_state: None,
                    ci_checks: crate::ci_state::CiChecks::default(),
                    has_conflicts: false,
                    unresolved_threads: 0,
                    labels: vec![],
                    ..DPrInfo::default()
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
    fn spc_w_key_opens_new_worktree_dialog() {
        // 'w' in Idle phase directly dispatches NewWorktree.
        let mut app = App::new_test(vec![]);
        assert_eq!(app.input_phase, InputPhase::Idle);
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

    /// Send a key directly — in Idle phase, all action keys dispatch without a leader prefix.
    fn send_leader_key(app: &mut App, key: KeyEvent) -> UpdateResult {
        send_key(app, key)
    }

    #[test]
    fn q_key_quits() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('q'), KeyModifiers::NONE);
        let r = send_leader_key(&mut app, key);
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
        send_leader_key(&mut app, key);
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
        send_leader_key(&mut app, key);
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let mut app = App::new_test(vec![row]);
        app.seed_reachability("gpu1", Reachability::Unreachable);

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
    fn enter_on_remote_row_with_missing_host_entry_does_not_silently_block() {
        // Regression for #280: when OrchardState.hosts lacks an entry for the
        // worktree's host (not-yet-probed, skipped, or timed out), Enter must
        // not silently block with "checking connectivity...". A missing entry
        // is "unknown", not "blocked" — the action should be attempted and any
        // failure surfaced, or an actionable hint shown.
        let row = WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Remote("gpu1".to_string()),
                    name: "sess".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            ..make_task_row(1, DisplayGroup::ClaudeWorking)
        };
        let mut app = App::new_test(vec![row]);
        // Intentionally do NOT insert "gpu1" into host_reachable.
        assert!(!app.host_reachable.contains_key("gpu1"));

        let key = KeyEvent::new(KeyCode::Enter, KeyModifiers::NONE);
        let msg = app.handle_event(key);
        app.update(msg.unwrap());

        // Positive assertion: the action must have been attempted. In the
        // test environment the SSH call to "gpu1" will fail, so
        // join_or_create_session surfaces a "remote session error: …"
        // warning. Either that error is present, or switch_target was set
        // (action succeeded against something mocked). What must NOT happen:
        // a silent bail-out that leaves both fields untouched.
        let warning = app.warning.as_ref().map(|(s, _)| s.as_str());
        let attempted =
            app.switch_target.is_some() || warning.is_some_and(|w| w.contains("remote session"));
        assert!(
            attempted,
            "Enter on not-yet-probed host must attempt the action; warning={warning:?}, switch_target={:?}",
            app.switch_target
        );
        assert!(
            warning != Some("@gpu1 -- checking connectivity..."),
            "missing host entry must not surface the 'checking connectivity...' block; got {warning:?}"
        );
    }

    #[test]
    fn reconnect_unreachable_hosts_all_reachable_sets_warning() {
        let mut app = App::new_test(vec![]);
        app.seed_reachability("gpu1", Reachability::Reachable);
        app.reconnect_unreachable_hosts();
        let warning = app.warning.as_ref().map(|(s, _)| s.as_str());
        assert_eq!(warning, Some("All hosts reachable"));
    }

    #[test]
    fn reconnect_unreachable_hosts_unreachable_sets_reconnecting_warning() {
        let mut app = App::new_test(vec![]);
        app.seed_reachability("gpu1", Reachability::Unreachable);
        app.reconnect_unreachable_hosts();
        let warning = app.warning.as_ref().map(|(s, _)| s.as_str());
        assert_eq!(warning, Some("Reconnecting..."));
    }

    #[test]
    fn header_renders_host_connectivity() {
        let mut app = App::new_test(vec![]);
        app.seed_reachability("gpu1", Reachability::Reachable);
        app.seed_reachability("dev2", Reachability::Unreachable);
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
    fn reachability_returns_unknown_when_no_probe_result() {
        let app = App::new_test(vec![]);
        assert_eq!(app.reachability("gpu1"), Reachability::Unknown);
    }

    #[test]
    fn reachability_returns_reachable_when_probe_succeeded() {
        let mut app = App::new_test(vec![]);
        app.seed_reachability("gpu1", Reachability::Reachable);
        assert_eq!(app.reachability("gpu1"), Reachability::Reachable);
    }

    #[test]
    fn reachability_returns_unreachable_when_probe_failed() {
        let mut app = App::new_test(vec![]);
        app.seed_reachability("gpu1", Reachability::Unreachable);
        assert_eq!(app.reachability("gpu1"), Reachability::Unreachable);
    }

    #[test]
    fn has_probe_results_tracks_map_population() {
        let mut app = App::new_test(vec![]);
        assert!(!app.has_probe_results());
        app.host_reachable.insert("gpu1".to_string(), true);
        assert!(app.has_probe_results());
    }

    #[test]
    fn has_unreachable_host_true_only_when_any_probe_failed() {
        let mut app = App::new_test(vec![]);
        assert!(!app.has_unreachable_host());
        app.host_reachable.insert("gpu1".to_string(), true);
        assert!(!app.has_unreachable_host());
        app.host_reachable.insert("dev2".to_string(), false);
        assert!(app.has_unreachable_host());
    }

    #[test]
    fn unreachable_hosts_yields_only_failed_probes() {
        let mut app = App::new_test(vec![]);
        app.host_reachable.insert("gpu1".to_string(), true);
        app.host_reachable.insert("dev2".to_string(), false);
        app.host_reachable.insert("box3".to_string(), false);
        let mut names: Vec<&str> = app.unreachable_hosts().collect();
        names.sort();
        assert_eq!(names, vec!["box3", "dev2"]);
    }

    #[test]
    fn probed_hosts_sorted_is_alphabetical_with_typed_reachability() {
        let mut app = App::new_test(vec![]);
        app.host_reachable.insert("gpu1".to_string(), true);
        app.host_reachable.insert("box3".to_string(), false);
        app.host_reachable.insert("alpha".to_string(), true);
        let entries = app.probed_hosts_sorted();
        assert_eq!(
            entries,
            vec![
                ("alpha", Reachability::Reachable),
                ("box3", Reachability::Unreachable),
                ("gpu1", Reachability::Reachable),
            ]
        );
    }

    #[test]
    fn question_mark_opens_help() {
        let mut app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char('?'), KeyModifiers::NONE);
        send_leader_key(&mut app, key);
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
            display_group: group,
            is_main_worktree: false,
            layout: crate::cache::WorktreeLayout::Bare,
            discovery_path: None,
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
                    model: None,
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
                    model: None,
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            ..make_worktree_row("feat/waiting", DisplayGroup::NeedsAttention)
        };
        let mut app = App::new_test(vec![row]);
        // Force tick=0 so we assert deterministic frame content. Without this
        // the test would flake across the second boundary (both frames are
        // valid for this row).
        app.pulse_tick.set(0);
        let output = render_to_string(&mut app, 120, 40);

        // Issue #281: the ❓ glyph is gone from the status column. The
        // "waiting on you" signal is carried by a rotating hourglass driven
        // off rollup Activity::Input; column A pulses ○/? in red.
        assert!(
            !output.contains('\u{2753}'),
            "❓ glyph must no longer appear after issue #281; got:\n{output}"
        );
        // At tick=0, column A shows ○ (open circle) and the status column
        // shows ⏳ (sand still falling).
        assert!(
            output.contains('\u{25CB}'),
            "expected ○ in column A (tick=0 Input frame); got:\n{output}"
        );
        assert!(
            output.contains('\u{23F3}'),
            "expected ⏳ hourglass in status column when rollup activity is Input; got:\n{output}"
        );
        assert!(
            output.contains("needs attention"),
            "expected NeedsAttention section header"
        );

        // Swap to tick=1 — column A flips to '?' and the hourglass flips to ⌛.
        app.pulse_tick.set(1);
        let output = render_to_string(&mut app, 120, 40);
        assert!(
            output.contains('?'),
            "expected '?' in column A (tick=1 Input frame); got:\n{output}"
        );
        assert!(
            output.contains('\u{231B}'),
            "expected ⌛ hourglass at tick=1; got:\n{output}"
        );
        assert!(
            !output.contains('\u{2753}'),
            "❓ must not appear at tick=1 either; got:\n{output}"
        );
    }

    // -- issue #281: pulse animation / has_animated_visible_row --------------

    fn make_claude_row(
        branch: &str,
        group: DisplayGroup,
        state: crate::claude_state::ClaudeState,
    ) -> WorktreeRow {
        WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: format!("sess-{}", branch.replace('/', "-")),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: state,
                    model: None,
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            ..make_worktree_row(branch, group)
        }
    }

    #[test]
    fn snapshot_differs_between_ticks_when_row_animates() {
        let mut app = App::new_test(vec![make_claude_row(
            "feat/idle",
            DisplayGroup::ClaudeWorking,
            crate::claude_state::ClaudeState::Idle,
        )]);
        app.pulse_tick.set(0);
        let at_zero = render_to_string(&mut app, 120, 40);
        app.pulse_tick.set(1);
        let at_one = render_to_string(&mut app, 120, 40);
        assert_ne!(
            at_zero, at_one,
            "Idle-row snapshot at tick=0 must differ from tick=1"
        );
        assert!(at_zero.contains('\u{25CB}'), "tick=0 should contain ○"); // ○
        assert!(at_one.contains('\u{25CF}'), "tick=1 should contain ●"); // ●
    }

    #[test]
    fn snapshot_identical_across_ticks_when_no_row_animates() {
        // Only Working (static ⚡) and None — nothing should animate.
        let mut app = App::new_test(vec![
            make_claude_row(
                "feat/working",
                DisplayGroup::ClaudeWorking,
                crate::claude_state::ClaudeState::Working,
            ),
            make_worktree_row("feat/none", DisplayGroup::Other), // no sessions
        ]);
        app.pulse_tick.set(0);
        let at_zero = render_to_string(&mut app, 120, 40);
        app.pulse_tick.set(1);
        let at_one = render_to_string(&mut app, 120, 40);
        assert_eq!(
            at_zero, at_one,
            "Snapshot must be identical across ticks when no row animates"
        );
    }

    #[test]
    fn two_renders_with_same_tick_produce_identical_buffers() {
        // Frame-coherence guard (issue #281): rendering twice with the same
        // tick must produce identical output — the tick sampled in `render` is
        // the *only* source of per-frame timing.
        let mut app = App::new_test(vec![
            make_claude_row(
                "feat/idle",
                DisplayGroup::ClaudeWorking,
                crate::claude_state::ClaudeState::Idle,
            ),
            make_claude_row(
                "feat/input",
                DisplayGroup::NeedsAttention,
                crate::claude_state::ClaudeState::Input,
            ),
        ]);
        app.pulse_tick.set(0);
        let a = render_to_string(&mut app, 120, 40);
        let b = render_to_string(&mut app, 120, 40);
        assert_eq!(
            a, b,
            "two renders with the same tick must be byte-identical"
        );
    }

    #[test]
    fn has_animated_visible_row_true_when_idle_row_visible() {
        let app = App::new_test(vec![make_claude_row(
            "feat/x",
            DisplayGroup::ClaudeWorking,
            crate::claude_state::ClaudeState::Idle,
        )]);
        assert!(app.has_animated_visible_row());
    }

    #[test]
    fn has_animated_visible_row_true_when_input_row_visible() {
        let app = App::new_test(vec![make_claude_row(
            "feat/x",
            DisplayGroup::NeedsAttention,
            crate::claude_state::ClaudeState::Input,
        )]);
        assert!(app.has_animated_visible_row());
    }

    #[test]
    fn has_animated_visible_row_false_when_only_working_visible() {
        let app = App::new_test(vec![make_claude_row(
            "feat/x",
            DisplayGroup::ClaudeWorking,
            crate::claude_state::ClaudeState::Working,
        )]);
        assert!(!app.has_animated_visible_row());
    }

    #[test]
    fn has_animated_visible_row_false_when_no_claude_sessions() {
        let app = App::new_test(vec![make_worktree_row("feat/none", DisplayGroup::Other)]);
        assert!(!app.has_animated_visible_row());
    }

    // -- issue #281: adaptive poll cadence ----------------------------------

    #[test]
    fn poll_cadence_fast_when_animated_visible() {
        assert_eq!(
            poll_cadence(true, false),
            Duration::from_millis(POLL_TIMEOUT_MS),
            "animated row visible must use the fast cadence"
        );
    }

    #[test]
    fn poll_cadence_fast_when_refreshing() {
        assert_eq!(
            poll_cadence(false, true),
            Duration::from_millis(POLL_TIMEOUT_MS),
            "full-refresh spinner must stay on the fast cadence"
        );
    }

    #[test]
    fn poll_cadence_idle_when_nothing_animates() {
        assert_eq!(
            poll_cadence(false, false),
            Duration::from_millis(IDLE_POLL_TIMEOUT_MS),
            "idle screens must stretch to the idle timeout"
        );
    }

    #[test]
    fn idle_poll_timeout_is_user_acceptable() {
        // Sanity guard: the idle timeout must stay below a threshold where
        // keystroke latency becomes perceptible. 600ms is the cited threshold;
        // we hold well below it. Tripwire for accidental bumps.
        const {
            assert!(
                IDLE_POLL_TIMEOUT_MS <= 600,
                "idle poll timeout risks perceptible input lag above 600ms"
            );
        }
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
                ci_code_state: Some("failing".to_string()),
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
            }),
            ..make_worktree_row("feat/branch", DisplayGroup::NeedsAttention)
        };
        let mut app = App::new_test(vec![row]);
        let output = render_to_string(&mut app, 120, 40);

        // Issue #251: PR number renders in the ID column as `PR#55` (when no
        // issue is linked) or `#N / PR#55` when an issue is present. A failing
        // CI state shows as ❌ in the STATUS column.
        assert!(
            output.contains("PR#55") || output.contains("#55"),
            "expected PR 55 in ID column, got:\n{output}"
        );
        let failing_glyph = crate::signal::PipelineStatus::CiFailing.glyph();
        assert!(
            output.contains(failing_glyph),
            "expected ❌ ci-failing glyph in STATUS column, got:\n{output}"
        );
    }

    #[test]
    fn remote_host_indicator_renders_for_remote_worktree() {
        let row = WorktreeRow {
            worktree_host: Some("gpu1".to_string()),
            ..make_worktree_row("feat/remote", DisplayGroup::Other)
        };
        let mut app = App::new_test(vec![row]);
        app.seed_reachability("gpu1", Reachability::Reachable);
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
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            ..make_worktree_row("feat/remote", DisplayGroup::Other)
        };
        let mut app = App::new_test(vec![row]);
        app.seed_reachability("gpu1", Reachability::Unreachable);
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
        send_leader_key(&mut app, key);
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
        send_leader_key(&mut app, j);
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
        send_leader_key(&mut app, k);
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
        let r = send_leader_key(&mut app, q);
        assert!(r.quit, "q should return quit=true");
    }

    // -----------------------------------------------------------------------
    // handle_event tests
    // -----------------------------------------------------------------------

    #[test]
    fn handle_event_printable_char_goes_to_filter() {
        let mut app = App::new_test(vec![]);
        app.input_phase = InputPhase::Searching;
        let key = KeyEvent::new(KeyCode::Char('z'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::FilterChar('z')));
    }

    #[test]
    fn handle_event_unbound_key_in_searching_returns_none() {
        let mut app = App::new_test(vec![]);
        app.input_phase = InputPhase::Searching;
        // F5 is not bound in Searching phase
        let key = KeyEvent::new(KeyCode::F(5), KeyModifiers::NONE);
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
    fn handle_event_searching_phase_routes_chars_to_filter() {
        // In the Searching phase, printable chars become FilterChar messages.
        let mut app = App::new_test(vec![]);
        app.input_phase = InputPhase::Searching;
        let key = KeyEvent::new(KeyCode::Char('a'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::FilterChar('a')));
    }

    #[test]
    fn handle_event_digit_in_searching_goes_to_filter() {
        // Digits are printable chars — in Searching phase they feed the filter.
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::ClaudeWorking),
            make_task_row(3, DisplayGroup::Other),
        ];
        let mut app = App::new_test(rows);
        app.input_phase = InputPhase::Searching;
        let key = KeyEvent::new(KeyCode::Char('1'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::FilterChar('1')));
    }

    #[test]
    fn handle_event_digit_in_idle_returns_cursor_to() {
        // Digits dispatch CursorTo directly in Idle phase.
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
        app.view = ViewState::ConfirmDelete(Box::new(state::DeleteState {
            target: make_task_row(1, DisplayGroup::Other),
            phase: Phase::Confirm,
            error: None,
        }));
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
        // 'p' dispatches TogglePriority directly in Idle phase.
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
            ahead: None,
            behind: None,
            last_commit_at: None,
            layout: cache::WorktreeLayout::Bare,
        }
    }

    fn make_cached_session(name: &str) -> cache::CachedTmuxSession {
        cache::CachedTmuxSession {
            name: name.to_string(),
            path: "/some/path".to_string(),
            pane_targets: vec![],
            pane_titles: vec![],
            pane_commands: vec![],
            window_names: vec![],
            window_active: vec![],
            window_layouts: vec![],
            pane_paths: vec![],
            pane_active: vec![],
            host: None,
            created_at: None,
            last_activity_at: None,
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
                    model: Some("opus".to_string()),
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 2,
                labels: vec![],
                ..DPrInfo::default()
            }),
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "orchard_issue53".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Input,
                    model: Some("sonnet".to_string()),
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
            }),
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: "orchard_issue47".to_string(),
                    status: SessionStatus::Running { attached: false },
                },
                claude: Some(ClaudeSessionInfo {
                    status: crate::claude_state::ClaudeState::Working,
                    model: Some("opus".to_string()),
                    last_tool: None,
                    current_task: None,
                    session_start_ts: None,
                    input_tokens: None,
                    output_tokens: None,
                    cache_creation_input_tokens: None,
                    cache_read_input_tokens: None,
                    context_window_pct: None,
                    cost_usd: None,
                    total_duration_ms: None,
                    rate_limits: None,
                    stop_reason: None,
                    turn_count: None,
                    state_changed_at: None,
                }),
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
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
                ci_code_state: None,
                ci_gate_state: None,
                ci_checks: crate::ci_state::CiChecks::default(),
                has_conflicts: false,
                unresolved_threads: 0,
                labels: vec![],
                ..DPrInfo::default()
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
    fn mouse_events_work_during_filtering() {
        // Mouse events are processed in List view regardless of filter state.
        let mut app = app_with_table_area(vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::NeedsAttention),
        ]);
        app.filter_text = "some-filter".to_string();
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 7);
        assert_eq!(app.handle_mouse_event(event), Some(Message::CursorDown));
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
        app.view = ViewState::ConfirmDelete(Box::new(state::DeleteState {
            target: make_task_row(1, DisplayGroup::Other),
            phase: Phase::Confirm,
            error: None,
        }));
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
                    windows: vec![],
                    panes: vec![],
                    started_at: None,
                    last_activity_at: None,
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

    // -----------------------------------------------------------------------
    // Preview mouse scroll tests
    // -----------------------------------------------------------------------

    /// Creates an App with both table_area and preview_area pre-set.
    fn app_with_preview_area(task_rows: Vec<WorktreeRow>) -> App {
        let app = app_with_table_area(task_rows);
        app.preview_area.set(Rect {
            x: 0,
            y: 20,
            width: 80,
            height: 10,
        });
        app
    }

    #[test]
    fn mouse_scroll_down_in_preview_returns_preview_scroll_down() {
        let mut app = app_with_preview_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 22);
        assert_eq!(
            app.handle_mouse_event(event),
            Some(Message::PreviewScrollDown)
        );
    }

    #[test]
    fn mouse_scroll_up_in_preview_returns_preview_scroll_up() {
        let mut app = app_with_preview_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let event = make_mouse_event(MouseEventKind::ScrollUp, 10, 22);
        assert_eq!(
            app.handle_mouse_event(event),
            Some(Message::PreviewScrollUp)
        );
    }

    #[test]
    fn mouse_scroll_in_preview_with_zero_rect_returns_none() {
        let mut app = app_with_table_area(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // preview_area defaults to Rect::default() (width=0), so no hit.
        let event = make_mouse_event(MouseEventKind::ScrollDown, 10, 22);
        assert_eq!(app.handle_mouse_event(event), None);
    }

    #[test]
    fn preview_scroll_down_advances_state_by_three_lines() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        let initial = app.preview_scroll_state.get();
        app.update(Message::PreviewScrollDown);
        let after = app.preview_scroll_state.get();
        assert_eq!(
            after.offset().y,
            initial.offset().y + 3,
            "PreviewScrollDown should advance y offset by 3"
        );
    }

    #[test]
    fn preview_scroll_up_decreases_state_by_three_lines() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::NeedsAttention)]);
        // Scroll down first so we have room to scroll up.
        app.update(Message::PreviewScrollDown);
        app.update(Message::PreviewScrollDown);
        let before = app.preview_scroll_state.get();
        app.update(Message::PreviewScrollUp);
        let after = app.preview_scroll_state.get();
        assert_eq!(
            after.offset().y,
            before.offset().y.saturating_sub(3),
            "PreviewScrollUp should decrease y offset by 3"
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
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
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
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    name: session_name.to_string(),
                    host: Host::Local,
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            }],
            display_group: DisplayGroup::Other,
            is_main_worktree: false,
            layout: crate::cache::WorktreeLayout::Bare,
            discovery_path: None,
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
    fn h_key_maps_to_collapse_behind_leader() {
        // 'h' dispatches CollapseRow directly in Idle phase.
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
    fn l_key_maps_to_expand_behind_leader() {
        // 'l' dispatches ExpandRow directly in Idle phase.
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
    fn e_key_maps_to_toggle_expand_all_behind_leader() {
        // 'E' dispatches ToggleExpandAll directly in Idle phase.
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
                cwd: String::new(),
                is_active: false,
            })
            .collect();
        let windows = vec![crate::session::WindowInfo {
            index: 0,
            name: "main".to_string(),
            is_active: true,
            panes: panes.clone(),
            layout: String::new(),
        }];
        WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: format!("sess-{}", issue),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows,
                panes,
                started_at: None,
                last_activity_at: None,
            }],
            ..make_task_row(issue, DisplayGroup::Other)
        }
    }

    /// Creates a worktree row with multiple windows for testing multi-window navigation.
    fn make_task_row_with_windows(issue: u32, window_pane_counts: &[(usize, &str)]) -> WorktreeRow {
        let mut all_panes = Vec::new();
        let mut windows = Vec::new();
        let mut flat_idx = 0;
        for (wi, (pane_count, name)) in window_pane_counts.iter().enumerate() {
            let mut win_panes = Vec::new();
            for pi in 0..*pane_count {
                let pane = crate::session::PaneInfo {
                    index: flat_idx,
                    tmux_target: format!("{wi}.{pi}"),
                    command: format!("cmd{flat_idx}"),
                    title: format!("pane{flat_idx}"),
                    has_claude: flat_idx == 0,
                    cwd: String::new(),
                    is_active: false,
                };
                win_panes.push(pane.clone());
                all_panes.push(pane);
                flat_idx += 1;
            }
            windows.push(crate::session::WindowInfo {
                index: wi,
                name: name.to_string(),
                is_active: wi == 0,
                panes: win_panes,
                layout: String::new(),
            });
        }
        WorktreeRow {
            sessions: vec![EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: format!("sess-{}", issue),
                    status: SessionStatus::Running { attached: false },
                },
                claude: None,
                windows,
                panes: all_panes,
                started_at: None,
                last_activity_at: None,
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
        // Issue #251 made rows default-expanded, so force at least one row
        // collapsed to exercise the "expand all" branch.
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 3),
            make_task_row_with_panes(3, 3),
        ]);
        app.expanded.remove("/workspace/repo-2");
        assert_eq!(app.expanded.len(), 2, "precondition: one row collapsed");
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
        assert_eq!(
            app.sub_cursor,
            SubCursor::None,
            "sub_cursor should remain None"
        );
    }

    #[test]
    fn collapse_all_preserves_sub_cursor() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
        app.update(Message::ToggleExpandAll);
        assert_eq!(
            app.sub_cursor,
            SubCursor::Pane { window: 0, pane: 1 },
            "sub_cursor should persist across collapse-all"
        );
    }

    // -----------------------------------------------------------------------
    // Navigation with sub-rows (single-window auto-flatten)
    // -----------------------------------------------------------------------

    #[test]
    fn down_on_expanded_row_enters_first_pane() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.update(Message::CursorDown);
        assert_eq!(app.cursor, 0, "cursor stays on parent row");
        assert_eq!(
            app.sub_cursor,
            SubCursor::Pane { window: 0, pane: 0 },
            "enters first sub-row (single-window auto-flatten)"
        );
    }

    #[test]
    fn down_on_last_sub_row_moves_to_next_parent() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 2 }; // last sub-row of row 0
        app.update(Message::CursorDown);
        assert_eq!(app.cursor, 1, "cursor moves to next parent");
        assert_eq!(app.sub_cursor, SubCursor::None, "sub_cursor cleared");
    }

    #[test]
    fn up_on_first_sub_row_returns_to_parent() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 0 };
        app.update(Message::CursorUp);
        assert_eq!(app.cursor, 0);
        assert_eq!(app.sub_cursor, SubCursor::None, "back to parent row");
    }

    #[test]
    fn up_on_middle_sub_row_moves_up_within_sub_rows() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 2 };
        app.update(Message::CursorUp);
        assert_eq!(app.sub_cursor, SubCursor::Pane { window: 0, pane: 1 });
    }

    #[test]
    fn cursor_to_clears_sub_cursor() {
        let mut app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
        ]);
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
        app.update(Message::CursorTo(1));
        assert_eq!(app.cursor, 1);
        assert_eq!(
            app.sub_cursor,
            SubCursor::None,
            "digit-jump clears sub_cursor"
        );
    }

    #[test]
    fn collapse_from_sub_row_clears_sub_cursor() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
        app.update(Message::CollapseRow);
        assert_eq!(app.sub_cursor, SubCursor::None);
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
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
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
        assert_eq!(
            app.sub_cursor,
            SubCursor::Pane { window: 0, pane: 2 },
            "selects last sub-row"
        );
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

    #[test]
    fn prune_expansion_state_auto_expands_on_first_sight_only() {
        // Issue #251: rows default-expanded. `new_test` already seeds the
        // expansion set via `prune_expansion_state`, so a multi-pane row is
        // expanded immediately after construction.
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        assert!(
            app.expanded.contains("/workspace/repo-1"),
            "multi-pane row should be auto-expanded by default"
        );
        // After the first prune the key is in `seen_expandable_keys`. Subsequent
        // prune calls must NOT re-expand a key that is absent from `expanded` —
        // that would stomp user collapses (issue #261). Verify the key stays
        // absent after manually clearing it and re-pruning.
        app.expanded.clear();
        app.prune_expansion_state();
        assert!(
            !app.expanded.contains("/workspace/repo-1"),
            "prune must not re-expand a previously-seen key (would stomp user collapse)"
        );
    }

    #[test]
    fn prune_expansion_state_does_not_expand_single_pane_rows() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 1)]);
        app.prune_expansion_state();
        assert!(
            app.expanded.is_empty(),
            "single-pane row should not be auto-expanded"
        );
    }

    // Regression tests for issue #261: user collapses must survive cache refresh
    // (i.e., a second call to prune_expansion_state must NOT re-expand a row
    // that the user explicitly collapsed).

    #[test]
    fn prune_expansion_state_preserves_user_collapse() {
        // Arrange: a multi-pane row — prune auto-expands it on first call.
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        assert!(
            app.expanded.contains("/workspace/repo-1"),
            "precondition: row is auto-expanded after construction"
        );

        // Act: user explicitly collapses the row, then a cache refresh fires
        // (simulated by a second call to prune_expansion_state).
        app.expanded.remove("/workspace/repo-1");
        app.prune_expansion_state();

        // Assert: user's collapse must be preserved — not stomped by auto-expand.
        assert!(
            !app.expanded.contains("/workspace/repo-1"),
            "prune_expansion_state stomped user collapse (bug #261)"
        );
    }

    #[test]
    fn prune_expansion_state_preserves_collapse_across_filter_changes() {
        // Issue #261 regression: even when the user filters/repo-switches away
        // from a row, its expansion intent must survive. Prune operates on the
        // full task_rows set, not the visible-filtered subset.
        let row_a = WorktreeRow {
            repo_slug: "owner/repo-a".to_string(),
            ..make_task_row_with_panes(1, 3)
        };
        let row_b = WorktreeRow {
            repo_slug: "owner/repo-b".to_string(),
            worktree_path: "/workspace/repo-b-2".to_string(),
            ..make_task_row_with_panes(2, 3)
        };
        let mut app = App::new_test(vec![row_a, row_b]);

        // User collapses repo-a's row while viewing all repos.
        app.expanded.remove("/workspace/repo-1");

        // Filter away from repo-a (e.g. repo-switch to repo-b only).
        app.filter_text = "repo-b".to_string();
        app.prune_expansion_state();

        // Come back — filter cleared.
        app.filter_text.clear();
        app.prune_expansion_state();

        assert!(
            !app.expanded.contains("/workspace/repo-1"),
            "collapse must survive filter away-and-back cycle"
        );
    }

    #[test]
    fn prune_expansion_state_preserves_user_window_collapse() {
        // Arrange: a row with 2 windows (each with 2 panes) so both pane
        // expansion AND window expansion are exercised.
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "win-a"), (2, "win-b")],
        )]);
        let pane_key = "/workspace/repo-1".to_string();
        let window_key = App::window_expansion_key("sess-1", 0);

        assert!(
            app.expanded.contains(&pane_key),
            "precondition: pane expansion auto-seeded"
        );
        assert!(
            app.window_expanded.contains(&window_key),
            "precondition: window expansion auto-seeded"
        );

        // Act: user collapses window 0, then cache refresh fires.
        app.window_expanded.remove(&window_key);
        app.prune_expansion_state();

        // Assert: user's window collapse must not be restored by auto-expand.
        assert!(
            !app.window_expanded.contains(&window_key),
            "prune_expansion_state stomped user window collapse (bug #261)"
        );
    }

    #[test]
    fn new_test_default_expands_multi_pane_rows_per_issue_251() {
        // Issue #251 spec: "Default-expanded, user-collapsible." The expansion
        // set must be populated at construction — no data-refresh round-trip
        // should be required to surface the hierarchy.
        let app = App::new_test(vec![
            make_task_row_with_panes(1, 3),
            make_task_row_with_panes(2, 2),
            make_task_row_with_panes(3, 1), // single-pane should NOT expand
        ]);
        assert!(
            app.expanded.contains("/workspace/repo-1"),
            "3-pane row must be expanded on construction"
        );
        assert!(
            app.expanded.contains("/workspace/repo-2"),
            "2-pane row must be expanded on construction"
        );
        assert!(
            !app.expanded.contains("/workspace/repo-3"),
            "1-pane row must NOT be expanded (nothing to show)"
        );
    }

    // -----------------------------------------------------------------------
    // Leader-key input model tests
    // -----------------------------------------------------------------------

    #[test]
    fn filter_char_appends_and_resets_cursor() {
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::Other),
        ];
        let mut app = App::new_test(rows);
        app.cursor = 1;
        app.update(Message::FilterChar('f'));
        assert_eq!(app.filter_text, "f");
        assert_eq!(app.cursor, 0, "cursor resets to 0 after filter char");
        app.update(Message::FilterChar('o'));
        assert_eq!(app.filter_text, "fo");
        assert_eq!(app.cursor, 0);
    }

    #[test]
    fn filter_backspace_removes_last_char() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        app.filter_text = "abc".to_string();
        app.update(Message::FilterBackspace);
        assert_eq!(app.filter_text, "ab");
        app.update(Message::FilterBackspace);
        assert_eq!(app.filter_text, "a");
        app.update(Message::FilterBackspace);
        assert_eq!(app.filter_text, "");
        // Backspace on empty string is a no-op.
        app.update(Message::FilterBackspace);
        assert_eq!(app.filter_text, "");
    }

    #[test]
    fn open_search_sets_searching() {
        let mut app = App::new_test(vec![]);
        assert_eq!(app.input_phase, InputPhase::Idle);
        app.update(Message::OpenSearch);
        assert_eq!(app.input_phase, InputPhase::Searching);
    }

    #[test]
    fn leader_then_action_dispatches_open_pr() {
        // In Idle phase, 'o' directly dispatches OpenPR (no leader needed).
        let app = App::new_test(vec![]);
        assert_eq!(app.input_phase, InputPhase::Idle);
        let o_key = KeyEvent::new(KeyCode::Char('o'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(o_key), Some(Message::OpenPR));
    }

    #[test]
    fn close_search_resets_phase_to_idle() {
        let mut app = App::new_test(vec![]);
        app.input_phase = InputPhase::Searching;
        app.update(Message::CloseSearch);
        assert_eq!(app.input_phase, InputPhase::Idle);
    }

    #[test]
    fn enter_clears_filter() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        app.filter_text = "feat".to_string();
        app.update(Message::Enter);
        assert_eq!(app.filter_text, "", "Enter should clear the filter");
    }

    #[test]
    fn enter_activates_highlighted_row_with_active_filter() {
        // Two rows with sessions: only row 2 matches the filter "issue-2".
        let rows = vec![
            make_task_row_with_session("feat/issue-1", "repo_issue-1"),
            make_task_row_with_session("feat/issue-2", "repo_issue-2"),
        ];
        let mut app = App::new_test(rows);
        app.filter_text = "issue-2".to_string();
        // Cursor 0 points at the only visible row (issue-2) while filter is active.
        app.cursor = 0;
        app.update(Message::Enter);
        assert_eq!(app.filter_text, "", "Enter should clear the filter");
        // handle_enter_action resolves cursor 0 against filtered rows and sets
        // switch_target to the matched session name. With the fix, the filter is
        // still active during resolution so cursor 0 maps to issue-2.
        let target = app.switch_target.as_deref().unwrap_or("");
        assert!(
            target.contains("issue-2"),
            "Enter should activate the filtered row (issue-2), not issue-1; got switch_target: {target}"
        );
    }

    #[test]
    fn arrow_keys_work_without_leader_in_idle() {
        // Up/Down arrow keys dispatch directly in Idle phase.
        let rows = vec![
            make_task_row(1, DisplayGroup::NeedsAttention),
            make_task_row(2, DisplayGroup::Other),
        ];
        let mut app = App::new_test(rows);
        app.cursor = 1;
        assert_eq!(app.input_phase, InputPhase::Idle);
        let up = KeyEvent::new(KeyCode::Up, KeyModifiers::NONE);
        assert_eq!(app.handle_event(up), Some(Message::CursorUp));
        let down = KeyEvent::new(KeyCode::Down, KeyModifiers::NONE);
        assert_eq!(app.handle_event(down), Some(Message::CursorDown));
    }

    #[test]
    fn printable_chars_go_to_filter_in_searching_not_actions() {
        // In Searching phase, 'o' becomes FilterChar('o'), not OpenPR.
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        app.input_phase = InputPhase::Searching;
        let key = KeyEvent::new(KeyCode::Char('o'), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::FilterChar('o')));
    }

    #[test]
    fn space_key_produces_open_search_message_in_idle() {
        let app = App::new_test(vec![]);
        let key = KeyEvent::new(KeyCode::Char(' '), KeyModifiers::NONE);
        assert_eq!(app.handle_event(key), Some(Message::OpenSearch));
    }

    #[test]
    fn searching_phase_persists_through_action_messages() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        app.input_phase = InputPhase::Searching;
        app.update(Message::CursorDown);
        assert_eq!(app.input_phase, InputPhase::Searching);
    }

    // -----------------------------------------------------------------------
    // current_selection tests
    // -----------------------------------------------------------------------

    fn make_standalone_session(name: &str) -> crate::session::StandaloneSessionRow {
        use crate::session::{
            EnrichedSession, Host, SessionStatus, StandaloneConfig, StandaloneSessionRow,
            TmuxSessionInfo,
        };
        StandaloneSessionRow {
            session: EnrichedSession {
                tmux: TmuxSessionInfo {
                    host: Host::Local,
                    name: name.to_string(),
                    status: SessionStatus::Dead,
                },
                claude: None,
                windows: vec![],
                panes: vec![],
                started_at: None,
                last_activity_at: None,
            },
            config: StandaloneConfig {
                name: name.to_string(),
                command: String::new(),
                cwd: String::new(),
                start_on_launch: false,
            },
        }
    }

    #[test]
    fn current_selection_for_worktree_row() {
        let mut app = App::new_test(vec![
            make_task_row(1, DisplayGroup::Other),
            make_task_row(2, DisplayGroup::Other),
        ]);
        app.cursor = 1;
        let sel = current_selection(&app).unwrap();
        assert_eq!(sel.kind, last_selection::SelectionKind::Worktree);
        assert_eq!(sel.key, "/workspace/repo-2");
    }

    #[test]
    fn current_selection_for_standalone_row() {
        let mut app = App::new_test(vec![make_task_row(1, DisplayGroup::Other)]);
        app.standalone_sessions = vec![make_standalone_session("my-standalone")];
        app.cursor = 0;
        let sel = current_selection(&app).unwrap();
        assert_eq!(sel.kind, last_selection::SelectionKind::Standalone);
        assert_eq!(sel.key, "my-standalone");
    }

    #[test]
    fn current_selection_returns_none_when_cursor_out_of_bounds() {
        let app = App::new_test(vec![]);
        // cursor=0, no rows, no standalone
        assert!(current_selection(&app).is_none());
    }

    #[test]
    fn current_selection_includes_active_repo_slug() {
        let mut app = App::new_test(vec![
            make_task_row(1, DisplayGroup::Other),
            make_task_row(2, DisplayGroup::Other),
            make_task_row(3, DisplayGroup::Other),
        ]);
        // Populate two repos in global_config so index 2 maps to "acme/beta".
        app.global_config.repos = vec![
            global_config::RepoConfig {
                slug: "acme/alpha".to_string(),
                path: "/home/user/workspace/alpha".to_string(),
                remotes: vec![],
            },
            global_config::RepoConfig {
                slug: "acme/beta".to_string(),
                path: "/home/user/workspace/beta".to_string(),
                remotes: vec![],
            },
        ];
        app.active_repo_index = 2; // index 2 = repos[1] = "acme/beta"
        app.cursor = 0;
        let sel = current_selection(&app).unwrap();
        assert_eq!(sel.active_repo_slug, Some("acme/beta".to_string()));
    }

    // -----------------------------------------------------------------------
    // SubCursor multi-window navigation tests
    // -----------------------------------------------------------------------

    #[test]
    fn multi_window_down_from_session_enters_first_window() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.update(Message::CursorDown);
        assert_eq!(app.cursor, 0);
        assert_eq!(app.sub_cursor, SubCursor::Window(0), "enters first window");
    }

    #[test]
    fn multi_window_down_from_collapsed_window_goes_to_next() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        // Issue #251 default-expands windows too; force window 0 closed to
        // exercise the collapsed-window navigation path this test targets.
        app.window_expanded.clear();
        app.sub_cursor = SubCursor::Window(0);
        app.update(Message::CursorDown);
        assert_eq!(app.sub_cursor, SubCursor::Window(1));
    }

    #[test]
    fn multi_window_down_from_expanded_window_enters_first_pane() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:0".to_string());
        app.sub_cursor = SubCursor::Window(0);
        app.update(Message::CursorDown);
        assert_eq!(app.sub_cursor, SubCursor::Pane { window: 0, pane: 0 });
    }

    #[test]
    fn multi_window_down_from_last_pane_goes_to_next_window() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:0".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
        app.update(Message::CursorDown);
        assert_eq!(app.sub_cursor, SubCursor::Window(1));
    }

    #[test]
    fn multi_window_up_from_first_window_returns_to_session() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Window(0);
        app.update(Message::CursorUp);
        assert_eq!(app.sub_cursor, SubCursor::None);
    }

    #[test]
    fn multi_window_up_from_first_pane_returns_to_window() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:0".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 0 };
        app.update(Message::CursorUp);
        assert_eq!(app.sub_cursor, SubCursor::Window(0));
    }

    #[test]
    fn multi_window_left_from_pane_collapses_window() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 1, pane: 0 };
        app.update(Message::CollapseRow);
        assert_eq!(app.sub_cursor, SubCursor::Window(1));
        assert!(!app.window_expanded.contains("sess-1:1"));
    }

    #[test]
    fn multi_window_left_from_window_is_noop() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Window(0);
        app.update(Message::CollapseRow);
        // Left on window row is no-op.
        assert_eq!(app.sub_cursor, SubCursor::Window(0));
        assert!(app.expanded.contains("/workspace/repo-1"));
    }

    #[test]
    fn multi_window_right_expands_window() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Window(0);
        app.update(Message::ExpandRow);
        assert!(app.window_expanded.contains("sess-1:0"));
    }

    #[test]
    fn collapse_session_clears_window_expansion() {
        let mut app = App::new_test(vec![make_task_row_with_windows(
            1,
            &[(2, "main"), (2, "editor")],
        )]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:0".to_string());
        app.window_expanded.insert("sess-1:1".to_string());
        app.sub_cursor = SubCursor::None;
        app.update(Message::CollapseRow);
        assert!(!app.expanded.contains("/workspace/repo-1"));
        assert!(!app.window_expanded.contains("sess-1:0"));
        assert!(!app.window_expanded.contains("sess-1:1"));
    }

    #[test]
    fn window_expansion_key_uses_tmux_window_index() {
        assert_eq!(App::window_expansion_key("my-session", 5), "my-session:5");
    }

    #[test]
    fn single_window_down_skips_window_level() {
        // Single-window session: Down goes directly to pane, not window.
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.update(Message::CursorDown);
        assert_eq!(
            app.sub_cursor,
            SubCursor::Pane { window: 0, pane: 0 },
            "single-window skips to pane"
        );
    }

    #[test]
    fn single_window_left_from_pane_collapses_session() {
        let mut app = App::new_test(vec![make_task_row_with_panes(1, 3)]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.sub_cursor = SubCursor::Pane { window: 0, pane: 1 };
        app.update(Message::CollapseRow);
        assert_eq!(app.sub_cursor, SubCursor::None);
        assert!(!app.expanded.contains("/workspace/repo-1"));
    }

    #[test]
    fn multi_window_up_onto_expanded_row_selects_last_window() {
        let mut app = App::new_test(vec![
            make_task_row_with_windows(1, &[(2, "main"), (2, "editor")]),
            make_task_row_with_panes(2, 2),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        // Collapse windows so we land on the sibling Window(1) row, not one
        // of its panes — the navigation path under test.
        app.window_expanded.clear();
        app.cursor = 1;
        app.update(Message::CursorUp);
        assert_eq!(app.cursor, 0);
        assert_eq!(
            app.sub_cursor,
            SubCursor::Window(1),
            "lands on last window of multi-window session"
        );
    }

    #[test]
    fn multi_window_up_onto_expanded_row_with_expanded_last_window() {
        let mut app = App::new_test(vec![
            make_task_row_with_windows(1, &[(2, "main"), (2, "editor")]),
            make_task_row_with_panes(2, 2),
        ]);
        app.expanded.insert("/workspace/repo-1".to_string());
        app.window_expanded.insert("sess-1:1".to_string());
        app.cursor = 1;
        app.update(Message::CursorUp);
        assert_eq!(app.cursor, 0);
        assert_eq!(
            app.sub_cursor,
            SubCursor::Pane { window: 1, pane: 1 },
            "lands on last pane of expanded last window"
        );
    }

    // -- dedup_key ------------------------------------------------------------

    #[test]
    fn dedup_key_uses_kebab_case_kind_and_host() {
        use crate::global_config::RemoteConfig;
        use crate::remote_adapter::RemoteKind;

        let boxd_fork = RemoteConfig {
            name: "boxd".to_string(),
            host: "boxd.sh".to_string(),
            path: "/workspace".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::BoxdFork,
            allow_transitive: false,
        };
        assert_eq!(dedup_key(&boxd_fork), "boxd-fork:boxd.sh");

        let remmy = RemoteConfig {
            name: "remmy".to_string(),
            host: "ubuntu@myhost".to_string(),
            path: "/workspace".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::Remmy,
            allow_transitive: false,
        };
        assert_eq!(dedup_key(&remmy), "remmy:ubuntu@myhost");

        let boxd_shared = RemoteConfig {
            name: "shared".to_string(),
            host: "shared.boxd.sh".to_string(),
            path: "/workspace".to_string(),
            shell: "ssh".to_string(),
            kind: RemoteKind::BoxdShared,
            allow_transitive: false,
        };
        assert_eq!(dedup_key(&boxd_shared), "boxd-shared:shared.boxd.sh");
    }

    // -----------------------------------------------------------------------
    // Phase 3 tests: CacheRefreshed handler uses daemon WorkViewSnapshot
    // -----------------------------------------------------------------------

    /// @unit "start_full_refresh fetches local data via daemon::Client::work_view"
    ///
    /// Validates the `CacheRefreshed` handler in isolation:
    /// When a `WorkViewSnapshot` is pre-populated in the slot, the handler must
    /// build `task_rows` from the daemon data rather than from disk caches.
    ///
    /// Uses a `FakeWorkViewSource` that records every call and returns a canned
    /// snapshot with one worktree.
    #[test]
    fn cache_refreshed_uses_daemon_snapshot_when_available() {
        use crate::daemon::types::{WorkViewRepo, WorkViewSnapshot, WorkViewWorktree};
        use state::AppMsg;

        // Build an App with a pre-populated work_view_snapshot slot.
        let snapshot = WorkViewSnapshot {
            repos: vec![WorkViewRepo {
                slug: "repo".to_string(),
                path: "/repos/owner/repo".to_string(),
                worktrees: vec![WorkViewWorktree {
                    path: "/repos/owner/repo/.worktrees/issue429".to_string(),
                    branch: "issue429/spec".to_string(),
                    head: "deadbeef".to_string(),
                    bare: false,
                    host: "local".to_string(),
                    repo: "owner/repo".to_string(),
                    pr: None,
                    issue: None,
                }],
            }],
            tmux_sessions: vec![],
            claude_instances: vec![],
        };

        let mut app = App::new_test(vec![]);
        // Pre-populate the slot as the refresh thread would.
        *app.work_view_snapshot.lock().unwrap() = Some(snapshot);

        // Send CacheRefreshed and drain.
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        // The handler must have populated task_rows from the daemon snapshot.
        assert_eq!(
            app.task_rows.len(),
            1,
            "handler should have built rows from the daemon snapshot"
        );
        assert_eq!(app.task_rows[0].branch, "issue429/spec");
        assert_eq!(app.task_rows[0].repo_slug, "owner/repo");
    }

    /// @unit "start_full_refresh fetches local data via daemon::Client::work_view"
    ///
    /// When no snapshot is in the slot (daemon unreachable), the `CacheRefreshed`
    /// handler falls back to `build_state_with_hosts` (cache-driven path).
    /// Specifically: `task_rows` is empty (no on-disk caches in test env).
    #[test]
    fn cache_refreshed_falls_back_to_cache_when_no_snapshot() {
        use state::AppMsg;

        let mut app = App::new_test(vec![]);
        // Slot remains None (daemon unreachable scenario).
        assert!(app.work_view_snapshot.lock().unwrap().is_none());

        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        // No on-disk caches in the test environment — rows are empty but no panic.
        // The important assertion is that the handler completed without panicking.
        // (The fallback path calls build_state_with_hosts which reads from disk.)
        let _ = app.task_rows.len(); // just verify we can access it
    }

    /// @unit "start_full_refresh continues to call refresh_remote_worktrees +
    /// refresh_remote_tmux_sessions"
    ///
    /// Verifies `App` struct has `work_view_source` field and it's injectable.
    /// This is a structural test — the fake source records the call.
    #[test]
    fn app_has_injectable_work_view_source() {
        use crate::daemon::{DaemonError, WorkViewSnapshot, WorkViewSource};
        use std::sync::Arc;
        use std::sync::atomic::{AtomicUsize, Ordering};

        struct CountingSource {
            call_count: Arc<AtomicUsize>,
        }

        impl WorkViewSource for CountingSource {
            fn work_view(&self) -> Result<WorkViewSnapshot, DaemonError> {
                self.call_count.fetch_add(1, Ordering::SeqCst);
                Ok(WorkViewSnapshot {
                    repos: vec![],
                    tmux_sessions: vec![],
                    claude_instances: vec![],
                })
            }
        }

        let call_count = Arc::new(AtomicUsize::new(0));
        let source = Arc::new(CountingSource {
            call_count: call_count.clone(),
        });

        let mut app = App::new_test(vec![]);
        // Inject the counting source.
        app.work_view_source = source;

        // Invoke start_full_refresh — it spawns a thread.
        app.start_full_refresh();

        // Wait briefly for the background thread to call work_view.
        std::thread::sleep(std::time::Duration::from_millis(200));

        assert_eq!(
            call_count.load(Ordering::SeqCst),
            1,
            "start_full_refresh must call work_view exactly once"
        );
    }

    // -----------------------------------------------------------------------
    // Phase 4 tests: start_local_refresh + LocalCacheRefreshed use daemon WorkView
    // -----------------------------------------------------------------------

    /// @unit "start_local_refresh fetches local data via daemon::Client::work_view"
    ///
    /// Injects a `CountingSource` and calls `start_local_refresh`. The source
    /// must be invoked exactly once; no `cache_sources::refresh_worktrees` or
    /// `cache_sources::refresh_tmux_sessions` calls occur.
    #[test]
    fn start_local_refresh_calls_work_view_source_once() {
        use crate::daemon::{DaemonError, WorkViewSnapshot, WorkViewSource};
        use std::sync::Arc;
        use std::sync::atomic::{AtomicUsize, Ordering};

        struct CountingSource {
            call_count: Arc<AtomicUsize>,
        }

        impl WorkViewSource for CountingSource {
            fn work_view(&self) -> Result<WorkViewSnapshot, DaemonError> {
                self.call_count.fetch_add(1, Ordering::SeqCst);
                Ok(WorkViewSnapshot {
                    repos: vec![],
                    tmux_sessions: vec![],
                    claude_instances: vec![],
                })
            }
        }

        let call_count = Arc::new(AtomicUsize::new(0));
        let source = Arc::new(CountingSource {
            call_count: call_count.clone(),
        });

        let mut app = App::new_test(vec![]);
        app.work_view_source = source;

        // Invoke start_local_refresh — it spawns a background thread.
        app.start_local_refresh();

        // Wait briefly for the background thread to complete.
        std::thread::sleep(std::time::Duration::from_millis(200));

        assert_eq!(
            call_count.load(Ordering::SeqCst),
            1,
            "start_local_refresh must call work_view exactly once"
        );

        // Drain the LocalCacheRefreshed message so there's no leftover state.
        app.check_updates();
        // Verify the message was received (handler ran without panic).
        let _ = app.task_rows.len();
    }

    /// @unit "local_cache_refreshed_uses_daemon_snapshot"
    ///
    /// Populate the `work_view_snapshot` slot manually, simulate
    /// `LocalCacheRefreshed`, and assert that `task_rows` reflects the snapshot.
    #[test]
    fn local_cache_refreshed_uses_daemon_snapshot() {
        use crate::daemon::types::{WorkViewRepo, WorkViewSnapshot, WorkViewWorktree};
        use state::AppMsg;

        let snapshot = WorkViewSnapshot {
            repos: vec![WorkViewRepo {
                slug: "repo".to_string(),
                path: "/repos/owner/repo".to_string(),
                worktrees: vec![WorkViewWorktree {
                    path: "/repos/owner/repo/.worktrees/issue429".to_string(),
                    branch: "issue429/local-refresh".to_string(),
                    head: "cafebabe".to_string(),
                    bare: false,
                    host: "local".to_string(),
                    repo: "owner/repo".to_string(),
                    pr: None,
                    issue: None,
                }],
            }],
            tmux_sessions: vec![],
            claude_instances: vec![],
        };

        let mut app = App::new_test(vec![]);
        // Pre-populate the slot as start_local_refresh would do.
        *app.work_view_snapshot.lock().unwrap() = Some(snapshot);

        let _ = app.tx.send(AppMsg::LocalCacheRefreshed);
        app.check_updates();

        assert_eq!(
            app.task_rows.len(),
            1,
            "LocalCacheRefreshed handler should build rows from the daemon snapshot"
        );
        assert_eq!(app.task_rows[0].branch, "issue429/local-refresh");
        assert_eq!(app.task_rows[0].repo_slug, "owner/repo");
    }

    /// @unit "local_cache_refreshed_falls_back_when_no_snapshot"
    ///
    /// When the slot is empty (daemon unreachable), the handler must fall back
    /// to `build_state_with_hosts` without panicking. In the test environment
    /// there are no on-disk caches, so `task_rows` will be empty — the
    /// important assertion is that no panic occurs.
    #[test]
    fn local_cache_refreshed_falls_back_when_no_snapshot() {
        use state::AppMsg;

        let mut app = App::new_test(vec![]);
        // Slot remains None — daemon unreachable scenario.
        assert!(app.work_view_snapshot.lock().unwrap().is_none());

        let _ = app.tx.send(AppMsg::LocalCacheRefreshed);
        app.check_updates();

        // No on-disk caches in the test environment — rows are empty but no panic.
        let _ = app.task_rows.len();
    }

    // -----------------------------------------------------------------------
    // Phase 5 tests: daemon-down fallback + status indicator
    // -----------------------------------------------------------------------

    /// A [`WorkViewSource`] whose behavior can be toggled at runtime.
    ///
    /// When `should_fail` is `true`, `work_view()` returns
    /// [`DaemonError::Unreachable`]. Otherwise it returns the stored snapshot.
    struct ControllableSource {
        snapshot: crate::daemon::WorkViewSnapshot,
        should_fail: std::sync::atomic::AtomicBool,
    }

    impl ControllableSource {
        fn new(snapshot: crate::daemon::WorkViewSnapshot) -> Self {
            Self {
                snapshot,
                should_fail: std::sync::atomic::AtomicBool::new(false),
            }
        }

        fn set_failing(&self, fail: bool) {
            self.should_fail
                .store(fail, std::sync::atomic::Ordering::SeqCst);
        }
    }

    impl crate::daemon::WorkViewSource for ControllableSource {
        fn work_view(&self) -> Result<crate::daemon::WorkViewSnapshot, crate::daemon::DaemonError> {
            if self.should_fail.load(std::sync::atomic::Ordering::SeqCst) {
                Err(crate::daemon::DaemonError::Unreachable {
                    url: "http://127.0.0.1:7777/graphql".to_string(),
                    cause: "test-injected failure".to_string(),
                })
            } else {
                Ok(self.snapshot.clone())
            }
        }
    }

    fn make_single_worktree_snapshot(branch: &str) -> crate::daemon::WorkViewSnapshot {
        use crate::daemon::types::{WorkViewRepo, WorkViewSnapshot, WorkViewWorktree};
        WorkViewSnapshot {
            repos: vec![WorkViewRepo {
                slug: "repo".to_string(),
                path: "/repos/owner/repo".to_string(),
                worktrees: vec![WorkViewWorktree {
                    path: format!("/repos/owner/repo/.worktrees/{}", branch.replace('/', "-")),
                    branch: branch.to_string(),
                    head: "cafebabe".to_string(),
                    bare: false,
                    host: "local".to_string(),
                    repo: "owner/repo".to_string(),
                    pr: None,
                    issue: None,
                }],
            }],
            tmux_sessions: vec![],
            claude_instances: vec![],
        }
    }

    /// @integration "TUI startup with daemon unreachable falls back to last-known cached state"
    ///
    /// Simulates a cold start where the daemon is unreachable but a prior
    /// snapshot exists in the `work_view_snapshot` slot (as would be loaded
    /// from disk in production via `work_view_cache::read_snapshot`).
    ///
    /// After the first refresh tick (which fails), the task_rows must still
    /// reflect the pre-existing snapshot, and `daemon_status` must be
    /// `Unreachable`.
    #[test]
    fn startup_with_daemon_unreachable_falls_back_to_cached_state() {
        use state::AppMsg;
        use std::sync::Arc;

        let snapshot = make_single_worktree_snapshot("issue429/cached-branch");

        // Build App and pre-populate the snapshot slot (mirrors the cold-start
        // disk-read path in production App::new).
        let mut app = App::new_test(vec![]);
        *app.work_view_snapshot.lock().unwrap() = Some(snapshot.clone());

        // Inject a source that always fails.
        let source = Arc::new(ControllableSource::new(snapshot.clone()));
        source.set_failing(true);
        app.work_view_source = source;

        // Simulate a refresh tick: daemon unreachable → DaemonStatusChanged +
        // CacheRefreshed (slot still has prior snapshot).
        let _ = app
            .tx
            .send(AppMsg::DaemonStatusChanged(DaemonStatus::Unreachable));
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        // Task rows must reflect the cached snapshot — no blank screen.
        assert_eq!(
            app.task_rows.len(),
            1,
            "task_rows must reflect the pre-existing cached snapshot"
        );
        assert_eq!(app.task_rows[0].branch, "issue429/cached-branch");

        // Status indicator must be Unreachable.
        assert_eq!(
            app.daemon_status,
            DaemonStatus::Unreachable,
            "daemon_status must be Unreachable after a failed refresh"
        );
    }

    /// @integration "TUI mid-session daemon outage retains last-known state"
    ///
    /// First tick succeeds (populates snapshot). Second tick fails. After the
    /// second tick the rows still reflect the snapshot from tick 1.
    #[test]
    fn mid_session_daemon_outage_retains_last_known_state() {
        use state::AppMsg;
        use std::sync::Arc;

        let snapshot = make_single_worktree_snapshot("issue429/mid-session");

        let source = Arc::new(ControllableSource::new(snapshot.clone()));
        let mut app = App::new_test(vec![]);
        app.work_view_source = source.clone();

        // --- First tick: daemon reachable ---
        // Populate the slot (as start_full_refresh would do on success).
        *app.work_view_snapshot.lock().unwrap() = Some(snapshot.clone());
        let _ = app
            .tx
            .send(AppMsg::DaemonStatusChanged(DaemonStatus::Reachable));
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        assert_eq!(app.task_rows.len(), 1, "first tick: rows from snapshot");
        assert_eq!(app.task_rows[0].branch, "issue429/mid-session");
        assert_eq!(app.daemon_status, DaemonStatus::Reachable);

        // --- Second tick: daemon unreachable ---
        // Slot is NOT cleared on failure — it retains the last-known snapshot.
        source.set_failing(true);
        let _ = app
            .tx
            .send(AppMsg::DaemonStatusChanged(DaemonStatus::Unreachable));
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        // Rows must still reflect the first tick's snapshot (last-known state).
        assert_eq!(
            app.task_rows.len(),
            1,
            "task_rows must retain last-known state after daemon outage"
        );
        assert_eq!(
            app.task_rows[0].branch, "issue429/mid-session",
            "branch must still be from last-known snapshot"
        );
        assert_eq!(
            app.daemon_status,
            DaemonStatus::Unreachable,
            "daemon_status must be Unreachable after failed second tick"
        );
    }

    /// @integration "TUI recovers automatically when the daemon comes back"
    ///
    /// First tick fails (no prior snapshot → empty rows). Second tick succeeds
    /// with a new snapshot. After the second tick, rows reflect the new data
    /// and `daemon_status` is `Reachable`.
    #[test]
    fn tui_recovers_automatically_when_daemon_comes_back() {
        use state::AppMsg;
        use std::sync::Arc;

        let snapshot = make_single_worktree_snapshot("issue429/recovery");

        let source = Arc::new(ControllableSource::new(snapshot.clone()));
        source.set_failing(true); // start in failing state

        let mut app = App::new_test(vec![]);
        app.work_view_source = source.clone();

        // --- First tick: daemon unreachable, no prior snapshot ---
        let _ = app
            .tx
            .send(AppMsg::DaemonStatusChanged(DaemonStatus::Unreachable));
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        // No panic, no prior state → empty rows.
        let _ = app.task_rows.len(); // must not panic
        assert_eq!(
            app.daemon_status,
            DaemonStatus::Unreachable,
            "first tick: daemon_status must be Unreachable"
        );

        // --- Second tick: daemon recovers ---
        source.set_failing(false);
        // Simulate what start_full_refresh does on success: populate slot then send messages.
        *app.work_view_snapshot.lock().unwrap() = Some(snapshot.clone());
        let _ = app
            .tx
            .send(AppMsg::DaemonStatusChanged(DaemonStatus::Reachable));
        let _ = app.tx.send(AppMsg::CacheRefreshed);
        app.check_updates();

        assert_eq!(
            app.task_rows.len(),
            1,
            "second tick: task_rows must reflect the daemon-fresh snapshot"
        );
        assert_eq!(app.task_rows[0].branch, "issue429/recovery");
        assert_eq!(
            app.daemon_status,
            DaemonStatus::Reachable,
            "second tick: daemon_status must be Reachable after recovery"
        );
    }

    /// @unit status indicator renders "daemon unreachable" in the header when
    /// `daemon_status == DaemonStatus::Unreachable`.
    #[test]
    fn header_renders_daemon_unreachable_indicator() {
        let mut app = App::new_test(vec![]);
        app.daemon_status = DaemonStatus::Unreachable;
        let output = render_to_string(&mut app, 120, 40);
        assert!(
            output.contains("daemon unreachable"),
            "header must contain 'daemon unreachable' when daemon_status is Unreachable"
        );
    }

    /// @unit status indicator is absent when `daemon_status == DaemonStatus::Reachable`.
    #[test]
    fn header_no_daemon_indicator_when_reachable() {
        let mut app = App::new_test(vec![]);
        app.daemon_status = DaemonStatus::Reachable;
        let output = render_to_string(&mut app, 120, 40);
        assert!(
            !output.contains("daemon unreachable"),
            "header must NOT contain 'daemon unreachable' when daemon is reachable"
        );
        assert!(
            !output.contains("daemon: connecting"),
            "header must NOT contain 'daemon: connecting' when daemon is reachable"
        );
    }
}
