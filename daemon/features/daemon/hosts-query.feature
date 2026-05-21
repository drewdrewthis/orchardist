@integration
Feature: Daemon hosts query — HostsList response shape
  As any daemon consumer (GUI FleetTopBar, TUI federated switcher)
  I need the hosts query to return host identity, reachability, and resource load
  So that consumers can render fleet health without client-side data collection.

  Background:
    Given a daemon httptest.Server is running

  @integration
  Scenario: HostsList returns local host with required fields
    When the HostsList query runs
    Then hosts is a non-empty list
    And each host has id, hostname, os, reachable, lastSeenAt
    And each host has a nullable kernel field
    And each host has a nullable resourceLoad field
    And no GraphQL errors are present

  @integration
  Scenario: resourceLoad is null at cold boot
    When the daemon has not yet sampled resource metrics
    Then hosts[0].resourceLoad is null
    And no GraphQL errors are present

  @integration
  Scenario: resourceLoad present — fields are correct types
    Given the daemon has completed at least one resource sample
    When the HostsList query runs
    Then resourceLoad.cpuPercent is a float in [0, 100]
    And resourceLoad.memPercent is a float in [0, 100]
    And resourceLoad.diskPercent is a float in [0, 100]
    And resourceLoad.loadAvg1m, loadAvg5m, loadAvg15m are non-negative floats
    And no GraphQL errors are present

  @integration
  Scenario: claudeAccounts included in HostsList response
    When the HostsList query runs
    Then the response includes claudeAccounts
    And each account has id, email, quotaUsed, quotaCap, quotaResetsAt, quotaEstimated
    And no GraphQL errors are present
