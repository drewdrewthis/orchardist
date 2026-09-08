# orchardist#851 — orchard shell: create a default inner session instead of failing fast
Feature: orchard shell creates a default inner session when none exists
  As an orchard user running `orchard shell`
  I want the wrapper to start even when the inner tmux server has no sessions
  So that the sidebar comes up on a fresh machine without me hand-running `tmux new` first

  Background:
    Given the orchard daemon is running and reachable
    And no outer tmux server exists on socket "orchard-shell"

  @integration
  Scenario: No inner server at all boots with a fresh default session
    Given no tmux server exists on the inner socket X
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 0
    And "tmux -L X list-sessions" shows exactly one session named "main"
    And "tmux -L Y list-panes" on the outer session shows exactly two panes: the sidebar pane and the inner-attach pane

  # The session-less-but-present inner server is a fake-tmux construct: a real
  # tmux >=3.x exits the server when its last session dies, so this branch is
  # provable only against the fake (go test), not a live transcript.
  @unit
  Scenario: Inner server present but empty boots with a fresh default session
    Given an inner tmux server on socket X is present and "list-sessions" returns empty without erroring
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 0
    And "tmux -L X list-sessions" shows exactly one session named "main"
    And "tmux -L Y list-panes" on the outer session shows exactly two panes: the sidebar pane and the inner-attach pane

  @integration
  Scenario: Existing inner sessions are attached unchanged, none created
    Given the inner server has sessions "myrepo_main" and "myrepo_feature-x"
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 0
    And pane 0.1 attaches to the most-recently-attached existing session
    And no additional inner session is created

  @integration
  Scenario: A real tmux error fails fast and mutates nothing
    Given the inner socket X path is unwritable so "new-session" fails
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 1
    And the failure prints the underlying tmux error
    And "tmux -L X list-sessions" after the run still reports no server
    And no outer session is created on socket Y

  @integration
  Scenario: The former empty-inner fail-fast contract is replaced, not bypassed
    Given no tmux server exists on the inner socket X
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command does not print the "orchard new" hint
    And the command does not exit non-zero for the empty-inner case

  @integration
  Scenario: The removed hint text no longer appears and genuine errors carry the tmux cause
    Given the inner socket X path is unwritable so "new-session" fails
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the error message does not contain "Start one first:  orchard new"
    And the error message carries the underlying tmux error text

  @integration
  Scenario: The auto-created session's working directory is $HOME
    Given no tmux server exists on the inner socket X
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then "tmux -L X display -p -t main '#{pane_current_path}'" prints "$HOME"

  @integration
  Scenario: --session names the created session when no inner server exists
    Given no tmux server exists on the inner socket X
    When I run "orchard-shell --session foo --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 0
    And "tmux -L X list-sessions" shows exactly one session named "foo"

  # Fake-tmux construct, same as the AC2 empty-server scenario: provable only
  # against the fake (go test), not a live tmux transcript.
  @unit
  Scenario: --session names the created session when the inner server is empty
    Given an inner tmux server on socket X is present and "list-sessions" returns empty without erroring
    When I run "orchard-shell --session foo --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 0
    And "tmux -L X list-sessions" shows exactly one session named "foo"

  @integration
  Scenario: An unresolvable tmux binary fails fast and mutates nothing
    Given tmux is not on PATH
    And tmux is absent from every orchard-shell fallback path "/opt/homebrew/bin", "/usr/local/bin" and "/usr/bin"
    When I run "orchard-shell --inner-socket X --outer-socket Y --detach"
    Then the command exits with status 1
    And the failure reports that tmux was not found
    And no session is created on socket X
    And no outer session is created on socket Y

  # --- AC Coverage Map ---
  # AC1: "No inner server → exit 0, one session 'main', outer shows exactly two panes"
  #      → Scenario: No inner server at all boots with a fresh default session
  # AC2: "Inner server present but empty (list-sessions empty, not error) → identical to AC1"
  #      → Scenario: Inner server present but empty boots with a fresh default session
  # AC3: "Existing inner sessions → unchanged, no extra session created"
  #      → Scenario: Existing inner sessions are attached unchanged, none created
  # AC4: "Real tmux error (unwritable socket) → exit 1, tmux error printed, no session, Y untouched"
  #      → Scenario: A real tmux error fails fast and mutates nothing
  # AC5: "#747 fail-fast tests updated (not deleted); stale scenario updated; new tests cover AC1-AC4, AC7-AC9"
  #      → Scenario: The former empty-inner fail-fast contract is replaced, not bypassed
  #      (the Go test edits + the outer-shell-launch-reattach.feature scenario update are the coder's, per the plan)
  # AC6: "docs auto-create; errors.go 'orchard new' hint removed; noInnerServerError removed/repointed"
  #      → Scenario: The removed hint text no longer appears and genuine errors carry the tmux cause
  #      (docs/outer-shell.md prose is verified by quoted diff / doc review, not a runtime scenario)
  # AC7: "auto-created session cwd is $HOME"
  #      → Scenario: The auto-created session's working directory is $HOME
  # AC8: "--session foo with no inner server AND with empty inner server → one session named foo"
  #      → Scenario: --session names the created session when no inner server exists
  #      → Scenario: --session names the created session when the inner server is empty
  # AC9: "tmux unresolvable (not on PATH and not at any fallback path) → exit 1, tmux-not-found, no session on X, Y untouched"
  #      → Scenario: An unresolvable tmux binary fails fast and mutates nothing
