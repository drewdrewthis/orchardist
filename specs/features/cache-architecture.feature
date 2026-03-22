Feature: Per-source cache architecture
  As an orchard user
  I want each data source cached independently
  So that the TUI renders instantly from disk and refreshes each source without blocking others

  Background:
    Given the cache directory is "~/.cache/orchard/"
    And the config file is "~/.config/orchard/config.json"

  # ===================================================================
  # Cache module — per-source files
  # ===================================================================

  @unit
  Scenario: Each data source has its own cache file
    Then the following cache files exist per repo:
      | source             | filename                                    |
      | GitHub Issues      | {owner}_{repo}_issues.json                  |
      | GitHub PRs         | {owner}_{repo}_prs.json                     |
      | Git Worktrees      | {owner}_{repo}_worktrees.json               |
      | Remote Worktrees   | {owner}_{repo}_remote_worktrees.json        |
    And the following cache file is global (not per-repo):
      | source             | filename                                    |
      | Tmux Sessions      | tmux_sessions.json                          |

  @unit
  Scenario: Cache file naming uses owner and repo slug separated by underscore
    Given a repo with slug "langwatch/langwatch"
    Then the issues cache file is "~/.cache/orchard/langwatch_langwatch_issues.json"
    And the PRs cache file is "~/.cache/orchard/langwatch_langwatch_prs.json"
    And the worktrees cache file is "~/.cache/orchard/langwatch_langwatch_worktrees.json"

  @unit
  Scenario: Reading a missing cache file returns empty data
    Given no cache file exists at "~/.cache/orchard/langwatch_langwatch_issues.json"
    When the issues cache is read for "langwatch/langwatch"
    Then an empty list of issues is returned
    And no error is raised

  @unit
  Scenario: Reading a missing tmux sessions cache returns empty data
    Given no cache file exists at "~/.cache/orchard/tmux_sessions.json"
    When the tmux sessions cache is read
    Then an empty list of sessions is returned
    And no error is raised

  @unit
  Scenario: Cache writes are atomic
    When the issues cache for "langwatch/langwatch" is written
    Then it is first written to "~/.cache/orchard/langwatch_langwatch_issues.json.tmp"
    And then renamed to "~/.cache/orchard/langwatch_langwatch_issues.json"
    So that a crash mid-write does not leave a partial cache file

  @unit
  Scenario: Each source cache is read and written independently
    Given an issues cache and a PRs cache exist for "langwatch/langwatch"
    When the PRs cache is updated
    Then the issues cache file is not modified
    And the PRs cache file is overwritten atomically

  @unit
  Scenario: Failed API call does not overwrite existing cache with empty data
    Given a valid issues cache exists at "~/.cache/orchard/langwatch_langwatch_issues.json"
    When the GitHub Issues API call fails during a background refresh
    Then the cache file "~/.cache/orchard/langwatch_langwatch_issues.json" is preserved unchanged
    And no empty or partial data is written to disk

  @unit
  Scenario: Each cache file includes a last_refreshed timestamp
    When any cache file is written after a successful refresh
    Then the file's metadata contains a "last_refreshed" field
    And the value is an ISO8601 timestamp representing when the refresh completed

  @unit
  Scenario: Issues cache entry structure
    When the issues cache for "langwatch/langwatch" is written
    Then each entry has the fields:
      | field    | type    | description                       |
      | number   | integer | GitHub issue number               |
      | title    | string  | Issue title                       |
      | state    | string  | "open" or "closed"                |
      | labels   | array   | list of label name strings        |

  @unit
  Scenario: PRs cache entry structure
    When the PRs cache for "langwatch/langwatch" is written
    Then each entry has the fields:
      | field              | type             | description                                      |
      | number             | integer          | GitHub PR number                                 |
      | branch             | string           | head branch name                                 |
      | linked_issue       | integer or null  | issue number this PR is linked to via GitHub     |
      | state              | string           | "open", "closed", or "merged"                    |
      | review_decision    | string           | "approved", "changes_requested", or null         |
      | checks_state       | string           | "passing", "failing", "pending", or null         |
      | has_conflicts      | boolean          | true if PR has merge conflicts                   |
      | unresolved_threads | integer          | count of unresolved review threads               |

  @unit
  Scenario: Worktrees cache entry structure
    When the worktrees cache for "langwatch/langwatch" is written
    Then each entry has the fields:
      | field    | type    | description                          |
      | path     | string  | absolute path to the worktree        |
      | branch   | string  | checked-out branch name              |
      | is_bare  | boolean | true for the bare worktree           |
      | is_locked| boolean | true if worktree is locked           |

  @unit
  Scenario: Tmux sessions cache entry structure
    When the tmux sessions cache is written
    Then each entry has the fields:
      | field        | type             | description                                        |
      | name         | string           | tmux session name                                  |
      | path         | string           | working directory of the first window              |
      | pane_titles  | array            | list of pane title strings                         |
      | pane_commands| array            | list of pane command strings                       |
      | host         | string or null   | null for local; hostname string for remote hosts   |

  # ===================================================================
  # Startup flow
  # ===================================================================

  @unit
  Scenario: App reads all cache files on startup before any network call
    Given cache files exist for repos "langwatch/langwatch" and "hopegrace/git-orchard-rs"
    When the app starts
    Then all cache files are read from disk synchronously
    And the TUI renders before any background refresh begins

  @integration
  Scenario: Background refresh runs each source independently after startup
    Given the app has started and rendered from cache
    When the background refresh begins
    Then each of the following sources refreshes independently and concurrently:
      | source                                         |
      | GitHub Issues for each configured repo         |
      | GitHub PRs for each configured repo            |
      | Git Worktrees for each configured repo         |
      | Remote Worktrees for each configured repo      |
      | Tmux Sessions (global)                         |

  @integration
  Scenario: TUI re-renders after each individual source update
    Given the app is running with cached data
    When the GitHub Issues refresh for "langwatch/langwatch" completes
    Then the cache file "langwatch_langwatch_issues.json" is updated
    And the TUI re-derives and re-renders immediately
    And other sources that have not yet refreshed remain at their cached values

  @integration
  Scenario: Slow remote worktree refresh does not block local data display
    Given the remote SSH host is unreachable
    When the background refresh runs
    Then local issues, PRs, worktrees, and tmux sessions still refresh and update the TUI
    And the remote worktrees source shows its last cached data
    And an error is logged for the failed remote refresh

  # ===================================================================
  # Derived view — join logic
  # ===================================================================

  @unit
  Scenario: Issues are the base rows in the derived view
    Given an issues cache with issues #10, #11, #12
    And empty worktrees, PRs, and sessions caches
    When the derived view is computed
    Then the view contains exactly 3 rows, one per issue
    And each row has no associated worktree, PR, or session

  @unit
  Scenario: Closed issues are not fetched or displayed
    Given the GitHub Issues API returns issue #10 (open) and issue #20 (closed)
    When the issues cache for "langwatch/langwatch" is written
    Then only issue #10 is stored in the cache
    And the derived view contains no row for issue #20

  @unit
  Scenario: PR joins to an issue via the GitHub API linked_issue field
    Given a PRs cache with a PR #55 whose linked_issue is 47 and branch is "feat/task-centric"
    And an issues cache with issue #47
    When the derived view is computed
    Then the row for issue #47 includes PR #55 and its review/checks data

  @unit
  Scenario: PR with no linked issue is ignored in the derived view
    Given a PRs cache with a PR #99 whose linked_issue is null
    And an issues cache with issues #10 and #11
    When the derived view is computed
    Then no task row references PR #99
    And the rows for issue #10 and issue #11 have no associated PR

  @unit
  Scenario: One task row per PR when an issue has multiple linked PRs
    Given an issues cache with issue #47
    And a PRs cache with PR #55 (linked_issue: 47) and PR #56 (linked_issue: 47)
    When the derived view is computed
    Then the view contains 2 rows for issue #47 — one for PR #55 and one for PR #56

  @unit
  Scenario: Worktree joins to a PR via shared branch name
    Given a PRs cache with PR #55 whose linked_issue is 47 and branch is "feat/task-centric"
    And a worktrees cache with a worktree at path "/workspace/langwatch-47" on branch "feat/task-centric"
    When the derived view is computed
    Then the row for issue #47 / PR #55 has the worktree path "/workspace/langwatch-47"

  @unit
  Scenario: Worktree with a branch that matches no PR branch is not joined
    Given a worktrees cache with a worktree on branch "main"
    And no PR in the PRs cache has branch "main"
    When the derived view is computed
    Then the "main" worktree is not surfaced as a task row

  @unit
  Scenario: Tmux session joins to a task row via worktree path
    Given issue #47 / PR #55 has an associated worktree at path "/workspace/langwatch-47"
    And a tmux sessions cache with a session at path "/workspace/langwatch-47"
    When the derived view is computed
    Then the row for issue #47 / PR #55 includes that tmux session

  @unit
  Scenario: Multiple tmux sessions at the same worktree path all join to the task row
    Given issue #47 / PR #55 has a worktree at path "/workspace/langwatch-47"
    And tmux sessions "langwatch_47_main" and "langwatch_47_claude" both have path "/workspace/langwatch-47"
    When the derived view is computed
    Then the row for issue #47 / PR #55 includes both sessions

  @unit
  Scenario: Display group "needs_attention" is derived when PR has changes requested
    Given issue #47 has a PR with review_decision "changes_requested"
    When the display group is derived for issue #47
    Then the display group is "needs_attention"

  @unit
  Scenario: Display group "needs_attention" is derived when PR has merge conflicts
    Given issue #47 has a PR with has_conflicts true
    When the display group is derived for issue #47
    Then the display group is "needs_attention"

  @unit
  Scenario: Display group "needs_attention" is derived when PR has failing CI
    Given issue #47 has a PR with checks_state "failing"
    When the display group is derived for issue #47
    Then the display group is "needs_attention"

  @unit
  Scenario: Display group "needs_attention" is derived when PR has unresolved review threads
    Given issue #47 has a PR with unresolved_threads > 0
    When the display group is derived for issue #47
    Then the display group is "needs_attention"

  @unit
  Scenario: Display group "claude_working" is derived when a Claude agent is active in a pane
    Given issue #47 has a session whose pane_commands include "claude"
    And the PR is not in a needs_attention state
    When the display group is derived for issue #47
    Then the display group is "claude_working"

  @unit
  Scenario: Display group "ready_to_merge" is derived when PR is approved with passing checks and no conflicts
    Given issue #47 has a PR with review_decision "approved" and checks_state "passing"
    And has_conflicts is false and unresolved_threads is 0
    When the display group is derived for issue #47
    Then the display group is "ready_to_merge"

  @unit
  Scenario: Display group "backlog" is derived when issue has no associated PR
    Given issue #47 has no PR in the PRs cache with linked_issue 47
    When the display group is derived for issue #47
    Then the display group is "backlog"

  @unit
  Scenario: Display groups are ordered for rendering
    Then the display group rendering order is:
      | order | group            |
      | 1     | needs_attention  |
      | 2     | claude_working   |
      | 3     | ready_to_merge   |
      | 4     | backlog          |

  @unit
  Scenario: No TaskStatus is stored anywhere
    When the derived view is computed from cache files
    Then no "status" field is read from any cache file
    And no "status" field is written to any cache file
    And the display group is computed fresh every time the view is derived

  # ===================================================================
  # Remote tmux sessions
  # ===================================================================

  @integration
  Scenario: Remote hosts have their own tmux sessions polled via SSH
    Given repo "langwatch/langwatch" has a remote at "ubuntu@10.0.0.1"
    When the tmux sessions refresh runs
    Then local tmux sessions are polled directly
    And remote tmux sessions on "ubuntu@10.0.0.1" are polled via SSH independently
    And both sets of sessions are stored in the tmux sessions cache with the appropriate host field

  @unit
  Scenario: Remote tmux sessions join to remote worktrees the same way local sessions join to local worktrees
    Given a remote worktree at path "/home/ubuntu/langwatch-workspace/feat-branch" on host "ubuntu@10.0.0.1"
    And a remote tmux session with host "ubuntu@10.0.0.1" and path "/home/ubuntu/langwatch-workspace/feat-branch"
    When the derived view is computed
    Then the task row for the associated PR includes the remote tmux session

  # ===================================================================
  # Multi-repo
  # ===================================================================

  @integration
  Scenario: Global config declares repos to manage
    Given a config file "~/.config/orchard/config.json" containing:
      """json
      {
        "repos": [
          {
            "slug": "langwatch/langwatch",
            "path": "/workspace/langwatch",
            "remote": { "host": "ubuntu@10.0.0.1", "path": "/home/ubuntu/langwatch-workspace" }
          },
          {
            "slug": "hopegrace/git-orchard-rs",
            "path": "/workspace/git-orchard-rs"
          }
        ]
      }
      """
    When the config is loaded
    Then 2 repos are configured
    And repo "langwatch/langwatch" has a remote on host "ubuntu@10.0.0.1"
    And repo "hopegrace/git-orchard-rs" has no remote

  @integration
  Scenario: Each repo's cache sources refresh independently
    Given two repos "langwatch/langwatch" and "hopegrace/git-orchard-rs" are configured
    When the background refresh runs
    Then the issues refresh for "langwatch/langwatch" does not wait for "hopegrace/git-orchard-rs"
    And each repo writes to its own separate cache files

  @integration
  Scenario: TUI shows tasks from all configured repos
    Given "langwatch/langwatch" issues cache has issues #10, #11
    And "hopegrace/git-orchard-rs" issues cache has issues #47, #48
    When the derived view is computed
    Then the TUI shows 4 task rows total
    And each row identifies which repo it belongs to

  @integration
  Scenario: A repo with no cache files yet shows an empty section until first refresh
    Given "newrepo/project" is declared in config
    And no cache files exist for "newrepo/project"
    When the TUI starts
    Then the TUI renders with 0 tasks for "newrepo/project"
    And the background refresh populates the cache without blocking the TUI

  @integration
  Scenario: Remote worktrees refresh independently of local worktrees
    Given repo "langwatch/langwatch" has a remote at "ubuntu@10.0.0.1"
    When the background refresh runs
    Then the local worktrees refresh runs via "git worktree list"
    And the remote worktrees refresh runs via SSH independently
    And each writes to its own cache file

  # ===================================================================
  # Migration — removal of state.json and TaskStatus
  # ===================================================================

  @unit
  Scenario: state.json is not read on startup
    Given a file exists at "~/.local/state/git-orchard/state.json"
    When the app starts
    Then the file "~/.local/state/git-orchard/state.json" is not opened or read
    And the TUI renders entirely from cache files under "~/.cache/orchard/"

  @unit
  Scenario: state.json is not written during a session
    Given the app is running
    When any cache source is refreshed and the TUI re-renders
    Then the file "~/.local/state/git-orchard/state.json" is not created or modified

  @unit
  Scenario: TaskStatus enum does not exist in the codebase
    Then no type or enum named "TaskStatus" is defined
    And no fields named "status" appear in any cache file schema
    And the display group is the only status-like concept used in rendering

  @unit
  Scenario: issue_sync.rs logic is replaced by the GitHub Issues cache source
    Then the application does not call any function from "issue_sync.rs"
    And GitHub issues are fetched directly by the cache refresh for each configured repo
    And the result is written to the per-repo issues cache file

  @unit
  Scenario: merge_worktrees_into_tasks is replaced by the join logic
    Then the application does not call any function named "merge_worktrees_into_tasks"
    And worktrees are joined to PRs by branch name at derive time, not stored in a merged structure
