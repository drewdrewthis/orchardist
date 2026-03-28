Feature: UI polish with ratatui ecosystem widgets
  As an orchard user
  I want a polished, modern terminal UI using ratatui ecosystem widgets
  So that the TUI feels more refined and informative

  # Issue: #56
  #
  # Current state:
  #   - Uses only: Block, Paragraph, Table, Row, Clear, Layout, Line, Span
  #   - No third-party widget crates
  #   - Hand-rolled braille spinner (SPINNER_FRAMES cycling every render)
  #   - Filter modes cycle with 'f' key, no visual indicator of current mode
  #   - No scrollbar on worktree table
  #   - No progress indication beyond spinner for async operations
  #   - Preview pane shows fixed last-N-lines, not scrollable
  #   - FULL_HEADER_MIN_HEIGHT = 30 controls header size threshold
  #
  # Dependencies to add (actual compatible versions TBD via cargo add):
  #   throbber-widgets-tui  # spinner widget with multiple styles
  #   tui-scrollview        # scrollable viewports for preview pane
  #   tui-big-text          # large text rendering for header
  #
  # Descoped from original issue (moved to separate issues):
  #   - tachyonfx animations (Phase 2) -> requires architecture changes (#53)
  #   - Sparkline commit activity -> new data pipeline, not visual polish
  #   - tui-term evaluation -> research task, not feature work
  #   - LineGauge for progress -> no ops report progress; use labeled throbber
  #
  # Non-goals:
  #   - No architecture changes (that's #53)
  #   - No theme system (that's #54)
  #   - No new features -- purely visual polish on existing functionality
  #   - No new data gathering (e.g., git log for sparklines)
  #   - No render loop changes or animation state management

  Background:
    Given the orchard TUI is running inside tmux
    And a repository with multiple worktrees

  # ===================================================================
  # Widget upgrades: replace DIY implementations with ecosystem widgets
  # ===================================================================

  @integration
  Scenario: Throbber widget replaces DIY spinner
    Given the TUI is performing an async operation (refresh, cleanup, transfer)
    When the spinner is rendered
    Then it uses throbber-widgets-tui instead of manual SPINNER_FRAMES
    And the spinner animates through its configured style
    # Constraint: SPINNER_FRAMES constant and manual cycling logic must be removed

  @integration
  Scenario: Labeled throbber for async operations
    Given an async operation is in progress (refresh, cleanup, transfer)
    When the status area is rendered
    Then a throbber widget displays with a label describing the operation
    And the label indicates what operation is running (e.g., "Refreshing...", "Deleting...")

  @integration
  Scenario: Vertical scrollbar renders when worktree list overflows visible area
    Given the worktree list has more items than visible rows
    When the table is rendered
    Then a vertical scrollbar is visible on the right side of the table
    And the scrollbar position reflects the current scroll offset
    And the scrollbar thumb size reflects the ratio of visible to total items

  @integration
  Scenario: Scrollbar is not rendered when all worktree rows fit within visible area
    Given the worktree list has fewer items than visible rows
    When the table is rendered
    Then no scrollbar is displayed

  @integration
  Scenario: Tabs widget shows filter modes
    Given the TUI is displaying the worktree table
    When the header area is rendered
    Then a tab bar is visible showing filter options (All, Active, Mine, etc.)
    And the currently active filter tab is visually highlighted
    And pressing 'f' advances to the next tab (existing behavior preserved)

  # ===================================================================
  # Rich content: enhanced display using ecosystem widgets
  # ===================================================================

  @integration
  Scenario: Scrollable preview pane with tui-scrollview
    Given a worktree row is selected with a tmux pane preview
    When the preview pane is rendered
    Then it uses tui-scrollview for scrollable content
    And the user can scroll through the full pane capture
    And a scrollbar indicates position within the preview

  @integration
  Scenario: Big text header when terminal is tall enough
    Given the terminal height meets FULL_HEADER_MIN_HEIGHT threshold (30+ rows)
    When the header area is rendered
    Then the repository name is displayed using tui-big-text
    And when the terminal is shorter than the threshold, the header falls back to normal text
