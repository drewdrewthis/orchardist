Feature: Full tmux session/window/pane hierarchy in TUI
  As an orchard user with multi-window tmux sessions
  I want to see the full session → window → pane hierarchy when expanding a session
  So I can navigate to a specific window and pane and understand the structure of my tmux sessions

  # Current state:
  #   - PaneInfo has tmux_target as "window.pane" (e.g. "1.2") but no WindowInfo struct
  #   - push_pane_sub_rows() renders panes flat under the session — no window grouping
  #   - Window names/titles are not captured from tmux
  #   - No window-level operations exist
  #   - pane_fmt = "#{window_index}.#{pane_index}\t#{pane_title}:#{pane_current_command}"
  #   - CachedTmuxSession stores flat pane_targets, pane_titles, pane_commands
  #   - EnrichedSession has panes: Vec<PaneInfo> (flat)
  #
  # What changes:
  #   1. Data model: add WindowInfo struct, group panes by window
  #   2. Cache: extend CachedTmuxSession with window_names, window_active flags
  #   3. Tmux queries: extend pane_fmt to include #{window_name} and #{window_active}
  #   4. TUI: double nesting — session expands to windows, window expands to panes
  #   5. JSON output: sessions[].windows[].panes[] hierarchy
  #   6. Backward compat: single-window sessions auto-flatten to current behavior
  #
  # Files affected:
  #   - src/session.rs — add WindowInfo struct, update EnrichedSession
  #   - src/cache.rs — add window_names/window_active to CachedTmuxSession
  #   - src/cache_sources.rs — extend pane_fmt, parse_pane_lines → parse_pane_lines_with_windows
  #   - src/build_state.rs — group panes into windows when building EnrichedSession
  #   - src/tui/list.rs — push_window_sub_rows with nested push_pane_sub_rows
  #   - src/tui/mod.rs — two-level expansion state, window+pane navigation
  #   - src/json_output.rs — add JsonWindow, nest panes under windows
  #   - src/orchard_state.rs — add WindowState to SessionState
  #
  # Design decisions (from /challenge review):
  #
  #   Cursor model: SubCursor enum replaces Option<usize> selected_pane
  #     SubCursor::None — parent row selected
  #     SubCursor::Window(usize) — window sub-row selected (tmux window_index)
  #     SubCursor::Pane { window: usize, pane: usize } — pane selected (window tmux index + pane vec index)
  #     Auto-flatten: single-window sessions use SubCursor::Pane { window: 0, pane: n }
  #
  #   Navigation state transitions:
  #     Down from session row → SubCursor::Window(first_window) [or SubCursor::Pane(0, 0) if flattened]
  #     Down from Window(w) [collapsed] → Window(next_w) or None on next row
  #     Down from Window(w) [expanded] → Pane { w, 0 }
  #     Down from Pane { w, last } → Window(next_w) or None on next row
  #     Up from Pane { w, 0 } → Window(w) [or session row if flattened]
  #     Up from Window(first) → None (session row)
  #     Left from Pane → collapse window, cursor to Window(w) [or collapse session if flattened]
  #     Left from Window → no-op (must navigate to session row to collapse session)
  #     Right from Window → expand window
  #     Enter from Window → switch tmux to that window (not expand)
  #     Enter from Pane → select that specific pane + zoom
  #
  #   Backward compatibility:
  #     - EnrichedSession keeps denormalized `panes: Vec<PaneInfo>` alongside `windows`
  #       (derived from windows in constructor, avoids touching 28 call sites)
  #     - Cache upgrade: when window_names is empty, derive windows from pane_targets
  #       by parsing "window.pane" format, synthesize window name as "window:{idx}"
  #     - WindowInfo.index uses tmux's stable window_index (not sequential 0..N)
  #       so closing window 1 doesn't break expansion state for window 2
  #     - Window expansion key format: "session_name:window_index" (tmux index)
  #
  #   Mouse click mapping:
  #     - Visual-to-logical row mapping updated for variable sub-row counts
  #     - Sub-row count = sum of (1 per window + expanded_panes_per_window)
  #
  #   JSON:
  #     - SessionState in OrchardState gets WindowState for JSON conversion
  #     - JSON always shows full hierarchy (no auto-flattening)
  #     - Version bumps to 4
  #
  # Non-goals:
  #   - Window/pane creation and management operations
  #   - Fork session with Claude support
  #   - New session creation flow

  Background:
    Given the TUI is running with at least one repo configured
    And there are worktree rows with active tmux sessions

  # ===================================================================
  # 1. WindowInfo data model
  # ===================================================================

  @unit
  Scenario: WindowInfo groups panes by window index
    Given a tmux session with pane targets ["0.0", "0.1", "1.0", "1.1"]
    And window names ["main", "editor"]
    And window active flags [true, false]
    When windows are built from the pane data
    Then there are 2 WindowInfo entries
    And window 0 has name "main", is_active true, and 2 panes
    And window 1 has name "editor", is_active false, and 2 panes

  @unit
  Scenario: WindowInfo contains correct pane references
    Given a tmux session with pane targets ["0.0", "0.1", "1.0"]
    And window names ["shell", "code"]
    When windows are built from the pane data
    Then window 0 contains panes with targets "0.0" and "0.1"
    And window 1 contains pane with target "1.0"

  @unit
  Scenario: Single-window session produces one WindowInfo
    Given a tmux session with pane targets ["0.0", "0.1"]
    And window names ["main"]
    When windows are built from the pane data
    Then there is 1 WindowInfo entry with 2 panes

  @unit
  Scenario: Session with no panes produces empty windows list
    Given a tmux session with no panes
    When windows are built from the pane data
    Then the windows list is empty

  # ===================================================================
  # 2. EnrichedSession holds windows instead of flat panes
  # ===================================================================

  @unit
  Scenario: EnrichedSession contains windows with nested panes
    Given a CachedTmuxSession with pane targets ["0.0", "0.1", "1.0"]
    And window names ["main", "code"]
    When the session is enriched
    Then EnrichedSession.windows has 2 entries
    And EnrichedSession.windows[0].panes has 2 PaneInfo entries
    And EnrichedSession.windows[1].panes has 1 PaneInfo entry

  @unit
  Scenario: EnrichedSession backward-compat helper returns all panes flat
    Given an EnrichedSession with 2 windows containing 3 total panes
    When all_panes() is called
    Then it returns a flat slice of 3 PaneInfo entries in window order

  # ===================================================================
  # 3. CachedTmuxSession captures window metadata
  # ===================================================================

  @unit
  Scenario: CachedTmuxSession includes window names and active flags
    Given tmux list-panes output includes window_name and window_active
    When the output is parsed
    Then CachedTmuxSession.window_names contains the window names per pane row
    And CachedTmuxSession.window_active contains "1" or "0" per pane row

  @unit
  Scenario: CachedTmuxSession serialization roundtrip with window fields
    Given a CachedTmuxSession with window_names ["main", "editor"]
    And window_active ["1", "0"]
    When serialized to JSON and deserialized back
    Then the window_names and window_active fields are preserved

  @unit
  Scenario: Missing window fields default to empty on deserialization
    Given a cached JSON file without window_names or window_active fields
    When it is deserialized as CachedTmuxSession
    Then window_names defaults to empty vec
    And window_active defaults to empty vec

  # ===================================================================
  # 4. Tmux query format extended
  # ===================================================================

  @unit
  Scenario: pane_fmt includes window_name and window_active
    When the tmux list-panes format string is constructed
    Then it includes "#{window_name}" and "#{window_active}"
    And the parse function extracts them into separate fields

  @unit
  Scenario: parse_pane_lines extracts window metadata alongside pane data
    Given tmux output "0.0\tmain\t1\tbash:bash\n0.1\tmain\t1\tclaude:claude\n1.0\teditor\t0\tnvim:nvim"
    When parse_pane_lines is called
    Then targets are ["0.0", "0.1", "1.0"]
    And window_names are ["main", "main", "editor"]
    And window_active are ["1", "1", "0"]
    And titles are ["bash", "claude", "nvim"]
    And commands are ["bash", "claude", "nvim"]

  # ===================================================================
  # 5. TUI display — double nesting
  # ===================================================================

  @unit
  Scenario: Expanding a multi-window session shows window sub-rows
    Given a worktree row with a session having 2 windows (3 total panes)
    And the session row is expanded
    When the table renders
    Then 2 window sub-rows appear immediately after the session row
    And no pane sub-rows are visible (windows are collapsed by default)

  @unit
  Scenario: Window sub-row shows window name and index
    Given an expanded session with window 0 named "main" and window 1 named "editor"
    When the window sub-rows render
    Then window sub-row 0 shows "├─ window:0 main" (or similar tree connector)
    And window sub-row 1 shows "└─ window:1 editor"

  @unit
  Scenario: Expanding a window sub-row shows its pane sub-rows
    Given an expanded session with window 0 having 2 panes
    And window 0 is expanded
    When the table renders
    Then 2 pane sub-rows appear nested under window 0
    And pane sub-rows use deeper tree connectors (e.g. "│ ├─ pane:0 zsh")

  @unit
  Scenario: Pane sub-rows under window show command and claude indicator
    Given an expanded window with pane 0 running "claude" and pane 1 running "nvim"
    When the pane sub-rows render
    Then pane 0 shows the lightning bolt Claude indicator
    And pane 1 shows a dash indicator
    And the TITLE cell shows the pane command

  @unit
  Scenario: Collapsed window hides its pane sub-rows
    Given an expanded session with window 0 collapsed
    When the table renders
    Then window 0's pane sub-rows are not visible
    But window 0's sub-row is still visible

  # ===================================================================
  # 6. Single-window auto-flatten
  # ===================================================================

  @unit
  Scenario: Single-window session skips the window level
    Given a worktree row with a session having only 1 window with 3 panes
    And the session row is expanded
    When the table renders
    Then pane sub-rows appear directly under the session row (no window row)
    And the tree connectors match the current pane-level format

  @unit
  Scenario: Multi-window session always shows window level
    Given a worktree row with a session having 2+ windows
    And the session row is expanded
    When the table renders
    Then window sub-rows appear (not direct pane sub-rows)

  # ===================================================================
  # 7. Two-level expansion state
  # ===================================================================

  @unit
  Scenario: Session expansion tracked by worktree path (existing behavior)
    Given the user expands a session for worktree at "/workspace/repo/feat-42"
    When the expansion set is checked
    Then "/workspace/repo/feat-42" is in the session expanded set

  @unit
  Scenario: Window expansion tracked by session_name:window_index key
    Given an expanded session "my-session" and the user expands window 0
    When the window expansion set is checked
    Then "my-session:0" is in the window expanded set

  @unit
  Scenario: Collapsing session clears its window expansion state
    Given session "my-session" is expanded with windows 0 and 1 expanded
    When the user collapses the session
    Then "my-session:0" and "my-session:1" are removed from the window expanded set

  @unit
  Scenario: Expansion state survives data refresh
    Given session "my-session" is expanded and window 0 is expanded
    When a cache refresh completes
    Then the session and window expansion states are preserved

  # ===================================================================
  # 8. Navigation into windows and panes
  # ===================================================================

  @unit
  Scenario: Down arrow from session row enters window sub-rows
    Given an expanded multi-window session
    When the user presses Down
    Then the cursor moves to the first window sub-row

  @unit
  Scenario: Down arrow from window row enters pane sub-rows when expanded
    Given an expanded window sub-row with 2 panes
    When the user presses Down
    Then the cursor moves to the first pane sub-row under that window

  @unit
  Scenario: Down arrow from collapsed window skips to next window
    Given an expanded session with window 0 collapsed and window 1
    When the cursor is on window 0 and the user presses Down
    Then the cursor moves to window 1

  @unit
  Scenario: Right arrow on window sub-row expands it
    Given the cursor is on a collapsed window sub-row with 2 panes
    When the user presses Right (or "l")
    Then the window expands and pane sub-rows appear

  @unit
  Scenario: Left arrow on window sub-row collapses it
    Given the cursor is on an expanded window sub-row
    When the user presses Left (or "h")
    Then the window collapses and pane sub-rows disappear

  @unit
  Scenario: Left arrow on pane sub-row collapses parent window
    Given the cursor is on a pane sub-row under window 1
    When the user presses Left
    Then window 1 collapses
    And the cursor moves to the window 1 sub-row

  @unit
  Scenario: Enter on window sub-row switches to that window
    Given the cursor is on window sub-row for window 1 of session "my-session"
    When the user presses Enter
    Then tmux switches to session "my-session" window 1

  @unit
  Scenario: Enter on pane sub-row switches to that specific pane
    Given the cursor is on pane sub-row for window 1, pane 0 of session "my-session"
    When the user presses Enter
    Then tmux switches to session "my-session" and selects pane at target "1.0"

  # ===================================================================
  # 9. JSON output with window hierarchy
  # ===================================================================

  @unit
  Scenario: JSON output includes windows array in sessions
    Given an OrchardState with a session having 2 windows
    When serialized to JSON via --json
    Then the session object contains a "windows" array with 2 entries
    And each window has "index", "name", "isActive", and "panes" fields

  @unit
  Scenario: JSON window panes contain expected fields
    Given a window with 2 panes including one running "claude"
    When serialized to JSON
    Then each pane object has "index", "tmuxTarget", "command", "title", "hasClaude"
    And the Claude pane has "hasClaude": true

  @unit
  Scenario: JSON version bumps to 4
    When OrchardState is serialized to JSON
    Then the version field is 4

  @unit
  Scenario: Single-window session still shows windows array in JSON
    Given a session with only 1 window
    When serialized to JSON
    Then the session still contains a "windows" array with 1 entry
    # JSON always shows the full hierarchy, no auto-flattening

  # ===================================================================
  # 10. Keybinds
  # ===================================================================

  @unit
  Scenario: h/l and Left/Right work at both session and window levels
    Given a multi-window session is selected
    When the user presses Right to expand the session
    Then window sub-rows appear
    When the user navigates to window 0 and presses Right
    Then pane sub-rows appear under window 0
    When the user presses Left
    Then window 0 collapses
    When the user presses Left again (on window row)
    Then it does nothing (session collapse requires moving to session row)

  @unit
  Scenario: d key on session row kills the session (existing behavior)
    Given the cursor is on a session row
    When the user presses d
    Then the delete confirmation dialog appears for the session

  # ===================================================================
  # 11. Window-level expand indicator on window sub-rows
  # ===================================================================

  @unit
  Scenario: Multi-pane window shows collapsed indicator
    Given a window sub-row with 3 panes and the window is collapsed
    When the sub-row renders
    Then it shows a collapse indicator with pane count (e.g. "▶3")

  @unit
  Scenario: Multi-pane window shows expanded indicator
    Given a window sub-row with 3 panes and the window is expanded
    When the sub-row renders
    Then it shows an expand indicator (e.g. "▼3")

  @unit
  Scenario: Single-pane window shows no expand indicator
    Given a window sub-row with exactly 1 pane
    When the sub-row renders
    Then no expand/collapse indicator is shown for the window

  # ===================================================================
  # 12. Cache upgrade compatibility
  # ===================================================================

  @unit
  Scenario: Old cache without window fields degrades gracefully
    Given a CachedTmuxSession with pane_targets ["0.0", "0.1", "1.0"]
    And window_names is empty (old cache format)
    When the session is enriched
    Then windows are derived from pane_targets by parsing window indices
    And window 0 gets synthetic name "window:0" with 2 panes
    And window 1 gets synthetic name "window:1" with 1 pane

  @unit
  Scenario: Cache with window fields uses real window names
    Given a CachedTmuxSession with pane_targets ["0.0", "0.1", "1.0"]
    And window_names ["main", "main", "code"]
    When the session is enriched
    Then window 0 has name "main"
    And window 1 has name "code"

  # ===================================================================
  # 13. Window index stability (tmux window_index, not sequential)
  # ===================================================================

  @unit
  Scenario: WindowInfo.index uses tmux's stable window_index
    Given a tmux session with windows 0, 2, 5 (1, 3, 4 were closed)
    When windows are built
    Then WindowInfo entries have index 0, 2, 5 (not 0, 1, 2)

  @unit
  Scenario: Window expansion key uses tmux window_index
    Given a session with windows at indices 0 and 5
    And the user expands window 5
    Then the expansion key is "session:5" (not "session:1")

  # ===================================================================
  # 14. SubCursor state machine
  # ===================================================================

  @unit
  Scenario: SubCursor::None represents parent row selection
    Given the cursor is on a session row
    Then sub_cursor is SubCursor::None

  @unit
  Scenario: SubCursor::Window represents window sub-row selection
    Given the cursor is on window sub-row for window index 2
    Then sub_cursor is SubCursor::Window(2)

  @unit
  Scenario: SubCursor::Pane represents pane sub-row selection
    Given the cursor is on pane 1 within window index 2
    Then sub_cursor is SubCursor::Pane { window: 2, pane: 1 }

  @unit
  Scenario: Down from last pane of window lands on next window row
    Given expanded session with windows [0, 2] both expanded
    And cursor on last pane of window 0
    When user presses Down
    Then sub_cursor becomes SubCursor::Window(2)

  @unit
  Scenario: Up from first pane of window lands on window row
    Given cursor on first pane of window 2
    When user presses Up
    Then sub_cursor becomes SubCursor::Window(2)

  @unit
  Scenario: Up from first window lands on session row
    Given cursor on first window sub-row
    When user presses Up
    Then sub_cursor becomes SubCursor::None (session row)

  # ===================================================================
  # 15. Mouse click mapping with variable sub-row counts
  # ===================================================================

  @unit
  Scenario: Mouse click on window sub-row selects correct window
    Given an expanded session with 3 windows where window 0 is expanded (2 panes)
    When the user clicks the visual row for window 1
    Then sub_cursor becomes SubCursor::Window(window_1_index)

  @unit
  Scenario: Mouse click on pane sub-row selects correct pane
    Given an expanded session with window 0 expanded showing 2 pane sub-rows
    When the user clicks the visual row for the second pane
    Then sub_cursor becomes SubCursor::Pane { window: 0, pane: 1 }

  # ===================================================================
  # 16. Preview pane content follows cursor level
  # ===================================================================

  @unit
  Scenario: Preview shows active pane content when cursor is on window row
    Given cursor on window sub-row for window 1
    When preview content is fetched
    Then it captures from the active pane of window 1

  @unit
  Scenario: Preview shows specific pane content when cursor is on pane row
    Given cursor on pane sub-row for window 1, pane 0
    When preview content is fetched
    Then it captures from pane target "1.0"

  # ===================================================================
  # 17. Session-level expand indicator with windows
  # ===================================================================

  @unit
  Scenario: Multi-window session shows window count in expand indicator
    Given a session with 3 windows (5 total panes) and the session is collapsed
    When the session row renders
    Then the STATUS cell shows "▶3w" (window count, not pane count)

  @unit
  Scenario: Single-window session shows pane count in expand indicator
    Given a session with 1 window and 3 panes, collapsed
    When the session row renders
    Then the STATUS cell shows "▶3" (pane count, matching current behavior)
