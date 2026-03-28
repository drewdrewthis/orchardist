Feature: Fetch Claude state files from remote hosts over SSH
  As an orchard user with remote Claude sessions
  I want remote Claude hook state files read over SSH
  So that remote sessions show accurate state, cost, and model data instead of unreliable terminal scraping

  Background:
    Given a repo "acme/webapp" with remote host "ubuntu@10.0.0.1"
    And the remote Claude hook writes state files to "$TMPDIR/orchard-claude-{session}.json"

  # ===================================================================
  # SSH command construction — batching and TMPDIR expansion
  # ===================================================================

  @unit
  Scenario: Remote SSH command batches Claude state read with tmux list-sessions
    When refresh_tmux_sessions is called for host "ubuntu@10.0.0.1"
    Then a single SSH command is sent that includes both:
      | component                                                        |
      | tmux list-sessions                                               |
      | cat of orchard-claude-*.json files from the remote $TMPDIR       |
    And no additional SSH round-trip is made for Claude state files

  @unit
  Scenario: TMPDIR expands on the remote shell not locally
    When the SSH command string is constructed for fetching Claude state
    Then the command uses single-quoted '${TMPDIR:-/tmp}' so the variable expands on the remote shell
    And the local machine's temp directory path does not appear in the command

  @unit
  Scenario: Sentinel delimiter separates tmux output from Claude state JSON
    When the batched SSH command runs
    Then the output contains a "---CLAUDE_STATE---" sentinel line
    And everything before the sentinel is tmux session list output
    And everything after the sentinel is concatenated Claude state JSON (or empty)

  # ===================================================================
  # Parsing remote Claude state from batched SSH output
  # ===================================================================

  @unit
  Scenario: Parses Claude state JSON from batched SSH output with fresh state files
    Given the batched SSH output contains after the sentinel:
      """json
      {"state":"working","session_id":"s1","tmux_session":"repo_47_claude","cwd":"/workspace","event":"PreToolUse","timestamp":"<now>"}
      {"state":"idle","session_id":"s2","tmux_session":"repo_48_main","cwd":"/workspace","event":"Stop","timestamp":"<now>"}
      """
    When the output is parsed
    Then 2 remote Claude state files are extracted
    And the state for "repo_47_claude" is "working"
    And the state for "repo_48_main" is "idle"

  @unit
  Scenario: Parses output when no Claude state files exist on remote
    Given the batched SSH output contains nothing after the sentinel
    When the output is parsed
    Then 0 remote Claude state files are extracted
    And no error is raised

  @unit
  Scenario: Malformed JSON lines in remote output are skipped
    Given the batched SSH output contains after the sentinel:
      """
      {"state":"working","session_id":"s1","tmux_session":"repo_47_claude","cwd":"/workspace","event":"PreToolUse","timestamp":"<now>"}
      not valid json
      {"state":"idle","session_id":"s2","tmux_session":"repo_48_main","cwd":"/workspace","event":"Stop","timestamp":"<now>"}
      """
    When the output is parsed
    Then 2 remote Claude state files are extracted
    And the malformed line is silently skipped

  # ===================================================================
  # Error handling — no files vs SSH failure
  # ===================================================================

  @unit
  Scenario: No Claude state files on remote is not an error
    Given the remote has no orchard-claude-*.json files in $TMPDIR
    When the batched SSH command runs
    Then the cat command returns exit code 1 (no matching files)
    And the overall SSH command succeeds due to the "; true" suffix
    And the parsed result is an empty list of Claude state files

  @integration
  Scenario: SSH failure leaves existing cache intact
    Given cached tmux sessions exist from a previous refresh
    When refresh_tmux_sessions is called for host "ubuntu@10.0.0.1"
    And the SSH connection fails
    Then the existing tmux sessions cache is not overwritten
    And an error is logged
    And the TUI continues to display previously cached sessions

  # ===================================================================
  # Staleness — discard old state files
  # ===================================================================

  @unit
  Scenario: Remote state file within 30 seconds is fresh
    Given a remote Claude state file for "repo_47_claude" with timestamp 25 seconds ago
    When the state is derived for session "repo_47_claude"
    Then the hook state is used directly
    And terminal scraping is not performed

  @unit
  Scenario: Remote state file older than 30 seconds is stale
    Given a remote Claude state file for "repo_47_claude" with timestamp 35 seconds ago
    When the state is derived for session "repo_47_claude"
    Then the hook state is discarded as stale
    And the derive step falls back to terminal scraping

  @unit
  Scenario: Remote staleness threshold is 30 seconds (not the local 300s)
    Given a remote Claude state file with timestamp 60 seconds ago
    When the state is derived for the remote session
    Then the file is treated as stale
    And terminal scraping is used as fallback

  # ===================================================================
  # Cache storage — remote Claude state alongside tmux sessions
  # ===================================================================

  @unit
  Scenario: Remote Claude state files are stored in the tmux sessions cache
    Given 3 remote Claude state files are parsed from SSH output
    When the tmux sessions cache for "ubuntu@10.0.0.1" is written
    Then the cache includes the Claude state data associated with the host
    And the data is available to derive without additional SSH calls

  @unit
  Scenario: Local Claude state files are still read from local temp directory
    Given local Claude state files exist in the system temp directory
    When build_state runs
    Then local state files are read from std::env::temp_dir() as before
    And remote state files come from the tmux sessions cache

  # ===================================================================
  # Derive integration — remote sessions use hook state
  # ===================================================================

  @integration
  Scenario: Remote session shows accurate claude_state from hook file
    Given a remote tmux session "repo_47_claude" on host "ubuntu@10.0.0.1"
    And a fresh remote Claude state file with state "working" for "repo_47_claude"
    When derive_all_repos runs
    Then the enriched session for "repo_47_claude" has claude_state "working"
    And the state was sourced from the hook file, not terminal scraping

  @integration
  Scenario: Remote session with enrichment data includes cost and model
    Given a remote Claude state file for "repo_47_claude" with:
      | field              | value   |
      | state              | working |
      | context_window_pct | 73.0    |
      | cost_usd           | 1.23    |
      | model              | opus    |
    When the session is enriched
    Then context_window_pct is 73.0
    And cost_usd is 1.23
    And model is "opus"

  @integration
  Scenario: Remote session falls back to scraping when no state file exists
    Given a remote tmux session "repo_48_main" on host "ubuntu@10.0.0.1"
    And no Claude state file exists for "repo_48_main"
    And the terminal output contains "(y/n)"
    When the session is enriched
    Then the claude_state is derived from terminal scraping
    And the state is "input"

  @integration
  Scenario: Remote session falls back to scraping when state file is stale
    Given a remote tmux session "repo_47_claude" on host "ubuntu@10.0.0.1"
    And a remote Claude state file with state "working" and timestamp 60 seconds ago
    When the session is enriched
    Then terminal scraping is used as the source
    And the stale "working" state from the file is not used

  # ===================================================================
  # Bonus: capture-pane skip optimization
  # ===================================================================

  @unit
  Scenario: capture-pane SSH call is skipped when fresh hook state exists
    Given a remote session "repo_47_claude" has a fresh Claude state file
    When refresh_tmux_sessions fetches pane data for "repo_47_claude"
    Then the capture-pane SSH call is skipped for that session
    And the total SSH calls are reduced by 1 per session with fresh state

  @unit
  Scenario: capture-pane SSH call is still made when no hook state exists
    Given a remote session "repo_48_main" has no Claude state file
    When refresh_tmux_sessions fetches pane data for "repo_48_main"
    Then the capture-pane SSH call is made as usual
    And the terminal output is stored in last_output_lines

  # ===================================================================
  # End-to-end — full remote refresh cycle
  # ===================================================================

  @e2e
  Scenario: Full remote refresh reads Claude state alongside tmux sessions
    Given a remote host "ubuntu@10.0.0.1" running:
      | tmux session      | claude state file         |
      | repo_47_claude    | state: "working"          |
      | repo_48_main      | (none)                    |
      | repo_49_claude    | state: "idle", stale: yes |
    When the background cache refresh runs
    Then a single SSH command fetches both tmux sessions and Claude state
    And the TUI shows:
      | session           | claude_state | source        |
      | repo_47_claude    | working      | hook file     |
      | repo_48_main      | (scraped)    | capture-pane  |
      | repo_49_claude    | (scraped)    | capture-pane  |

  @e2e
  Scenario: Remote Claude state appears in JSON output
    Given fresh remote Claude state files exist on "ubuntu@10.0.0.1"
    When the user runs "orchard --json"
    Then the JSON output includes claude_state for remote sessions
    And the state values come from hook files, not scraping
