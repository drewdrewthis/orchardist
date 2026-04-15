Feature: TOON output flag for token-efficient agent consumption
  As an AI agent consuming orchard output
  I want a --toon flag that emits TOON-formatted data
  So that I pay fewer tokens than the equivalent --json output

  Background:
    Given `JsonOutput` in `src/json_output.rs` is the single source of truth for machine-readable output
    And `--toon` is a thin transform: serialize `JsonOutput` → `serde_json::Value` → TOON via `json2toon_rs`
    And versioning stays aligned with `--json` (same underlying struct)

  # ===================================================================
  # CLI flag — clap wiring
  # ===================================================================

  @unit
  Scenario: --toon flag is defined on the CLI
    When `orchard --help` is printed
    Then the help text lists a `--toon` flag
    And the help text for `--toon` mentions AI agent consumption

  @unit
  Scenario: --toon and --json are mutually exclusive
    # Note: the issue AC says "clap enforces", but this crate parses args manually.
    # Enforcement stays in the same manual parser; the user-visible behaviour is identical.
    When `orchard --json --toon` is invoked
    Then the CLI rejects the invocation with a conflict error printed to stderr
    And exit status is non-zero

  # ===================================================================
  # TOON output correctness
  # ===================================================================

  @unit
  Scenario: --toon emits valid TOON v2.0 derived from the same data as --json
    Given a fixture `OrchardState` identical to a `--json` test fixture
    When the state is rendered with the `--toon` writer
    Then the output parses as valid TOON v2.0
    And the underlying data matches the `JsonOutput` produced from the same state

  @unit
  Scenario: TOON snapshot test for fixture OrchardState
    Given a fixture `OrchardState` with at least one repo, worktree, session, issue, and PR
    When rendered via the TOON writer
    Then the output matches the committed snapshot

  # ===================================================================
  # Integration — orchard --toon binary output
  # ===================================================================

  @integration
  Scenario: orchard --toon emits TOON to stdout
    Given a fixture cache with repos, worktrees, and sessions
    When `orchard --toon` is run against the fixture
    Then stdout is valid TOON v2.0
    And stderr is empty on the happy path

  # ===================================================================
  # Documentation
  # ===================================================================

  @docs
  Scenario: README documents the --toon flag
    Given the project README
    When the CLI section is read
    Then it includes a snippet showing `orchard --toon` usage
    And it notes the flag is intended for AI agent consumption

  # ===================================================================
  # Token savings measurement (PR description evidence)
  # ===================================================================

  @evidence
  Scenario: Token count measured and recorded in PR description
    Given a representative `orchard --json` dump
    When the same data is emitted as `--toon`
    Then token counts for both (via tiktoken or equivalent) are recorded in the PR description
    And the savings percentage is reported
