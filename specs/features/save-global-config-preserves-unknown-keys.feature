Feature: save_global_config preserves unknown top-level keys (round-trip merge)
  As an orchard user with a multi-writer global config
  I want the Rust TUI's save path to merge into existing JSON, not rewrite it
  So that fields written by other tools (like the daemon's `peers[]`) are not silently dropped on save

  Background:
    Given the canonical global config path is "~/.orchard/config.json"
    And the Rust TUI is one of several writers to this file (daemon `config add-peer`, future tools)
    And `GlobalConfig` (in `crates/orchard/src/global_config.rs`) models a strict subset of the file's top-level keys

  # ===================================================================
  # AC 1 — `save_global_config` preserves all unknown top-level keys round-trip
  # ===================================================================

  @unit
  Scenario: save_to_path preserves an unknown top-level key when the file already contains it
    Given a config file at a temp path containing the JSON object:
      """
      {
        "repos": [],
        "terminal_app": "com.apple.Terminal",
        "peers": [{"name": "boxd-vm", "address": "user@vm.example", "tls": true}]
      }
      """
    When `save_to_path` is called with a `GlobalConfig` whose `terminal_app` is "com.googlecode.iterm2"
    Then the file on disk contains `terminal_app` equal to "com.googlecode.iterm2"
    And the file on disk still contains the `peers` array with the original entry intact

  @unit
  Scenario: save_to_path preserves multiple unrelated unknown top-level keys in one save
    Given a config file containing unknown top-level keys "peers", "watch_observability", and "federation_tls_pin"
    When `save_to_path` is called with any in-struct field changed
    Then all three unknown top-level keys are present on disk after the save with their original values

  @unit
  Scenario: save_to_path preserves nested values inside an unknown top-level key verbatim
    Given the file contains an unknown key "peers" whose value is an array of objects with nested fields ("name", "address", "tls", "metadata.region")
    When `save_to_path` rewrites the file via the merge path
    Then every nested field under "peers" is byte-for-byte preserved (no key reordering inside objects required, only that values are not lost)

  @unit
  Scenario: save_to_path overwrites known keys present in the struct
    Given the existing file contains `terminal_app` = "old.app" AND an unknown key "peers"
    When `save_to_path` is called with `GlobalConfig.terminal_app` = "new.app"
    Then the file's `terminal_app` is "new.app" (struct wins for known keys)
    And the unknown key "peers" still exists

  @unit
  Scenario: save_to_path writes the struct as-is when the existing file is absent
    Given no config file exists at the target path
    When `save_to_path` is called with a populated `GlobalConfig`
    Then the file is created with exactly the struct's JSON representation
    And no spurious unknown keys appear (nothing to merge from)

  @unit
  Scenario: save_to_path falls back to a plain struct write when the existing file is unreadable
    Given a file exists at the target path but is malformed JSON ("not valid {")
    When `save_to_path` is called with a populated `GlobalConfig`
    Then the file is overwritten with the struct's JSON representation (clean recovery)
    And the save returns success (callers do not see a malformed-input error)
    And a warning is logged that the existing file could not be parsed for merge

  @unit
  Scenario: save_to_path preserves unknown keys whose values are null, false, or empty
    Given the existing file contains unknown top-level keys with edge-case values:
      | key                | value         |
      | peers              | []            |
      | watch_observability| null          |
      | feature_flag_x     | false         |
      | empty_object_field | {}            |
    When `save_to_path` is called
    Then every one of those keys is present after the save with the same JSON value
    And no key is dropped because its value is "empty"

  @unit
  Scenario: Merge is shallow — top-level only, not deep recursive
    Given the existing file contains a known key `watch` with an unknown nested key `watch.experimental_flag`
    And the in-memory `GlobalConfig.watch` does not model `experimental_flag`
    When `save_to_path` is called
    Then the resulting file's `watch` block matches the struct's serialization (the nested unknown is dropped — by design, this scope is shallow merge only)
    And a follow-up issue may track a deep-merge variant if needed (out of scope here)

  @unit
  Scenario: save_to_path remains atomic via the tmp-and-rename path
    Given the existing file contains an unknown key "peers"
    When `save_to_path` is invoked
    Then the merged JSON is first written to a `<path>.tmp` file
    And then renamed atomically to the final path (no partial writes observable to other readers)

  # ===================================================================
  # AC 2 — Test asserts `peers` field survives a TUI save
  # (Reproduction of the original federation incident)
  # ===================================================================

  @integration
  Scenario: `peers` field survives a TUI-driven save of `terminal_app`
    Given the user has run `orchard config add-peer --name X --address Y --tls` (daemon-side)
    And `~/.orchard/config.json` contains a populated `peers[]` array with that entry
    When the TUI's save path runs (e.g. `init` wizard finalises, or `register_cwd_repo_if_new` persists, or `terminal_app` save fires)
    And the only field the TUI mutates in-struct is `terminal_app`
    Then re-reading `~/.orchard/config.json` shows `peers[]` still contains the original entry untouched
    And `terminal_app` reflects the TUI's new value

  @integration
  Scenario: `peers` field survives back-to-back TUI saves
    Given the file starts with one peer "boxd-vm" present
    When the TUI executes two consecutive saves (e.g. terminal_app then chat_target)
    Then after each save the `peers[]` array is intact and unchanged
    And no save iteration drops or re-orders the peer entry

  @integration
  Scenario: `peers` field survives a save that also auto-registers a new CWD repo
    Given the file contains peers "boxd-vm" AND no repo entry for the current working directory
    When `register_cwd_repo_if_new` detects the CWD repo and triggers `save_global_config`
    Then the saved file contains BOTH:
      | content                                         |
      | the new repo entry appended to `repos[]`        |
      | the original `peers[]` array unmodified         |

  # ===================================================================
  # AC 3 — No regression in existing tests
  # ===================================================================

  @integration
  Scenario: All existing global_config tests continue to pass
    Given the test suite under `crates/orchard/src/global_config.rs::tests` (and `crates/orchard/tests/`)
    When `cargo test -p orchard global_config` is run on the merge-aware implementation
    Then every existing test passes without modification (or with modifications that are purely additive — e.g. asserting the new behavior, never weakening an old assertion)

  @integration
  Scenario: `save_to_path` round-trip of a struct-only payload remains identical to today
    Given a `GlobalConfig` with no unknown keys on disk (clean install, struct-only)
    When `save_to_path` writes and `load_from_path` re-reads
    Then every modeled field round-trips identically (`repos`, `terminal_app`, `tmux_sessions`, `chat_target`, `watch`, `ci_gate_patterns`)
    And no extra keys appear in the file that were not in the struct

  @integration
  Scenario: `save_global_config` (real path) and `save_to_path` (test path) share the same merge behavior
    Given identical preconditions on a controlled temp path for both functions
    When `save_global_config` is invoked indirectly (via a HOME redirection or equivalent test plumbing) and `save_to_path` is invoked directly
    Then both produce byte-equivalent merged output (the `save_global_config` wrapper must not bypass the merge)

  # ===================================================================
  # Out-of-scope guards (issue "Out of scope")
  # ===================================================================

  @unit
  Scenario: No deep recursive merge is introduced
    Given the merge implementation
    When code is inspected
    Then it does not recursively walk nested objects to merge
    And it operates on the top-level `Map<String, Value>` only

  @unit
  Scenario: No schema-versioned migration is introduced
    Given the merge implementation
    When code is inspected
    Then no `schema_version` field is added to `GlobalConfig`
    And no migration step is run as part of `save_to_path`

  @unit
  Scenario: Config location is not changed by this work
    Given the canonical path is already `~/.orchard/config.json` (per ADR-014, issue #424)
    When this issue's changes ship
    Then the read/write path is unchanged
    And no movement of the config file is performed

  # --- AC Coverage Map ---
  # AC 1: "save_global_config preserves all unknown top-level keys round-trip"
  #   -> Scenario: save_to_path preserves an unknown top-level key when the file already contains it
  #   -> Scenario: save_to_path preserves multiple unrelated unknown top-level keys in one save
  #   -> Scenario: save_to_path preserves nested values inside an unknown top-level key verbatim
  #   -> Scenario: save_to_path overwrites known keys present in the struct
  #   -> Scenario: save_to_path writes the struct as-is when the existing file is absent
  #   -> Scenario: save_to_path falls back to a plain struct write when the existing file is unreadable
  #   -> Scenario: save_to_path preserves unknown keys whose values are null, false, or empty
  #   -> Scenario: Merge is shallow — top-level only, not deep recursive
  #   -> Scenario: save_to_path remains atomic via the tmp-and-rename path
  #
  # AC 2: "Test asserts `peers` field survives a TUI save"
  #   -> Scenario: `peers` field survives a TUI-driven save of `terminal_app`
  #   -> Scenario: `peers` field survives back-to-back TUI saves
  #   -> Scenario: `peers` field survives a save that also auto-registers a new CWD repo
  #
  # AC 3: "No regression in existing tests"
  #   -> Scenario: All existing global_config tests continue to pass
  #   -> Scenario: save_to_path round-trip of a struct-only payload remains identical to today
  #   -> Scenario: save_global_config (real path) and save_to_path (test path) share the same merge behavior
  #
  # Out-of-scope guards (issue "Out of scope" + scope guards):
  #   -> Scenario: No deep recursive merge is introduced
  #   -> Scenario: No schema-versioned migration is introduced
  #   -> Scenario: Config location is not changed by this work
  #
  # AC count: 3. Mapped scenarios: 3/3. No drops, no gaps.
