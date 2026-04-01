Feature: Expandable pane sub-rows with column reorder
  As an orchard user with multi-pane tmux sessions
  I want to expand worktree rows to see individual panes
  So I can navigate to a specific pane and see what each one is running

  # Current state:
  #   - `CachedTmuxSession` already collects `pane_titles` and `pane_commands` per session
  #   - `enrich_session()` / `enrich_session_from_scraping()` detect Claude at session level
  #   - Only pane 0 content is displayed (preview panel captures a single pane)
  #   - Column order: BAR | # | ISSUE | TITLE | BRANCH | HOST | STATUS | CLAUDE
  #   - Standalone sessions numbered separately from worktree rows (duplicate #1, #2, etc.)
  #   - Left/Right keys cycle repos; Tab is unused
  #
  # What changes:
  #   1. Column reorder: move CLAUDE column after # (before ISSUE)
  #   2. Unified sequential numbering across standalone sessions and worktree rows
  #   3. Expandable pane sub-rows: Right/l expands, Left/h collapses
  #   4. Key remapping: Tab/Shift+Tab for repo cycling (frees Left/Right)
  #   5. Pane-level navigation, preview, and tmux switch
  #   6. Data model: add PaneInfo to EnrichedSession
  #   7. Remove Heal keybinding ('h') entirely — heal is a CLI subcommand, not a TUI action
  #
  # Files affected:
  #   - `src/session.rs` — add PaneInfo struct, thread into EnrichedSession
  #   - `src/derive.rs` — populate PaneInfo in enrich_session()
  #   - `src/tui/list.rs` — column reorder, sub-row rendering, expand/collapse indicators
  #   - `src/tui/mod.rs` — App state (expanded set), key remapping, pane navigation
  #   - `src/tui/message.rs` — new messages for expand/collapse
  #   - `src/tmux.rs` — add pane index parameter to capture_pane_content()
  #
  # Non-goals:
  #   - Mouse click on sub-rows routes to parent row (not pane-specific; future work)
  #   - No drag-and-drop reorder of columns
  #   - No pane splitting/creating from TUI

  # Cursor model:
  #   The cursor uses a two-level addressing scheme:
  #   - `cursor: usize` indexes into the logical list (standalone sessions + visible worktree rows)
  #   - `selected_pane: Option<usize>` tracks which sub-row pane is selected (None = parent row)
  #   This preserves all existing handlers that use `self.cursor - standalone_count`
  #   Sub-rows are rendered as separate Table Rows but do not affect the logical cursor index

  Background:
    Given the TUI is running with at least one repo configured
    And there are worktree rows with active tmux sessions

  # ===================================================================
  # 1. Column reorder: CLAUDE after #
  # ===================================================================

  @unit
  Scenario: CLAUDE column appears immediately after row number
    When the task list header renders
    Then the column order is BAR, #, CLAUDE, ISSUE, TITLE, BRANCH, HOST, STATUS
    And the CLAUDE column uses the same width constraint as before (Length 10)

  @unit
  Scenario: Worktree row cells follow the new column order
    Given a worktree row with issue #42, branch "feat/42-thing", and Claude status "Working"
    When the row renders
    Then the CLAUDE cell appears in position 2 (after BAR and #)
    And the ISSUE cell appears in position 3 (after CLAUDE)

  @unit
  Scenario: Standalone session row cells follow the new column order
    Given a standalone session with Claude active
    When the standalone row renders
    Then the CLAUDE cell appears in position 2 (after BAR and #)
    And the ISSUE cell is empty in position 3

  # ===================================================================
  # 2. Unified sequential numbering
  # ===================================================================

  @unit
  Scenario: Single sequential counter across standalone sessions and worktree rows
    Given 2 standalone sessions and 3 worktree rows
    When the table renders
    Then standalone sessions are numbered 1 and 2
    And worktree rows are numbered 3, 4, and 5
    And no number is repeated

  @unit
  Scenario: Sequential numbers update when standalone session is removed from input
    Given standalone session "shepherd" at number 1 and worktree row at number 2
    When the input set no longer includes the standalone session
    Then the worktree row is renumbered to 1

  # ===================================================================
  # 3. PaneInfo data model
  # ===================================================================

  @unit
  Scenario: PaneInfo contains pane index, command, and claude detection
    Given a CachedTmuxSession with pane_commands ["claude", "nvim", "cargo watch -x test"]
    And pane_titles ["claude", "nvim", "cargo"]
    When enrich_session produces an EnrichedSession
    Then the EnrichedSession contains 3 PaneInfo entries
    And PaneInfo at index 0 has has_claude true and command "claude"
    And PaneInfo at index 1 has has_claude false and command "nvim"
    And PaneInfo at index 2 has has_claude false and command "cargo watch -x test"

  @unit
  Scenario: Pane-level Claude detection checks both command and title for "claude"
    Given a pane with command "claude --model opus"
    When PaneInfo is constructed
    Then has_claude is true

  @unit
  Scenario: Pane-level Claude detection is case-insensitive
    Given a pane with command "Claude"
    When PaneInfo is constructed
    Then has_claude is true

  @unit
  Scenario: Non-Claude pane command yields has_claude false
    Given a pane with command "nvim src/main.rs"
    When PaneInfo is constructed
    Then has_claude is false

  @unit
  Scenario: Session with zero panes produces empty PaneInfo list
    Given a CachedTmuxSession with empty pane_commands
    When enrich_session produces an EnrichedSession
    Then the EnrichedSession contains 0 PaneInfo entries

  # ===================================================================
  # 4. Expand/collapse indicators on parent row
  # ===================================================================

  @unit
  Scenario: Multi-pane row shows collapsed indicator with pane count
    Given a worktree row with 3 panes and the row is collapsed
    When the row renders
    Then the STATUS cell ends with "▶3"

  @unit
  Scenario: Multi-pane row shows expanded indicator with pane count
    Given a worktree row with 3 panes and the row is expanded
    When the row renders
    Then the STATUS cell ends with "▼3"

  @unit
  Scenario: Single-pane row shows no expand/collapse indicator
    Given a worktree row with exactly 1 pane
    When the row renders
    Then the STATUS cell does not contain "▶" or "▼"

  @unit
  Scenario: Zero-pane row shows no expand/collapse indicator
    Given a worktree row with no active session (0 panes)
    When the row renders
    Then the STATUS cell does not contain "▶" or "▼"

  # ===================================================================
  # 5. Pane sub-row rendering
  # ===================================================================

  @unit
  Scenario: Expanded row shows pane sub-rows underneath
    Given a worktree row with 3 panes is expanded
    When the table renders
    Then 3 sub-rows appear immediately after the parent row
    And each sub-row is before the next worktree row or group header

  @unit
  Scenario: Sub-row # cell shows tree connector and pane index
    Given an expanded row with 3 panes
    When the sub-rows render
    Then sub-row 0 shows "├─0" in the # cell
    And sub-row 1 shows "├─1" in the # cell
    And sub-row 2 shows "└─2" in the # cell (last pane uses └─)

  @unit
  Scenario: Sub-row CLAUDE cell shows lightning for Claude panes
    Given a pane sub-row with has_claude true
    When the sub-row renders
    Then the CLAUDE cell displays the lightning bolt indicator

  @unit
  Scenario: Sub-row CLAUDE cell shows dash for non-Claude panes
    Given a pane sub-row with has_claude false
    When the sub-row renders
    Then the CLAUDE cell displays "─"

  @unit
  Scenario: Sub-row TITLE cell shows the running command
    Given a pane running "cargo watch -x test"
    When the sub-row renders
    Then the TITLE cell displays "cargo watch -x test"

  @unit
  Scenario: Sub-row ISSUE, BRANCH, HOST, and STATUS cells are empty
    Given any pane sub-row
    When the sub-row renders
    Then the ISSUE cell is empty
    And the BRANCH cell is empty
    And the HOST cell is empty (if column is visible)
    And the STATUS cell is empty

  @unit
  Scenario: Sub-row BAR cell inherits parent repo color
    Given a pane sub-row under a worktree from repo at config index 1
    When the sub-row renders
    Then the BAR cell uses the same repo color as the parent row

  @unit
  Scenario: Collapsed row hides pane sub-rows
    Given a worktree row with 3 panes is collapsed (default)
    When the table renders
    Then no sub-rows appear for that worktree row

  # ===================================================================
  # 6. Key remapping: Tab/Shift+Tab for repo cycling
  # ===================================================================

  @unit
  Scenario: Tab cycles to next repo
    Given the active repo index is 0 (ALL) and there are 3 repos
    When the user presses Tab
    Then the active repo index becomes 1

  @unit
  Scenario: Shift+Tab cycles to previous repo
    Given the active repo index is 1
    When the user presses Shift+Tab
    Then the active repo index becomes 0

  @unit
  Scenario: Left and Right keys no longer cycle repos
    Given the active repo index is 0
    When the user presses Right
    Then the active repo index remains 0
    And no PrevRepo/NextRepo message is dispatched

  # ===================================================================
  # 7. Expand/collapse key bindings
  # ===================================================================

  @unit
  Scenario: Right arrow expands a collapsed multi-pane row
    Given the cursor is on a collapsed worktree row with 3 panes
    When the user presses Right (or "l")
    Then the row becomes expanded
    And the pane sub-rows appear

  @unit
  Scenario: Left arrow collapses an expanded row
    Given the cursor is on an expanded worktree row
    When the user presses Left (or "h")
    Then the row becomes collapsed
    And the pane sub-rows disappear

  @unit
  Scenario: Right on already-expanded row is a no-op
    Given the cursor is on an already-expanded worktree row
    When the user presses Right
    Then nothing changes (row stays expanded)

  @unit
  Scenario: Left on already-collapsed row is a no-op
    Given the cursor is on a collapsed worktree row
    When the user presses Left
    Then nothing changes (row stays collapsed)

  @unit
  Scenario: Right on a single-pane row is a no-op
    Given the cursor is on a worktree row with exactly 1 pane
    When the user presses Right
    Then nothing changes (no expand/collapse for single-pane rows)

  @unit
  Scenario: Right arrow expands a standalone session with multiple panes
    Given the cursor is on a collapsed standalone session row with 3 panes
    When the user presses Right (or "l")
    Then the row becomes expanded
    And the pane sub-rows appear

  @unit
  Scenario: E key expands all multi-pane rows when any are collapsed
    Given there are 3 multi-pane rows, all collapsed
    When the user presses "E"
    Then all 3 rows become expanded

  @unit
  Scenario: E key collapses all multi-pane rows when all are expanded
    Given there are 3 multi-pane rows, all expanded
    When the user presses "E"
    Then all 3 rows become collapsed

  @unit
  Scenario: Cursor stays on same logical row when another row expands
    Given the cursor is on worktree row 3 (logical index 3)
    And worktree row 1 is collapsed with 3 panes
    When the user expands worktree row 1 (via E or navigating there)
    Then the cursor still points to worktree row 3
    And selected_pane is None

  @unit
  Scenario: Expand-all preserves cursor's logical position
    Given the cursor is on worktree row 2 (logical index 2) with selected_pane None
    When the user presses "E" to expand all
    Then the cursor remains on worktree row 2
    And selected_pane remains None
    And the rendered row for worktree 2 is visually highlighted

  @unit
  Scenario: Collapse-all preserves selected_pane for re-expansion
    Given the cursor is on worktree row 2 with selected_pane Some(1)
    When the user presses "E" to collapse all
    Then the cursor remains on worktree row 2
    And selected_pane remains Some(1)
    # When E is pressed again to expand, cursor returns to pane 1

  @unit
  Scenario: Left arrow on sub-row collapses parent and selects it
    Given the cursor is on worktree row 2 with selected_pane Some(1)
    When the user presses Left (or "h")
    Then the parent row collapses
    And selected_pane becomes None
    And the cursor remains on worktree row 2

  @unit
  Scenario: Heal keybinding removed entirely
    When the user presses "h"
    Then no Heal view opens (h is now collapse)
    # Heal is a CLI subcommand (orchard heal), not a TUI action

  # ===================================================================
  # 8. Navigation into sub-rows
  # ===================================================================

  @unit
  Scenario: Down arrow navigates into sub-rows when expanded
    Given the cursor is on an expanded worktree row with 3 panes
    When the user presses Down (or "j")
    Then the cursor moves to the first pane sub-row (pane 0)

  @unit
  Scenario: Down arrow from last sub-row moves to next worktree row
    Given the cursor is on the last pane sub-row of an expanded row
    When the user presses Down
    Then the cursor moves to the next worktree row (or group header)

  @unit
  Scenario: Up arrow from first sub-row moves back to parent row
    Given the cursor is on the first pane sub-row (pane 0)
    When the user presses Up (or "k")
    Then the cursor moves to the parent worktree row

  @unit
  Scenario: Sub-rows are skipped when parent is collapsed during navigation
    Given a collapsed worktree row between two other rows
    When the user navigates Down past it
    Then the cursor skips directly to the next worktree/standalone row

  @unit
  Scenario: Digit-jump targets parent rows, ignoring sub-rows
    Given worktree rows numbered 1, 2, 3 and row 1 is expanded with 3 sub-rows
    When the user presses "3"
    Then the cursor moves to worktree row 3 (logical index matching number 3)
    And selected_pane becomes None
    # Constraint: digit-jump always targets parent rows, never sub-rows

  # ===================================================================
  # 9. Preview follows cursor for pane sub-rows
  # ===================================================================

  @unit
  Scenario: Pane index selection logic returns selected pane for sub-row cursor
    Given an expanded row with 3 panes and selected_pane is Some(1)
    When the pane index for preview capture is determined
    Then it returns pane index 1 (not 0)

  @unit
  Scenario: capture_pane_content builds correct tmux target with pane index
    When capture_pane_content is called with session "my-session" and pane index 2
    Then the tmux command target is "my-session.2"
    # Constraint: tmux capture-pane -t my-session.2

  @unit
  Scenario: Pane index selection logic returns 0 for parent row cursor
    Given the cursor is on the parent worktree row with selected_pane None
    When the pane index for preview capture is determined
    Then it returns pane index 0 (default behavior)

  # ===================================================================
  # 10. Enter action on pane sub-row
  # ===================================================================

  @unit
  Scenario: Enter on pane sub-row produces JoinPane action with pane index
    Given the cursor is on worktree row for session "my-session" with selected_pane Some(2)
    When the user presses Enter
    Then a TaskEnterAction with session "my-session" and pane_index 2 is produced
    # Constraint: add pane_index: Option<usize> to existing JoinSession variant
    # tmux command: select-pane -t session_name.{pane_index}

  @unit
  Scenario: Enter on parent row produces JoinSession action with default pane 0
    Given the cursor is on the parent worktree row with selected_pane None
    When the user presses Enter
    Then a TaskEnterAction with default pane_index 0 is produced

  # ===================================================================
  # 11. Expansion state persistence across refreshes
  # ===================================================================

  @unit
  Scenario: Expansion state tracked by worktree path
    Given the user expands a row for worktree at "/workspace/repo/feat-42"
    When the expansion set is checked
    Then "/workspace/repo/feat-42" is in the expanded set

  @unit
  Scenario: Expansion state survives data refresh
    Given the user expands a row for worktree at "/workspace/repo/feat-42"
    When a cache refresh completes and the table re-renders
    Then the row for "/workspace/repo/feat-42" remains expanded
    And sub-rows are still visible

  @unit
  Scenario: Expansion state silently clears when session disappears
    Given the user expanded a row for worktree at "/workspace/repo/feat-42"
    And that worktree had an active session with 3 panes
    When the session disappears on refresh (0 panes)
    Then the row silently becomes collapsed
    And no expand/collapse indicator is shown

  @integration
  Scenario: Expansion state clears when worktree row disappears entirely
    Given the user expanded a row for worktree path "/workspace/repo/feat-42"
    When that worktree is deleted and the table refreshes
    Then "/workspace/repo/feat-42" is removed from the expanded set
    And no orphan entries remain

  # ===================================================================
  # 12. Hints bar updates
  # ===================================================================

  @unit
  Scenario: Hints bar shows expand/collapse and new repo cycling keys
    When the hints bar renders in task mode
    Then it shows "Tab:repos" hint for repo cycling
    And it shows "l/h:expand" or equivalent hint for pane expansion
    And it shows "E:expand all" hint
    And it does not show Left/Right for repo cycling
