Feature: GUI error and edge cases — resilience at the daemon boundary
  As the orchard-gui
  I need the UI to degrade gracefully when the daemon is unavailable, the schema drifts,
  or edge-case data is returned
  So that operators are not left staring at a blank screen or a JavaScript crash.

  Background:
    Given the orchard-gui is running and pointing at the daemon proxy at /__daemon/graphql

  Scenario: Daemon unreachable at boot — all lens stores show loading, then error
    Given the daemon is not running (connection refused on 127.0.0.1:7777)
    When the lens stores fire their prefetch on mount
    Then each store's fetching flag is true until timeout
    And each store's errors array is non-empty after failure
    And the attention lens sidebar renders "Daemon couldn't fetch this lens. Try another lens or check the daemon logs."
    And no JavaScript uncaught exception is thrown

  Scenario: Daemon restarts mid-session — Houdini re-fetches after reconnect
    Given all lens stores are warm and rendering
    When the daemon process exits and restarts
    Then the WebSocket reconnects (graphql-ws reconnect policy)
    And the next Houdini cache update renders fresh data
    And pending subscriptions (conversationChanged, tmuxSessionsChanged) re-subscribe

  Scenario: Schema drift — GUI queries a field the daemon no longer exposes
    Given the GUI sends a query referencing "Worktree.claudeInstances"
    And the daemon schema has removed or renamed that field
    When the query executes
    Then the daemon returns a GraphQL error with "Cannot query field"
    And the lens store's errors array contains the error
    And the GUI degrades to an empty section rather than throwing

  Scenario: workView field missing from daemon schema — not yet in any domain partial
    Given the GUI fires any of the five lens queries referencing workView
    And workView is not present in the live daemon schema
    Then the daemon returns a GraphQL error
    And this is the most-likely gap to block lens rendering in a new daemon build
    # workView is a synthetic read-model (WorkView type, Query.workView field) that lives
    # in the live daemon schema but has no schema partial in daemon/<domain>/schema.graphql.
    # This feature documents it as a known coverage gap for the constitution effort.

  Scenario: conversationChanged subscription — null payload on file removal
    Given TranscriptView is subscribed to conversationChanged for sessionUuid "abc123"
    When the matching JSONL file is deleted from disk
    Then the daemon emits a null payload for the subscription
    And TranscriptView does not crash on a null push
    And the transcript renders the last-cached turns with an "earlier turns omitted" banner if truncated

  Scenario: Large fleet — 363+ conversations do not stall the recent lens
    Given the daemon reports 363 conversations in RecentLens
    When buildRecentItems runs
    Then the output is capped at 100 rows
    And the projection completes synchronously without blocking the render thread
    And the sidebar renders the first 100 rows in the next frame

  Scenario: tmuxServer null — no tmux daemon on this host
    When the TmuxLens query runs and tmuxServer is null
    Then buildTmuxSnapshot returns the EMPTY snapshot (alive: false, sessions: [], activePaneIds: {})
    And the sidebar renders "No tmux server reachable."
    And no null-dereference error occurs in the tmux lens projection

  Scenario: Houdini cache key version mismatch — stale v1/v2 cache purged silently
    Given localStorage contains "orchard:houdini:cache:v1" or "v2" from an old build
    When the layout hydrates the cache at boot
    Then the stale keys are removed from localStorage before hydration
    And the current key "orchard:houdini:cache:v3" is used
    And no stale schema shape corrupts the Houdini runtime

  Scenario: Houdini cache too large — not persisted to localStorage
    Given the serialized Houdini cache exceeds 2MB
    When persistHoudiniCache is called
    Then the cache is NOT written to localStorage
    And no QuotaExceededError is thrown
    And the next boot does a cold-fetch from the daemon (no stale half-write)

  Scenario: Tauri bridge unavailable in browser dev — tmuxSendText falls back to GraphQL
    Given the app is running in a browser (no window.__TAURI_INTERNALS__)
    When tmuxSendText is called
    Then a POST to /__daemon/graphql with the sendTextToPane mutation is fired
    And no "Tauri bridge not available" error is surfaced to the user for this path

  Scenario: sendTextToPane HTTP non-200 — error surfaces as toast
    Given the daemon proxy returns HTTP 503
    When tmuxSendText's fetch call rejects with HTTP 503
    Then an Error is thrown with message "sendTextToPane HTTP 503"
    And SessionComposer catches it, shows a toast, and removes the optimistic pending turn

  Scenario: Peer disconnected — federation host goes unreachable
    Given hosts[1] was reachable and is now unreachable
    When the next HostsList query runs
    Then hosts[1].reachable = false
    And hosts[1].lastSeenAt is the last-known RFC3339 timestamp
    And PeerCluster renders the pip as "bad" (red) with the last-seen tooltip
    And no rows from hosts[1]'s worktrees/sessions appear in lens data (they were on that peer)
