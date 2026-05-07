Feature: Schema ClaudeInstance.lastActivityAt — recency of activity for the LAST column
  As a thin-shell TUI consuming the orchard daemon's GraphQL schema
  I want ClaudeInstance to expose lastActivityAt as a derived ISO8601 timestamp
  So that the TUI v2 dashboard's LAST column can render "12m", "3h", "8s" without
  re-deriving recency from heartbeat files or tmux state client-side

  # Issue #443. Sister of #421 (false `no_claude` for live sessions): once both land,
  # "alive but inactive" rows can be visually distinguished from "broken claude" rows.
  #
  # Source-of-truth shape (per issue body):
  #   extend type ClaudeInstance {
  #     "ISO8601 timestamp of the most recent activity for this Claude instance —
  #     derived from the heartbeat's last_activity field, falling back to
  #     TmuxPane.lastActivityAt."
  #     lastActivityAt: String
  #   }
  #
  # Fallback chain (per AC 2):
  #   1. heartbeat file's last_activity field
  #   2. the TmuxPane's lastActivityAt (the pane hosting the claude pid)
  #   3. null

  Background:
    Given the daemon serves a GraphQL schema at 127.0.0.1:7777
    And the ClaudeInstance type exists with the existing fields (id, pane, process, account, state, rcUrl, rcEnabled, sessionUuid, startedAt)
    And the heartbeat reader parses orchard-claude-<session>.json files written by the orchard hook script
    And the heartbeat parser already accepts both snake_case and camelCase field names

  # ===================================================================
  # AC 1 — ClaudeInstance.lastActivityAt returns ISO8601 timestamp
  # ===================================================================

  @unit
  Scenario: lastActivityAt field is declared on ClaudeInstance and is nullable String
    Given the generated GraphQL schema
    When the ClaudeInstance.lastActivityAt field is inspected
    Then the field exists on type ClaudeInstance
    And its type is String (nullable, matching startedAt's shape)
    And it carries a doc comment describing the heartbeat→pane fallback chain

  @unit
  Scenario: lastActivityAt resolver returns RFC3339 string for an instance with a fresh heartbeat last_activity
    Given a heartbeat file with last_activity "2026-05-07T18:42:11Z"
    When a GraphQL query selects ClaudeInstance.lastActivityAt for that instance
    Then the resolver returns "2026-05-07T18:42:11Z"
    And the value parses cleanly as RFC3339

  @unit
  Scenario: lastActivityAt resolver returns RFC3339Nano when the heartbeat last_activity is sub-second
    Given a heartbeat file with last_activity "2026-05-07T18:42:11.123456Z"
    When a GraphQL query selects ClaudeInstance.lastActivityAt
    Then the resolver returns a value parseable as RFC3339Nano
    And the value preserves the sub-second precision

  # ===================================================================
  # AC 2 — Source: heartbeat.last_activity → TmuxPane.lastActivityAt → null
  # ===================================================================

  @unit
  Scenario: Resolver prefers heartbeat last_activity over the pane fallback
    Given a heartbeat with last_activity "2026-05-07T18:42:11Z"
    And the matched TmuxPane reports lastActivityAt "2026-05-07T18:30:00Z"
    When the lastActivityAt field is resolved
    Then the resolver returns "2026-05-07T18:42:11Z"
    And the pane's value is not consulted

  @unit
  Scenario: Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is absent
    Given a heartbeat with no last_activity field
    And the matched TmuxPane reports lastActivityAt "2026-05-07T18:30:00Z"
    When the lastActivityAt field is resolved
    Then the resolver returns "2026-05-07T18:30:00Z"

  @unit
  Scenario: Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is empty string
    Given a heartbeat with last_activity equal to ""
    And the matched TmuxPane reports lastActivityAt "2026-05-07T18:30:00Z"
    When the lastActivityAt field is resolved
    Then the resolver returns "2026-05-07T18:30:00Z"

  @unit
  Scenario: Resolver returns null when heartbeat lacks last_activity AND no pane matches
    Given a heartbeat with no last_activity field
    And no TmuxPane is matched for this instance (pane: null)
    When the lastActivityAt field is resolved
    Then the resolver returns null

  @unit
  Scenario: Resolver returns null when heartbeat lacks last_activity AND the matched pane has no lastActivityAt
    Given a heartbeat with no last_activity field
    And the matched TmuxPane reports lastActivityAt as null/zero
    When the lastActivityAt field is resolved
    Then the resolver returns null

  @unit
  Scenario: Heartbeat parser accepts both last_activity (snake_case) and lastActivity (camelCase)
    Given a heartbeat file written with the field name "last_activity"
    And another heartbeat file written with the field name "lastActivity"
    When each file is parsed
    Then both produce the same parsed last_activity timestamp
    And the dual-tag pattern mirrors how claudePid/claude_pid are accepted today

  @unit
  Scenario: Malformed heartbeat last_activity is treated as absent (does not crash, falls back)
    Given a heartbeat with last_activity "not-a-timestamp"
    And the matched TmuxPane reports lastActivityAt "2026-05-07T18:30:00Z"
    When the lastActivityAt field is resolved
    Then the resolver returns "2026-05-07T18:30:00Z"
    And the parser logs/skips silently rather than failing the whole sweep

  # ===================================================================
  # AC 3 — Subscription.nodeChanged emits when lastActivityAt changes
  # ===================================================================

  @integration
  Scenario: Heartbeat refresh that changes only last_activity emits a nodeChanged event
    Given a subscriber is connected to Subscription.nodeChanged for "ClaudeInstance:<host>:<pid>"
    And the cached instance currently has lastActivityAt "2026-05-07T18:30:00Z"
    When a new heartbeat sweep produces the same instance with lastActivityAt "2026-05-07T18:42:11Z" and no other field changes
    Then the subscriber receives one nodeChanged event for that instance
    And the event payload's lastActivityAt is "2026-05-07T18:42:11Z"

  @integration
  Scenario: Heartbeat refresh where lastActivityAt did not change does NOT emit a noise event
    Given a subscriber is connected to Subscription.nodeChanged for "ClaudeInstance:<host>:<pid>"
    And the cached instance currently has lastActivityAt "2026-05-07T18:30:00Z"
    When a new heartbeat sweep produces an instance where lastActivityAt is still "2026-05-07T18:30:00Z" and no other observable field changed
    Then the subscriber receives no nodeChanged event

  @unit
  Scenario: instancesEqual treats lastActivityAt as observable
    Given two ClaudeInstance values that differ only in lastActivityAt
    When instancesEqual compares them
    Then the function returns false
    # Today instancesEqual ignores lastActivityAt because the field doesn't exist;
    # this scenario locks in that the change-detection path is updated alongside the field.

  # ===================================================================
  # End-to-end — full dashboard query against the live daemon
  # ===================================================================

  @e2e
  Scenario: Live daemon query returns lastActivityAt for tracked Claude instances
    Given the daemon is running on 127.0.0.1:7777
    And at least one heartbeat file exists in $TMPDIR with last_activity set
    When a GraphQL query selects { claudeInstances { id state lastActivityAt } }
    Then every instance with a heartbeat last_activity returns a non-null lastActivityAt parseable as RFC3339
    And every instance whose heartbeat lacks last_activity AND whose pane has no activity returns null

  @e2e
  Scenario: Updates to a heartbeat are observable in the next nodeChanged event
    Given the daemon is running on 127.0.0.1:7777
    And a subscriber is open on Subscription.nodeChanged for an existing ClaudeInstance id
    When the heartbeat file for that instance is rewritten with a newer last_activity
    Then the subscriber receives a nodeChanged event whose lastActivityAt reflects the new value

  # --- AC Coverage Map ---
  # AC 1: "ClaudeInstance.lastActivityAt returns ISO8601 timestamp"
  #   -> @unit "lastActivityAt field is declared on ClaudeInstance and is nullable String"
  #   -> @unit "lastActivityAt resolver returns RFC3339 string for an instance with a fresh heartbeat last_activity"
  #   -> @unit "lastActivityAt resolver returns RFC3339Nano when the heartbeat last_activity is sub-second"
  #   -> @e2e  "Live daemon query returns lastActivityAt for tracked Claude instances"
  #
  # AC 2: "Sourced from heartbeat file's `last_activity` field if present, falling back to `TmuxPane.lastActivityAt`, falling back to `null`"
  #   -> @unit "Resolver prefers heartbeat last_activity over the pane fallback"
  #   -> @unit "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is absent"
  #   -> @unit "Resolver falls back to the TmuxPane's lastActivityAt when heartbeat last_activity is empty string"
  #   -> @unit "Resolver returns null when heartbeat lacks last_activity AND no pane matches"
  #   -> @unit "Resolver returns null when heartbeat lacks last_activity AND the matched pane has no lastActivityAt"
  #   -> @unit "Heartbeat parser accepts both last_activity (snake_case) and lastActivity (camelCase)"
  #   -> @unit "Malformed heartbeat last_activity is treated as absent (does not crash, falls back)"
  #   -> @e2e  "Live daemon query returns lastActivityAt for tracked Claude instances"
  #
  # AC 3: "Updates are reflected in the next Subscription.nodeChanged event for the instance"
  #   -> @integration "Heartbeat refresh that changes only last_activity emits a nodeChanged event"
  #   -> @integration "Heartbeat refresh where lastActivityAt did not change does NOT emit a noise event"
  #   -> @unit        "instancesEqual treats lastActivityAt as observable"
  #   -> @e2e         "Updates to a heartbeat are observable in the next nodeChanged event"
  #
  # Open notes for /plan or /investigate (do not drop ACs without asking the user):
  #   - The issue body says fallback is `TmuxPane.lastActivityAt`, but the schema today
  #     defines lastActivityAt only on TmuxSession (line 470) and TmuxClient (line 605),
  #     not on TmuxPane. Implementer must either (a) read pane.window.session.lastActivityAt,
  #     (b) add a new TmuxPane.lastActivityAt field, or (c) confirm with the issue author
  #     which is intended. Spec stays faithful to the issue's wording; resolver wiring is
  #     implementer's call.
  #   - Hook script (crates/orchard/hooks/orchard-state.sh) does not currently emit a
  #     last_activity field. Updating it is part of the same change so the read path has
  #     a value to surface. The hook update is implicit in AC 2's "heartbeat file's
  #     last_activity field if present" — out of scope for this spec to dictate which
  #     event(s) write it; coder + tests will lock the semantics.
