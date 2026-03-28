Feature: Mouse support in TUI
  As an orchard user
  I want to interact with the TUI using the mouse
  So that I can click rows, scroll, and open links without memorizing keybindings

  # Issue: #46
  #
  # Current state:
  #   - Event loop only matches Event::Key; Event::Mouse is silently dropped
  #   - No EnableMouseCapture in terminal setup (raw mode + alternate screen only)
  #   - TEA pattern: handle_event(KeyEvent) -> Option<Message>, update(msg) -> UpdateResult
  #   - Message::CursorTo(usize), CursorUp, CursorDown, Enter, OpenPR, OpenIssue exist
  #   - cursor is a flat index across standalone_sessions then filtered worktree rows
  #   - Table body starts at y = hdr_height + 1 (spacer) + 1 (border) + 1 (header row)
  #   - Table rect computed dynamically in render_task_list but not stored
  #   - crossterm 0.29 has mouse support in default features
  #   - crate::browser::open_url already handles opening URLs
  #   - Backend is CrosstermBackend<File> writing to /dev/tty
  #
  # Design decisions:
  #   - Store table_area and url_area as Cell<Rect> for interior mutability
  #     (render is &self, but these need mutation; Rect is Copy so Cell works)
  #   - Double-click detection via timestamp + row tracking in App state
  #     Threshold: DOUBLE_CLICK_MS = 400 (named constant, between macOS 300ms and Windows 500ms)
  #     Double-click emits CursorTo(row) + Enter chained (safe even if first click was missed)
  #   - handle_mouse_event parallel to handle_event, returns Option<Message>
  #   - Reuse existing messages (CursorTo, Enter, OpenPR, OpenIssue, CursorUp, CursorDown)
  #   - Mouse capture enabled/disabled in terminal setup alongside raw mode
  #   - Mouse support is always on (no config toggle) -- crossterm passes through
  #     unhandled events so terminal selection still works with shift held
  #   - Mouse events ignored during search mode (search_active: true)
  #   - Clicks on group header rows are ignored (not selectable items)

  Background:
    Given the orchard TUI is running inside a terminal
    And mouse capture is enabled

  # ===================================================================
  # Terminal setup: EnableMouseCapture / DisableMouseCapture
  # ===================================================================

  @unit
  Scenario: Terminal setup enables mouse capture
    When the TUI terminal is initialized
    Then crossterm::EnableMouseCapture is written to the terminal
    And crossterm::EnterAlternateScreen is still written
    And raw mode is still enabled

  @unit
  Scenario: Terminal teardown disables mouse capture
    When the TUI terminal is torn down
    Then crossterm::DisableMouseCapture is written to the terminal
    And crossterm::LeaveAlternateScreen is still written
    And raw mode is still disabled

  # ===================================================================
  # Event loop: mouse events routed to handler
  # ===================================================================

  @unit
  Scenario: Mouse events are dispatched to handle_mouse_event
    Given the TUI is in List view
    When a crossterm Event::Mouse is received
    Then handle_mouse_event is called with the MouseEvent
    And the returned Message (if any) is passed to update

  @unit
  Scenario: Keyboard events are still dispatched to handle_event
    Given the TUI is in List view
    When a crossterm Event::Key is received
    Then handle_event is called with the KeyEvent
    And mouse handling is not invoked

  # ===================================================================
  # Mouse wheel scrolling
  # ===================================================================

  @e2e
  Scenario: Mouse wheel down moves cursor down in the task list
    Given the TUI is in List view
    And the cursor is on row 0
    And there are at least 3 rows visible
    When I scroll the mouse wheel down over the task list
    Then the cursor moves to row 1

  @e2e
  Scenario: Mouse wheel up moves cursor up in the task list
    Given the TUI is in List view
    And the cursor is on row 2
    When I scroll the mouse wheel up over the task list
    Then the cursor moves to row 1

  @unit
  Scenario: ScrollDown maps to Message::CursorDown
    Given the TUI is in List view
    When a MouseEvent with kind ScrollDown arrives
    And the mouse position is within the table area
    Then handle_mouse_event returns Some(Message::CursorDown)

  @unit
  Scenario: ScrollUp maps to Message::CursorUp
    Given the TUI is in List view
    When a MouseEvent with kind ScrollUp arrives
    And the mouse position is within the table area
    Then handle_mouse_event returns Some(Message::CursorUp)

  @unit
  Scenario: Scroll at bottom of list does not wrap around
    Given the TUI is in List view
    And the cursor is on the last row
    When I scroll the mouse wheel down over the task list
    Then the cursor stays on the last row

  @unit
  Scenario: Scroll at top of list does not wrap around
    Given the TUI is in List view
    And the cursor is on row 0
    When I scroll the mouse wheel up over the task list
    Then the cursor stays on row 0

  @unit
  Scenario: Scroll outside the table area is ignored
    Given the TUI is in List view
    When a MouseEvent with kind ScrollDown arrives
    And the mouse position is above the table area (in the header)
    Then handle_mouse_event returns None

  # ===================================================================
  # Click to select row
  # ===================================================================

  @e2e
  Scenario: Clicking a row selects it
    Given the TUI is in List view
    And the cursor is on row 0
    And there are at least 3 rows visible
    When I click on the third visible row in the table
    Then the cursor moves to row 2
    And the row is highlighted as selected

  @unit
  Scenario: Click maps to Message::CursorTo with computed row index
    Given the TUI is in List view
    And the table body starts at y offset 5
    And each row is 1 cell tall
    When a MouseEvent with kind Down at position (10, 7) arrives
    Then the clicked row index is (7 - 5) = 2
    And handle_mouse_event returns Some(Message::CursorTo(2))

  @unit
  Scenario: Click on table header row does not change cursor
    Given the TUI is in List view
    When a MouseEvent with kind Down arrives at the table header row
    Then handle_mouse_event returns None

  @unit
  Scenario: Click below the last row does not change cursor
    Given the TUI is in List view
    And there are 5 visible rows
    When a MouseEvent with kind Down arrives at y corresponding to row 8
    Then handle_mouse_event returns None

  @unit
  Scenario: Click outside table x-bounds does not change cursor
    Given the TUI is in List view
    When a MouseEvent with kind Down arrives with x outside the table rect
    Then handle_mouse_event returns None

  # ===================================================================
  # Double-click to activate (switch to session)
  # ===================================================================

  @e2e
  Scenario: Double-clicking a row switches to that session
    Given the TUI is in List view
    And row 1 has an active tmux session
    When I double-click on row 1
    Then the TUI exits
    And the tmux client switches to the session for row 1

  @unit
  Scenario: Double-click detected when second click within DOUBLE_CLICK_MS on same row
    Given the TUI is in List view
    And a click occurred on row 2 at time T
    When another click occurs on row 2 at time T + 300ms
    Then handle_mouse_event returns messages to chain CursorTo(2) then Enter
    And last_click is reset to None

  @unit
  Scenario: Two clicks more than DOUBLE_CLICK_MS apart are not a double-click
    Given the TUI is in List view
    And a click occurred on row 2 at time T
    When another click occurs on row 2 at time T + 500ms
    Then handle_mouse_event returns Some(Message::CursorTo(2))
    And it is not treated as a double-click

  @unit
  Scenario: Two clicks on different rows are not a double-click
    Given the TUI is in List view
    And a click occurred on row 1 at time T
    When another click occurs on row 3 at time T + 200ms
    Then handle_mouse_event returns Some(Message::CursorTo(3))
    And it is not treated as a double-click

  @unit
  Scenario: Double-click on a standalone session activates it
    Given the TUI is in List view
    And row 0 is a standalone tmux session
    When I double-click on row 0
    Then handle_mouse_event returns Some(Message::Enter)

  # ===================================================================
  # Stored table area for hit testing
  # ===================================================================

  @unit
  Scenario: Table area is stored after each render via Cell<Rect>
    When render_task_list completes
    Then self.table_area (Cell<Rect>) contains the Rect of the table body
    And the Rect excludes the border and column header rows
    And Cell allows mutation inside &self render method

  @unit
  Scenario: Table area is initialized to zero before first render
    Given a newly constructed App
    Then self.table_area is Cell::new(Rect::default())
    And mouse clicks before the first render are ignored

  @unit
  Scenario: URL area is stored after each render via Cell<Rect>
    When render_task_list completes
    Then self.url_area (Cell<Rect>) contains the Rect of the attribution URL text
    And the Rect covers only the URL portion of the hints bar

  # ===================================================================
  # URL click opens browser
  # ===================================================================

  @e2e
  Scenario: Clicking the attribution URL opens it in the browser
    Given the TUI is in List view
    And the footer contains the attribution URL
    When I click on the attribution URL text
    Then the URL is opened in the default browser via crate::browser::open_url

  @unit
  Scenario: Click on footer URL area maps to browser open
    Given the TUI is in List view
    And the footer area spans y = terminal_height - 1
    When a MouseEvent with kind Down arrives within the URL text bounds
    Then handle_mouse_event triggers crate::browser::open_url with the attribution URL

  @unit
  Scenario: Click on footer outside URL text does nothing
    Given the TUI is in List view
    When a MouseEvent with kind Down arrives on the footer row
    But the x position is outside the URL text bounds
    Then handle_mouse_event returns None

  # ===================================================================
  # Group header rows and search mode
  # ===================================================================

  @unit
  Scenario: Click on a group header row is ignored
    Given the TUI is in List view
    And the table has group header rows (repo separators)
    When a MouseEvent with kind Down arrives on a group header row
    Then handle_mouse_event returns None
    And the cursor does not move

  @unit
  Scenario: Mouse events are ignored when search is active
    Given the TUI is in List view
    And search_active is true
    When any mouse event arrives
    Then handle_mouse_event returns None
    And the search bar remains focused

  # ===================================================================
  # Keyboard navigation unchanged
  # ===================================================================

  @integration
  Scenario: Arrow keys still navigate after mouse support is added
    Given the TUI is in List view with mouse capture enabled
    When I press the Down arrow key
    Then the cursor moves down
    And the behavior is identical to before mouse support

  @integration
  Scenario: Number keys still jump to rows
    Given the TUI is in List view with mouse capture enabled
    When I press "3"
    Then the cursor jumps to row 2 (0-indexed)
    And Message::CursorTo(2) is produced

  @integration
  Scenario: Enter key still activates the selected row
    Given the TUI is in List view with mouse capture enabled
    And the cursor is on a row with a tmux session
    When I press Enter
    Then the session is activated
    And the behavior is identical to before mouse support

  @integration
  Scenario: All existing keybindings produce the same messages
    Given the TUI is in List view with mouse capture enabled
    When I press any previously-bound key (j, k, o, i, d, etc.)
    Then the same Message variant is returned as before mouse support

  # ===================================================================
  # Mouse events in non-List views
  # ===================================================================

  @unit
  Scenario: Mouse events in dialog views are ignored
    Given the TUI is in ConfirmDelete view
    When a mouse click event arrives
    Then handle_mouse_event returns None
    And the dialog is not affected

  @unit
  Scenario: Mouse events in Help overlay are ignored
    Given the TUI is in Help view
    When a mouse click event arrives
    Then handle_mouse_event returns None
    And the help overlay remains visible

  @unit
  Scenario: Mouse scroll in Heal view is ignored
    Given the TUI is in Heal view
    When a mouse scroll event arrives
    Then handle_mouse_event returns None

  # ===================================================================
  # Double-click state tracking
  # ===================================================================

  @unit
  Scenario: Last click state is stored with row index and timestamp
    Given a newly constructed App
    Then last_click is None
    When a click occurs on row 2
    Then last_click is Some with row = 2 and the current timestamp

  @unit
  Scenario: Last click state is reset after a double-click fires
    Given a double-click was detected on row 2
    Then last_click is reset to None
    So that the next click starts fresh
