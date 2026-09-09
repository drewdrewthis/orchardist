# orchardist#856 — sidebar: attached row never shows the done glyph
Feature: sidebar attached row never shows the done glyph
  As a user watching the sidebar
  I want the session I am attached to right now to never show the "done" (✓) glyph
  So that ✓ reliably means "this session needs my attention," not "this is the one I'm looking at"

  @unit
  Scenario: The current row never buckets as done, even when the push lane still says unattached
    Given a row is idle, hooked, and not yet marked attached by the push lane
    And the client-tty lane reports that row's session as the current one
    When the row bucket is computed
    Then the bucket is running, not done

  @e2e
  Scenario: Live proof — the attached row never shows the done glyph, on boot, switch, or click
    Given the sidebar is running in the outer shell
    When the sidebar boots, when the user switches sessions, and when the user clicks the current row
    Then the current row never renders the done glyph
    And clicking the current row does not change its glyph

  @unit
  Scenario: A genuinely idle, unattached, non-current session still shows the done glyph
    Given a row is idle, hooked, and not attached
    And that row's session is not the client-tty lane's current session
    When the row bucket is computed
    Then the bucket is done

  @unit
  Scenario: Suppression follows focus and is not sticky
    Given two idle, hooked, unattached rows A and B
    And the client-tty lane reports A as the current session
    When the sidebar rebuilds
    Then A buckets as running and B buckets as done
    When focus switches so the client-tty lane reports B as the current session
    And the sidebar rebuilds again
    Then B buckets as running and A buckets as done

# --- AC Coverage Map ---
# AC 1  "Current row is never bucketDone"        → Scenario: The current row never buckets as done, even when the push lane still says unattached
# AC 2  "Live proof, outer shell"                → Scenario: Live proof — the attached row never shows the done glyph, on boot, switch, or click
# AC 3  "No regression: idle+unattached+non-current still shows done" → Scenario: A genuinely idle, unattached, non-current session still shows the done glyph
# AC 4  "Suppression is not sticky"              → Scenario: Suppression follows focus and is not sticky
