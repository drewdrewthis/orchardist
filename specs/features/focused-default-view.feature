Feature: Focused default view
  As an orchard user managing multiple worktrees
  I want the TUI to show only what needs my attention by default
  So I can quickly see active work and jump to the right session

  Background:
    Given the TUI is running with task rows loaded
    And there are rows in multiple display groups (Shepherd, NeedsAttention, ClaudeWorking, ReadyToMerge, Other)

  # --- Backlog Collapse ---

  @unit
  Scenario: Backlog items are collapsed by default
    Given there are 15 rows in the Other display group
    When the task list renders with default settings
    Then the Other rows are replaced with a single summary line
    And the summary line reads "15 backlog items -- press b to expand"
    And non-Other groups (Shepherd, NeedsAttention, ClaudeWorking, ReadyToMerge) are fully visible

  @unit
  Scenario: Toggle backlog expansion with b key
    Given backlog is collapsed (default)
    When the user presses 'b'
    Then all Other rows become visible
    And the summary line is removed
    When the user presses 'b' again
    Then Other rows are collapsed back to the summary line

  @unit
  Scenario: Backlog summary shows zero when no backlog items
    Given there are 0 rows in the Other display group
    When the task list renders
    Then no backlog summary line is shown

  @unit
  Scenario: Cursor does not land on collapsed backlog summary
    Given backlog is collapsed
    And the cursor is on the last visible non-backlog row
    When the user presses 'j' (down)
    Then the cursor does not move past the last selectable row

  # --- Shepherd Prominence ---

  @unit
  Scenario: Shepherd section header is visually prominent
    Given there are shepherd rows
    When the task list renders
    Then the Shepherd section header uses bold styling
    And the Shepherd section header includes the repo name
    And the Shepherd section header uses a distinct color (Cyan)

  # --- BRANCH Column Toggle ---

  @unit
  Scenario: BRANCH column is hidden by default
    When the task list renders with default settings
    Then the BRANCH column is not displayed
    And all other columns (# ISSUE TITLE HOST STATUS CLAUDE) are displayed

  @unit
  Scenario: Toggle BRANCH column visibility with B key
    Given the BRANCH column is hidden (default)
    When the user presses 'B' (shift+b)
    Then the BRANCH column becomes visible
    When the user presses 'B' again
    Then the BRANCH column is hidden again

  # --- Quick Filtering ---

  @unit
  Scenario: Cycle through filter modes with f key
    Given no filter is active (showing All)
    When the user presses 'f'
    Then the filter changes to "Has Session"
    And only rows with active sessions are shown
    When the user presses 'f' again
    Then the filter changes to "Has Claude"
    And only rows with active Claude sessions are shown
    When the user presses 'f' again
    Then the filter changes to "Has PR"
    And only rows with linked PRs are shown
    When the user presses 'f' again
    Then the filter returns to "All"

  @unit
  Scenario: Active filter is shown in hints bar
    Given the filter is set to "Has Session"
    When the task list renders
    Then the hints bar includes the active filter label

  @unit
  Scenario: Text filter with / key
    When the user presses '/'
    Then a filter input field appears in the hints bar
    And as the user types, rows are filtered by repo name or branch containing the typed text
    When the user presses Escape
    Then the text filter is cleared and all rows are shown again
    When the user presses Enter
    Then the text filter is applied and the input field closes

  @unit
  Scenario: Shepherd rows are always visible regardless of filter
    Given the filter is set to "Has PR"
    And a shepherd row has no PR
    When the task list renders
    Then the shepherd row is still visible

  # --- Hints Bar ---

  @unit
  Scenario: Hints bar shows available toggles
    When the task list renders
    Then the hints bar shows: b:backlog B:branch f:filter /:search
    And existing hints (Enter, o, i, q, etc.) are preserved
