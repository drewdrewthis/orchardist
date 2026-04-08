Feature: Quick-chat popup to orchardist
  As an orchard user living in tmux
  I want a fast one-line prompt to reach my orchardist Claude session
  So that I can dispatch tasks without breaking my current focus

  Background:
    Given the user has tmux >= 3.2
    And orchard init --install has been run

  # ===================================================================
  # Keybinding opens the popup
  # ===================================================================

  @e2e
  Scenario: Pressing prefix + O opens the quick-chat popup
    Given I am in a tmux session
    When I press "prefix + O"
    Then a tmux display-popup opens running orchard-chat
    And the popup shows a "> " prompt
    And the popup is 60% wide and 20% tall

  # ===================================================================
  # Message delivery
  # ===================================================================

  @integration
  Scenario: Typing a message and pressing Enter delivers it to the orchardist
    Given the orchardist session "orchardist" is running with a live Claude pane
    And the quick-chat popup is open
    When I type "prune everything" and press Enter
    Then the popup closes
    And the orchardist pane receives "prune everything" followed by Enter

  @integration
  Scenario: Empty input closes the popup without sending anything
    Given the quick-chat popup is open
    When I press Enter without typing anything
    Then the popup closes
    And nothing is sent to the orchardist pane

  # ===================================================================
  # Target resolution
  # ===================================================================

  @unit
  Scenario: --target flag overrides config
    Given global config has chat_target "orchardist" and tmux_sessions ["shepherd"]
    When I run "orchard chat --target shepherd --message hi"
    Then the message is delivered to session "shepherd"

  @unit
  Scenario: chat_target config field is used when no --target flag is given
    Given global config has chat_target "orchardist"
    When I run "orchard chat --message hi"
    Then the message is delivered to session "orchardist"

  @unit
  Scenario: Falls back to first tmux_sessions entry when chat_target is unset
    Given global config has no chat_target and tmux_sessions ["shepherd"]
    When I run "orchard chat --message hi"
    Then the message is delivered to session "shepherd"

  # ===================================================================
  # Error handling
  # ===================================================================

  @integration
  Scenario: Orchardist session does not exist shows error
    Given no tmux session named "orchardist" exists
    When I run "orchard chat --target orchardist --message hi"
    Then the command exits non-zero
    And stderr contains "orchardist"

  @integration
  Scenario: No target configured and no sessions shows error
    Given global config has no chat_target and no tmux_sessions
    When I run "orchard chat --message hi"
    Then the command exits non-zero
    And stderr contains "orchardist session not running"

  @integration
  Scenario: Error in send-keys surfaces via tmux display-message
    Given the orchard-chat wrapper script is installed
    And orchard chat exits non-zero
    When the wrapper runs
    Then it calls "tmux display-message" with the error text

  # ===================================================================
  # Init wizard installs both scripts
  # ===================================================================

  @integration
  Scenario: orchard init --install creates orchard-chat wrapper
    When I run "orchard init --install"
    Then ~/.local/bin/orchard-chat exists
    And it is executable
    And it contains "orchard chat --message"

  @integration
  Scenario: orchard init --install writes chat keybinding to tmux.conf
    When I run "orchard init --install"
    Then tmux.conf contains a bind-key line for "O" pointing to orchard-chat
    And the binding uses display-popup -E -w 60% -h 20%

  @integration
  Scenario: orchard init --install is idempotent for both bindings
    Given orchard init --install has already been run
    When I run "orchard init --install" again
    Then tmux.conf contains exactly one orchard marker block
    And both "orchard-popup" and "orchard-chat" bindings are present

  # ===================================================================
  # Help text
  # ===================================================================

  @unit
  Scenario: orchard --help includes the chat subcommand
    When I run "orchard --help"
    Then the output contains "orchard chat"
    And the output contains "--message"
    And the output contains "prefix + O"
