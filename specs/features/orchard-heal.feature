Feature: Orchard heal command for self-repair and cleanup
  As an orchard user
  I want a heal command that audits and repairs drifted state
  So I can keep my environment clean without manual investigation

  Background:
    Given a repo with orchard configured
    And tmux is available

  # --- Dry Run (default mode) ---

  @unit
  Scenario: Dry run reports what it would do without making changes
    Given there is an orphaned tmux session "myrepo_old-feature" with no matching worktree
    And there is a stale claude state file for dead session "myrepo_dead"
    When the user runs "orchard heal"
    Then the output shows a health check report
    And the report lists the orphaned session as a warning
    And the report lists the stale claude state file as a warning
    And no sessions are killed
    And no files are deleted
    And the output suggests "Run `orchard heal --fix` to repair."

  @unit
  Scenario: Dry run reports all-healthy when nothing is wrong
    Given all tmux sessions have matching worktrees
    And all worktrees have valid branches
    And no stale cache or claude state files exist
    When the user runs "orchard heal"
    Then the output shows only success checkmarks
    And the output does not suggest running --fix

  # --- Orphaned Tmux Sessions ---

  @unit
  Scenario: Detect tmux session with no matching worktree
    Given tmux session "myrepo_old-feature" exists
    And no worktree path matches that session's directory
    When the user runs "orchard heal"
    Then the report warns about orphaned session "myrepo_old-feature"

  @unit
  Scenario: Fix kills orphaned tmux session
    Given tmux session "myrepo_old-feature" exists
    And no worktree path matches that session's directory
    When the user runs "orchard heal --fix"
    Then tmux session "myrepo_old-feature" is killed
    And the report confirms the session was killed

  @unit
  Scenario: Detect tmux session whose directory doesn't exist on disk
    Given tmux session "myrepo_gone" points to "/tmp/nonexistent-path"
    And that path does not exist on disk
    When the user runs "orchard heal --fix"
    Then tmux session "myrepo_gone" is killed

  # --- Stale Claude State Files ---

  @unit
  Scenario: Detect claude state file for dead tmux session
    Given /tmp/orchard-claude-abc123.json exists with tmux_session "myrepo_dead"
    And no tmux session named "myrepo_dead" exists
    When the user runs "orchard heal"
    Then the report warns about stale claude state file for "myrepo_dead"

  @unit
  Scenario: Fix deletes claude state file for dead session
    Given /tmp/orchard-claude-abc123.json exists with tmux_session "myrepo_dead"
    And no tmux session named "myrepo_dead" exists
    When the user runs "orchard heal --fix"
    Then /tmp/orchard-claude-abc123.json is deleted
    And the report confirms the state file was cleaned up

  # --- Stale Cache Entries ---

  @unit
  Scenario: Detect cache files for non-existent repo
    Given cache file "ghost_repo_issues.json" exists in ~/.cache/orchard/
    And no git remote matches "ghost/repo"
    When the user runs "orchard heal"
    Then the report warns about stale cache entry "ghost_repo_issues.json"

  @unit
  Scenario: Fix deletes stale cache files
    Given cache file "ghost_repo_issues.json" exists in ~/.cache/orchard/
    And no git remote matches "ghost/repo"
    When the user runs "orchard heal --fix"
    Then "ghost_repo_issues.json" is deleted from ~/.cache/orchard/
    And the report confirms the cache entry was removed

  # --- Worktrees for Merged PRs ---

  @unit
  Scenario: Flag worktree whose PR is merged
    Given worktree ".worktrees/issue3-tests" exists with branch "issue3/tests"
    And PR #12 for branch "issue3/tests" has state "merged"
    When the user runs "orchard heal"
    Then the report flags worktree ".worktrees/issue3-tests" as stale
    And the message mentions "PR #12 merged"

  @unit
  Scenario: Flag worktree whose PR is closed
    Given worktree ".worktrees/issue5-fix" exists with branch "issue5/fix"
    And PR #15 for branch "issue5/fix" has state "closed"
    When the user runs "orchard heal"
    Then the report flags worktree ".worktrees/issue5-fix" as stale

  @unit
  Scenario: Fix does not auto-delete worktrees for merged PRs
    Given worktree ".worktrees/issue3-tests" exists with branch "issue3/tests"
    And PR #12 for branch "issue3/tests" has state "merged"
    When the user runs "orchard heal --fix"
    Then worktree ".worktrees/issue3-tests" is NOT deleted
    And the report flags it for manual cleanup

  # --- Worktrees for Closed Issues ---

  @unit
  Scenario: Flag worktree whose issue is closed
    Given worktree ".worktrees/issue8-refactor" exists with branch "issue8/refactor"
    And issue #8 has state "closed"
    And no active tmux session exists for that worktree
    When the user runs "orchard heal"
    Then the report flags worktree ".worktrees/issue8-refactor" as stale

  # --- Session Naming Mismatches ---

  @unit
  Scenario: Report session name that doesn't match derived pattern
    Given repo name is "myrepo" and branch is "feature/login"
    And tmux session "wrong-name" points to the worktree for that branch
    When the user runs "orchard heal"
    Then the report warns about naming mismatch
    And the message shows expected "myrepo_feature-login" vs actual "wrong-name"

  # --- Multiple Sessions Per Worktree ---

  @unit
  Scenario: Report multiple sessions pointing to same worktree
    Given worktree ".worktrees/issue10-api" exists
    And tmux sessions "myrepo_issue10-api" and "extra-session" both point to that worktree
    When the user runs "orchard heal"
    Then the report warns about multiple sessions for the same worktree

  # --- Health Report Format ---

  @unit
  Scenario: Report uses structured output with icons
    Given there are 5 healthy sessions and 1 orphaned session
    And there are 3 healthy worktrees and 1 stale worktree
    When the user runs "orchard heal"
    Then the output includes a line like "5 tmux sessions OK" with a checkmark
    And the output includes a line like "3 worktrees OK" with a checkmark
    And the output includes warning lines with a warning icon
    And the output includes error/flag lines with an error icon

  # --- JSON Output ---

  @unit
  Scenario: Heal supports --json flag for machine-readable output
    Given there is an orphaned tmux session
    When the user runs "orchard heal --json"
    Then the output is valid JSON
    And the JSON contains a "findings" array
    And each finding has "category", "severity", "message", and "action" fields

  # --- TUI Integration ---

  @unit
  Scenario: Press h in list view to run heal
    Given the TUI is running in list view
    When the user presses 'h'
    Then the heal check runs
    And the results are displayed in a heal view

  @unit
  Scenario: Heal view shows fix option
    Given the TUI is showing heal results with warnings
    When the user presses 'f'
    Then the fix actions are executed
    And the heal view updates to show the results

  # --- CLI Entry Point ---

  @unit
  Scenario: Heal command is accessible as a subcommand
    When the user runs "orchard heal"
    Then the heal check executes and prints a report

  @unit
  Scenario: Heal --fix flag triggers repairs
    When the user runs "orchard heal --fix"
    Then the heal check executes and performs repairs
    And the report shows what was fixed
