Feature: heal must never kill the invoking tmux session
  As an orchard user running `orchard heal --fix` from inside a tmux session
  I want heal to identify the session it is running in and refuse to kill it
  So that running heal can never destroy the agent that just asked for cleanup

  Background:
    Given a repo with orchard configured
    And tmux is available

  # --- AC 1: Self-detection and exclusion from kill set ---

  @e2e
  Scenario: heal --fix from inside tmux excludes the invoking session from the kill set
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" has no matching worktree (would otherwise be classified as orphan)
    And tmux session "myrepo_old-feature" also has no matching worktree
    When the user runs "orchard heal --fix"
    Then the invoking session "orchardist" is NOT killed
    And the orchard process continues running to completion
    And tmux session "myrepo_old-feature" IS killed
    And the report shows the invoking session was skipped

  @integration
  Scenario: handle_heal reads the invoking session name from tmux before diagnosing
    Given the user is inside tmux session "orchardist"
    When handle_heal runs
    Then it queries the current tmux session name via "tmux display-message -p '#S'"
    And it passes the resulting session name as current_session to diagnose

  @integration
  Scenario: outside tmux, heal behaves as before with no self-protection applied
    Given the user is NOT inside tmux (the TMUX env var is unset)
    And tmux session "myrepo_old-feature" has no matching worktree
    When the user runs "orchard heal --fix"
    Then current_session is None
    And no finding is flagged is_self
    And tmux session "myrepo_old-feature" IS killed

  @integration
  Scenario: $TMUX is set but tmux display-message fails (server gone)
    Given the TMUX env var is set
    And "tmux display-message -p '#S'" returns a non-zero exit code
    When current_session_name is called
    Then it returns None without panicking
    And heal proceeds with no self-protection (degrades gracefully)

  @unit
  Scenario: diagnose marks a KillSession finding as is_self when name matches current_session
    Given a tmux session named "orchardist" with no matching worktree
    And current_session = Some("orchardist")
    When diagnose is invoked
    Then the resulting finding for "orchardist" has action KillSession("orchardist")
    And that finding has is_self = true

  @unit
  Scenario: diagnose does not mark non-matching KillSession findings as is_self
    Given a tmux session named "myrepo_old-feature" with no matching worktree
    And current_session = Some("orchardist")
    When diagnose is invoked
    Then the finding for "myrepo_old-feature" has is_self = false

  @unit
  Scenario: diagnose with current_session = None never sets is_self = true
    Given multiple orphaned tmux sessions
    And current_session = None
    When diagnose is invoked
    Then every finding has is_self = false

  @unit
  Scenario: is_self is false on findings whose action is not KillSession
    Given a stale claude state file
    And a stale cache entry
    And a worktree for a merged PR
    And current_session = Some("orchardist")
    When diagnose is invoked
    Then the StaleClaudeState finding has is_self = false
    And the StaleCache finding has is_self = false
    And the MergedPrWorktree finding has is_self = false

  @unit
  Scenario: apply_fixes never invokes kill_tmux_session for an is_self finding
    Given a finding with action KillSession("orchardist") and is_self = true
    When apply_fixes runs
    Then tmux::kill_tmux_session is NOT called for "orchardist"
    And a FixResult is recorded with success = true
    And the FixResult message starts with "Skipped session"
    And the FixResult message mentions "refusing to kill self"

  # --- AC 2: Abort with clear error when invoking session is Error-severity ---

  @e2e
  Scenario: heal --fix aborts when the invoking session is classified Error severity
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" points to a working directory that no longer exists
    And the resulting finding has severity Error and is_self = true
    When the user runs "orchard heal --fix"
    Then the process prints "refusing to kill the session I'm running in; run from outside tmux" to stderr
    And the process exits with a non-zero status code
    And NO fixes from apply_fixes are executed
    And tmux session "orchardist" is NOT killed

  @integration
  Scenario: Warning-severity self finding does not trigger abort
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" is classified as orphan with severity Warning and is_self = true
    And tmux session "myrepo_old-feature" is also orphaned with severity Warning
    When the user runs "orchard heal --fix"
    Then the process does NOT abort
    And apply_fixes runs to completion
    And tmux session "orchardist" is skipped (not killed)
    And tmux session "myrepo_old-feature" IS killed

  @unit
  Scenario: handle_heal detects an Error-severity is_self finding before applying any fixes
    Given a HealReport containing one finding with severity = Error, is_self = true, action = KillSession("orchardist")
    And other findings of various severities
    When the abort-check pass runs
    Then it returns an abort signal with the AC-specified error message
    And apply_fixes is never invoked

  # --- AC 3: Dry-run marks the invoking session as "skipped (self)" ---

  @e2e
  Scenario: heal --dry-run from inside tmux clearly marks the invoking session as skipped (self)
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" has no matching worktree (would otherwise be classified as orphan)
    When the user runs "orchard heal --dry-run"
    Then the output includes the string "skipped (self)" on the line for "orchardist"
    And no sessions are killed
    And no files are deleted

  @integration
  Scenario: format_report renders is_self findings with a "skipped (self)" annotation
    Given a HealFinding with action KillSession("orchardist") and is_self = true
    When format_report is invoked
    Then the output for that finding contains "skipped (self)"
    And the warning icon is preserved

  @integration
  Scenario: --json output exposes is_self on findings
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" would be classified as orphan
    When the user runs "orchard heal --json"
    Then the output is valid JSON
    And the finding for "orchardist" has "is_self": true
    And other findings have "is_self": false

  @unit
  Scenario: format_report leaves non-is_self KillSession findings unchanged
    Given a HealFinding with action KillSession("myrepo_old-feature") and is_self = false
    When format_report is invoked
    Then the output for that finding does NOT contain "skipped (self)"

  # --- AC 4: Regression coverage of the self-kill path ---

  @integration
  Scenario: regression — full pipeline from inside the named tmux session never kills self
    Given the user is inside tmux session "orchardist"
    And tmux session "orchardist" matches no worktree and no StandaloneConfig entry
    When diagnose runs with current_session = Some("orchardist")
    And apply_fixes runs over the resulting findings
    Then no call to tmux::kill_tmux_session targets "orchardist"
    And the FixResult collection contains a "Skipped session" entry for "orchardist"

  @integration
  Scenario: regression — sister window of the invoking session is still treated as self
    Given the user is inside a sister window of tmux session "orchardist"
    And "tmux display-message -p '#S'" still returns "orchardist" (outer session name)
    When the user runs "orchard heal --fix"
    Then current_session = Some("orchardist")
    And the orchardist session is skipped, not killed

  @unit
  Scenario: helper tmux::current_session_name returns None when TMUX env var is unset
    Given the TMUX environment variable is not set
    When current_session_name is called
    Then it returns None
    And it does not shell out to tmux

  @unit
  Scenario: helper tmux::current_session_name returns Some(name) when inside tmux
    Given the TMUX environment variable is set
    And "tmux display-message -p '#S'" returns "orchardist" with exit code 0
    When current_session_name is called
    Then it returns Some("orchardist")

  # --- AC Coverage Map ---
  # AC 1: "heal --fix detects invoking tmux session via $TMUX / tmux display-message and excludes it from the kill set"
  #   → @e2e Scenario: heal --fix from inside tmux excludes the invoking session from the kill set
  #   → @integration Scenario: handle_heal reads the invoking session name from tmux before diagnosing
  #   → @integration Scenario: outside tmux, heal behaves as before with no self-protection applied
  #   → @integration Scenario: $TMUX is set but tmux display-message fails (server gone)
  #   → @unit Scenario: diagnose marks a KillSession finding as is_self when name matches current_session
  #   → @unit Scenario: diagnose does not mark non-matching KillSession findings as is_self
  #   → @unit Scenario: diagnose with current_session = None never sets is_self = true
  #   → @unit Scenario: is_self is false on findings whose action is not KillSession
  #   → @unit Scenario: apply_fixes never invokes kill_tmux_session for an is_self finding
  #
  # AC 2: "If the invoking session IS classified as stale/orphan, heal --fix aborts with a clear error"
  #   → @e2e Scenario: heal --fix aborts when the invoking session is classified Error severity
  #   → @integration Scenario: Warning-severity self finding does not trigger abort
  #   → @unit Scenario: handle_heal detects an Error-severity is_self finding before applying any fixes
  #
  # AC 3: "heal --dry-run from inside the same session prints what it *would* do without killing anything,
  #        and clearly marks the invoking session as 'skipped (self)'"
  #   → @e2e Scenario: heal --dry-run from inside tmux clearly marks the invoking session as skipped (self)
  #   → @integration Scenario: format_report renders is_self findings with a "skipped (self)" annotation
  #   → @integration Scenario: --json output exposes is_self on findings
  #   → @unit Scenario: format_report leaves non-is_self KillSession findings unchanged
  #
  # AC 4: "Regression test covers the self-kill path"
  #   → @integration Scenario: regression — full pipeline from inside the named tmux session never kills self
  #   → @integration Scenario: regression — sister window of the invoking session is still treated as self
  #   → @unit Scenario: helper tmux::current_session_name returns None when TMUX env var is unset
  #   → @unit Scenario: helper tmux::current_session_name returns Some(name) when inside tmux
