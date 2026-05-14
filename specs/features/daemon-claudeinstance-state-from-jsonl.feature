Feature: Derive ClaudeInstance state from conversation jsonl
  As an orchard daemon operator
  I want ClaudeInstance state derived strictly from the conversation jsonl
  So that state is consistent with Claude Code's persisted artifacts and the
    orchard-state.sh hook + $TMPDIR sidecars can be retired

  Background:
    Given the daemon is running on host "lw-dev"
    And conversation jsonl files live at "~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl"
    And the orchard-state.sh hook sidecars under "$TMPDIR/orchard-claude-*.json" are no longer read by the daemon

  # ===========================================================================
  # AC 1 - jsonl-tail provider exists and exposes the sidecar-equivalent fields
  # ===========================================================================

  @unit
  Scenario: jsonl-tail provider parses conversation transcript and surfaces state-relevant fields
    Given a conversation jsonl with assistant, user, attachment, and system records
    When the jsonl provider parses the tail
    Then the parsed records carry "message.model", "message.stop_reason", and "message.usage"
    And the parsed records carry "message.content[].type", "id", "name", and "tool_use_id"
    And the parsed records carry "attachment.hookEvent" when present
    And the parsed records carry "system.subtype" when present

  @unit
  Scenario: jsonl provider exposes the sidecar-equivalent ClaudeInstance fields
    Given a conversation jsonl for session "11111111-2222-3333-4444-555555555555"
    When the jsonl provider reports the instance fields
    Then the result exposes "state"
    And the result exposes "event"
    And the result exposes "stopReason"
    And the result exposes "inflightToolCount"
    And the result exposes "stateChangedAt"
    And the result exposes "model"
    And the result exposes "lastActivityAt"
    And the result exposes session start, cwd, and sessionUuid

  # ===========================================================================
  # AC 2 - All ClaudeInstance fields resolve from the jsonl provider; no daemon
  #        code reads $TMPDIR/orchard-claude-*.json after this PR (heartbeat
  #        retains ONLY ClaudePid / RcURL / RcEnabled per the plan)
  # ===========================================================================

  @integration
  Scenario: ClaudeInstance state, lastActivityAt, and inflightToolCount resolve from the jsonl provider
    Given a live ClaudeInstance with both a heartbeat sidecar and a conversation jsonl available
    And the heartbeat sidecar reports state "idle"
    And the jsonl tail reports state "working" with inflightToolCount 1
    When the GraphQL resolver returns the ClaudeInstance
    Then "state" is "working"
    And "inflightToolCount" is 1
    And "lastActivityAt" matches the jsonl tail timestamp (not the sidecar)

  @integration
  Scenario: Daemon process does not read $TMPDIR/orchard-claude-*.json for state classification
    Given the heartbeat sidecar file is removed mid-session
    When the daemon resolves ClaudeInstance.state
    Then the result still reflects the jsonl tail
    And no daemon code path opens any "$TMPDIR/orchard-claude-*.json" file for state classification

  @integration
  Scenario: ClaudePid, RcURL, and RcEnabled continue to come from the heartbeat (out of scope for this migration)
    Given the heartbeat sidecar carries "ClaudePid", "RcURL", and "RcEnabled"
    When the GraphQL resolver returns the ClaudeInstance
    Then "claudePid", "rcUrl", and "rcEnabled" are populated from the heartbeat
    And the jsonl tail does not override those three fields

  # ===========================================================================
  # AC 3 - crates/orchard/hooks/orchard-state.sh is deleted from the repo
  # ===========================================================================

  @integration
  Scenario: In-repo hook script is removed
    When the repository tree is inspected
    Then the file "crates/orchard/hooks/orchard-state.sh" does not exist
    And no Rust source under "crates/orchard/src/" references "orchard-state.sh"
    And no Rust source under "crates/orchard/src/" reads "$TMPDIR/orchard-claude-*.json"

  # ===========================================================================
  # AC 4 - Codex-side cleanup is out of scope for this PR; the daemon ships a
  #        startup janitor so orphan sidecars do not accumulate during the
  #        cohabitation window
  # ===========================================================================

  @integration
  Scenario: Codex hook files remain in place after this PR
    Given the codex repo at "~/.claude" still contains "hooks/orchard-state.sh"
    And "~/.claude/settings.json" still registers the orchard-state hook
    When this PR is applied to the daemon repo
    Then no daemon-repo change deletes the codex-side hook
    And no daemon-repo change rewrites "~/.claude/settings.json"

  @integration
  Scenario: Daemon startup janitor removes orphan sidecars whose tmux session is dead
    Given "$TMPDIR/orchard-claude-old-session.json" exists
    And "$TMPDIR/orchard-claude-old-session.inflight.json" exists
    And no live tmux session named "old-session" is running
    When the daemon starts up
    Then both files are deleted
    And the daemon logs the cleanup action

  @integration
  Scenario: Daemon startup janitor leaves sidecars for live tmux sessions alone
    Given "$TMPDIR/orchard-claude-repo_47_claude.json" exists
    And a live tmux session "repo_47_claude" is running
    When the daemon starts up
    Then "$TMPDIR/orchard-claude-repo_47_claude.json" still exists

  # ===========================================================================
  # AC 5 - Strict jsonl-only state classification with one regression test per
  #        state, plus a negative test for Notification-driven input collapse
  # ===========================================================================

  @unit
  Scenario: working state when last assistant stop_reason is tool_use
    Given a jsonl whose tail ends with an assistant record where "stop_reason" is "tool_use"
    When the classifier runs
    Then the classified state is "working"

  @unit
  Scenario: working state when a tool_use is open without a matching tool_result in the current turn
    Given a jsonl whose current turn contains an assistant tool_use with id "tu_42"
    And no user record with "tool_result.tool_use_id" of "tu_42" follows it
    When the classifier runs
    Then the classified state is "working"
    And the inflight tool count is at least 1

  @unit
  Scenario: idle state when last assistant stop_reason is end_turn
    Given a jsonl whose tail ends with an assistant record where "stop_reason" is "end_turn"
    When the classifier runs
    Then the classified state is "idle"

  @unit
  Scenario: idle state when tail ends with system stop_hook_summary
    Given a jsonl whose tail ends with a system record where "subtype" is "stop_hook_summary"
    When the classifier runs
    Then the classified state is "idle"

  @unit
  Scenario: idle state when tail ends with system turn_duration
    Given a jsonl whose tail ends with a system record where "subtype" is "turn_duration"
    When the classifier runs
    Then the classified state is "idle"

  @unit
  Scenario: input state when an AskUserQuestion tool_use is open without a matching tool_result
    Given a jsonl whose current turn contains a tool_use with "name" "AskUserQuestion" and id "ask_99"
    And no user record with "tool_result.tool_use_id" of "ask_99" follows it
    When the classifier runs
    Then the classified state is "input"

  @unit
  Scenario: input state ends when AskUserQuestion's tool_result lands in jsonl
    Given a jsonl that contains a tool_use with "name" "AskUserQuestion" and id "ask_99"
    And a subsequent user record with "tool_result.tool_use_id" of "ask_99"
    And a subsequent assistant record with "stop_reason" "end_turn"
    When the classifier runs
    Then the classified state is "idle"

  @unit
  Scenario: Notification-driven input collapses to working - no AskUserQuestion in jsonl means no input state
    Given a jsonl with a non-AskUserQuestion tool_use that has no matching tool_result yet
    And no record in the tail mentions "AskUserQuestion"
    When the classifier runs
    Then the classified state is "working"
    And the classified state is not "input"

  @unit
  Scenario: Sidechain records are excluded from classification
    Given a jsonl where the only open tool_use is on a record with "isSidechain" true
    And the parent turn's tail ends with an assistant record where "stop_reason" is "end_turn"
    When the classifier runs
    Then the classified state is "idle"
    And the sidechain tool_use does not pull the parent into "working"

  @unit
  Scenario: Inflight tool count is scoped to the current turn
    Given a jsonl with two completed prior turns and one in-flight turn with 3 open tool_use ids
    When the classifier runs
    Then the inflight tool count is 3
    And tool_use ids from completed prior turns are not counted

  # ===========================================================================
  # AC 6 - Closes #593, #592, #555, #553 via PR trailers
  # ===========================================================================

  @integration
  Scenario: PR body carries Closes trailers for the four migrated issues
    Given the pull request opened for this issue
    When its body is inspected
    Then it contains "Closes #593"
    And it contains "Closes #592"
    And it contains "Closes #555"
    And it contains "Closes #553"

  # ===========================================================================
  # AC 7 - Existing daemon tests stay green; named regressions are preserved;
  #        new tests cover the jsonl provider with jsonl fixtures
  # ===========================================================================

  @integration
  Scenario: Named regression tests remain green after the migration
    When the daemon test suite runs
    Then "internal/server/providers/claudeinstance/last_activity_at_test.go" passes
    And "internal/server/providers/claudeinstance/last_activity_at_ac2_test.go" passes
    And "internal/server/providers/claudeinstance/last_activity_at_ac3_test.go" passes
    And "internal/server/providers/claudeinstance/last_activity_at_ac3_e2e_test.go" passes
    And "internal/server/providers/claudeinstance/regression_pid_join_test.go" passes
    And "internal/server/providers/claudeinstance/e2e_test.go" passes
    And "internal/server/providers/claudeinstance/adapter_test.go" passes

  @integration
  Scenario: New jsonl provider tests use jsonl fixtures, not sidecar fixtures
    Given the new state-classification tests under "internal/server/providers/claudeinstance/"
    When the test files are inspected
    Then they reference fixture files ending in ".jsonl"
    And they do not reference "orchard-claude-*.json" sidecar fixtures

  @integration
  Scenario: cwd path-encoding collisions are disambiguated by sessionUuid
    Given two cwds "/a/b-c" and "/a-b/c" that both encode to the directory "-a-b-c"
    And each cwd has a distinct conversation jsonl with a different "<uuid>.jsonl" filename
    When the jsonl provider resolves state for one of the sessions
    Then the resolved state belongs to the session whose "sessionId" matches the requested uuid
    And the other session's records do not contaminate the result

  # ===========================================================================
  # AC 8 - Daemon CI green on the PR
  # ===========================================================================

  @e2e
  Scenario: Daemon CI is green on the PR
    Given the pull request opened for this issue
    When GitHub Actions reports the latest workflow run
    Then the daemon CI job concludes successfully
    And no required check is failing

  @e2e
  Scenario: Live daemon serves jsonl-derived state via GraphQL after restart
    Given the daemon binary is rebuilt from this PR
    And the daemon is restarted on host "lw-dev"
    And a Claude session is actively producing tool_use records in its jsonl
    When the client queries "{ claudeInstances { sessionUuid state inflightToolCount lastActivityAt } }"
    Then the response shows "state" "working"
    And "inflightToolCount" is greater than 0
    And "lastActivityAt" is within the last 30 seconds
    And the values match what jq derives from the same jsonl

  @e2e
  Scenario: Permission-prompt session classifies as working (the intentional Notification collapse)
    Given a Claude session paused on a Bash permission prompt
    And the session's jsonl shows an open tool_use without a matching tool_result
    And the session has no AskUserQuestion in flight
    When the client queries "{ claudeInstances { sessionUuid state } }"
    Then the session's "state" is "working"
    And the session's "state" is not "input"

  # --- AC Coverage Map ---
  # AC 1 (jsonl-tail provider exists and exposes sidecar-equivalent fields)
  #   - @unit "jsonl-tail provider parses conversation transcript and surfaces state-relevant fields"
  #   - @unit "jsonl provider exposes the sidecar-equivalent ClaudeInstance fields"
  #
  # AC 2 (All ClaudeInstance GraphQL fields resolve from jsonl; no daemon code
  #        reads $TMPDIR/orchard-claude-*.json after this PR)
  #   - @integration "ClaudeInstance state, lastActivityAt, and inflightToolCount resolve from the jsonl provider"
  #   - @integration "Daemon process does not read $TMPDIR/orchard-claude-*.json for state classification"
  #   - @integration "ClaudePid, RcURL, and RcEnabled continue to come from the heartbeat (out of scope for this migration)"
  #
  # AC 3 (crates/orchard/hooks/orchard-state.sh is deleted from the repo)
  #   - @integration "In-repo hook script is removed"
  #
  # AC 4 (Codex-side cleanup is out of scope; janitor handles disk cohabitation)
  #   - @integration "Codex hook files remain in place after this PR"
  #   - @integration "Daemon startup janitor removes orphan sidecars whose tmux session is dead"
  #   - @integration "Daemon startup janitor leaves sidecars for live tmux sessions alone"
  #
  # AC 5 (Strict jsonl-only state classification; one regression test per state;
  #        Notification-driven input intentionally removed; negative test)
  #   - @unit "working state when last assistant stop_reason is tool_use"
  #   - @unit "working state when a tool_use is open without a matching tool_result in the current turn"
  #   - @unit "idle state when last assistant stop_reason is end_turn"
  #   - @unit "idle state when tail ends with system stop_hook_summary"
  #   - @unit "idle state when tail ends with system turn_duration"
  #   - @unit "input state when an AskUserQuestion tool_use is open without a matching tool_result"
  #   - @unit "input state ends when AskUserQuestion's tool_result lands in jsonl"
  #   - @unit "Notification-driven input collapses to working - no AskUserQuestion in jsonl means no input state" (negative test)
  #   - @unit "Sidechain records are excluded from classification"
  #   - @unit "Inflight tool count is scoped to the current turn"
  #
  # AC 6 (Closes #593, #592, #555, #553 via PR trailers)
  #   - @integration "PR body carries Closes trailers for the four migrated issues"
  #
  # AC 7 (Existing daemon tests pass; new tests use jsonl fixtures)
  #   - @integration "Named regression tests remain green after the migration"
  #   - @integration "New jsonl provider tests use jsonl fixtures, not sidecar fixtures"
  #   - @integration "cwd path-encoding collisions are disambiguated by sessionUuid"
  #
  # AC 8 (Daemon CI green on the PR)
  #   - @e2e "Daemon CI is green on the PR"
  #   - @e2e "Live daemon serves jsonl-derived state via GraphQL after restart"
  #   - @e2e "Permission-prompt session classifies as working (the intentional Notification collapse)"
