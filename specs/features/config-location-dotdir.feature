Feature: Global config location moves to ~/.orchard/ (dotdir convention)
  As an orchard user
  I want orchard's global config to live at ~/.orchard/config.json
  So that orchard follows the same dotdir convention as ~/.aws, ~/.kube, ~/.ssh, ~/.cargo, ~/.claude

  Background:
    Given the canonical global config path is "~/.orchard/config.json"
    And the legacy path "~/.config/orchard/config.json" is no longer read or written

  # ===================================================================
  # Path resolution — Rust CLI/TUI (global_config.rs)
  # ===================================================================

  @unit
  Scenario: global_config_path resolves to ~/.orchard/config.json
    Given the user's home directory is "/home/alice"
    When global_config_path is called
    Then it returns "/home/alice/.orchard/config.json"

  @unit
  Scenario: global_config_write_path resolves to ~/.orchard/config.json
    Given the user's home directory is "/home/alice"
    When global_config_write_path is called
    Then it returns "/home/alice/.orchard/config.json"

  @unit
  Scenario: global_config_path ignores XDG_CONFIG_HOME
    Given the user's home directory is "/home/alice"
    And the XDG_CONFIG_HOME environment variable is set to "/custom/xdg"
    When global_config_path is called
    Then it returns "/home/alice/.orchard/config.json"
    And the returned path does not contain "/custom/xdg"
    And the returned path does not contain ".config/orchard"

  @unit
  Scenario: global_config_path ignores macOS Application Support fallback
    Given the user's home directory is "/Users/alice"
    And the platform is macOS
    When global_config_path is called
    Then it returns "/Users/alice/.orchard/config.json"
    And the returned path does not contain "Library/Application Support"

  # ===================================================================
  # Path resolution — Go daemon (orchpaths.go)
  # ===================================================================

  @unit
  Scenario: orchpaths.ConfigDir returns ~/.orchard
    Given the user's home directory is "/home/alice"
    When orchpaths.ConfigDir is called
    Then it returns "/home/alice/.orchard"

  @unit
  Scenario: orchpaths.ConfigFile returns ~/.orchard/config.json
    Given the user's home directory is "/home/alice"
    When orchpaths.ConfigFile is called
    Then it returns "/home/alice/.orchard/config.json"

  @unit
  Scenario: orchpaths.ConfigDir ignores XDG_CONFIG_HOME
    Given the user's home directory is "/home/alice"
    And the XDG_CONFIG_HOME environment variable is set to "/custom/xdg"
    When orchpaths.ConfigDir is called
    Then it returns "/home/alice/.orchard"

  @unit
  Scenario: State directory remains at ~/.local/state/orchard
    Given config has moved to ~/.orchard
    When orchpaths.StateDir is called
    Then it still returns "~/.local/state/orchard"
    And the state directory move is explicitly out of scope of this change

  # ===================================================================
  # AC 1 — `orchard config add-peer` writes to new path
  # ===================================================================

  @integration
  Scenario: orchard config add-peer writes to ~/.orchard/config.json
    Given a fresh user with no existing orchard config
    When the user runs "orchard config add-peer --slug acme/webapp --host ubuntu@10.0.0.1"
    Then the file "~/.orchard/config.json" exists
    And the new peer entry is present in "~/.orchard/config.json"
    And no file is created at "~/.config/orchard/config.json"

  @integration
  Scenario: orchard config add-peer creates ~/.orchard parent dir if absent
    Given the directory "~/.orchard" does not exist
    When the user runs "orchard config add-peer ..."
    Then the directory "~/.orchard" is created with mode 0700 (or platform default)
    And the file "~/.orchard/config.json" is written

  @integration
  Scenario: orchard config add-peer appends to an existing ~/.orchard/config.json
    Given "~/.orchard/config.json" already contains one peer "acme/foo"
    When the user runs "orchard config add-peer --slug acme/bar --host ubuntu@10.0.0.1"
    Then "~/.orchard/config.json" contains both peers "acme/foo" and "acme/bar"
    And no peer is written to "~/.config/orchard/config.json"

  # ===================================================================
  # AC 2 — Daemon reads from ~/.orchard/config.json only
  # ===================================================================

  @integration
  Scenario: Daemon loads config from ~/.orchard/config.json on startup
    Given "~/.orchard/config.json" contains a valid config with one repo "acme/webapp"
    And no file exists at "~/.config/orchard/config.json"
    When the daemon starts
    Then the daemon loads the repo "acme/webapp" from "~/.orchard/config.json"
    And the daemon does not stat or open "~/.config/orchard/config.json"

  @integration
  Scenario: Daemon ignores config at the legacy path when both exist
    Given "~/.orchard/config.json" contains repo "acme/new"
    And "~/.config/orchard/config.json" contains repo "acme/legacy"
    When the daemon starts
    Then the daemon serves repo "acme/new"
    And the daemon does not surface repo "acme/legacy"

  @integration
  Scenario: Rust CLI loads config from ~/.orchard/config.json
    Given "~/.orchard/config.json" contains a valid config
    When any orchard CLI command that reads global config runs
    Then the config is loaded from "~/.orchard/config.json"
    And no read attempt is made against "~/.config/orchard/config.json"

  # ===================================================================
  # AC 3 — Missing new path errors with migration hint
  # ===================================================================

  @integration
  Scenario: Daemon errors with migration hint when only legacy config exists
    Given no file exists at "~/.orchard/config.json"
    And a file exists at "~/.config/orchard/config.json"
    When the daemon starts
    Then the daemon exits with a config-not-found error
    And the error message points the user at the new path "~/.orchard/config.json"
    And the error message includes the migration command "mv ~/.config/orchard ~/.orchard"

  @integration
  Scenario: Daemon errors with config-not-found when neither path exists
    Given no file exists at "~/.orchard/config.json"
    And no file exists at "~/.config/orchard/config.json"
    When the daemon starts
    Then the daemon exits with a config-not-found error
    And the error message points the user at "~/.orchard/config.json"
    And the error message does not include the migration command (no legacy config to migrate)

  @integration
  Scenario: Rust CLI errors with migration hint when only legacy config exists
    Given no file exists at "~/.orchard/config.json"
    And a file exists at "~/.config/orchard/config.json"
    When the user runs an orchard CLI command that requires global config
    Then the CLI exits with a config-not-found error
    And the error message points the user at "~/.orchard/config.json"
    And the error message includes "mv ~/.config/orchard ~/.orchard"

  @unit
  Scenario: Migration hint is emitted only at the config-load failure site
    Given the daemon resolves the config path many times during startup
    When the new config is missing and legacy is present
    Then the legacy-path stat is performed only once at the failure site
    And every other call to ConfigFile()/global_config_path() returns the new path without statting the legacy path

  @unit
  Scenario: Legacy path is never loaded as a fallback
    Given "~/.config/orchard/config.json" contains a valid config
    And no file exists at "~/.orchard/config.json"
    When config loading runs
    Then no peer, repo, or terminal_app value from the legacy file is loaded into runtime state

  # ===================================================================
  # AC 4 — Release notes call out the migration prominently
  # ===================================================================

  @integration
  Scenario: Release notes contain a BREAKING migration line for the config move
    When the release notes / CHANGELOG entry for this change is inspected
    Then it contains a "BREAKING" marker for the config-path move
    And it states the new path "~/.orchard/config.json"
    And it states the migration command "mv ~/.config/orchard ~/.orchard"
    And the migration line is prominent (top-of-section, not buried in a list)

  # ===================================================================
  # AC 5 — Specs and docs updated
  # ===================================================================

  @integration
  Scenario: All feature files reference the new config path
    Then no .feature file under specs/features/ references "~/.config/orchard/" as the canonical config path
    And every .feature file that previously referenced the canonical config path now references "~/.orchard/config.json"

  @integration
  Scenario: Architecture docs reference the new config path
    Then docs/architecture.md references "~/.orchard/config.json" (not "~/.config/orchard/config.json")
    And docs/webhook-setup.md references "~/.orchard/config.json"
    And the README references "~/.orchard/config.json"

  @integration
  Scenario: Inline doc comments and code mention the new path
    Then no Rust // or /// doc comment mentions "~/.config/orchard/config.json" as the canonical config location
    And no Go // doc comment mentions "~/.config/orchard/config.json" as the canonical config location
    And the only remaining mentions of "~/.config/orchard" are historical (release notes, deprecated ADR notes, migration hint string)

  @integration
  Scenario: Test fixtures use the new path
    Then no test under crates/orchard/tests/ writes a fixture to "<home>/.config/orchard/config.json"
    And every fixture that previously wrote to the legacy path now writes to "<home>/.orchard/config.json"
    And no Go integration test creates a config at "<home>/.config/orchard/config.json"

  # ===================================================================
  # AC 6 — ADR records the move
  # ===================================================================

  @integration
  Scenario: A new ADR records the config-location decision
    Then a new ADR file exists under docs/adr/ with the next free number
    And the ADR title states the move to "~/.orchard/"
    And the ADR records the rationale (every other dotdir tool ignores XDG; consistency with ~/.aws, ~/.kube, ~/.ssh, ~/.cargo, ~/.claude)
    And the ADR notes that ADR-001 and ADR-003 are amended-by-reference where they mention the legacy path

  # ===================================================================
  # Out-of-scope guards (from issue "Out of scope" section)
  # ===================================================================

  @unit
  Scenario: Orchardist working directory is not moved
    Given the orchardist daemon's working directory is "~/.config/orchard/.orchardist/"
    When this change ships
    Then the orchardist working directory remains at "~/.config/orchard/.orchardist/"
    And it is not relocated as part of this change

  @unit
  Scenario: State directory is not moved
    Given the state directory is "~/.local/state/orchard"
    When this change ships
    Then the state directory remains at "~/.local/state/orchard"

  @unit
  Scenario: Per-repo config files are unaffected
    Given a repo at "/workspace/webapp" has ".orchard.json" and ".git/orchard.json"
    When this change ships
    Then ".orchard.json" and ".git/orchard.json" remain in the repo and are read as before

  # --- AC Coverage Map ---
  # AC 1: "orchard config add-peer ... writes to ~/.orchard/config.json"
  #   -> Scenario: orchard config add-peer writes to ~/.orchard/config.json
  #   -> Scenario: orchard config add-peer creates ~/.orchard parent dir if absent
  #   -> Scenario: orchard config add-peer appends to an existing ~/.orchard/config.json
  #
  # AC 2: "Daemon reads from ~/.orchard/config.json only"
  #   -> Scenario: Daemon loads config from ~/.orchard/config.json on startup
  #   -> Scenario: Daemon ignores config at the legacy path when both exist
  #   -> Scenario: Rust CLI loads config from ~/.orchard/config.json
  #   -> Scenario: global_config_path resolves to ~/.orchard/config.json
  #   -> Scenario: orchpaths.ConfigFile returns ~/.orchard/config.json
  #   -> Scenario: global_config_path ignores XDG_CONFIG_HOME
  #   -> Scenario: orchpaths.ConfigDir ignores XDG_CONFIG_HOME
  #   -> Scenario: global_config_path ignores macOS Application Support fallback
  #
  # AC 3: "Old path is ignored — config-not-found error pointing at new path"
  #   -> Scenario: Daemon errors with migration hint when only legacy config exists
  #   -> Scenario: Daemon errors with config-not-found when neither path exists
  #   -> Scenario: Rust CLI errors with migration hint when only legacy config exists
  #   -> Scenario: Migration hint is emitted only at the config-load failure site
  #   -> Scenario: Legacy path is never loaded as a fallback
  #
  # AC 4: "Release notes call out the migration step prominently"
  #   -> Scenario: Release notes contain a BREAKING migration line for the config move
  #
  # AC 5: "Specs and docs updated"
  #   -> Scenario: All feature files reference the new config path
  #   -> Scenario: Architecture docs reference the new config path
  #   -> Scenario: Inline doc comments and code mention the new path
  #   -> Scenario: Test fixtures use the new path
  #
  # AC 6: "ADR amended (or new ADR filed) recording the move"
  #   -> Scenario: A new ADR records the config-location decision
  #
  # Out-of-scope guards (issue "Out of scope" + plan):
  #   -> Scenario: Orchardist working directory is not moved
  #   -> Scenario: State directory is not moved
  #   -> Scenario: Per-repo config files are unaffected
  #   -> Scenario: State directory remains at ~/.local/state/orchard
  #
  # AC count: 6. Mapped scenarios: 6/6. No drops, no gaps.
