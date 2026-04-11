Feature: Richer Claude telemetry in orchard --json
  As an orchard consumer (orchardist, TUI, scripts)
  I want orchard --json to expose model, session age, last tool, current task,
  and raw token counts for each Claude session
  So that downstream tooling can make smarter decisions without scraping tmux panes

  # ===================================================================
  # Background and scope
  # ===================================================================
  #
  # Issue #220 proposes richer Claude telemetry. The issue's original
  # implementation note suggested parsing the tmux pane footer; that
  # approach is explicitly rejected here in favour of:
  #
  #   1. Claude Code hook stdin payloads (event name, tool_name, cwd,
  #      session_id, transcript_path, prompt) — already wired via
  #      crates/orchard/hooks/orchard-state.sh.
  #   2. The per-session Claude Code JSONL transcript (path carried on
  #      stdin as transcript_path) which holds authoritative
  #      message.usage and message.model values.
  #
  # Every new field is optional and back-compat: when unavailable, the
  # field is omitted from the emitted JSON (serde's default Option
  # behaviour). The existing `status` field is NOT changed.
  #
  # ---
  # Architectural note: JSONL parsing lives in a small Rust subcommand
  # (e.g. `orchard hook-enrich --transcript <path>`) that the shell hook
  # invokes. Bash dispatches events; Rust parses JSONL. This keeps the
  # hook fast (one binary fork, no jq streaming over 20 MB transcripts)
  # and all business logic (field shapes, edge cases) testable in Rust
  # without a shell harness.
  # ---
  #
  # Fields explicitly OUT OF SCOPE because they are either UI-only,
  # semantically ambiguous, or a maintenance treadmill (follow-up
  # issues will track them):
  #   - cost_usd — requires hardcoded pricing table that goes stale.
  #     We ship raw token counts instead so consumers can compute cost.
  #   - token_ctx_pct — the Claude Code UI shows cumulative live-context
  #     occupancy, not last-turn footprint. A "percentage" field would
  #     disagree with the UI and erode trust in the other telemetry.
  #     Defer until we have a signal that matches the UI.
  #   - token_5h_pct, token_7d_pct — rate-limit %, not exposed to hooks.
  #   - effort — reasoning effort, not exposed to hooks.
  #   - shells_running — background bash count, not exposed to hooks.
  #   - pr_refs — PR references extracted from prompts; not in MVP.

  Background:
    Given Claude state files are written to "$TMPDIR/orchard-claude-<tmux_session>.json"
    And each hook event carries a "transcript_path" on stdin pointing at the session JSONL
    And all new telemetry fields are serialized as optional and omitted when absent

  # ===================================================================
  # AC1 — --json surfaces new telemetry fields when available
  # ===================================================================

  @e2e
  Scenario: orchard --json emits all new telemetry fields for a live Claude session
    Given a tmux session "repo_47_claude" running Claude
    And the hook has written a state file with:
      | field                      | value                  |
      | state                      | "working"              |
      | model                      | "claude-opus-4-6"      |
      | last_tool                  | "Bash"                 |
      | current_task               | "fix flaky hook test"  |
      | session_start_ts           | 1700000000             |
      | input_tokens               | 50000                  |
      | output_tokens              | 800                    |
      | cache_creation_input_tokens| 10000                  |
      | cache_read_input_tokens    | 40000                  |
    When the user runs "orchard --json"
    Then the session's "claude" object contains:
      | key                     | type   |
      | status                  | string |
      | model                   | string |
      | lastTool                | string |
      | currentTask             | string |
      | sessionAgeSec           | number |
      | inputTokens             | number |
      | outputTokens            | number |
      | cacheCreationInputTokens| number |
      | cacheReadInputTokens    | number |
    And "claude.status" equals "working"
    And "claude.model" equals "claude-opus-4-6"
    And "claude.lastTool" equals "Bash"

  @integration
  Scenario: Missing telemetry fields are omitted (not null) from the JSON output
    Given a tmux session "repo_47_claude" running Claude
    And the hook state file contains only "state" and "session_id"
    When the user runs "orchard --json"
    Then the session's "claude" object contains key "status"
    And the keys "model", "lastTool", "currentTask", "sessionAgeSec", "inputTokens", "outputTokens", "cacheCreationInputTokens", "cacheReadInputTokens" are absent from the "claude" object

  @integration
  Scenario: Mixed old/new state files in the same TMPDIR are each surfaced with their own fields
    Given an "old-hook" state file with only state, session_id, tmux_session, cwd, event, timestamp
    And a "new-hook" state file with all enrichment fields populated
    When the user runs "orchard --json"
    Then the old-hook session emits only the legacy fields
    And the new-hook session emits the legacy fields plus the new telemetry fields

  # ===================================================================
  # AC2 — status shape preserved for back-compat
  # ===================================================================

  @unit
  Scenario: Existing claude.status values remain exactly "working" | "idle" | "input" | "none"
    Given a ClaudeState value
    When it is serialized for JSON output
    Then the possible string values are restricted to:
      | value     |
      | "working" |
      | "idle"    |
      | "input"   |
      | "none"    |

  @integration
  Scenario: Existing JSON output snapshot tests continue to pass without modification
    Given the existing fixtures in json_output tests that do not populate new fields
    When the serializer runs
    Then the serialized payload is byte-identical to the previous snapshot for the "claude" object
    And no new keys appear unless the underlying state file carries them

  # ===================================================================
  # AC3 — hook script writes enrichment fields to the state JSON
  # These are integration-level because each scenario spawns the real
  # shell hook with a crafted stdin payload and asserts on-disk state.
  # ===================================================================

  @integration
  Scenario: Hook records last_tool on PreToolUse
    Given a Claude session running in tmux session "repo_47_claude"
    When the hook script receives a PreToolUse event with tool_name "Edit"
    Then the state file contains:
      | field     | value  |
      | last_tool | "Edit" |

  @integration
  Scenario: Hook records current_task on UserPromptSubmit
    Given a Claude session running in tmux session "repo_47_claude"
    When the hook script receives a UserPromptSubmit event with prompt "refactor the cache module to support tagging"
    Then the state file contains:
      | field        | value                                          |
      | current_task | "refactor the cache module to support tagging" |

  @integration
  Scenario: current_task is truncated to 80 characters
    Given a Claude session running in tmux session "repo_47_claude"
    When the hook script receives a UserPromptSubmit event with a 200-character prompt
    Then the "current_task" field in the state file is exactly 80 characters long

  @integration
  Scenario: current_task keeps only the first line of a multiline prompt
    Given a Claude session running in tmux session "repo_47_claude"
    When the hook script receives a UserPromptSubmit event with prompt "fix the bug\n\nbackground: the hook swallows errors"
    Then the "current_task" field equals "fix the bug"

  @integration
  Scenario: Hook records session_start_ts and model on SessionStart
    Given no state file exists for session "repo_47_claude"
    When the hook script receives a SessionStart event with model "claude-opus-4-6" at unix time 1700000000
    Then the state file contains:
      | field            | value             |
      | session_start_ts | 1700000000        |
      | model            | "claude-opus-4-6" |

  @integration
  Scenario: session_start_ts is preserved across subsequent events
    Given a state file exists for "repo_47_claude" with session_start_ts 1700000000
    When the hook script receives a PreToolUse event at unix time 1700000500
    Then the state file still contains session_start_ts 1700000000
    And the state file's "timestamp" field reflects the new event time

  @integration
  Scenario: last_tool is cleared on Stop so idle sessions do not look busy
    Given a state file exists for "repo_47_claude" with last_tool "Bash"
    When the hook script receives a Stop event with stop_reason "end_turn"
    Then the state file no longer contains a "last_tool" field
    And the state file's "state" equals "idle"

  # ===================================================================
  # AC4 — JSONL transcript parsing (Rust subcommand)
  # ===================================================================

  @unit
  Scenario: hook-enrich Rust subcommand reads the most recent assistant message
    Given a JSONL transcript with a final assistant message containing:
      | field                             | value              |
      | message.usage.input_tokens        | 1000               |
      | message.usage.cache_creation_input_tokens | 500        |
      | message.usage.cache_read_input_tokens     | 20000      |
      | message.usage.output_tokens       | 800                |
      | message.model                     | "claude-opus-4-6"  |
    When the Rust subcommand "orchard hook-enrich --transcript <path>" runs
    Then stdout is a JSON object containing:
      | key                        | value              |
      | model                      | "claude-opus-4-6"  |
      | inputTokens                | 1000               |
      | outputTokens               | 800                |
      | cacheCreationInputTokens   | 500                |
      | cacheReadInputTokens       | 20000              |

  @unit
  Scenario: Missing transcript file yields an empty enrichment object
    Given the transcript file does not exist
    When the Rust subcommand "orchard hook-enrich --transcript <path>" runs
    Then stdout is the JSON object "{}"
    And the exit code is 0

  @unit
  Scenario: Empty transcript file yields an empty enrichment object
    Given the transcript file exists and is zero bytes
    When the Rust subcommand runs
    Then stdout is "{}"
    And the exit code is 0

  @unit
  Scenario: Skips malformed JSONL lines and still returns valid assistant message data
    Given a JSONL transcript containing:
      | line                                                                                                                                         |
      | {"type":"user","message":{"role":"user","content":"hi"}}                                                                                     |
      | not-valid-json                                                                                                                               |
      | {"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","usage":{"input_tokens":100,"output_tokens":50}}} |
    When the Rust subcommand runs
    Then the malformed line is skipped without error
    And stdout contains model "claude-opus-4-6"
    And stdout contains inputTokens 100

  @unit
  Scenario: Large transcripts tail-read only the bounded trailing region
    # Spec constraint: the Rust subcommand must not load files larger than
    # its tail ceiling (256 KB) into memory. The test fixture uses a
    # ~300 KB synthetic file — enough to prove the seek+tail behavior
    # without generating 20 MB of disk pressure in CI.
    Given a JSONL transcript file of 300 KB whose final assistant message is within the last 256 KB
    When the Rust subcommand runs
    Then only the bounded tail region is read (not the full file)
    And stdout reflects the final assistant message's model and usage

  @unit
  Scenario: Transcript with no assistant messages yields an empty object
    Given a JSONL transcript containing only "user" and "file-history-snapshot" entries
    When the Rust subcommand runs
    Then stdout is "{}"

  @unit
  Scenario: Sidechain messages are skipped — the last non-sidechain assistant message wins
    Given a JSONL transcript whose final two assistant messages are:
      | isSidechain | model             | input_tokens |
      | false       | claude-opus-4-6   | 100          |
      | true        | claude-haiku-4-5  | 200          |
    When the Rust subcommand runs
    Then stdout contains model "claude-opus-4-6"
    And stdout contains inputTokens 100

  @unit
  Scenario: Partial in-flight assistant message with no usage falls back to the previous complete message
    Given a JSONL transcript whose final assistant message has no usage field
    And the previous assistant message has valid model and usage
    When the Rust subcommand runs
    Then stdout reflects the previous message's model and usage

  @unit
  Scenario: Transcript paths containing spaces and unicode characters are read correctly
    Given a JSONL transcript at a path containing spaces and a non-ASCII character
    And the transcript contains a valid final assistant message
    When the Rust subcommand runs with that --transcript path
    Then stdout reflects the final assistant message's model and usage

  # ===================================================================
  # AC5 — session_age_sec computed at read time
  # ===================================================================

  @unit
  Scenario: session_age_sec is computed from session_start_ts at read time
    Given a state file with session_start_ts 1700000000
    And the current time is unix 1700003600
    When the Rust reader enriches the ClaudeInfo for JSON output
    Then sessionAgeSec equals 3600
    And the state file on disk does not contain a "session_age_sec" key

  @unit
  Scenario: session_age_sec stays fresh between hook writes
    Given a state file with session_start_ts 1700000000 and timestamp 10 seconds ago
    And the current time advances by 60 more seconds without a new hook event
    When orchard --json is invoked
    Then sessionAgeSec reflects the current elapsed time, not the time at last write

  # ===================================================================
  # AC6 — no Claude session means no claude substructure
  # ===================================================================

  @integration
  Scenario: Windows without a Claude process omit the claude object entirely
    Given a tmux window running "zsh" with no Claude process
    When the user runs "orchard --json"
    Then the corresponding session entry has no "claude" key
    And no new telemetry fields leak into unrelated sessions

  # ===================================================================
  # AC7 — unit test matrix for enrichment states
  # ===================================================================

  @unit
  Scenario: Fresh session (SessionStart only) — only model and session age available
    Given a Claude session that just received SessionStart with model "claude-opus-4-6"
    And no assistant messages exist in the transcript yet
    When the ClaudeInfo is derived for JSON output
    Then model is present from the SessionStart payload
    And sessionAgeSec is present
    And inputTokens, outputTokens, cacheCreationInputTokens, cacheReadInputTokens, lastTool, and currentTask are absent

  @unit
  Scenario: Heavy working — all fields populated
    Given a Claude session with state "working"
    And a transcript with multiple assistant messages whose final message has model and usage
    And a recent PreToolUse event for "Bash"
    And a recent UserPromptSubmit with prompt "ship the release"
    When the ClaudeInfo is derived
    Then status, model, sessionAgeSec, inputTokens, outputTokens, cacheReadInputTokens, cacheCreationInputTokens, lastTool, and currentTask are all present

  @unit
  Scenario: Idle after Stop — session age preserved, lastTool cleared, token counts preserved
    Given a Claude session that has received a Stop event with stop_reason "end_turn"
    When the ClaudeInfo is derived
    Then status equals "idle"
    And sessionAgeSec is present
    And lastTool is absent
    And inputTokens and outputTokens are present from the last assistant message in the transcript

  @unit
  Scenario: JSONL parse error — partial data still returned
    Given a transcript whose last line is truncated mid-JSON
    And the second-to-last line is a valid assistant message with usage and model
    When the ClaudeInfo is derived
    Then model is present from the second-to-last line
    And inputTokens are present from the second-to-last line
    And no panic or error propagates to the caller

  # ===================================================================
  # AC8 — hook script integration test
  # ===================================================================

  @integration
  Scenario: Hook script integration writes new fields end-to-end
    Given a temporary TMPDIR
    And a mock JSONL transcript with one assistant message containing model and usage
    When the hook script is invoked with a PreToolUse stdin payload referencing that transcript
    Then the state file on disk contains:
      | key                        | present |
      | state                      | yes     |
      | last_tool                  | yes     |
      | session_start_ts           | yes     |
      | model                      | yes     |
      | input_tokens               | yes     |
      | output_tokens              | yes     |
      | cache_read_input_tokens    | yes     |
      | cache_creation_input_tokens| yes     |

  @integration
  Scenario: Hook script integration for UserPromptSubmit populates current_task
    Given a temporary TMPDIR
    When the hook script is invoked with a UserPromptSubmit stdin payload with prompt "draft the release notes"
    Then the state file contains current_task equal to "draft the release notes"

  @integration
  Scenario: Hook script integration for SessionStart populates session_start_ts and model
    Given a temporary TMPDIR
    When the hook script is invoked with a SessionStart stdin payload at unix time 1700000000 and model "claude-opus-4-6"
    Then the state file contains session_start_ts equal to 1700000000
    And the state file contains model equal to "claude-opus-4-6"

  @integration
  Scenario: Hook script still writes state when transcript_path points at a missing file
    Given a temporary TMPDIR
    And a PreToolUse stdin payload whose transcript_path points at a nonexistent file
    When the hook script runs
    Then the state file is written
    And the state file contains state "working" and last_tool
    And the state file does not contain any token count fields

  # ===================================================================
  # AC9 — remote deployment graceful degradation
  #
  # A live SSH round-trip would make this flaky and slow. The behaviour
  # we actually care about is "the merge/deserialization layer handles
  # legacy-shaped state files without surfacing errors" — which is a
  # pure Rust unit test against a fixture directory.
  # ===================================================================

  @unit
  Scenario: Legacy-shaped state files (no enrichment fields) deserialize cleanly and omit new fields in JSON output
    Given a state file containing only: state, session_id, tmux_session, cwd, event, timestamp
    When the Rust reader parses the file into a ClaudeStateFile
    Then deserialization succeeds
    And the derived JsonClaudeInfo has "status" present
    And the derived JsonClaudeInfo has all new enrichment fields absent
    And no error is logged or returned

  # ===================================================================
  # NON-goals — scenarios that MUST NOT be implemented in this issue
  # ===================================================================
  #
  # These are listed as explicit non-scenarios so future contributors
  # understand the boundary. They are not executable; they document
  # decisions reached during planning and /challenge review.
  #
  # NON-GOAL: tmux pane footer parsing. The hook + JSONL transcript is
  #           the authoritative data source.
  # NON-GOAL: cost_usd. Hardcoded pricing tables go stale silently.
  #           We ship raw token counts; consumers compute cost.
  # NON-GOAL: token_ctx_pct. Last-turn footprint disagrees with the
  #           Claude Code UI's cumulative occupancy number. Shipping a
  #           field that disagrees with the UI would erode trust.
  # NON-GOAL: token_5h_pct / token_7d_pct. Not exposed to hooks.
  # NON-GOAL: effort. Not exposed to hooks.
  # NON-GOAL: shells_running. Not exposed to hooks.
  # NON-GOAL: pr_refs. Deferred to a follow-up issue.
  # NON-GOAL: TUI rendering changes. A separate issue will surface
  #           these fields in the TUI row detail; this issue is strictly
  #           about --json.
  # NON-GOAL: Remote hook auto-deployment. Users install remote hooks
  #           manually. The graceful-degradation scenario (AC9) covers
  #           pre-deployment state.
