Feature: TUI search bar replaces space-leader key
  The space key now opens a dedicated search bar between the tab bar and the
  table instead of entering a leader-key dispatch mode. When the search bar is
  closed, bare keys are direct actions (no Spc+ prefix). When it is open, all
  printable characters feed the fuzzy filter.

  Phase reset rule: Searching persists through ALL messages. Only
  Message::CloseSearch and Message::Quit transition from Searching to Idle.
  This is the inverse of the old blanket-reset pattern.

  Background:
    Given the TUI is running with worktree rows loaded

  # --- State model ---

  @unit
  Scenario: InputPhase has Idle and Searching variants
    Then InputPhase has variants Idle and Searching
    And InputPhase::AwaitingLeader does not exist
    And InputPhase::Filtering does not exist

  @unit
  Scenario: Default input phase is Idle
    Then the default InputPhase is Idle

  # --- Message enum ---

  @unit
  Scenario: Message enum replaces leader messages with search messages
    Then Message::OpenSearch exists
    And Message::CloseSearch exists
    And Message::LeaderKey does not exist
    And Message::LeaderCancel does not exist

  # --- Opening the search bar ---

  @unit
  Scenario: Space opens the search bar from Idle
    Given the input phase is Idle
    When the user presses Space
    Then the message OpenSearch is dispatched
    And the input phase changes to Searching

  @unit
  Scenario: Space reopens search bar preserving existing filter text
    Given the filter text is "deploy"
    And the input phase is Idle
    When the user presses Space
    Then the input phase changes to Searching
    And the filter text is still "deploy"

  @unit
  Scenario: Space while Searching inserts a space character
    Given the input phase is Searching
    And the filter text is "my"
    When the user presses Space
    Then the filter text is "my "

  # --- Typing in the search bar ---

  @unit
  Scenario: Printable characters feed the filter when searching
    Given the input phase is Searching
    When the user types "abc"
    Then the filter text is "abc"
    And the cursor resets to 0

  @unit
  Scenario: Backspace removes last character in search bar
    Given the input phase is Searching
    And the filter text is "abc"
    When the user presses Backspace
    Then the filter text is "ab"

  @unit
  Scenario: Backspace on empty filter closes the search bar
    Given the input phase is Searching
    And the filter text is ""
    When the user presses Backspace
    Then the message CloseSearch is dispatched
    And the input phase changes to Idle

  # --- Closing the search bar ---

  @unit
  Scenario: Esc while Searching closes the search bar and preserves filter text
    Given the input phase is Searching
    And the filter text is "deploy"
    When the user presses Esc
    Then the message CloseSearch is dispatched
    And the input phase changes to Idle
    And the filter text is still "deploy"

  @unit
  Scenario: Esc in Idle phase quits the application
    Given the input phase is Idle
    When the user presses Esc
    Then the application quits

  # --- Enter behavior ---

  @unit
  Scenario: Enter activates the selected session (quits TUI for tmux switch)
    Given a worktree row is selected
    When the user presses Enter
    Then the selected session is activated
    And UpdateResult.quit is true

  @unit
  Scenario: Enter works identically in both Idle and Searching phases
    Given the input phase is Searching
    And a worktree row is selected
    When the user presses Enter
    Then the selected session is activated
    And UpdateResult.quit is true

  # --- Navigation works in both phases ---

  @unit
  Scenario: Up/Down arrow keys navigate in Idle phase
    Given the input phase is Idle
    When the user presses Down
    Then the cursor moves down

  @unit
  Scenario: Up/Down arrow keys navigate in Searching phase
    Given the input phase is Searching
    When the user presses Down
    Then the cursor moves down
    And the input phase is still Searching

  @unit
  Scenario: Tab switches repo tab in Searching phase
    Given the input phase is Searching
    When the user presses Tab
    Then the active repo tab advances
    And the input phase is still Searching

  @unit
  Scenario: PageUp/PageDown scroll preview in both phases
    Given the input phase is Searching
    When the user presses PageDown
    Then the preview pane scrolls down
    And the input phase is still Searching

  @unit
  Scenario: j/k are direct navigation in Idle phase
    Given the input phase is Idle
    When the user presses 'j'
    Then the cursor moves down

  @unit
  Scenario: j/k feed filter text in Searching phase
    Given the input phase is Searching
    When the user presses 'j'
    Then the filter text contains "j"
    And the input phase is still Searching

  @unit
  Scenario: Digit keys 1-9 jump to row in Idle phase
    Given the input phase is Idle
    When the user presses '3'
    Then the cursor jumps to row 3

  @unit
  Scenario: Digit keys feed filter text in Searching phase
    Given the input phase is Searching
    When the user presses '3'
    Then the filter text contains "3"

  # --- Direct action keys in Idle phase ---

  @unit
  Scenario Outline: Bare action keys dispatch in Idle phase
    Given the input phase is Idle
    When the user presses '<key>'
    Then the message <message> is dispatched

    Examples:
      | key | message          |
      | o   | OpenPR           |
      | d   | Delete           |
      | p   | TogglePriority   |
      | B   | ToggleBranchCol  |
      | c   | Cleanup          |
      | r   | Refresh          |
      | R   | ReconnectHosts   |
      | ?   | ToggleHelp       |
      | q   | Quit             |
      | n   | NewSession       |
      | w   | NewWorktree      |
      | i   | OpenIssue        |
      | h   | CollapseRow      |
      | l   | ExpandRow        |
      | E   | ToggleExpandAll  |

  @unit
  Scenario: Action keys are consumed as filter chars when Searching
    Given the input phase is Searching
    When the user presses 'o'
    Then the filter text contains "o"
    And no action message is dispatched
    And the input phase is still Searching

  # --- Phase persistence rule ---

  @unit
  Scenario: Searching phase persists through navigation messages
    Given the input phase is Searching
    When CursorDown is dispatched
    Then the input phase is still Searching

  @unit
  Scenario: Searching phase persists through CacheRefreshed
    Given the input phase is Searching
    When CacheRefreshed is dispatched
    Then the input phase is still Searching

  @unit
  Scenario: Searching phase persists through Refresh
    Given the input phase is Searching
    When Refresh is dispatched
    Then the input phase is still Searching

  @unit
  Scenario: Only CloseSearch transitions from Searching to Idle
    Given the input phase is Searching
    When CloseSearch is dispatched
    Then the input phase changes to Idle

  # --- Layout ---

  @unit
  Scenario: Search bar row appears in layout when Searching
    Given the input phase is Searching
    Then the layout has a Length(1) constraint between the tab bar and the table

  @unit
  Scenario: Search bar row absent from layout when Idle
    Given the input phase is Idle
    Then the layout has no search bar constraint between tab bar and table

  # --- Search bar rendering ---

  @unit
  Scenario: Search bar displays magnifying glass icon and filter text
    Given the input phase is Searching
    And the filter text is "deploy"
    Then the search bar renders a magnifying glass icon followed by "deploy"

  @unit
  Scenario: Search bar shows empty input field when no filter text
    Given the input phase is Searching
    And the filter text is ""
    Then the search bar renders a magnifying glass icon with empty input

  # --- Filter active indicator when bar is closed ---

  @unit
  Scenario: Hint bar shows filter indicator when filter active and bar closed
    Given the input phase is Idle
    And the filter text is "deploy"
    Then the hint bar shows a filter-active indicator with the text "deploy"

  @unit
  Scenario: No filter indicator when filter text is empty
    Given the input phase is Idle
    And the filter text is ""
    Then the hint bar does not show a filter-active indicator

  # --- Hint bar ---

  @unit
  Scenario: Hint bar shows bare key labels without Spc+ prefix
    Then the hint bar contains "o pr" not "Spc+o pr"
    And the hint bar contains "r refresh" not "Spc+r refresh"
    And the hint bar contains "Space search"

  # --- Help overlay ---

  @unit
  Scenario: Help overlay documents search bar
    When the help overlay is displayed
    Then it includes "Space" with description "Open search bar"
    And it includes "Esc" with description "Close search bar / quit"
    And it does not mention leader key

  # --- Filter persistence ---

  @unit
  Scenario: Filter text persists across search bar open/close cycles
    Given the input phase is Searching
    And the user types "api"
    When the user presses Esc
    Then the input phase is Idle
    And the filter text is "api"
    When the user presses Space
    Then the input phase is Searching
    And the filter text is "api"

  # --- Clearing filter text ---

  @unit
  Scenario: Enter clears filter text when activating a session
    Given the input phase is Idle
    And the filter text is "deploy"
    And a worktree row is selected
    When the user presses Enter
    Then the filter text is cleared
    And the selected session is activated

  # --- Build verification ---

  @unit
  Scenario: All existing tests compile and pass after refactor
    Then cargo test passes
    And cargo build --release succeeds
