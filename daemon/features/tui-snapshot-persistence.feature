Feature: TUI workView snapshot persistence
  As the orchard TUI
  I need to persist the latest daemon workView snapshot to disk
  So that cold starts show real data immediately rather than a blank screen.

  Background:
    Given the snapshot is written to ~/.cache/orchard/work_view_snapshot.json
    And the file format is { "version": 1, "snapshot": { ... } }

  Scenario: Snapshot is written atomically after every successful workView fetch
    When the TUI receives a successful workView response
    Then it writes the snapshot via a tmp-then-rename sequence
    And the .json.tmp file does not remain after a successful write
    And the final file has permissions 0600 on Unix

  Scenario: Snapshot read on cold start
    Given a valid snapshot file exists with version: 1
    When the TUI boots
    Then it reads the snapshot and pre-populates the work_view_snapshot slot
    And the first render shows stale data rather than empty rows

  Scenario: Version mismatch on cold start is treated as absent
    Given the snapshot file has version: 999
    When the TUI tries to read it
    Then it returns None (no snapshot)
    And the TUI starts with an empty dashboard until the first successful fetch

  Scenario: Missing snapshot file does not prevent startup
    Given no work_view_snapshot.json file exists
    When the TUI boots
    Then it starts successfully with an empty snapshot slot
    And it fires the first workView query normally

  Scenario: Malformed JSON snapshot does not prevent startup
    Given work_view_snapshot.json contains invalid JSON
    When the TUI boots
    Then it returns None for the snapshot read
    And no panic or error propagates to the user

  Scenario: Write failure is logged but does not crash the refresh
    Given the cache directory is not writable
    When the TUI receives a workView snapshot and tries to persist it
    Then the write failure is logged at WARN level
    And the in-memory snapshot slot is still updated
    And the dashboard refreshes normally from the in-memory snapshot
