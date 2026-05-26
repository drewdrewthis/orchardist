Feature: GUI new conversation modal — WorktreesList + HostsList
  As the orchard-gui NewConversation modal
  I need a flat list of non-bare worktrees and a list of reachable hosts
  So that the user can pick a target (host, cwd) to launch a new Claude REPL.

  Operations consumed:
    WorktreesListStore (WorktreesList query):
      repos[].worktrees { id, path, branch, bare, host, repo }

    HostsListStore (HostsList query):
      hosts { id, hostname, os, reachable, resourceLoad{ cpuPercent } }
      claudeAccounts { ... }   (read alongside hosts; quota shown in topbar, not here)

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the NewConversation modal opens

  @integration
  Scenario: Modal reads from already-warm Houdini stores — no extra network requests
    Given hostsStore and worktreesStore were fetched on LensSidebar/FleetTopBar mount
    When the NewConversation modal opens
    Then no new GraphQL queries are fired to the daemon
    And the modal renders instantly from cache

  @integration
  Scenario: WorktreesList response shape — bare worktrees excluded from picker
    When the WorktreesList query runs
    Then repos is a list of repos each with id, slug, and worktrees
    And each worktree has: id, path, branch, bare, host, repo
    And buildWorktreePickerRows filters out worktrees where bare = true
    And the picker displays only non-bare worktrees

  @integration
  Scenario: WorktreesList null repo fallback — uses repo.slug
    Given a worktree whose repo field is null (no GitHub origin remote detected)
    When buildWorktreePickerRows processes that worktree
    Then the picker row displays the parent repo.slug as the repo label
    And no null-reference error occurs

  @integration
  Scenario: Modal snapshots data on open — no live updates while open
    Given the modal is open with snapshotWorktrees and snapshotHosts captured at open time
    When the underlying hostsStore or worktreesStore receives a Houdini cache update
    Then the modal's displayed worktrees and hosts do NOT change
    And the user is not surprised by shifting options during selection

  @integration
  Scenario: Unreachable host renders as disabled in the host picker
    Given hosts[1].reachable = false
    When the host picker renders
    Then the button for that host is disabled
    And a "down" chip is displayed next to the hostname
    And the user cannot select an unreachable host as the launch target

  @integration
  Scenario: CPU load shown in host picker when resourceLoad is available
    Given host.reachable = true and host.resourceLoad.cpuPercent = 72
    When the host picker renders
    Then cpu: 72% is displayed under the hostname button

  @integration
  Scenario: Launch emits (worktreeId, cwd, host, model, task) and calls launchSession
    When the user picks a worktree and clicks Launch
    Then onLaunch is called with { worktreeId, cwd, host, model, task }
    And cwd is the picked worktree's path read from the already-loaded picker row (no fresh query)
    And the page calls the daemon launchSession mutation with { cwd, model, prompt }
    And model defaults to "claude-sonnet-4-5" unless changed
    And Launch is disabled until a worktree is selected (host and cur both required)
