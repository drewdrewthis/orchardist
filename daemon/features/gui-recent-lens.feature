Feature: GUI recent lens — all conversations sorted by last activity
  As the orchard-gui LensSidebar in "recent" mode
  I need every Claude conversation known to the daemon (live and historical)
  So that the operator can find and re-open any session, not just live REPLs.

  Operation consumed:
    RecentLens:
      conversations { id, sessionUuid, agentName, customTitle, cwd, firstSeenAt, lastSeenAt, messageCount, open, recap }
      claudeInstances [...SessionCard]  (enrichment overlay — matched by sessionUuid)

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the claude projects directory contains at least one JSONL file

  Scenario: RecentLens returns all conversations — not just live processes
    When the RecentLens query runs
    Then conversations includes entries whose claudeInstances is empty (historical/dead sessions)
    And conversations includes entries whose claudeInstances has a live match
    And the list is ordered latest-first by lastSeenAt

  Scenario: Dormant conversation row derives state from open field
    Given a conversation with open = false and no matching live ClaudeInstance
    When buildRecentItems processes it
    Then the dormant row's synthetic state is "no_claude"
    Given a conversation with open = true and no matching live ClaudeInstance
    Then the dormant row's synthetic state is "idle"

  Scenario: Live enrichment overlay — live ClaudeInstance lifts the row
    Given conversations[0].sessionUuid = "abc123"
    And claudeInstances contains a SessionCard with sessionUuid = "abc123"
    When buildRecentItems processes the data
    Then the row for "abc123" uses the live ClaudeInstance's state, pane, worktree, and process
    And the row's lastActivityAt comes from the conversation's lastSeenAt (authoritative) falling back to the instance's lastActivityAt

  Scenario: Recent lens capped at 100 rows
    Given the daemon reports 363 conversations
    When buildRecentItems runs
    Then the output list has at most 100 items
    And the 100 items are the most-recently-active conversations

  Scenario: Dedup by sessionUuid — no duplicate rows when a pid appears in two conversations
    Given two conversations share a sessionUuid prefix (edge case)
    When buildRecentItems runs
    Then each sessionUuid appears exactly once in the output

  Scenario: Empty state — no conversations discovered
    Given the claude projects directory is empty (no JSONL files)
    When the RecentLens query runs
    Then conversations is an empty list
    And the sidebar renders "No Claude sessions known."

  Scenario: Recent lens loading state
    Given recentStore.fetching = true and recentStore.data = null
    When the sidebar renders
    Then the sidebar shows "Loading…" in the recent section
