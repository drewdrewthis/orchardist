Feature: Schema Worktree.host, repo, pr, issue, priorityFlag — joined enrichment for TUI dashboard
  As a thin-shell TUI consuming the orchard daemon's GraphQL schema
  I want the Worktree type to expose host, repo, joined PR/issue, and priorityFlag
  So that the TUI renders the federated dashboard without re-doing joins client-side

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
    Given a worktree whose project's origin remote is "git@github.com:drewdrewthis/git-orchard-rs.git"
    When a GraphQL query selects Worktree.repo
    Then the resolver returns "drewdrewthis/git-orchard-rs"

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
  # AC 5 — Worktree.priorityFlag: Boolean! + Mutation.setWorktreePriority
  # ===================================================================

  @unit
  Scenario: priorityFlag returns false when no priority is recorded for the worktree path
    Given the daemon's priorities store contains no entry for the worktree's path
    When the priorityFlag field is resolved
    Then the resolver returns false

  @unit
  Scenario: priorityFlag returns true when the worktree's path is in the priorities store
    Given the daemon's priorities store contains the worktree's absolute path
    When the priorityFlag field is resolved
    Then the resolver returns true

  @unit
  Scenario: priorityFlag is non-nullable in the schema
    Given the generated GraphQL schema
    When the Worktree.priorityFlag field is inspected
    Then its type is Boolean! (non-null)

  @integration
  Scenario: setWorktreePriority(path, true) persists and reflects in the next read
    Given a worktree at path "/Users/dev/repo/.worktrees/wt-1" with priorityFlag false
    When Mutation.setWorktreePriority(path, true) is invoked
    Then the mutation returns true
    And a subsequent query for that worktree's priorityFlag returns true
    And the priorities.json file on disk contains "/Users/dev/repo/.worktrees/wt-1"

  @integration
  Scenario: setWorktreePriority(path, false) removes the entry and persists
    Given a worktree at path "/Users/dev/repo/.worktrees/wt-1" with priorityFlag true
    When Mutation.setWorktreePriority(path, false) is invoked
    Then the mutation returns false
    And a subsequent query for that worktree's priorityFlag returns false
    And the priorities.json file on disk does not contain that path

  @integration
  Scenario: priorities store reuses Rust's existing JSON file shape (zero-migration)
    Given an existing ~/.cache/orchard/priorities.json with shape {"priorities":["/abs/path"]}
    When the daemon starts and reads the file
    Then the loaded set contains "/abs/path"
    And subsequent writes preserve the same shape

  @integration
  Scenario: priorities store uses flock and atomic temp+rename on writes
    Given two concurrent setWorktreePriority calls on different paths
    When both mutations execute
    Then both updates persist atomically with no lost write
    And the on-disk file is never observed mid-write (atomic rename)

  @integration
  Scenario: Rust TUI's toggle_priority callsites cut over to the daemon mutation
    Given the Rust TUI has been recompiled in this PR
    When the user toggles priority on a worktree row
    Then the TUI invokes Mutation.setWorktreePriority via GraphQL
    And the Rust crate no longer writes priorities.json directly

  # ===================================================================
  # AC 6 — PullRequestsForRepo DataLoader batches multi-repo queries
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
  # AC 7 — Typed error codes on gh-derived field failures
  # ===================================================================

  @integration
  Scenario: pr field surfaces GH_NOT_AUTHED when gh CLI is not authenticated
    Given the gh provider returns an authentication failure
    When Worktree.pr is resolved
    Then the field value is null
    And the per-field GraphQL error has extensions.code == "GH_NOT_AUTHED"
    And sibling fields on the same Worktree continue to resolve

  @integration
  Scenario: pr field surfaces NOT_GITHUB_REPO when repo cannot be derived
    Given a worktree whose project's origin is not a GitHub URL
    When Worktree.pr is resolved
    Then the field value is null
    And the per-field GraphQL error has extensions.code == "NOT_GITHUB_REPO"

  @integration
  Scenario: issue field surfaces typed error codes the same way as pr field
    Given the gh provider returns an authentication failure
    When Worktree.issue is resolved
    Then the field value is null
    And the per-field GraphQL error has extensions.code == "GH_NOT_AUTHED"

  @integration
  Scenario: typed errors let TUI collapse N identical errors into one indicator
    Given 50 worktrees and a broken gh auth
    When the TUI fetches the federated dashboard
    Then 50 per-field errors arrive each carrying extensions.code == "GH_NOT_AUTHED"
    And the TUI can deduplicate by code into a single status indicator

  # ===================================================================
  # End-to-end — full federated dashboard query against the live daemon
  # ===================================================================

  @e2e
  Scenario: Full dashboard query returns host, repo, pr, issue, priorityFlag for every worktree
    Given the daemon is running with at least one project and one worktree on a branch like "issue441/foo"
    And the worktree's repo has an open PR with headRef "issue441/foo"
    When a GraphQL query selects { worktree { host repo branch pr { number } issue { number } priorityFlag } }
    Then host == "local"
    And repo matches "owner/name"
    And pr.number is the open PR's number
    And issue.number == 441
    And priorityFlag is a boolean

  @e2e
  Scenario: Toggling priority via mutation updates a subsequent dashboard query
    Given the daemon is running with at least one worktree
    And the worktree's priorityFlag is initially false
    When Mutation.setWorktreePriority(path, true) is invoked
    And a subsequent { worktree { priorityFlag } } query runs
    Then priorityFlag is true

  # --- AC Coverage Map ---
  # AC 1: "Worktree.host: String! returns 'local' sentinel for v1"
  #   -> @unit "host resolver returns the literal 'local' sentinel for a locally-discovered worktree"
  #   -> @unit "host is non-nullable in the schema"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue, priorityFlag for every worktree"
  #
  # AC 2: "Worktree.repo: String — owner/repo slug derived from project origin; null when origin is not a GitHub URL"
  #   -> @unit "repo resolver returns owner/repo slug when origin is a GitHub URL"
  #   -> @unit "repo resolver returns null when origin is not a GitHub URL"
  #   -> @unit "repo resolver returns null when project has no origin remote"
  #   -> @unit "repo field is nullable in the schema"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue, priorityFlag for every worktree"
  #
  # AC 3: "Worktree.pr: PullRequest — joined via headRef = branch, scoped to the worktree's repo, default branch excluded"
  #   -> @unit "pr resolver matches PR by exact headRef equality with worktree branch"
  #   -> @unit "pr resolver returns null when no PR matches the branch"
  #   -> @unit "pr resolver returns null when worktree branch is the project's default branch"
  #   -> @unit "pr resolver returns null when worktree is in detached-head state"
  #   -> @unit "pr resolver scopes the PR search to the worktree's own repo"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue, priorityFlag for every worktree"
  #
  # AC 4: "Worktree.issue: Issue — joined by issue<N>/... branch parse for v1"
  #   -> @unit "issue resolver parses 'issue<N>/...' branch convention"
  #   -> @unit "issue resolver parses 'issue-<N>-...' (case-insensitive, hyphen variant)"
  #   -> @unit "issue resolver parses leading number with N>=100"
  #   -> @unit "issue resolver parses embedded number with N>=100"
  #   -> @unit "issue resolver enforces N>=100 floor on bare-leading numeric branches"
  #   -> @unit "issue resolver returns null when branch has no parseable issue number"
  #   -> @unit "issue resolver matches Rust's regex precedence exactly"
  #   -> @e2e  "Full dashboard query returns host, repo, pr, issue, priorityFlag for every worktree"
  #
  # AC 5: "Worktree.priorityFlag: Boolean! — daemon-owned read+write store; pair with Mutation.setWorktreePriority(path, value)"
  #   -> @unit "priorityFlag returns false when no priority is recorded for the worktree path"
  #   -> @unit "priorityFlag returns true when the worktree's path is in the priorities store"
  #   -> @unit "priorityFlag is non-nullable in the schema"
  #   -> @integration "setWorktreePriority(path, true) persists and reflects in the next read"
  #   -> @integration "setWorktreePriority(path, false) removes the entry and persists"
  #   -> @integration "priorities store reuses Rust's existing JSON file shape (zero-migration)"
  #   -> @integration "priorities store uses flock and atomic temp+rename on writes"
  #   -> @integration "Rust TUI's toggle_priority callsites cut over to the daemon mutation"
  #   -> @e2e  "Toggling priority via mutation updates a subsequent dashboard query"
  #
  # AC 6: "PR fetches go through a PullRequestsForRepo DataLoader so a multi-repo dashboard query collapses to one round-trip per repo"
  #   -> @integration "Multi-repo dashboard query collapses to one PR fetch per repo"
  #   -> @integration "PullRequestsForRepo loader batches concurrent worktree pr resolutions"
  #   -> @integration "PullRequestsForRepo loader is per-request scoped (mirrors WorktreeForCwd)"
  #
  # AC 7: "gh-derived field failures (pr, issue) carry typed error codes (e.g. GH_NOT_AUTHED, NOT_GITHUB_REPO)"
  #   -> @integration "pr field surfaces GH_NOT_AUTHED when gh CLI is not authenticated"
  #   -> @integration "pr field surfaces NOT_GITHUB_REPO when repo cannot be derived"
  #   -> @integration "issue field surfaces typed error codes the same way as pr field"
  #   -> @integration "typed errors let TUI collapse N identical errors into one indicator"
  #
  # Math: 7 issue ACs -> all mapped, every AC has >=1 scenario. No drops.
