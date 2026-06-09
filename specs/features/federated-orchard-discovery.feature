Feature: Federated orchard — remote discovery via `ssh host orchard --json`
  As an orchardist with multiple machines running orchard
  I want each remote machine to be the authority on its own worktrees and sessions
  And I want the local orchard to federate remote `JsonOutput` snapshots into one unified view
  So that PR/issue/claude enrichment is computed once on the remote, not re-derived locally,
  And un-upgraded remotes still work through the legacy shell-discovery fallback

  # Scope: READ PATH ONLY. Mutating operations (create worktree, kill session, transfer)
  # continue to use the existing Remmy / BoxdShared / BoxdFork adapter paths (AC11).

  Background:
    Given the per-repo `remotes[]` array may contain entries with `"type": "orchard-proxy"`
    And each `OrchardProxy` remote has fields: name, host, path, type, optional fallback_kind
    And the local cache directory is "~/.cache/orchard/"
    And the remote orchard emits a versioned `JsonOutput` schema via `orchard --json`

  # =======================================================================
  # AC1 — `RemoteKind::OrchardProxy` exists and is selectable by config
  # =======================================================================

  @unit
  Scenario: RemoteConfig accepts "orchard-proxy" as a valid type
    Given a RemoteConfig with `"type": "orchard-proxy"`, host "boxd@vm.boxd.sh", path "~/orchardist"
    When the config is loaded
    Then loading succeeds
    And the parsed RemoteConfig.kind equals RemoteKind::OrchardProxy

  @unit
  Scenario: Adapter dispatch returns an OrchardProxyAdapter for OrchardProxy kind
    Given a RemoteConfig with type "orchard-proxy"
    When the core constructs a RemoteWorktreeService for it
    Then an OrchardProxyAdapter is returned
    And the adapter carries the configured host and an injected SSH exec seam

  @unit
  Scenario: OrchardProxy appears in the supported-types error message
    Given a RemoteConfig with an invalid type "kubernetes"
    When the config is loaded
    Then loading fails with an error naming the supported types
    And the supported-types list includes "orchard-proxy"

  # =======================================================================
  # AC2 — list_worktrees() is sourced from `ssh host orchard --json`
  # =======================================================================

  @unit
  Scenario: OrchardProxyAdapter.list_worktrees parses `ssh host orchard --json` output
    Given an OrchardProxyAdapter for host "boxd@vm.boxd.sh"
    And the adapter is constructed with a fake SSH exec runner
    And the fake runner, when invoked with `ssh boxd@vm.boxd.sh orchard --json`, returns a canned JsonOutput containing one repo with one non-bare worktree on branch "issue329/federated-orchard"
    When OrchardProxyAdapter.list_worktrees() is called
    Then exactly 1 CachedWorktree is returned for branch "issue329/federated-orchard"
    And its host equals "boxd@vm.boxd.sh"
    And no `git worktree list --porcelain` command is invoked on the fake runner

  @unit
  Scenario: OrchardProxyAdapter does NOT invoke raw `git worktree list --porcelain`
    Given an OrchardProxyAdapter with a fake SSH exec runner that records every command it receives
    When list_worktrees() succeeds via the `orchard --json` code path
    Then the recorded command list contains `orchard --json`
    And the recorded command list does NOT contain any `git worktree list --porcelain` invocation

  # =======================================================================
  # AC3 — list_sessions() is sourced from the same `orchard --json` output
  # =======================================================================

  @unit
  Scenario: OrchardProxyAdapter.list_sessions parses sessions from the same snapshot
    Given the fake SSH runner returns a JsonOutput with two tmux sessions attached to the worktree
    And one standalone tmux session ("shepherd")
    When OrchardProxyAdapter.list_sessions() is called
    Then 3 CachedTmuxSessions are returned
    And each session carries host "boxd@vm.boxd.sh"
    And no `tmux list-sessions` command is invoked on the fake runner

  @unit
  Scenario: list_worktrees and list_sessions share a single ssh round-trip where possible
    Given a fake SSH runner that counts `orchard --json` invocations
    When list_worktrees() and list_sessions() are both called for the same adapter within one refresh
    Then the fake runner records at most 1 `orchard --json` invocation (snapshot is reused)

  # =======================================================================
  # AC4 — Remote enrichment is preserved; local build_state does NOT re-derive
  # =======================================================================

  @unit
  Scenario: Remote JsonOutput carries PR enrichment that is preserved locally
    Given a remote JsonOutput with a worktree on branch "issue329/federated" whose `pr.number == 335` and `pr.state == "open"`
    When the snapshot is merged into the local OrchardState
    Then the resulting WorktreeState.pr.number equals 335
    And its pr.state equals "open"
    And local join logic did NOT look up PRs cache files to derive this field

  @unit
  Scenario: Remote JsonOutput carries issue enrichment that is preserved locally
    Given a remote JsonOutput with a worktree whose `issue.number == 329` and `issue.state == "open"`
    When the snapshot is merged into the local OrchardState
    Then the resulting WorktreeState.issue.number equals 329
    And its issue.state equals "open"
    And local join logic did NOT look up issues cache files to derive this field

  @unit
  Scenario: Remote JsonOutput carries claude and check-state enrichment
    Given a remote JsonOutput worktree with claude session state "working" and CI checks "passing"
    When the snapshot is merged locally
    Then the resulting WorktreeState preserves claude state "working"
    And the CI check state "passing" is preserved
    And display_group from the remote snapshot is preserved, not recomputed

  @unit
  Scenario: build_state skips join/enrichment for remote-sourced worktrees
    Given an OrchardState under construction that has both local sources (needing joining) and a remote JsonOutput (already joined)
    When build_state processes the remote snapshot
    Then no call into the PR-join / issue-join / claude-enrichment functions is made for those entries
    And only local worktrees are passed through join.rs

  # =======================================================================
  # AC5 — Standalone remote sessions appear in `OrchardState::standalone_sessions`
  # =======================================================================

  @unit
  Scenario: Remote standalone sessions are merged into OrchardState.standalone_sessions with host set
    Given a remote JsonOutput with `standalone_sessions` containing a session "shepherd" not tied to any worktree
    When the snapshot is merged into the local OrchardState
    Then OrchardState.standalone_sessions contains one StandaloneSessionRow for "shepherd"
    And its host field equals the remote host
    And it is not duplicated as a worktree session

  @integration
  Scenario: Local and remote standalone sessions coexist in a single merged state
    Given a local OrchardState with one standalone session "global"
    And a remote JsonOutput with one standalone session "shepherd"
    When the merge runs
    Then OrchardState.standalone_sessions contains exactly 2 entries
    And "global" has host == None
    And "shepherd" has host == Some(remote_host)

  # =======================================================================
  # AC6 — Proxy failures surface clearly; last-known snapshot stays visible.
  # No silent fallback to legacy shell-discovery. If users want legacy for a
  # specific host, they reconfigure that remote as `"type": "remmy"`.
  # =======================================================================

  @unit
  Scenario: Non-zero exit from remote orchard surfaces as a proxy_failure event
    Given an OrchardProxyAdapter with a fake SSH runner that returns exit-code 127 (command not found) for `orchard --json`
    When list_worktrees() is called
    Then the adapter returns AdapterError::FetchFailure — it does NOT attempt any legacy git/tmux shell command
    And the fake SSH runner records only the `orchard --json` attempt; no `git worktree list --porcelain` invocation appears
    And a `remote_adapter.proxy_failure` event is appended to `events.jsonl` with the host and reason "remote orchard missing (exit 127)"

  @unit
  Scenario: Malformed JSON from remote orchard surfaces as a proxy_failure event
    Given the fake SSH runner returns stdout `{ "repos": [malformed...` (truncated JSON) with exit 0
    When list_worktrees() is called
    Then the adapter returns AdapterError::ParseFailure — it does NOT attempt any legacy code path
    And a `remote_adapter.proxy_failure` event is written with reason "parse failure"
    And the event includes the host and a bounded snippet of the payload (or its length) — not the full unbounded payload
    And the snippet is truncated on a UTF-8 scalar boundary (never a byte boundary — emoji-safe)

  @unit
  Scenario: Unknown `version` surfaces as a proxy_failure event
    Given the fake SSH runner returns a JsonOutput with `"version": 0`
    And the local code expects a specific supported version range
    When list_worktrees() is called
    Then the adapter returns AdapterError::ParseFailure
    And a `remote_adapter.proxy_failure` event is written with reason "version skew" and the unexpected version value
    And no legacy path is attempted

  @unit
  Scenario: SSH connection failure surfaces as a proxy_failure event
    Given the fake SSH runner returns a network error / non-zero exit 255 for `orchard --json`
    When list_worktrees() is called
    Then the adapter returns AdapterError::FetchFailure
    And a `remote_adapter.proxy_failure` event is written with reason "fetch failure (exit 255)"
    And no legacy shell-discovery is attempted

  @integration
  Scenario: Last-known snapshot stays visible after a proxy failure
    Given a prior successful `{host}_orchard_snapshot.json` exists on disk for host "vm.boxd.sh" with 2 worktrees
    And the next OrchardProxyAdapter call for that host fails (exit 127)
    When the pipeline builds `OrchardState` via `build_state_with_cached_snapshots`
    Then the 2 worktrees from the stale snapshot remain visible in the merged state
    And the `remote_adapter.proxy_failure` event is still present in `events.jsonl`
    And the cache file is NOT deleted (next successful refresh will overwrite it)

  # =======================================================================
  # AC7 — Dashboard never blocks on network. Reads are cache-only; all I/O
  # runs in background services (`orchard watch`, `orchard refresh`).
  # =======================================================================

  @unit
  Scenario: `orchard --json` reads from cache only; never initiates SSH
    Given a configured OrchardProxy remote at "vm.boxd.sh" that is unreachable
    And no cached snapshot exists on disk for that host
    When the user runs `orchard --json`
    Then the command completes in under 100ms wall-clock
    And no SSH process is spawned during its execution
    And no probe is issued during its execution
    And the output includes local state without waiting on the remote

  @unit
  Scenario: TUI render does not block on probe or SSH
    Given a configured OrchardProxy remote that is unreachable
    When the TUI initial frame is constructed
    Then construction completes in under 100ms
    And no blocking probe or SSH call runs on the render path
    And any refresh happens in a background task that writes to the cache

  @unit
  Scenario: `orchard refresh` is the explicit fresh-data entry point
    Given the subcommand `orchard refresh` exists
    When the user runs `orchard refresh`
    Then the process probes configured hosts, fetches from OrchardProxy remotes, writes snapshots + caches, and exits
    And a subsequent `orchard --json` reads the freshly-written cache

  @unit
  Scenario: Probes run inside `orchard refresh` / `orchard watch`, bounded by PROBE_TIMEOUT
    Given `orchard refresh` is invoked against an unreachable OrchardProxy host
    Then the probe is bounded by `PROBE_TIMEOUT` (3s)
    And the probe command is orchard-specific (`orchard --version`), not the generic `true`

  @e2e
  Scenario: `orchard --json` against an unreachable host returns in under 100ms when cache is present
    Given 3 OrchardProxy remotes configured against unreachable hosts
    And cached snapshots exist for all three
    When the user runs `orchard --json`
    Then the command completes in under 100ms wall-clock
    And the output contains the 3 remotes' cached worktrees with their stored enrichment

  # =======================================================================
  # AC8 — Per-host cache snapshot with version-aware invalidation
  # =======================================================================

  @unit
  Scenario: Successful refresh writes `{host}_orchard_snapshot.json`
    Given an OrchardProxyAdapter for host "vm.boxd.sh"
    And a fake SSH runner returning a valid JsonOutput (version = current supported)
    When the refresh completes successfully
    Then a file "~/.cache/orchard/vm.boxd.sh_orchard_snapshot.json" is written
    And it contains the raw remote JsonOutput (not a locally-rederived shape)
    And it includes the `version` field

  @unit
  Scenario: TUI cold start reads `{host}_orchard_snapshot.json` for instant render
    Given a fresh `vm.boxd.sh_orchard_snapshot.json` exists on disk
    And no SSH calls have been made yet
    When the TUI starts and builds initial OrchardState
    Then the remote worktrees from the snapshot are present in the initial render
    And no SSH invocation has been attempted (first-phase render is offline)

  @unit
  Scenario: Snapshot with mismatched `version` is invalidated on read
    Given `vm.boxd.sh_orchard_snapshot.json` exists with `"version": 99` (unrecognized)
    When the local orchard reads the snapshot at cold start
    Then the snapshot is treated as absent (not merged into initial state)
    And a diagnostic is logged identifying the version mismatch
    And the next refresh will attempt a fresh fetch and overwrite the file

  @integration
  Scenario: Snapshot refresh overwrites the prior file atomically
    Given a prior `vm.boxd.sh_orchard_snapshot.json` exists with one worktree
    And a fresh SSH fetch returns two worktrees
    When the refresh writes the new snapshot
    Then the file ends up containing exactly the two new worktrees
    And a partial-write failure does not leave the file in a half-written state (write-and-rename or equivalent)

  # =======================================================================
  # AC9 — End-to-end validation on a Boxd VM (required)
  # =======================================================================

  @e2e
  Scenario: Fresh Boxd VM with orchard installed surfaces remote enrichment locally
    Given a Boxd VM forked from golden and named "orchard-federated-<timestamp>"
    And the built orchard binary is installed on the VM
    And a remote worktree "issue329-smoke" on branch "issue329/smoke" exists on the VM
    And a tmux session "or_issue329_smoke" runs at that worktree path
    And the VM is configured in the local `.orchard.json` as `"type": "orchard-proxy"`
    When the user runs `orchard refresh` followed by `orchard --json` locally
    Then the output includes a worktree with host == VM host and branch "issue329/smoke"
    And the worktree's `issue` and/or `pr` fields are populated (enrichment computed on the VM, preserved locally)
    And a tmux session on that worktree is listed

  @e2e
  Scenario: When remote orchard is removed, last-known snapshot stays visible and proxy_failure event is written
    Given the VM from the previous scenario
    And `~/.local/bin/orchard` has been removed on the VM (remote orchard unavailable)
    When the user runs `orchard refresh` followed by `orchard --json` locally
    Then the worktree for branch "issue329/smoke" is STILL visible in the output — sourced from the cached `{host}_orchard_snapshot.json` written during the happy-path scenario
    And the local `events.jsonl` contains a `remote_adapter.proxy_failure` event with reason "remote orchard missing (exit 127)"
    And NO legacy shell-discovery (`git worktree list --porcelain`) was attempted against the VM
    And the VM is destroyed at end-of-test (`boxd destroy <vm> -y`)

  # =======================================================================
  # AC10 — Union of local + remote is correct; schemars schema is unchanged
  # =======================================================================

  @integration
  Scenario: `orchard --json` unions local and remote repos/worktrees/sessions
    Given a local OrchardState with 1 repo containing 2 worktrees
    And a remote JsonOutput with 1 repo (same slug) containing 3 worktrees on different branches
    When `orchard --json` runs
    Then the output has 1 repo entry for that slug
    And the repo's worktrees array has exactly 5 entries
    And the 3 remote entries all have non-null `host`
    And the 2 local entries have null `host`

  @unit
  Scenario: `(host, path)` deduplication keeps remote over local on conflict
    Given a local CachedWorktree and a remote JsonOutput entry with the same `(host, path)` tuple
    When the merge runs
    Then exactly 1 WorktreeState is emitted for that tuple
    And the emitted entry is the remote (already-joined) one — preference order: proxy > legacy
    And no duplicate row appears in the final OrchardState

  @unit
  Scenario: Standalone sessions are unioned without duplication
    Given a local standalone session "shepherd" and a remote standalone session "shepherd" on different hosts
    When the merge runs
    Then both appear in standalone_sessions, distinguished by host
    And no dedup collapses them into one row

  @unit
  Scenario: schemars-generated JSON schema is byte-identical after adding Deserialize derives
    Given a pre-federation schema snapshot of `JsonOutput`
    When the schema is regenerated after adding `Deserialize` derives to ingestion types
    Then the two schemas are byte-identical
    And no new fields were introduced by the federation change (host is reused)
    And if the schema diverges, implementers split ingestion types into `json_input_types.rs` as documented in the plan

  # =======================================================================
  # AC11 — Mutation paths are unchanged (create/kill/transfer)
  # =======================================================================

  @integration
  Scenario: Creating a worktree on a remote still goes through the legacy adapter path
    Given an OrchardProxy remote with fallback_kind "remmy"
    When the user invokes "new worktree on remote"
    Then the underlying mutation uses the Remmy / BoxdShared / BoxdFork code path, NOT `ssh host orchard ...`
    And no new RPC or remote-orchard mutation call is introduced

  @integration
  Scenario: Killing a remote tmux session still goes through the legacy adapter path
    Given an OrchardProxy remote with a session discovered from the remote snapshot
    When the user kills the session
    Then the kill command uses the legacy per-kind adapter path (ssh host tmux kill-session ...)
    And no `ssh host orchard ...` mutation is attempted

  @integration
  Scenario: Worktree transfer to/from a remote is unaffected by federation
    Given an OrchardProxy remote
    When a transfer is performed between local and that remote
    Then the transfer code path is byte-identical to pre-federation behavior
    And no federation-specific branch in `transfer.rs` is exercised

  # =======================================================================
  # AC12 — Docs: architecture.md updated + ADR added
  # =======================================================================

  @unit
  Scenario: docs/architecture.md documents federated discovery
    Then `docs/architecture.md` contains a section describing the federated discovery model
    And the section explains that remote `JsonOutput` is the wire protocol
    And it notes that `build_state` trusts remote-sourced enrichment and does not re-derive it

  @unit
  Scenario: An ADR is added under docs/adr/ recording the federation decision
    Then a new ADR file exists under `docs/adr/` documenting the decision
    And the ADR records: remote orchard is authority, `ssh host orchard --json` is the protocol, fallback is required for un-upgraded remotes
    And the ADR is cross-referenced from `docs/architecture.md`

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # AC1 "RemoteKind::OrchardProxy exists and is selectable via `type`: `orchard-proxy`"
  #   -> "RemoteConfig accepts 'orchard-proxy' as a valid type"
  #   -> "Adapter dispatch returns an OrchardProxyAdapter for OrchardProxy kind"
  #   -> "OrchardProxy appears in the supported-types error message"
  # AC2 "list_worktrees() returns worktrees from `ssh host orchard --json`, not raw git"
  #   -> "OrchardProxyAdapter.list_worktrees parses `ssh host orchard --json` output"
  #   -> "OrchardProxyAdapter does NOT invoke raw `git worktree list --porcelain`"
  # AC3 "list_sessions() sourced from the same snapshot"
  #   -> "OrchardProxyAdapter.list_sessions parses sessions from the same snapshot"
  #   -> "list_worktrees and list_sessions share a single ssh round-trip where possible"
  # AC4 "Remote-sourced worktrees carry remote-computed issue/PR/check/claude enrichment; local build_state does not re-derive"
  #   -> "Remote JsonOutput carries PR enrichment that is preserved locally"
  #   -> "Remote JsonOutput carries issue enrichment that is preserved locally"
  #   -> "Remote JsonOutput carries claude and check-state enrichment"
  #   -> "build_state skips join/enrichment for remote-sourced worktrees"
  # AC5 "Standalone tmux sessions from the remote appear in OrchardState::standalone_sessions with host set"
  #   -> "Remote standalone sessions are merged into OrchardState.standalone_sessions with host set"
  #   -> "Local and remote standalone sessions coexist in a single merged state"
  # AC6 "Proxy failures surface clearly; last-known snapshot stays visible; NO silent fallback"
  #   -> "Non-zero exit from remote orchard surfaces as a proxy_failure event"
  #   -> "Malformed JSON from remote orchard surfaces as a proxy_failure event"
  #   -> "Unknown `version` surfaces as a proxy_failure event"
  #   -> "SSH connection failure surfaces as a proxy_failure event"
  #   -> "Last-known snapshot stays visible after a proxy failure"
  #   -> Structural invariants (no FallbackAdapter, no fallback_kind) enforced by
  #      crates/orchard/tests/ac6_no_fallback.rs; see docs/adr/008-federated-discovery.md
  #      "Structural invariants" section for the prose description.
  # AC7 "Dashboard never blocks on network; reads are cache-only; I/O runs in background services"
  #   -> "`orchard --json` reads from cache only; never initiates SSH"
  #   -> "TUI render does not block on probe or SSH"
  #   -> "`orchard refresh` is the explicit fresh-data entry point"
  #   -> "Probes run inside `orchard refresh` / `orchard watch`, bounded by PROBE_TIMEOUT"
  #   -> "`orchard --json` against an unreachable host returns in under 100ms when cache is present"
  # AC8 "Per-host `{host}_orchard_snapshot.json` cache; invalidated on version drift"
  #   -> "Successful refresh writes `{host}_orchard_snapshot.json`"
  #   -> "TUI cold start reads `{host}_orchard_snapshot.json` for instant render"
  #   -> "Snapshot with mismatched `version` is invalidated on read"
  #   -> "Snapshot refresh overwrites the prior file atomically"
  # AC9 "End-to-end on Boxd VM: happy path preserves enrichment; after remote orchard is removed, last-known snapshot stays visible + proxy_failure event written"
  #   -> "Fresh Boxd VM with orchard installed surfaces remote enrichment locally"
  #   -> "When remote orchard is removed, last-known snapshot stays visible and proxy_failure event is written"
  # AC10 "`orchard --json` unions local+remote correctly; schemars schema unchanged (new host data uses existing fields)"
  #   -> "`orchard --json` unions local and remote repos/worktrees/sessions"
  #   -> "`(host, path)` deduplication keeps remote over local on conflict"
  #   -> "Standalone sessions are unioned without duplication"
  #   -> "schemars-generated JSON schema is byte-identical after adding Deserialize derives"
  # AC11 "Existing mutations (new remote worktree, kill remote session, transfer) still work unchanged"
  #   -> "Creating a worktree on a remote still goes through the legacy adapter path"
  #   -> "Killing a remote tmux session still goes through the legacy adapter path"
  #   -> "Worktree transfer to/from a remote is unaffected by federation"
  # AC12 "docs/architecture.md updated + ADR added under docs/adr/"
  #   -> "docs/architecture.md documents federated discovery"
  #   -> "An ADR is added under docs/adr/ recording the federation decision"
