Feature: Auto-conversation-contract on first user message (with v0.8 contracts substrate)
  As a Claude Code user
  I want every conversation gated by a contract that only the user can close
  So that sessions can't terminate on Claude's unreliable "I'm done" signal alone

  Background:
    Given a Claude Code marketplace at ".claude-plugin/marketplace.json" named "orchard"
    And the marketplace catalogs one plugin: "conversation-contracts" (MCP tools + UserPromptSubmit + Stop hook + close-conversation skill)
    And the v0.8 contracts model treats session jsonl as the durable store (no per-id contract files)
    And the daemon ContractFold projection scans "~/.claude/projects/*/*.jsonl" for "open_contract" and "close_contract" tool_use events
    And the conversation-contracts plugin registers a UserPromptSubmit hook and a Stop hook
    And the fixed conversation-contract deliverable is "user agrees conversation has come to a close and there are no loose ends"

  # ===================================================================
  # LAYER 1 — v0.8 contracts substrate (folded in from #644)
  # ===================================================================

  # ---- L1 / Marketplace scaffold ----

  @integration
  Scenario: Marketplace and plugin scaffold load cleanly
    Given the worktree contains ".claude-plugin/marketplace.json"
    And the worktree contains "plugins/conversation-contracts/.claude-plugin/plugin.json"
    And the worktree contains "plugins/conversation-contracts/hooks/hooks.json"
    When the user runs "/plugin marketplace add <worktree-path>"
    And the user runs "/plugin install conversation-contracts@orchard"
    Then the plugin is listed as installed
    And its UserPromptSubmit and Stop hooks are registered in the session
    And its open_contract and close_contract MCP tools are available

  # ---- L1 / MCP tools: open_contract and close_contract ----

  @e2e
  Scenario: open_contract MCP call writes a tool_use event to the calling session's jsonl
    Given the conversation-contracts plugin is installed
    And a Claude session with sessionUuid "S-MCP-OPEN-001" is active
    When the user invokes "open_contract" with deliverable "ship the widget"
    Then exactly one "open_contract" tool_use event is written to the session jsonl for "S-MCP-OPEN-001"
    And the event carries fields { id, deliverable, createdAt }
    And the event id matches "C-YYYY-MM-DD-XXXXXXXX" format

  @e2e
  Scenario: close_contract MCP call writes a tool_use event to the calling session's jsonl
    Given the conversation-contracts plugin is installed
    And the contract "C-2026-05-21-ABCD1234" is open with ownerSessionId "S-MCP-CLOSE-001"
    When the user invokes "close_contract" with id "C-2026-05-21-ABCD1234" and reason "delivered"
    Then exactly one "close_contract" tool_use event is written to the session jsonl for "S-MCP-CLOSE-001"
    And the event carries fields { id, closedAt, closedReason: "delivered" }
    And Query.contract(id:"C-2026-05-21-ABCD1234") returns status CLOSED with closedReason DELIVERED

  @integration
  Scenario: MCP surface no longer exposes update_contract
    Given the conversation-contracts plugin is installed
    When the user lists the contracts MCP tools
    Then only "open_contract" and "close_contract" are present
    And "update_contract" is not exposed
    And "accept_contract" is not exposed

  @integration
  Scenario: F2 non-owner abandon writes close event to the abandoner's jsonl with aboutSessionId
    Given contract "C-2026-05-21-EEEE5555" is open with ownerSessionId "S-OWNER"
    And a different session "S-OTHER" wishes to abandon it
    When "S-OTHER" invokes "close_contract" with id "C-2026-05-21-EEEE5555", reason "abandoned", and aboutSessionId "S-OWNER"
    Then the close_contract tool_use event is written to "S-OTHER"'s jsonl (not "S-OWNER"'s)
    And the event carries aboutSessionId equal to "S-OWNER"
    And the ContractFold resolves the close against the original open event by contract id
    And Query.contract(id:"C-2026-05-21-EEEE5555") returns status CLOSED with closedReason ABANDONED

  # ---- L1 / Daemon ContractFold projection ----

  @e2e
  Scenario: ContractFold derives Contract nodes by id-indexing across session jsonls
    Given session jsonls under "~/.claude/projects/" contain mixed open_contract and close_contract events
    When the daemon builds the ContractFold
    Then each unique contract id resolves to exactly one Contract node
    And close events are matched to open events by contract id regardless of source jsonl
    And Query.contracts(filter:{statuses:[OPEN]}) returns the set of contracts with no matching close event

  @integration
  Scenario: ContractFold filters by ownerSessionId
    Given session "S-FOLD-001" has two open contracts and one closed contract
    And session "S-FOLD-002" has one open contract
    When the client calls Query.contracts(filter:{ownerSessionId:"S-FOLD-001", statuses:[OPEN]})
    Then exactly two contracts are returned
    And none of the contracts owned by "S-FOLD-002" appear in the response

  @integration
  Scenario: ContractFold invalidates on jsonl change via fsnotify
    Given the ContractFold is built and cached
    When a new "open_contract" event is appended to a watched session jsonl
    Then the ContractFold re-derives the affected session's contracts within one fsnotify cycle
    And Query.contracts reflects the new contract without daemon restart

  # ---- L1 / GraphQL schema break ----

  @integration
  Scenario: ContractStatus collapses to two values
    When the client introspects the GraphQL schema at "http://127.0.0.1:7777/graphql"
    Then enum "ContractStatus" exposes exactly the values SIGNED and CLOSED
    And the removed values (e.g. COOLDOWN, WAITING, JUDGE_RUN, etc.) are absent

  @unit
  Scenario: ContractReason enum exposes exactly DELIVERED and ABANDONED
    When the client introspects the GraphQL schema
    Then enum "ContractReason" exposes exactly DELIVERED and ABANDONED
    And type "Contract" has field "closedReason" of type "ContractReason"

  @unit
  Scenario: Removed Contract fields are absent from the schema
    When the client introspects type "Contract"
    Then fields "cooldown", "waiting", and "judgeRun" are absent
    And ContractFilter does not expose filters for the removed fields

  # ---- L1 / One-shot migration of v0.7 contracts ----

  @integration
  Scenario: First daemon boot after upgrade migrates open v0.7 contracts into owning session jsonls
    Given "~/.claude/contracts/" contains v0.7 per-id JSONL files for two open contracts owned by session "S-MIGRATE-001"
    And no "~/.claude/contracts/.migrated-v0.8" sentinel exists
    When the daemon starts
    Then for each open v0.7 contract an "open_contract" tool_use event is appended to "S-MIGRATE-001"'s session jsonl
    And the sentinel file "~/.claude/contracts/.migrated-v0.8" is created
    And Query.contracts(filter:{ownerSessionId:"S-MIGRATE-001", statuses:[OPEN]}) returns the migrated contracts

  @integration
  Scenario: Migration is idempotent across daemon restarts
    Given the "~/.claude/contracts/.migrated-v0.8" sentinel exists
    When the daemon starts again
    Then no additional migration writes occur
    And no duplicate open_contract events appear in any session jsonl

  @integration
  Scenario: Closed v0.7 contracts move to archive untouched
    Given "~/.claude/contracts/" contains v0.7 per-id JSONL files for closed contracts
    When the daemon performs the one-shot migration
    Then closed v0.7 contract files are moved to "~/.claude/contracts/.archive/"
    And no migration events are appended to session jsonls for closed contracts

  @integration
  Scenario: Migration of v0.7 contract whose owning session jsonl is gone archives with orphan note
    Given a v0.7 open contract whose ownerSessionId no longer maps to any session jsonl on disk
    When the daemon performs the one-shot migration
    Then the contract file is moved to "~/.claude/contracts/.archive/"
    And an accompanying "migration-orphan.md" note records the missing session

  # ---- L1 / Consumer cleanup for the schema break ----

  @integration
  Scenario: CLI and skills are updated for the v0.8 schema break
    When the PR diff is inspected
    Then "internal/cli/query/contracts.go" no longer references removed enum values
    And the "/digest" skill query no longer references removed Contract fields
    And the "~/.claude/CLAUDE.md" update_contract workflow section (formerly L117-124) is removed
    And the "~/.claude/skills/accept-contract/" directory is deleted
    And "make" runs clean with no references to removed enum values or "update_contract"

  # ===================================================================
  # LAYER 2 — auto-conversation-contract (original #650 scope)
  # ===================================================================

  # ---- AC 1 — UserPromptSubmit hook emits open_contract on first user message ----

  @e2e
  Scenario: First user message in a new session opens the conversation contract
    Given a fresh Claude session with sessionUuid "S-FRESH-001"
    And no contracts exist for sessionUuid "S-FRESH-001"
    When the user submits their first prompt
    Then the UserPromptSubmit hook fires
    And an "open_contract" tool_use event is written into the session jsonl
    And the event has deliverable equal to the fixed conversation-contract deliverable
    And the event id matches "C-YYYY-MM-DD-XXXXXXXX" format
    And Query.contracts(filter:{ownerSessionId:"S-FRESH-001", statuses:[OPEN]}) returns exactly one contract with the fixed deliverable

  @integration
  Scenario: Subsequent user messages do not open additional conversation contracts (fold dedups)
    Given a Claude session with sessionUuid "S-DEDUP-002" already has one open conversation contract
    When the user submits a second, third, and fourth prompt
    Then the UserPromptSubmit hook fires on each prompt
    And the ContractFold dedupes per (ownerSessionId, deliverable) so subsequent opens are no-ops
    And Query.contracts(filter:{ownerSessionId:"S-DEDUP-002", statuses:[OPEN]}) still returns exactly one conversation contract

  @integration
  Scenario: Plugin does not write any state file to ${CLAUDE_PLUGIN_DATA}
    Given the conversation-contracts plugin is installed
    When the UserPromptSubmit hook fires multiple times across multiple sessions
    Then no firstmsg flag files are written under "${CLAUDE_PLUGIN_DATA}/sessions/"
    And idempotency is derived entirely from the ContractFold

  # ---- AC 2 — Stop hook surfaces the loose-ends inventory ----

  @e2e
  Scenario: Stop hook surfaces the loose-ends inventory when the conversation contract is open
    Given a Claude session "S-STOP-001" has an open conversation contract
    And the session has one open child contract "C-CHILD-001"
    And the session has one open TodoWrite item "fix flaky test"
    When Claude attempts to Stop
    Then the existing universal block-stop hook hard-blocks the Stop
    And the new Stop hook contributes a systemMessage enumerating the inventory:
      | inventory item            | source                                                                   |
      | open child contracts      | Query.contracts(filter:{ownerSessionId, statuses:[OPEN]}) minus self     |
      | open TodoWrite items      | system/stop_hook_summary records or harness TaskList                     |
    And the inventory message lists "C-CHILD-001" and "fix flaky test"

  @integration
  Scenario: Stop hook excludes the conversation contract itself from the open-child-contracts inventory
    Given a Claude session "S-STOP-002" has only its conversation contract open (no children)
    When Claude attempts to Stop
    Then the inventory section for "open child contracts" is empty
    And the inventory does not list the conversation contract as a child of itself

  # ---- AC 3 — Inventory composition (amended: no regex `?` heuristic) ----

  @unit
  Scenario: Inventory does not run a regex `?` heuristic over user messages
    Given a Claude session "S-INV-001" with multiple user messages containing "?"
    When the Stop hook composes the inventory
    Then the inventory does not include an "unanswered user questions" section
    And no regex over user message bodies runs

  @integration
  Scenario: Inventory includes only hard signals (child contracts and TodoWrite)
    Given a Claude session "S-INV-002" with:
      | signal                  | count |
      | open child contracts    | 2     |
      | open TodoWrite items    | 3     |
    When the Stop hook composes the inventory
    Then the inventory contains exactly two sections: open child contracts and open TodoWrite items
    And no other heuristic-derived sections appear

  @integration
  Scenario: Inventory degrades to open-child-contracts only when TodoWrite extraction is unavailable
    Given a Claude session "S-INV-003" where system/stop_hook_summary does not expose open TodoWrite items
    When the Stop hook composes the inventory
    Then the inventory still surfaces open child contracts
    And the TodoWrite section is omitted gracefully (no hook failure)

  # ---- AC 4 — User confirmation closes; naming open items keeps it open; /exit auto-closes ----

  @e2e
  Scenario: User confirms closure via /close-conversation skill (contract closes delivered)
    Given a Claude session "S-CLOSE-001" with an open conversation contract and no open child contracts
    When the user invokes the "/close-conversation" skill
    Then a "close_contract" tool_use event is written with reason "delivered"
    And the close-note contains the inventory summary at the moment of close
    And Query.contracts(filter:{ownerSessionId:"S-CLOSE-001", statuses:[OPEN]}) returns zero contracts

  @integration
  Scenario: User names what is still open (contract stays open; named items become child contracts)
    Given a Claude session "S-CLOSE-002" with an open conversation contract
    When the user names two unresolved items at Stop instead of closing
    Then the conversation contract remains open
    And two new "open_contract" tool_use events are written as child contracts for the named items
    And Query.contracts(filter:{ownerSessionId:"S-CLOSE-002", statuses:[OPEN]}) returns three contracts (conversation + two children)

  @e2e
  Scenario: /exit auto-closes the conversation contract as delivered
    Given a Claude session "S-EXIT-001" with an open conversation contract
    When the user types "/exit"
    And the session jsonl gains a system/local_command record whose content matches "<command-name>/exit</command-name>"
    Then the ContractFold synthesizes a virtual close_contract(reason:"delivered", note:"exit:exit") for the conversation contract
    And no plugin write path is invoked for the close

  @integration
  Scenario Outline: /quit and /bye also auto-close as delivered
    Given a Claude session "<sessionUuid>" with an open conversation contract
    When the user types "<verb>"
    Then the fold synthesizes a virtual close_contract with reason "delivered" and note "exit:<bare_verb>"

    Examples:
      | sessionUuid | verb  | bare_verb |
      | S-EXIT-002  | /quit | quit      |
      | S-EXIT-003  | /bye  | bye       |

  @integration
  Scenario: Resume after /exit reopens a new conversation contract
    Given a Claude session "S-RESUME-001" whose jsonl contains an "/exit" marker followed by additional user messages (claude -r resume)
    When the fold replays the jsonl
    Then the fold synthesizes a virtual close_contract(reason:"delivered") at the exit marker
    And the fold accepts a fresh open_contract for the conversation deliverable on the next user message after the marker
    And Query.contracts(filter:{ownerSessionId:"S-RESUME-001"}) returns two conversation contracts: one CLOSED/DELIVERED, one OPEN

  # ---- AC 5 — CLAUDE.md updated in the same PR ----

  @e2e
  Scenario: CLAUDE.md teaches the conversation-contract close flow
    When the PR diff is inspected
    Then "~/.claude/CLAUDE.md" gains a "Conversation contracts" section under "Interrupt Discipline — Say → Do → Report"
    And the section documents:
      | topic                                                                  |
      | auto-open on first user message                                        |
      | loose-ends inventory surfaced at Stop                                  |
      | four close paths: /exit, /quit, /bye, /close-conversation              |
      | exit-equals-delivered semantics                                        |
      | resource consequence (workers/cron sessions can't cleanly Stop alone)  |
    And the added section is at most 25 lines

  # ---- AC 6 — Explicit close_contract bypasses the loose-ends inventory ----

  @integration
  Scenario: Direct close_contract MCP call bypasses the loose-ends inventory
    Given a Claude session "S-ESCAPE-001" with an open conversation contract
    And the session has one open child contract and one open TodoWrite item
    When the user explicitly invokes the close_contract MCP tool against the conversation contract id
    Then the contract closes immediately with closedReason DELIVERED
    And no loose-ends inventory prompt is surfaced
    And the existing universal block-stop hook no longer blocks Stop on that contract

  # ===================================================================
  # Plugin composition (enabling infrastructure)
  # ===================================================================

  # --- AC Coverage Map ---
  #
  # Layer 1 (v0.8 contracts substrate, folded in from #644 per 2026-05-21 amendment):
  #   Marketplace scaffold:
  #     → Scenario: Marketplace and plugin scaffold load cleanly
  #   MCP tools (open_contract / close_contract; update_contract retired):
  #     → Scenario: open_contract MCP call writes a tool_use event to the calling session's jsonl
  #     → Scenario: close_contract MCP call writes a tool_use event to the calling session's jsonl
  #     → Scenario: MCP surface no longer exposes update_contract
  #     → Scenario: F2 non-owner abandon writes close event to the abandoner's jsonl with aboutSessionId
  #   Daemon ContractFold projection:
  #     → Scenario: ContractFold derives Contract nodes by id-indexing across session jsonls
  #     → Scenario: ContractFold filters by ownerSessionId
  #     → Scenario: ContractFold invalidates on jsonl change via fsnotify
  #   GraphQL schema break (ContractStatus 9→2, ContractReason added):
  #     → Scenario: ContractStatus collapses to two values
  #     → Scenario: ContractReason enum exposes exactly DELIVERED and ABANDONED
  #     → Scenario: Removed Contract fields are absent from the schema
  #   One-shot migration of v0.7 contracts:
  #     → Scenario: First daemon boot after upgrade migrates open v0.7 contracts into owning session jsonls
  #     → Scenario: Migration is idempotent across daemon restarts
  #     → Scenario: Closed v0.7 contracts move to archive untouched
  #     → Scenario: Migration of v0.7 contract whose owning session jsonl is gone archives with orphan note
  #   Consumer cleanup:
  #     → Scenario: CLI and skills are updated for the v0.8 schema break
  #
  # Layer 2 (auto-conversation-contract, original #650 ACs 1–6):
  #
  # AC 1: "Plugin emits an open_contract event into the session jsonl on receipt of the first user message of a session."
  #   → Scenario: First user message in a new session opens the conversation contract
  #   → Scenario: Subsequent user messages do not open additional conversation contracts (fold dedups)
  #   → Scenario: Plugin does not write any state file to ${CLAUDE_PLUGIN_DATA}
  #
  # AC 2: "Stop hook detects the open conversation contract and surfaces a loose-ends inventory prompt to the user."
  #   → Scenario: Stop hook surfaces the loose-ends inventory when the conversation contract is open
  #   → Scenario: Stop hook excludes the conversation contract itself from the open-child-contracts inventory
  #
  # AC 3: "Inventory composes (open child contracts, open TodoWrite items). At least the first is real; the latter ships as best-effort heuristic. (Amended: no regex `?` heuristic.)"
  #   → Scenario: Inventory does not run a regex `?` heuristic over user messages
  #   → Scenario: Inventory includes only hard signals (child contracts and TodoWrite)
  #   → Scenario: Inventory degrades to open-child-contracts only when TodoWrite extraction is unavailable
  #
  # AC 4: "User confirmation closes the contract; naming open items keeps it open with the items recorded. (Amended: /exit auto-closes as delivered.)"
  #   → Scenario: User confirms closure via /close-conversation skill (contract closes delivered)
  #   → Scenario: User names what is still open (contract stays open; named items become child contracts)
  #   → Scenario: /exit auto-closes the conversation contract as delivered
  #   → Scenario Outline: /quit and /bye also auto-close as delivered
  #   → Scenario: Resume after /exit reopens a new conversation contract
  #
  # AC 5: "CLAUDE.md updated to teach the user about the conversation contract and the close flow."
  #   → Scenario: CLAUDE.md teaches the conversation-contract close flow
  #
  # AC 6: "A user-explicit close_contract call bypasses the loose-ends inventory (escape hatch)."
  #   → Scenario: Direct close_contract MCP call bypasses the loose-ends inventory
  #
  # AC 7 (RETIRED 2026-05-21 amendment): "File a follow-up issue for the daemon-side conversation-contract provider."
  #   — Retired because the daemon ContractFold projection is now part of this PR's scope (Layer 1).
  #   — No follow-up issue to file.
