Feature: Schema Worktree.host, repo, pr, issue — read-side joined enrichment for TUI dashboard
  As a thin-shell TUI consuming the orchard daemon's GraphQL schema
  I want the Worktree type to expose host, repo, joined PR, and joined issue
  So that the TUI renders the federated dashboard without re-doing joins client-side

  # Phase 1 of #441 (split). priorityFlag is owned by #466; typed gh-error codes are owned by #467.

  Background:
    Given the daemon serves a GraphQL schema at 127.0.0.1:7777
    And the existing Worktree type already exposes id, path, branch, head, bare, processes
    And the gh provider exposes ReadOriginURL and ParseGitHubURL helpers
    And the loaders package already implements the WorktreeForCwd DataLoader pattern
    And per-field GraphQL errors are the established contract for gh-derived fields

  # ===================================================================
  # AC 1 — Worktree.host: String! returns "local" sentinel for v1
  # ===================================================================

  @unit
  Scenario: host resolver returns the literal "local" sentinel for a locally-discovered worktree
    Given a worktree discovered by the local git provider
    When a GraphQL query selects Worktree.host
    Then the resolver returns the string "local"
    And the value is independent of os.Hostname()

  @unit
  Scenario: host is non-nullable in the schema
    Given the generated GraphQL schema
    When the Worktree.host field is inspected
    Then its type is String! (non-null)

  # ===================================================================
  # AC 2 — Worktree.repo: String — owner/repo slug from origin
  # ===================================================================

  @unit
  Scenario: repo resolver returns owner/repo slug when origin is a GitHub URL
    Given a worktree whose project's origin remote is "git@github.com:drewdrewthis/orchardist.git"
    When a GraphQL query selects Worktree.repo
    Then the resolver returns "drewdrewthis/orchardist"

  @unit
  Scenario: repo resolver returns null when origin is not a GitHub URL
    Given a worktree whose project's origin remote is "git@gitlab.com:other/repo.git"
    When a GraphQL query selects Worktree.repo
    Then the resolver returns null

  @unit
  Scenario: repo resolver returns null when project has no origin remote
    Given a worktree whose project has no origin remote configured
    When a GraphQL query selects Worktree.repo
    Then the resolver returns null

  @unit
  Scenario: repo field is nullable in the schema
    Given the generated GraphQL schema
    When the Worktree.repo field is inspected
    Then its type is String (nullable)

  # ===================================================================
  # AC 3 — Worktree.pr: PullRequest — joined via headRef=branch, default branch excluded
  # ===================================================================

  @unit
  Scenario: pr resolver matches PR by exact headRef equality with worktree branch
    Given a worktree on branch "issue441/schema-enrich"
    And the repo has an open PR with headRef "issue441/schema-enrich"
    When the pr field is resolved
    Then the resolver returns the matched PullRequest

  @unit
  Scenario: pr resolver returns null when no PR matches the branch
    Given a worktree on branch "feature/no-pr-yet"
    And the repo has no PR with that headRef
    When the pr field is resolved
    Then the resolver returns null

  @unit
  Scenario: pr resolver returns null when worktree branch is the project's default branch
    Given a worktree on branch "main"
    And "main" is the project's default branch (read from the bare's .git/HEAD)
    When the pr field is resolved
    Then the resolver returns null
    And no PR list is fetched for this worktree

  @unit
  Scenario: pr resolver returns null when worktree is in detached-head state
    Given a worktree with a detached HEAD (no branch)
    When the pr field is resolved
    Then the resolver returns null

  @unit
  Scenario: pr resolver scopes the PR search to the worktree's own repo
    Given two worktrees in two different repos sharing the same branch name "wip/feature"
    And each repo has its own PR with headRef "wip/feature"
    When the pr field is resolved on each worktree
    Then each worktree returns its own repo's PR, not the other repo's

  # ===================================================================
  # AC 4 — Worktree.issue: Issue — branch-parse for v1
  # ===================================================================

  @unit
  Scenario: issue resolver parses "issue<N>/..." branch convention
    Given a worktree on branch "issue441/schema-enrichment"
    And issue 441 exists in the worktree's repo
    When the issue field is resolved
    Then the resolver returns the Issue with number 441

  @unit
  Scenario: issue resolver parses "issue-<N>-..." (case-insensitive, hyphen variant)
    Given a worktree on branch "Issue-123-something"
    And issue 123 exists in the worktree's repo
    When the issue field is resolved
    Then the resolver returns the Issue with number 123

  @unit
  Scenario: issue resolver parses leading number with N>=100
    Given a worktree on branch "441-some-slug"
    And issue 441 exists in the worktree's repo
    When the issue field is resolved
    Then the resolver returns the Issue with number 441

  @unit
  Scenario: issue resolver parses embedded number with N>=100
    Given a worktree on branch "feature-441-slug"
    And issue 441 exists in the worktree's repo
    When the issue field is resolved
    Then the resolver returns the Issue with number 441

  @unit
  Scenario: issue resolver enforces N>=100 floor on bare-leading numeric branches
    Given a worktree on branch "12-something"
    When the issue field is resolved
    Then the resolver returns null
    And no gh API call is made for issue 12

  @unit
  Scenario: issue resolver returns null when branch has no parseable issue number
    Given a worktree on branch "feature/no-number"
    When the issue field is resolved
    Then the resolver returns null

  @unit
  Scenario: issue resolver matches Rust's regex precedence exactly
    Given a worktree on branch "issue441/441-other"
    When the issue field is resolved
    Then the resolver returns the issue identified by the first matching regex (issue441)
    And the precedence mirrors crates/orchard/src/github.rs:82-114

  # ===================================================================
  # AC 5 — PullRequestsForRepo DataLoader batches multi-repo queries
  # ===================================================================

  @integration
  Scenario: Multi-repo dashboard query collapses to one PR fetch per repo
    Given 5 repos each with 8 worktrees
    And every worktree resolves Worktree.pr in a single GraphQL query
    When the query executes against a cold daemon (no gh cache)
    Then the gh provider's ListPullRequests is called exactly 5 times (once per repo)
    And not 40 times (worktree count)

  @integration
  Scenario: PullRequestsForRepo loader batches concurrent worktree pr resolutions
    Given a single GraphQL query selecting pr on 8 worktrees in the same repo
    When the field resolvers race in parallel
    Then the loader batches them into one underlying gh ListPullRequests call

  @integration
  Scenario: PullRequestsForRepo loader is per-request scoped (mirrors WorktreeForCwd)
    Given two separate GraphQL queries against the same repo
    When each query independently resolves Worktree.pr
    Then each query gets its own loader instance
    And the loaders do not leak state across requests

  # ===================================================================
  # End-to-end — full dashboard query against the live daemon
  # ===================================================================

  @e2e
  Scenario: Full dashboard query returns host, repo, pr, issue for every worktree
    Given the daemon is running with at least one project and one worktree on a branch like "issue441/foo"
    And the worktree's repo has an open PR with headRef "issue441/foo"
    When a GraphQL query selects { worktree { host repo branch pr { number } issue { number } } }
    Then host == "local"
    And repo matches "owner/name"
    And pr.number is the open PR's number
    And issue.number == 441

  # --- AC Coverage Map ---
  # AC 1: "Worktree.host: String! returns 'local' sentinel for v1"
  #   -> @unit "host resolver returns the literal 'local' sentinel for a locally-discovered worktree"
  #   -> @unit "host is non-nullable in the schema"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue for every worktree"
  #
  # AC 2: "Worktree.repo: String — owner/repo slug derived from project origin; null when origin is not a GitHub URL"
  #   -> @unit "repo resolver returns owner/repo slug when origin is a GitHub URL"
  #   -> @unit "repo resolver returns null when origin is not a GitHub URL"
  #   -> @unit "repo resolver returns null when project has no origin remote"
  #   -> @unit "repo field is nullable in the schema"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue for every worktree"
  #
  # AC 3: "Worktree.pr: PullRequest — joined via headRef = branch, scoped to the worktree's repo, default branch excluded"
  #   -> @unit "pr resolver matches PR by exact headRef equality with worktree branch"
  #   -> @unit "pr resolver returns null when no PR matches the branch"
  #   -> @unit "pr resolver returns null when worktree branch is the project's default branch"
  #   -> @unit "pr resolver returns null when worktree is in detached-head state"
  #   -> @unit "pr resolver scopes the PR search to the worktree's own repo"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue for every worktree"
  #
  # AC 4: "Worktree.issue: Issue — joined by issue<N>/... branch parse for v1"
  #   -> @unit "issue resolver parses 'issue<N>/...' branch convention"
  #   -> @unit "issue resolver parses 'issue-<N>-...' (case-insensitive, hyphen variant)"
  #   -> @unit "issue resolver parses leading number with N>=100"
  #   -> @unit "issue resolver parses embedded number with N>=100"
  #   -> @unit "issue resolver enforces N>=100 floor on bare-leading numeric branches"
  #   -> @unit "issue resolver returns null when branch has no parseable issue number"
  #   -> @unit "issue resolver matches Rust's regex precedence exactly"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue for every worktree"
  #
  # AC 5: "PR fetches go through a PullRequestsForRepo DataLoader so a multi-repo dashboard query collapses to one round-trip per repo"
  #   -> @integration "Multi-repo dashboard query collapses to one PR fetch per repo"
  #   -> @integration "PullRequestsForRepo loader batches concurrent worktree pr resolutions"
  #   -> @integration "PullRequestsForRepo loader is per-request scoped (mirrors WorktreeForCwd)"
  #
  # Phase-1 scope only. AC 5 (priorityFlag) split to #466. AC 7 (typed gh errors) split to #467.
