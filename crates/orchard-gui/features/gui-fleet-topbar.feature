Feature: GUI fleet top bar — HostsList query
  As the orchard-gui FleetTopBar and PeerCluster
  I need host identity, reachability, resource load, and Claude account quota
  So that operators see fleet health and their API quota at a glance.

  Operation consumed:
    query HostsList {
      hosts {
        id, hostname, os, kernel, reachable, lastSeenAt
        resourceLoad { cpuPercent, memPercent, diskPercent, loadAvg1m, loadAvg5m, loadAvg15m }
      }
      claudeAccounts {
        id, email, quotaUsed, quotaCap, quotaResetsAt, quotaEstimated
      }
    }

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And the HostsListStore Houdini singleton is used (hostsStore)
    And FleetTopBar calls hostsStore.fetch() on mount

  @integration
  Scenario: HostsList returns local host with all required fields
    When the HostsList query runs
    Then hosts is a non-empty list
    And each host has: id, hostname, os, reachable, lastSeenAt
    And each host has a nullable kernel field
    And each host has a nullable resourceLoad field

  @integration
  Scenario: resourceLoad is null at cold boot — topbar shows "—" not fabricated zeros
    When the daemon has not yet sampled resource metrics (cold boot)
    Then hosts[0].resourceLoad is null
    And PeerCluster renders "no resource sample yet" in the tooltip
    And no fake CPU/memory values appear in the UI

  @integration
  Scenario: resourceLoad present — topbar renders real metrics
    Given the daemon has completed at least one 5s resource sample
    When the HostsList query runs
    Then resourceLoad.cpuPercent is a float in [0, 100]
    And resourceLoad.memPercent is a float in [0, 100]
    And resourceLoad.diskPercent is a float in [0, 100]
    And resourceLoad.loadAvg1m, loadAvg5m, loadAvg15m are non-negative floats

  @integration
  Scenario: Peer cluster pip color reflects CPU load
    Given host.resourceLoad.cpuPercent = 92
    Then the pip for that host renders with the "attn" class (amber/red)
    Given host.resourceLoad.cpuPercent = 40
    Then the pip renders with the "ok" class (green)
    Given host.reachable = false
    Then the pip renders with the "bad" class (red)

  @integration
  Scenario: Claude account quota renders only when cap is known
    Given claudeAccounts[0].quotaCap is null
    Then the quota bar is hidden in the topbar
    Given claudeAccounts[0].quotaCap = 100 and quotaUsed = 85
    Then the quota bar renders at 85% fill
    And the bar color is "attn" (overQuota flag = true because 85/100 > 0.8)
    Given claudeAccounts[0].quotaEstimated = true
    Then the quota tooltip reads "Estimated by ccusage"

  @integration
  Scenario: Multiple hosts — peer cluster shows one pip per host
    Given the daemon reports 3 hosts (1 local + 2 federation peers)
    When the HostsList query runs
    Then hosts.length = 3
    And PeerCluster renders 3 pips
    And each pip links to its host's resource tooltip

  @integration
  Scenario: Unreachable host — tooltip shows last seen time
    Given hosts[1].reachable = false and hosts[1].lastSeenAt = <5 minutes ago>
    When PeerCluster renders the tooltip for that host
    Then the tooltip shows "unreachable · last seen 5m ago"
    And the pip renders with the "bad" (red) class

  @integration
  Scenario: NewConversation modal reads from same HostsListStore — no second fetch
    When the user opens the NewConversation modal
    Then the modal reads hosts from the already-fetched hostsStore singleton
    And no second HostsList query is issued to the daemon
    And worktrees are read from the WorktreesListStore singleton (WorktreesList query)
