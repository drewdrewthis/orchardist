Feature: Theme struct to centralize TUI styling
  As a developer maintaining the TUI
  I want all color definitions in a single Theme struct
  So that styling is consistent, discoverable, and easy to change

  Background:
    Given the TUI module has files: list.rs, dialogs.rs, widgets.rs, mod.rs

  # ===================================================================
  # Theme struct — definition and defaults
  # ===================================================================

  @unit
  Scenario: Theme struct has named fields for every semantic color role
    Given the Theme struct in src/tui/theme.rs
    Then it has at least these minimum fields:
      | field              | type  |
      | accent             | Color |
      | error              | Color |
      | warning            | Color |
      | success            | Color |
      | dimmed             | Color |
      | selected_bg        | Color |
      | border             | Color |
      | claude_active      | Color |
      | claude_idle        | Color |
      | claude_needs_input | Color |
      | background         | Color |
      | text               | Color |
      | shepherd           | Color |
      | merge_conflict     | Color |
      | prioritized        | Color |
    And the full audit may add additional semantic fields (e.g., attribution_bg, pr_draft, search_highlight)

  @unit
  Scenario: Theme::default() returns current hardcoded color values
    When Theme::default() is called
    Then accent is Color::Cyan
    And error is Color::Red
    And warning is Color::Yellow
    And success is Color::Green
    And dimmed is Color::DarkGray
    And border is Color::DarkGray
    And claude_active is Color::Green
    And claude_idle is Color::DarkGray
    And claude_needs_input is Color::Red
    And background is Color::Black
    And shepherd is Color::Magenta
    And selected_bg is Color::DarkGray
    And text is Color::White
    And merge_conflict is Color::Red
    And prioritized is Color::White

  @unit
  Scenario: Theme derives Clone, Copy, Debug, PartialEq
    Given the Theme struct
    Then it derives Clone, Copy, Debug, and PartialEq

  # ===================================================================
  # App integration — theme stored on App
  # ===================================================================

  @unit
  Scenario: App struct holds a theme field
    Given the App struct in src/tui/mod.rs
    Then it has a field "theme" of type Theme

  @unit
  Scenario: App initializes with Theme::default()
    When App is constructed
    Then app.theme equals Theme::default()

  # ===================================================================
  # Refactor — no inline Color:: references remain in TUI code
  # ===================================================================

  @unit
  Scenario Outline: No hardcoded Color:: constants remain in <file>
    Given <file> rendering functions receive the theme
    Then <file> contains zero inline Color:: literals used for semantic styling
    And Color::Reset is exempt (it is a Ratatui sentinel, not a semantic color)
    And all semantic color references go through theme fields

    Examples:
      | file       |
      | list.rs    |
      | dialogs.rs |
      | widgets.rs |
      | mod.rs     |

  @integration
  Scenario: DisplayGroup color mapping uses theme via a TUI-layer function
    Given a Theme with default colors
    And a free function or method in the TUI layer maps DisplayGroup to themed colors
    Then Shepherd returns theme.shepherd
    And NeedsAttention returns theme.error
    And ClaudeWorking returns theme.claude_active
    And ReadyToMerge returns theme.accent
    And Prioritized returns theme.prioritized
    And Other returns theme.dimmed
    And DisplayGroup's own API is NOT modified (no &Theme parameter on DisplayGroup itself)

  # ===================================================================
  # Visual parity — output is pixel-identical
  # ===================================================================

  @integration
  Scenario: Default theme produces identical terminal output
    Given the app renders with Theme::default() to a Ratatui TestBackend
    When the Theme::default() values are compared to the pre-refactor hardcoded values
    Then every theme field maps to the exact same Color value previously hardcoded
    And no rendering function changes its styling logic (only the color source changes)

  # ===================================================================
  # Module structure
  # ===================================================================

  @unit
  Scenario: Theme lives in its own module within the TUI directory
    Given the file src/tui/theme.rs exists
    Then it is declared as a module in src/tui/mod.rs
    And Theme is re-exported from the tui module

  @unit
  Scenario: Theme module has doc comments
    Given the file src/tui/theme.rs
    Then it has a module-level doc comment explaining its purpose
    And the Theme struct has a doc comment
    And each public field has a doc comment describing its semantic role

  # ===================================================================
  # Scope guard — no new user-facing features
  # ===================================================================

  @unit
  Scenario: No theme configuration in config files
    Given the global config struct
    Then it has no theme-related fields

  @unit
  Scenario: No theme switching capability exposed
    Given the App struct
    Then there is no method to change the theme at runtime
    And there is no keybinding for theme switching

  # ===================================================================
  # Implementation notes
  # ===================================================================
  #
  # 1. Audit every Color:: usage in list.rs, dialogs.rs, widgets.rs
  #    to identify all semantic roles. Some colors serve the same role
  #    (e.g., Color::DarkGray is used for both "dimmed" text and borders).
  #
  # 2. The theme must be passed to rendering functions — either via &self
  #    on App methods or as an explicit &Theme parameter on free functions.
  #
  # 3. DisplayGroup::color() currently returns Color directly. After the
  #    refactor, a free function in the TUI layer maps DisplayGroup to
  #    themed colors. DisplayGroup's own API is NOT modified.
  #
  # 4. Additional semantic fields beyond the issue's initial list may be
  #    needed once the full audit is done (e.g., pr_draft, pr_merged,
  #    search_highlight, separator, group_header_bold).
  #
  # 5. Color::Rgb(25, 50, 30) in the attribution bar should become a
  #    named field like `attribution_bg`.
