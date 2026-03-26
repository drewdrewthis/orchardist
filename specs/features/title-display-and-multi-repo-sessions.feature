Feature: Branch tail segments in TITLE column and multi-repo main session auto-creation
  As a developer using git-orchard across multiple repos
  I want the TITLE column to show only the meaningful tail of branch names
  And I want main tmux sessions auto-created for each configured repo
  So that the TUI is readable at a glance and every repo has a shepherd session

  # ===================================================================
  # Improvement 1: Branch tail segments in TITLE column
  # ===================================================================
  #
  # When a worktree row has no issue title (issue_title is None or empty),
  # the TITLE column falls back to the branch name. Currently it shows the
  # full branch (e.g. "feat/issue-123"), which wastes horizontal space and
  # buries the meaningful part. Instead, show only the last segment after
  # the final `/` (e.g. "issue-123").
  #
  # This applies in two places:
  # 1. The TITLE cell in the task table row.
  # 2. The issue_part in the preview pane border title (when issue_number
  #    is None, it also falls back to the branch name).

  # -------------------------------------------------------------------------
  # Happy path: branch with prefix
  # -------------------------------------------------------------------------

  @unit
  Scenario: TITLE column shows tail segment when branch has slashes and no issue title
    Given a worktree row with branch "feat/issue-123" and no issue title
    When the TITLE column text is derived
    Then the displayed text is "issue-123"

  @unit
  Scenario: TITLE column shows full branch when branch has no slashes and no issue title
    Given a worktree row with branch "my-feature" and no issue title
    When the TITLE column text is derived
    Then the displayed text is "my-feature"

  @unit
  Scenario: TITLE column shows issue title when issue title is present
    Given a worktree row with branch "feat/issue-123" and issue title "Fix login bug"
    When the TITLE column text is derived
    Then the displayed text is "Fix login bug"

  # -------------------------------------------------------------------------
  # Multiple slash levels
  # -------------------------------------------------------------------------

  @unit
  Scenario: Branch with multiple slash levels shows only the last segment
    Given a worktree row with branch "user/feat/issue-456-refactor" and no issue title
    When the TITLE column text is derived
    Then the displayed text is "issue-456-refactor"

  # -------------------------------------------------------------------------
  # Preview pane border title
  # -------------------------------------------------------------------------

  @unit
  Scenario: Preview pane shows tail segment when no issue number and branch has slashes
    Given a worktree row with branch "feat/cleanup-ci" and no issue number
    When the preview pane border title is derived
    Then the issue_part of the title is "cleanup-ci"

  @unit
  Scenario: Preview pane shows issue number when present regardless of branch
    Given a worktree row with branch "feat/issue-99" and issue number 99
    When the preview pane border title is derived
    Then the issue_part of the title is "#99"

  # -------------------------------------------------------------------------
  # Edge cases
  # -------------------------------------------------------------------------

  @unit
  Scenario: Branch with trailing slash shows empty tail gracefully
    Given a worktree row with branch "feat/" and no issue title
    When the TITLE column text is derived
    Then the displayed text is ""

  @unit
  Scenario: Empty issue title falls back to branch tail same as None
    Given a worktree row with branch "feat/thing" and issue title ""
    When the TITLE column text is derived
    Then the displayed text is "thing"

  # ===================================================================
  # Improvement 2: Auto-create repo-specific main tmux sessions
  # ===================================================================
  #
  # The existing `ensure_main_session` in collector/mod.rs only operates
  # on the single local repo from the legacy pipeline. The cache-based
  # refresh pipeline (cache_sources + tui/mod.rs) handles multiple repos
  # but does NOT auto-create main sessions.
  #
  # The fix: after refreshing worktrees and tmux sessions for each repo,
  # the cache pipeline should ensure a main session exists for each
  # configured repo, using `derive_main_session_name` for repo-specific
  # naming (e.g. "git-orchard-rs_main", not just "main").
  #
  # This only applies in TUI mode. JSON mode remains read-only.

  Background:
    Given the global config declares repos:
      | slug                       | path                       |
      | hopegrace/git-orchard-rs   | /workspace/git-orchard-rs  |
      | langwatch/langwatch        | /workspace/langwatch       |
    And the worktree origin for "hopegrace/git-orchard-rs" is at "/workspace/git-orchard-rs" on branch "main"
    And the worktree origin for "langwatch/langwatch" is at "/workspace/langwatch" on branch "main"

  # -------------------------------------------------------------------------
  # Happy path: both repos get main sessions
  # -------------------------------------------------------------------------

  @e2e
  Scenario: Each configured repo gets its own main tmux session on TUI startup
    Given no tmux sessions exist
    When the TUI starts and the cache refresh pipeline runs
    Then a tmux session named "git-orchard-rs_main" is created at "/workspace/git-orchard-rs"
    And a tmux session named "langwatch_main" is created at "/workspace/langwatch"

  # -------------------------------------------------------------------------
  # Idempotency: sessions already exist
  # -------------------------------------------------------------------------

  @unit
  Scenario: Skips creation when repo-specific main session already exists
    Given a tmux session named "git-orchard-rs_main" already exists
    When the ensure main session logic runs for "hopegrace/git-orchard-rs"
    Then no new session is created for that repo

  @unit
  Scenario: Creates missing session even when other repos already have theirs
    Given a tmux session named "git-orchard-rs_main" already exists
    And no tmux session named "langwatch_main" exists
    When the cache refresh pipeline runs for all repos
    Then a tmux session named "langwatch_main" is created at "/workspace/langwatch"
    And no new session is created for "hopegrace/git-orchard-rs"

  # -------------------------------------------------------------------------
  # Pipeline integration: runs in cache-based pipeline, not just legacy
  # -------------------------------------------------------------------------

  @integration
  Scenario: Main session creation happens in the cache-based refresh pipeline
    Given no tmux sessions exist
    When the cache-based refresh pipeline runs (not the legacy collector)
    Then ensure_main_session logic executes for each configured repo
    And sessions are created before the derive step reads tmux data

  @integration
  Scenario: Newly created sessions appear in cache and derived view
    Given no tmux sessions exist
    When the cache refresh pipeline creates "git-orchard-rs_main"
    Then the tmux sessions cache includes the new session
    And the derived worktree view maps it to the origin worktree row

  # -------------------------------------------------------------------------
  # JSON mode: no side effects
  # -------------------------------------------------------------------------

  @integration
  Scenario: JSON mode does not auto-create main sessions for any repo
    Given no tmux sessions exist
    When orchard runs in JSON mode
    Then no tmux sessions are created for any configured repo

  # -------------------------------------------------------------------------
  # Session naming uses derive_main_session_name
  # -------------------------------------------------------------------------

  @unit
  Scenario: Session name uses repo directory name, not slug
    Given the worktree origin for "hopegrace/git-orchard-rs" is at "/workspace/git-orchard-rs" on branch "main"
    When the main session name is derived for this repo
    Then the session name is "git-orchard-rs_main"

  @unit
  Scenario: Session name uses the origin branch, not hardcoded "main"
    Given the worktree origin for "langwatch/langwatch" is at "/workspace/langwatch" on branch "develop"
    When the main session name is derived for this repo
    Then the session name is "langwatch_develop"

  @unit
  Scenario: Session name sanitizes dots in repo directory name
    Given the worktree origin path is "/workspace/my.project-v2" on branch "main"
    When the main session name is derived
    Then the session name is "my_project-v2_main"

  # -------------------------------------------------------------------------
  # Error handling
  # -------------------------------------------------------------------------

  @integration
  Scenario: Failed session creation for one repo does not block other repos
    Given tmux new-session will fail for "/workspace/langwatch"
    And no tmux sessions exist
    When the cache refresh pipeline runs for all repos
    Then a tmux session named "git-orchard-rs_main" is still created
    And an error is reported for the langwatch session creation failure
    And the TUI continues functioning

  @integration
  Scenario: Error from session creation is shown as a persistent message
    Given tmux new-session will fail for "/workspace/git-orchard-rs"
    When the cache refresh pipeline runs
    Then a persistent error message is displayed indicating the failure
    And the worktree row for the origin still appears without a session indicator
