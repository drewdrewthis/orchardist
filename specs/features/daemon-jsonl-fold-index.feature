Feature: Expand daemon jsonl provider with derivable folded fields
  As an orchard daemon consumer (clients, plugins, CLI)
  I want session-derived state surfaced uniformly from session.jsonl
  So that sidecar duplication can be retired and cross-session jsonl reads stop being reinvented per consumer

  Background:
    Given the daemon is running on host "lw-dev"
    And conversation jsonl files live at "~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl"
    And the claudeprojects provider owns an in-memory fold index per session
    And the contracts provider continues to own contract-event folding from its own event log

  # ===========================================================================
  # AC 1 - Conversation.recap: String folded from latest __update_recap tool_use
  # ===========================================================================

  @unit
  Scenario: recap folds from the latest __update_recap tool_use in file order
    Given a jsonl with two assistant records carrying tool_use entries whose name suffix is "__update_recap"
    And the first carries input.text "early recap"
    And the later carries input.text "current recap"
    When the fold index resolves Conversation.recap
    Then the value is "current recap"

  @unit
  Scenario: recap is null when no __update_recap event has been written
    Given a jsonl with no tool_use record whose name suffix is "__update_recap"
    When the fold index resolves Conversation.recap
    Then the value is null

  @unit
  Scenario: recap matches by suffix, not by exact plugin name
    Given a jsonl whose latest recap tool_use name is "mcp__plugin_orchard-conversations_conversations__update_recap"
    When the fold index resolves Conversation.recap
    Then the value reflects that tool_use's input.text
    And the fold rule does not require the exact string "mcp__plugin_claude-conversations_conversations__update_recap"

  @integration
  Scenario: recap reflects a mid-session update_recap within one watcher refresh tick
    Given a watched jsonl with no recap event
    When an __update_recap tool_use is appended with input.text "fresh recap"
    Then within one watcher refresh tick the GraphQL query "{ conversation(uuid: $u) { recap } }" returns "fresh recap"

  # ===========================================================================
  # AC 2 - Conversation.status: ConversationStatus folded from latest
  #        __update_status tool_use; enum RESOLVED|UNRESOLVED|null
  # ===========================================================================

  @unit
  Scenario: status folds from the latest __update_status tool_use
    Given a jsonl with an __update_status tool_use whose input.status is "resolved"
    When the fold index resolves Conversation.status
    Then the value is RESOLVED

  @unit
  Scenario: status enum maps unresolved correctly
    Given a jsonl with an __update_status tool_use whose input.status is "unresolved"
    When the fold index resolves Conversation.status
    Then the value is UNRESOLVED

  @unit
  Scenario: status is null when no __update_status event has been written
    Given a jsonl with no tool_use record whose name suffix is "__update_status"
    When the fold index resolves Conversation.status
    Then the value is null

  @unit
  Scenario: status null means never-set even if a later __update_status writes an empty input
    Given a jsonl with a later __update_status tool_use whose input.status is empty
    When the fold index resolves Conversation.status
    Then the value is null

  @integration
  Scenario: status updates live on jsonl append
    Given a watched jsonl where Conversation.status currently resolves to UNRESOLVED
    When an __update_status tool_use is appended with input.status "resolved"
    Then within one watcher refresh tick the GraphQL query "{ conversation(uuid: $u) { status } }" returns RESOLVED

  # ===========================================================================
  # AC 3 - Conversation.lastPromptText: String folded from latest
  #        type:"last-prompt" record
  # ===========================================================================

  @unit
  Scenario: lastPromptText folds from latest "last-prompt" record
    Given a jsonl with three "last-prompt" records whose payloads are "p1", "p2", and "p3" in file order
    When the fold index resolves Conversation.lastPromptText
    Then the value is "p3"

  @unit
  Scenario: lastPromptText is null when no last-prompt record exists
    Given a jsonl with no "last-prompt" record
    When the fold index resolves Conversation.lastPromptText
    Then the value is null

  @integration
  Scenario: lastPromptText live-updates on jsonl append
    Given a watched jsonl whose latest "last-prompt" payload is "earlier"
    When a new "last-prompt" record with payload "newest" is appended
    Then within one watcher refresh tick the GraphQL query "{ conversation(uuid: $u) { lastPromptText } }" returns "newest"

  # ===========================================================================
  # AC 4 - Conversation.permissionMode: String folded from latest
  #        type:"permission-mode" record
  # ===========================================================================

  @unit
  Scenario: permissionMode folds from latest "permission-mode" record
    Given a jsonl with two "permission-mode" records whose payloads are "default" and "acceptEdits" in file order
    When the fold index resolves Conversation.permissionMode
    Then the value is "acceptEdits"

  @unit
  Scenario: permissionMode is null when no permission-mode record exists
    Given a jsonl with no "permission-mode" record
    When the fold index resolves Conversation.permissionMode
    Then the value is null

  # ===========================================================================
  # AC 5 - Conversation.events(byKind, since, limit): folded ordered event
  #        stream; v1 kinds = COMPACTION, SLASH_COMMAND; bounded by limit
  # ===========================================================================

  @unit
  Scenario: events(byKind: [COMPACTION]) folds attachment.type "compact_file_reference" in file order
    Given a jsonl with three attachment records whose attachment.type is "compact_file_reference"
    When the resolver returns Conversation.events(byKind: [COMPACTION])
    Then the response contains exactly three events
    And the events are ordered by file position ascending
    And each event has kind COMPACTION

  @unit
  Scenario: events(byKind: [SLASH_COMMAND]) folds <command-name> tags from text-shaped user content
    Given a jsonl with two user records whose message.content is a string containing "<command-name>recap</command-name>" and "<command-name>compact</command-name>"
    When the resolver returns Conversation.events(byKind: [SLASH_COMMAND])
    Then the response contains exactly two events
    And the first event payload.name is "recap"
    And the second event payload.name is "compact"

  @unit
  Scenario: events(limit:) clamps to the requested cap; default 50; max 500
    Given a jsonl with 600 attachment "compact_file_reference" records
    When the resolver returns Conversation.events(byKind: [COMPACTION])
    Then the response contains exactly 50 events
    And calling with limit 501 returns exactly 500 events
    And calling with limit 10 returns exactly 10 events

  @unit
  Scenario: events(since:) filters by record timestamp
    Given a jsonl with 5 compaction events spread across timestamps T1..T5
    When the resolver returns Conversation.events(byKind: [COMPACTION], since: T3)
    Then the response contains 3 events
    And every returned event has timestamp >= T3

  @unit
  Scenario: events skips slash-command text scans beyond the length cap
    Given a user record whose message.content text is 200KB long with "<command-name>foo</command-name>" appearing only after byte 1000
    When the fold index parses the record
    Then no SLASH_COMMAND event is emitted for that record
    And the length-cap guard is logged

  @integration
  Scenario: events(byKind: [COMPACTION, SLASH_COMMAND]) interleaves both kinds in file order
    Given a jsonl where compaction and slash_command events alternate in file order
    When the resolver returns Conversation.events(byKind: [COMPACTION, SLASH_COMMAND])
    Then the response preserves their file-order interleaving
    And each event carries the correct kind

  # ===========================================================================
  # AC 6 - ConversationEvent node shape: id, kind, at, payload (JSON!)
  # ===========================================================================

  @unit
  Scenario: ConversationEvent for COMPACTION carries filename and displayPath
    Given a jsonl attachment record with attachment.type "compact_file_reference", filename "/path/abs.txt", displayPath "rel/path.txt"
    When the resolver returns Conversation.events(byKind: [COMPACTION])
    Then the event.payload contains "filename" "/path/abs.txt"
    And the event.payload contains "displayPath" "rel/path.txt"

  @unit
  Scenario: ConversationEvent for SLASH_COMMAND carries name and args
    Given a user record whose message.content text starts with "<command-name>recap</command-name><command-args>--since=1h</command-args>"
    When the resolver returns Conversation.events(byKind: [SLASH_COMMAND])
    Then the event.payload contains "name" "recap"
    And the event.payload contains "args" "--since=1h"

  @unit
  Scenario: ConversationEvent.id is stable across reads
    Given a jsonl with two compaction events
    When the resolver returns Conversation.events twice for the same query
    Then each event has the same id between the two responses
    And the two ids are distinct from each other

  @unit
  Scenario: ConversationEvent.at is the source record's timestamp
    Given a compaction attachment record whose timestamp is "2026-05-20T12:34:56Z"
    When the resolver returns the event for it
    Then event.at is "2026-05-20T12:34:56Z"

  # ===========================================================================
  # AC 7 - Per-session in-memory fold index; cold hydrate walks once;
  #        warm reads are RAM-only; fsnotify drives incremental updates
  # ===========================================================================

  @integration
  Scenario: cold-hydrate walks the jsonl once per session at boot
    Given the daemon is starting with two cached session jsonls on disk
    When boot completes
    Then each jsonl is read exactly once during hydrate
    And the fold index for each session is populated for every supported field

  @integration
  Scenario: warm resolver call does no disk I/O
    Given a session whose fold index has been hydrated
    When the resolver answers "{ conversation(uuid: $u) { recap status lastPromptText permissionMode } }"
    Then no read syscall is issued against the underlying jsonl
    And the response is served from the in-memory index

  @integration
  Scenario: fsnotify write to the jsonl appends to the fold index incrementally
    Given a session whose fold index has been hydrated
    When new records are appended to the jsonl
    Then the fold index is updated incrementally from the appended bytes
    And the file is not rewalked from offset 0

  @integration
  Scenario: tail-budget exhausted falls back to head-scan for rare-write fields
    Given a 26MB jsonl whose only __update_recap record is at byte offset 1MB
    And the tail-window budget is 4MB
    When the fold index hydrates the session at boot
    Then Conversation.recap resolves to that record's input.text
    And the fallback head-scan path was taken
    And the fallback runs at most once per session

  # ===========================================================================
  # AC 8 - Provider methods use ADR-022 by-axis naming; no For<Caller>
  # ===========================================================================

  @unit
  Scenario: provider exposes ConversationByUUID(uuid)
    When the claudeprojects provider's public surface is inspected
    Then a method named "ConversationByUUID" exists
    And it takes a single uuid argument and returns one Conversation
    And no method named "ConversationFor<Caller>" exists

  @unit
  Scenario: provider exposes ConversationEventsByKind(uuid, kind, since, limit)
    When the claudeprojects provider's public surface is inspected
    Then a method named "ConversationEventsByKind" exists
    And its parameters follow the by-axis signature
    And no method named "ConversationEventsFor<Caller>" exists

  @unit
  Scenario: resolver bodies do not loop over provider snapshots
    When the daemon resolver source files for the new fields are inspected
    Then each resolver body is a thin "Load(key)" + projection
    And no resolver contains a for-loop over the claudeprojects provider snapshot

  # ===========================================================================
  # AC 9 - customTitle, agentName, cwd reads switch to the same fold index;
  #        parallel tail-scan helpers in jsonl.go are removed
  # ===========================================================================

  @integration
  Scenario: customTitle resolves from the shared fold index
    Given a session whose fold index has been hydrated
    When the resolver answers "{ conversation(uuid: $u) { customTitle } }"
    Then the value matches the index entry
    And no call is made to readLatestMarkers during the resolve

  @integration
  Scenario: agentName resolves from the shared fold index
    Given a session whose fold index has been hydrated
    When the resolver answers "{ conversation(uuid: $u) { agentName } }"
    Then the value matches the index entry
    And no call is made to readLatestMarkers during the resolve

  @integration
  Scenario: cwd resolves from the shared fold index
    Given a session whose fold index has been hydrated
    When the resolver answers "{ conversation(uuid: $u) { cwd } }"
    Then the value matches the index entry
    And no call is made to readLatestCwd during the resolve

  @unit
  Scenario: parallel tail-scan helpers are deleted from jsonl.go
    When the repository tree is inspected
    Then "internal/server/providers/claudeprojects/jsonl.go" does not export "readLatestMarkers"
    And it does not export "readLatestCwd"
    And no code under "internal/server/providers/" references those identifiers

  # ===========================================================================
  # AC 10 - Existing tests stay green; new unit tests cover every fold rule;
  #         one integration test asserts mid-session update_recap visibility
  # ===========================================================================

  @integration
  Scenario: existing claudeprojects tests pass
    When the daemon test suite runs
    Then every test under "internal/server/providers/claudeprojects/" passes

  @integration
  Scenario: new unit tests exist for each fold rule
    When the test files under "internal/server/providers/claudeprojects/" are inspected
    Then a fixture-driven unit test exists for "recap"
    And a fixture-driven unit test exists for "status"
    And a fixture-driven unit test exists for "lastPromptText"
    And a fixture-driven unit test exists for "permissionMode"
    And a fixture-driven unit test exists for "COMPACTION" events
    And a fixture-driven unit test exists for "SLASH_COMMAND" events

  @e2e
  Scenario: mid-session __update_recap becomes visible within one watcher tick
    Given the daemon is running and watching a session jsonl
    When an __update_recap tool_use is appended to that jsonl
    Then within one watcher refresh tick the GraphQL response for "{ conversation(uuid: $u) { recap } }" reflects the new value
    And no daemon restart is required

  # ===========================================================================
  # AC 11 - No sidecar deletions in this issue; conversations plugin keeps
  #         writing recap/status sidecars
  # ===========================================================================

  @integration
  Scenario: conversations plugin recap and status sidecar writes remain in place
    When the conversations-plugin source tree is inspected
    Then the writer for "<uuid>.recap.txt" still exists
    And the writer for "<uuid>.status.txt" still exists
    And the daemon-side change does not modify the plugin's writer code

  @integration
  Scenario: sidecar files on disk are not deleted by this change
    Given a session whose recap.txt and status.txt sidecars exist on disk before the daemon starts
    When the daemon starts up
    Then both sidecar files are still on disk after startup
    And no janitor under this issue touches the recap/status sidecars

  # ===========================================================================
  # AC 12 - No contract folding from session.jsonl; contracts provider keeps
  #         its own event-log path
  # ===========================================================================

  @integration
  Scenario: ConversationEventKind does not include CONTRACT
    When the GraphQL schema is introspected for "ConversationEventKind"
    Then its values are exactly "COMPACTION" and "SLASH_COMMAND"
    And no value named "CONTRACT" or "CONTRACT_EVENT" is present

  @integration
  Scenario: contracts continue to come from the contracts provider
    Given a session whose jsonl contains contract tool_use events
    And the contracts provider's own event-log records a separate set of contract events
    When the GraphQL query "{ contracts { id status } }" runs
    Then the response is sourced from the contracts provider
    And no claudeprojects-side fold contributes to the contracts response

  @unit
  Scenario: claudeprojects fold index does not parse contract tool_use names
    When the fold index source for claudeprojects is inspected
    Then it does not match on any tool name containing "contracts"
    And it does not emit ConversationEvent records of kind CONTRACT or similar

  # --- AC Coverage Map ---
  # AC 1 (Conversation.recap: String folded from latest __update_recap tool_use; null when absent; live)
  #   - @unit "recap folds from the latest __update_recap tool_use in file order"
  #   - @unit "recap is null when no __update_recap event has been written"
  #   - @unit "recap matches by suffix, not by exact plugin name"
  #   - @integration "recap reflects a mid-session update_recap within one watcher refresh tick"
  #
  # AC 2 (Conversation.status: enum RESOLVED|UNRESOLVED|null; live)
  #   - @unit "status folds from the latest __update_status tool_use"
  #   - @unit "status enum maps unresolved correctly"
  #   - @unit "status is null when no __update_status event has been written"
  #   - @unit "status null means never-set even if a later __update_status writes an empty input"
  #   - @integration "status updates live on jsonl append"
  #
  # AC 3 (Conversation.lastPromptText: latest "last-prompt" record; null when absent; live)
  #   - @unit "lastPromptText folds from latest \"last-prompt\" record"
  #   - @unit "lastPromptText is null when no last-prompt record exists"
  #   - @integration "lastPromptText live-updates on jsonl append"
  #
  # AC 4 (Conversation.permissionMode: latest "permission-mode" record; null when absent; live)
  #   - @unit "permissionMode folds from latest \"permission-mode\" record"
  #   - @unit "permissionMode is null when no permission-mode record exists"
  #
  # AC 5 (Conversation.events(byKind, since, limit) returns folded event stream;
  #        v1 kinds = COMPACTION, SLASH_COMMAND; ordered, bounded; live)
  #   - @unit "events(byKind: [COMPACTION]) folds attachment.type \"compact_file_reference\" in file order"
  #   - @unit "events(byKind: [SLASH_COMMAND]) folds <command-name> tags from text-shaped user content"
  #   - @unit "events(limit:) clamps to the requested cap; default 50; max 500"
  #   - @unit "events(since:) filters by record timestamp"
  #   - @unit "events skips slash-command text scans beyond the length cap"
  #   - @integration "events(byKind: [COMPACTION, SLASH_COMMAND]) interleaves both kinds in file order"
  #
  # AC 6 (ConversationEvent node: id, kind, at, payload kind-specific)
  #   - @unit "ConversationEvent for COMPACTION carries filename and displayPath"
  #   - @unit "ConversationEvent for SLASH_COMMAND carries name and args"
  #   - @unit "ConversationEvent.id is stable across reads"
  #   - @unit "ConversationEvent.at is the source record's timestamp"
  #
  # AC 7 (In-memory fold index per session; cold-hydrate once at boot; warm RAM-only;
  #        fsnotify incremental updates; head-scan fallback for rare-write fields)
  #   - @integration "cold-hydrate walks the jsonl once per session at boot"
  #   - @integration "warm resolver call does no disk I/O"
  #   - @integration "fsnotify write to the jsonl appends to the fold index incrementally"
  #   - @integration "tail-budget exhausted falls back to head-scan for rare-write fields"
  #
  # AC 8 (Provider methods follow ADR-022 by-axis naming; no For<Caller> methods)
  #   - @unit "provider exposes ConversationByUUID(uuid)"
  #   - @unit "provider exposes ConversationEventsByKind(uuid, kind, since, limit)"
  #   - @unit "resolver bodies do not loop over provider snapshots"
  #
  # AC 9 (customTitle, agentName, cwd switch to the shared fold index;
  #        parallel tail-scan helpers in jsonl.go are deleted)
  #   - @integration "customTitle resolves from the shared fold index"
  #   - @integration "agentName resolves from the shared fold index"
  #   - @integration "cwd resolves from the shared fold index"
  #   - @unit "parallel tail-scan helpers are deleted from jsonl.go"
  #
  # AC 10 (Existing tests stay green; new unit tests per fold rule;
  #        integration test asserts mid-session update_recap visibility)
  #   - @integration "existing claudeprojects tests pass"
  #   - @integration "new unit tests exist for each fold rule"
  #   - @e2e "mid-session __update_recap becomes visible within one watcher tick"
  #
  # AC 11 (No sidecar deletions in this issue; conversations plugin keeps writing them)
  #   - @integration "conversations plugin recap and status sidecar writes remain in place"
  #   - @integration "sidecar files on disk are not deleted by this change"
  #
  # AC 12 (No contract folding from session.jsonl; contracts provider keeps its own path)
  #   - @integration "ConversationEventKind does not include CONTRACT"
  #   - @integration "contracts continue to come from the contracts provider"
  #   - @unit "claudeprojects fold index does not parse contract tool_use names"
