# orchardist#854 — the sidebar publishes MECHANICAL pane resizes as drags
Feature: sidebar width survives mechanical resizes
  As an orchard user who has dragged the sidebar to the width I want
  I want a terminal resize or a pane respawn to leave that width alone
  So that my 60-column sidebar does not silently shrink to 34 behind my back

  Background:
    Given an outer "shell" wrapper is running with a sidebar pane at pane 0.0
    And the sidebar's width lives in the outer server's main-pane-width and in
      "sidebar-state.json", re-pinned on every resize by outer.conf's hooks

  # The bug: on a terminal resize (or an attach reflow) tmux redistributes the
  # panes proportionally BEFORE the outer hook re-pins, so the sidebar sees an
  # intermediate width and publishes it at once — corrupting main-pane-width and
  # the state file. The fix distinguishes the two deterministically: a MECHANICAL
  # resize always changes the OUTER window's total width, while a border drag only
  # moves the split inside a fixed window. The sidebar reads the window width the
  # moment a divergent size arrives; a changed window means mechanical (publish
  # nothing, let the hooks re-pin the pane), an unchanged one means a real drag.

  # There is no respawn hook (after-respawn-pane is not a real tmux hook); this
  # rests on the sidebar rule alone — a respawned sidebar's first size is a boot
  # size and is never published.
  @integration
  Scenario: AC1+AC2 A pane respawn does not republish the width
    Given the sidebar has booted at its 40-column split width
    When pane 0.0 is respawned with "respawn-pane -k"
    Then the global main-pane-width stays 40 and the pane settles back to 40
    And the window-scoped main-pane-width is 40 or unset
    And "sidebar-state.json" is byte-for-byte unchanged

  @integration
  Scenario: AC3 A genuine drag is published after it settles
    When the pane is resized to 60 columns as a drag
    Then the outer server's window main-pane-width becomes 60
    And "sidebar-state.json" records a width of 60

  @integration
  Scenario: AC4 A terminal resize preserves the dragged width
    Given the pane has been dragged to 60 columns
    When the wrapper's terminal is resized to 200 columns
    Then the changed window marks the intermediate size mechanical, not a drag
    And the pane width is still 60, main-pane-width is still 60
    And "sidebar-state.json" still records a width of 60

  @unit
  Scenario: A mechanical resize publishes nothing
    Given a sized sidebar published at 40 with a window width of 133
    When the window grows to 266 and the pane is handed an intermediate 93
    And the settle timer lands
    Then nothing is published and the published width is still 40

  @unit
  Scenario: A drag that stays in a fixed window is published after the settle
    Given a sized sidebar published at 40 with a window width of 266
    When the pane is handed 60 while the window stays 266 and the settle lands
    Then 60 is published to the outer server and saved to disk exactly once

  @unit
  Scenario: A stale settle timer publishes nothing
    Given a sized sidebar handed 34 and then, before it settled, handed 50
    When the first settle timer lands
    Then nothing is published
    When the live settle timer lands
    Then only 50 is published
