Feature: Refactor heal::diagnose to take a HealInput struct
  As a developer maintaining the heal subsystem
  I want diagnose() to take a single &HealInput<'_> instead of 6 positional refs
  So that adding new inputs is safe and call sites are self-documenting

  Background:
    Given heal.rs is in crates/orchard/src/heal.rs
    And diagnose currently takes 6 positional reference arguments
    And cache_files and known_repo_slugs both have type &[String] (positionally swappable)

  # ===================================================================
  # HealInput struct — definition
  # ===================================================================

  @unit
  Scenario: HealInput struct exists with named fields for all current diagnose inputs
    Given the HealInput<'a> struct in crates/orchard/src/heal.rs
    Then it has these public fields:
      | field            | type                    |
      | sessions         | &'a [TmuxSession]       |
      | worktrees        | &'a [HealWorktree]      |
      | claude_states    | &'a [HealClaudeState]   |
      | cache_files      | &'a [String]            |
      | known_repo_slugs | &'a [String]            |
      | current_session  | Option<&'a str>         |

  @unit
  Scenario: HealInput derives Debug, Clone, Copy, Default
    Given the HealInput<'a> struct
    Then it derives Debug, Clone, Copy, and Default

  @unit
  Scenario: HealInput::default() yields empty slices and None
    When HealInput::default() is called
    Then sessions is an empty slice
    And worktrees is an empty slice
    And claude_states is an empty slice
    And cache_files is an empty slice
    And known_repo_slugs is an empty slice
    And current_session is None

  @unit
  Scenario: HealInput is public from the heal module
    Given crates/orchard/src/heal.rs
    Then HealInput is declared with `pub struct HealInput<'a>`
    And it is reachable as orchard::heal::HealInput from outside the module

  @unit
  Scenario: HealInput documents the borrowed-only invariant
    Given the doc comment on HealInput
    Then it states that all fields are borrowed
    And it warns that adding an owned field (e.g. Vec<T>) requires dropping the Default derive
    And it warns that adding an owned field requires revisiting the zero-copy contract

  # ===================================================================
  # diagnose signature — single &HealInput<'_> argument
  # ===================================================================

  @unit
  Scenario: diagnose takes a single &HealInput<'_> argument
    Given the diagnose function in crates/orchard/src/heal.rs
    Then its signature is `pub fn diagnose(input: &HealInput<'_>) -> HealReport`
    And it takes exactly one parameter

  @unit
  Scenario: diagnose destructures HealInput once at the top
    Given the body of diagnose
    Then it destructures *input into the six named fields once
    And the existing check_* helper signatures are unchanged
    And the helpers are called with the destructured locals

  # ===================================================================
  # Behavior preservation — no observable change
  # ===================================================================

  @integration
  Scenario: diagnose returns the same HealReport for the same inputs
    Given a fixed set of sessions, worktrees, claude_states, cache_files, known_repo_slugs, and current_session
    When the same inputs are passed to the new HealInput-based diagnose
    Then the returned HealReport.findings is identical to the pre-refactor output
    And findings order is preserved
    And every HealFinding field (category, severity, message, action, is_self) is unchanged

  @integration
  Scenario: Self-kill detection still flips is_self
    Given a tmux session named "myrepo_branch" with no matching worktree
    And current_session set to "myrepo_branch"
    When diagnose is called with a HealInput carrying these values
    Then the OrphanedSession finding for "myrepo_branch" has is_self = true

  @integration
  Scenario: Existing heal tests pass without modification of expectations
    When `cargo test -p orchard` is run
    Then every test in crates/orchard/src/heal.rs `#[cfg(test)]` block passes
    And every test in crates/orchard/tests/heal_test.rs passes
    And only call-site syntax changed; assertions on HealReport contents did not

  # ===================================================================
  # Call-site migration — production
  # ===================================================================

  @integration
  Scenario: Production caller in main.rs uses a full HealInput literal
    Given crates/orchard/src/main.rs at the heal-dispatch site
    Then it constructs `HealInput { sessions: …, worktrees: …, claude_states: …, cache_files: …, known_repo_slugs: …, current_session: … }`
    And it passes `&input` (or equivalent) to `heal::diagnose`
    And no positional 6-arg call to diagnose remains in the production code

  # ===================================================================
  # Call-site migration — tests
  # ===================================================================

  @integration
  Scenario: Sparse test call sites use `..Default::default()`
    Given a test that previously passed three or more empty slice arguments to diagnose
    Then the rewritten test constructs HealInput with only the relevant fields populated
    And the remaining fields come from `..Default::default()`

  @integration
  Scenario: Dense test call sites use full struct literals
    Given a test that previously populated most or all positional arguments
    Then the rewritten test uses a full HealInput literal with every field named
    And no test relies on positional argument order

  @integration
  Scenario: cache_files and known_repo_slugs are never swapped at any call site
    Given every migrated call site (production + tests)
    When the fields are inspected
    Then cache_files always receives the cache-file list
    And known_repo_slugs always receives the repo-slug list
    And the named-field syntax makes mis-binding impossible to introduce silently

  @integration
  Scenario: No positional 6-arg diagnose call survives in the workspace
    When the workspace is grepped for calls to `diagnose(`
    Then every call passes a single `&HealInput` (or `&input` binding) argument
    And no call uses the legacy 6-positional-argument form

  # ===================================================================
  # Quality gates
  # ===================================================================

  @integration
  Scenario: Workspace builds clean after the refactor
    When `cargo build --workspace` is run
    Then it succeeds with no errors
    And `cargo clippy --workspace --all-targets -- -D warnings` succeeds
    And `cargo fmt --check` succeeds

  @integration
  Scenario: Full workspace test suite passes
    When `cargo test --workspace` is run
    Then all tests pass

  # ===================================================================
  # Scope guards — what this refactor does NOT do
  # ===================================================================

  @unit
  Scenario: check_* helper signatures are NOT modified
    Given check_sessions, check_claude_states, check_cache_files,
          check_worktree_pr_states, check_worktree_issue_states,
          check_session_naming, check_multiple_sessions_per_worktree
    Then their parameter lists are unchanged from the pre-refactor signatures
    And they still take individual &[T] slices (not &HealInput)

  @unit
  Scenario: HealReport, HealFinding, HealAction, HealCategory are NOT modified
    Given the output types of heal::diagnose
    Then HealReport is unchanged
    And HealFinding is unchanged
    And HealAction is unchanged
    And HealCategory is unchanged

  @unit
  Scenario: No new fields are added to HealInput beyond the current six
    Given the HealInput struct as shipped by this refactor
    Then it contains exactly six fields matching the pre-refactor diagnose signature
    And cross-server detection or other new inputs are deferred to a follow-up issue

  @unit
  Scenario: HealInput is flat, not nested by domain
    Given the HealInput struct
    Then it does NOT contain sub-structs like TmuxState, GithubState, or CacheState
    And all six fields live directly on HealInput

  # ===================================================================
  # AC Coverage Map
  # ===================================================================
  # AC 1: "diagnose takes a single &HealInput<'_> argument."
  #       → "diagnose takes a single &HealInput<'_> argument"
  #       → "diagnose destructures HealInput once at the top"
  #
  # AC 2: "HealInput is a struct (with lifetimes) carrying current fields plus easy room for additions."
  #       → "HealInput struct exists with named fields for all current diagnose inputs"
  #       → "HealInput derives Debug, Clone, Copy, Default"
  #       → "HealInput::default() yields empty slices and None"
  #       → "HealInput is public from the heal module"
  #       → "HealInput documents the borrowed-only invariant"
  #       → "HealInput is flat, not nested by domain"
  #
  # AC 3: "All ~25 call sites (production + tests) migrate to the new signature."
  #       → "Production caller in main.rs uses a full HealInput literal"
  #       → "Sparse test call sites use `..Default::default()`"
  #       → "Dense test call sites use full struct literals"
  #       → "cache_files and known_repo_slugs are never swapped at any call site"
  #       → "No positional 6-arg diagnose call survives in the workspace"
  #
  # AC 4: "Behavior unchanged; existing tests pass."
  #       → "diagnose returns the same HealReport for the same inputs"
  #       → "Self-kill detection still flips is_self"
  #       → "Existing heal tests pass without modification of expectations"
  #       → "Workspace builds clean after the refactor"
  #       → "Full workspace test suite passes"
  #       → "check_* helper signatures are NOT modified"
  #       → "HealReport, HealFinding, HealAction, HealCategory are NOT modified"
