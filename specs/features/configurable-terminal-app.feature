Feature: Configurable terminal app for notifications
  As an orchard user
  I want to configure which terminal app opens when I click a notification
  So that notifications activate my preferred terminal instead of a hardcoded one

  Background:
    Given the user has orchard installed
    And the user has a global config at ~/.orchard/config.json

  # ===================================================================
  # GlobalConfig — terminal_app field
  # ===================================================================

  @unit
  Scenario: GlobalConfig deserializes terminal_app field via load_from_path
    Given a config.json with:
      """json
      {
        "terminal_app": "com.googlecode.iterm2",
        "repos": []
      }
      """
    When the config is loaded via load_from_path
    Then terminal_app is "com.googlecode.iterm2"

  @unit
  Scenario: GlobalConfig defaults terminal_app when omitted
    Given a config.json with:
      """json
      {
        "repos": []
      }
      """
    When the config is loaded via load_from_path
    Then terminal_app is "com.apple.Terminal"

  @unit
  Scenario: GlobalConfig serializes terminal_app field
    Given a GlobalConfig with terminal_app "dev.warp.Warp-Stable"
    When the config is serialized to JSON
    Then the JSON contains "terminal_app": "dev.warp.Warp-Stable"

  @unit
  Scenario: Existing configs without terminal_app still load successfully
    Given a config.json with only repos and no terminal_app
    When the config is loaded via load_from_path
    Then repos are parsed correctly
    And terminal_app defaults to "com.apple.Terminal"

  # ===================================================================
  # Notification — use configured terminal app
  # ===================================================================

  @unit
  Scenario: Notification command uses configured terminal app bundle ID
    Given terminal_app is configured as "com.googlecode.iterm2"
    When build_notification_command is called with a session name
    Then the -activate argument uses "com.googlecode.iterm2"
    And the hardcoded "dev.warp.Warp-Stable" is not present

  @unit
  Scenario: Notification command uses default when no terminal_app configured
    Given terminal_app uses the default "com.apple.Terminal"
    When build_notification_command is called with a session name
    Then the -activate argument uses "com.apple.Terminal"

  # ===================================================================
  # Init wizard — terminal selection step (macOS only)
  # ===================================================================

  @unit
  Scenario: Wizard presents terminal selection menu on macOS
    Given the user runs "orchard init" on macOS
    When the wizard reaches the terminal app step
    Then it displays numbered options:
      | # | Label                     | Bundle ID                  |
      | 1 | Terminal.app (default)    | com.apple.Terminal         |
      | 2 | iTerm2                    | com.googlecode.iterm2      |
      | 3 | Warp                      | dev.warp.Warp-Stable       |
      | 4 | Alacritty                 | org.alacritty              |
      | 5 | Ghostty                   | com.mitchellh.ghostty      |
      | 6 | Other (enter bundle ID)   | (user input)               |

  @unit
  Scenario: Wizard accepts numbered selection
    Given the wizard is at the terminal selection step
    When the user enters "2"
    Then terminal_app is set to "com.googlecode.iterm2"

  @unit
  Scenario: Wizard accepts custom bundle ID
    Given the wizard is at the terminal selection step
    When the user enters "6"
    And then enters "net.kovidgoyal.kitty"
    Then terminal_app is set to "net.kovidgoyal.kitty"

  @unit
  Scenario: Wizard defaults to Terminal.app on empty input
    Given the wizard is at the terminal selection step
    When the user presses Enter without input
    Then terminal_app is set to "com.apple.Terminal"

  @unit
  Scenario: Terminal selection step is skipped on non-macOS
    Given the user runs "orchard init" on Linux
    When the wizard runs through its steps
    Then the terminal app selection step does not appear
    And terminal_app is not set in the config

  # ===================================================================
  # Prettier init wizard UX
  # ===================================================================

  @integration
  Scenario: Wizard uses colored section headers
    When the wizard runs
    Then each step has a colored header with step number
    And the output uses ANSI color codes for emphasis

  @integration
  Scenario: Wizard shows progress indicator
    When the wizard runs
    Then each step shows its position (e.g., "[1/N]", "[2/N]")
    So the user knows how far along they are

  @integration
  Scenario: Wizard shows summary at end
    When the wizard completes all steps
    Then it displays a summary of what was configured
    Including the selected terminal app and tmux key binding

  # ===================================================================
  # Implementation notes (from challenge review)
  # ===================================================================
  #
  # 1. RawGlobalConfig must include terminal_app with #[serde(default)]
  #    to avoid silent field drop during load_from_path deserialization.
  #    All config tests must go through load_from_path, not direct deser.
  #
  # 2. Update ADR-003 to note that machine-local user preferences
  #    (terminal_app) are allowed in global config alongside repo registry.
  #
  # 3. The wizard needs save_global_config() to persist the terminal_app
  #    choice back to ~/.orchard/config.json.
  #
  # 4. Terminal selection step must be gated on macOS (cfg! or runtime check).
