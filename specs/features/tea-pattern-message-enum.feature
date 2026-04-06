Feature: TEA pattern — Message enum for TUI event handling
  As a developer maintaining the TUI
  I want key events decoupled from state mutations via a Message enum
  So that keybindings are a thin mapping layer, state transitions are testable, and borrow-checker workarounds are eliminated

  Background:
    Given the TUI module has files: mod.rs, list.rs, dialogs.rs, state.rs

  # ===================================================================
  # Message enum — definition
  # ===================================================================

  @unit
  Scenario: Message enum has semantic variants for every user action
    Given the Message enum in src/tui/message.rs
    Then it has at least these variants:
      | variant             | payload                    |
      | Quit                | none                       |
      | CursorUp            | none                       |
      | CursorDown          | none                       |
      | CursorTo            | usize                      |
      | Enter               | none                       |
      | OpenPR              | none                       |
      | OpenIssue           | none                       |
      | ToggleBranchColumn  | none                       |
      | Delete              | none                       |
      | Transfer            | none                       |
      | NewSession          | none                       |
      | FilterChar          | char                       |
      | FilterBackspace     | none                       |
      | LeaderKey           | none                       |
      | Cleanup             | none                       |
      | PrevRepo            | none                       |
      | NextRepo            | none                       |
      | Refresh             | none                       |
      | ReconnectHosts      | none                       |
      | ToggleHelp          | none                       |
    And each dialog view state has its own variants for dialog-specific actions

  # ===================================================================
  # handle_event — key-to-message mapping (pure function)
  # ===================================================================

  @unit
  Scenario: handle_event maps key events to Messages based on current view and input phase
    Given the App is in ViewState::List with input_phase=AwaitingLeader
    When a KeyEvent for 'q' is received
    Then handle_event returns Some(Message::Quit)

  @unit
  Scenario: handle_event in AwaitingLeader returns None for unbound keys
    Given the App is in ViewState::List with input_phase=AwaitingLeader
    When a KeyEvent for 'z' is received
    Then handle_event returns None

  @unit
  Scenario: Ctrl+C always maps to Quit regardless of view state
    Given the App is in any ViewState
    When a KeyEvent for Ctrl+C is received
    Then handle_event returns Some(Message::Quit)

  @unit
  Scenario: Filtering phase routes printable chars to FilterChar
    Given the App is in ViewState::List with input_phase=Filtering (default)
    When a KeyEvent for 'a' is received
    Then handle_event returns Some(Message::FilterChar('a'))
    And it does not return CursorUp or any list action

  # ===================================================================
  # update — message-to-state-transition (pure mutation)
  # ===================================================================

  @unit
  Scenario: update processes CursorDown and advances cursor
    Given an App with cursor=0 and 5 visible rows
    When update is called with Message::CursorDown
    Then cursor is 1

  @unit
  Scenario: update processes CursorUp and decrements cursor
    Given an App with cursor=3
    When update is called with Message::CursorUp
    Then cursor is 2

  @unit
  Scenario: update processes Quit and returns should_quit=true
    When update is called with Message::Quit
    Then the function signals the app should quit

  @unit
  Scenario: update can return a follow-up Message for chaining
    Given the App is in ViewState::ConfirmDelete with Phase::Confirm
    When update is called with Message::ConfirmYes
    Then it transitions phase to InProgress
    And it may return a follow-up message to trigger the async delete

  # ===================================================================
  # No std::mem::replace — borrow-checker workaround eliminated
  # ===================================================================

  @unit
  Scenario: No std::mem::replace hack in event handling
    Given the source files src/tui/mod.rs and src/tui/list.rs
    Then std::mem::replace is not used on self.view in handle_event or update
    And std::mem::replace is not used on self.view in render

  # ===================================================================
  # Behavioral regression — all existing keybindings preserved
  # ===================================================================

  @integration
  Scenario: All list-view keybindings produce correct messages
    Given the App is in ViewState::List with search_active=false
    Then these key-to-message mappings hold:
      | key         | message            |
      | q           | Quit               |
      | Esc         | Quit               |
      | Up          | CursorUp           |
      | k           | CursorUp           |
      | Down        | CursorDown         |
      | j           | CursorDown         |
      | Enter       | Enter              |
      | o           | OpenPR             |
      | i           | OpenIssue          |
      | B           | ToggleBranchColumn |
      | d           | Delete             |
      | p           | Transfer           |
      | n           | NewSession         |
      | f           | CycleFilter        |
      | /           | StartSearch        |
      | c           | Cleanup            |
      | Left        | PrevRepo           |
      | Right       | NextRepo           |
      | r           | Refresh            |
      | R           | ReconnectHosts     |
      | ?           | ToggleHelp         |
      | Ctrl+C      | Quit               |

  @integration
  Scenario: Digit keys produce CursorTo messages
    Given the App is in ViewState::List
    When keys '1' through '9' are pressed
    Then each produces Message::CursorTo with the appropriate index

  @integration
  Scenario: Delete dialog keybindings preserved
    Given the App is in ViewState::ConfirmDelete with Phase::Confirm
    Then 'y' maps to ConfirmYes
    And 'n' maps to ConfirmNo
    And Esc maps to ConfirmNo

  @integration
  Scenario: Transfer dialog keybindings preserved
    Given the App is in ViewState::Transfer with Phase::Confirm
    Then 'y' maps to ConfirmYes
    And 'n' maps to ConfirmNo
    And Esc maps to ConfirmNo

  @integration
  Scenario: Cleanup dialog keybindings preserved
    Given the App is in ViewState::Cleanup in selection phase
    Then Up/k maps to CursorUp
    And Down/j maps to CursorDown
    And Space maps to ToggleSelection
    And Enter maps to ConfirmCleanup
    And q/Esc maps to Cancel

  @integration
  Scenario: New session dialog keybindings preserved
    Given the App is in ViewState::NewSession
    Then Esc maps to Cancel
    And Enter maps to ConfirmNewSession
    And Backspace maps to DeleteChar
    And alphanumeric/dash/underscore chars map to InputChar

  @integration
  Scenario: Help view keybindings preserved
    Given the App is in ViewState::Help
    Then '?' maps to ToggleHelp
    And Esc maps to Cancel
    And 'q' maps to Cancel

  # ===================================================================
  # Architecture constraints
  # ===================================================================

  @unit
  Scenario: handle_event is a pure mapping with no side effects
    Given the handle_event function
    Then it takes &self and KeyEvent as parameters
    And it returns Option<Message>
    And it does not mutate any App state

  @unit
  Scenario: update takes &mut self and Message
    Given the update function
    Then it takes &mut self and Message as parameters
    And it returns UpdateResult indicating quit and optional follow-up message
    And all state mutations happen inside update, not handle_event

  @unit
  Scenario: render remains unchanged — stateless read of &self
    Given the render function
    Then it takes &self and Frame as parameters
    And it does not need std::mem::replace on self.view

  # ===================================================================
  # Module structure
  # ===================================================================

  @unit
  Scenario: Message enum lives in its own module
    Given src/tui/message.rs exists
    Then it defines the Message enum
    And it is re-exported from src/tui/mod.rs or src/tui/state.rs
