Feature: Federation peers hot-reload — add/remove peers without restarting the daemon
  As an orchardist running multiple boxd VMs that come and go
  I want the orchard daemon to pick up `peers[]` edits in `~/.orchard/config.json` live
  So that adding a new peer (e.g. `lw-fed-c`) makes it appear in `Host.peers` and stream
  attention events into `attend.sh` without a daemon restart — and removing one stops
  probes the same way

  # Scope: hot-reload of the explicit `peers[]` array. The original "auto-discover via
  # `boxd list`" framing was flipped during investigation — `boxd list` cannot tell us
  # which VMs are federation peers (the daemon is not publicly exposed by default).
  # Each peer VM still requires an explicit operator step
  # (`boxd proxy new graphql --vm=<name> --port=7777`) before being added to `peers[]`.
  # The auto-discovery idea is filed as a separate follow-up issue.
  #
  # Code surface:
  #   - Config schema:       internal/server/providers/config/types.go (PeerRow / FederationConfig)
  #   - Loader:              internal/server/providers/peerproxy/config.go (LoadFederationConfig)
  #   - Provider:            internal/server/providers/peerproxy/provider.go (currently no Add/Remove)
  #   - Daemon entry point:  internal/cli/daemon/daemon.go (where fsnotify watcher goes)
  #   - CLI writer:          internal/cli/config/add_peer.go (writes via tmp + os.Rename)

  Background:
    Given the orchard daemon is running with `peers[]` loaded from `~/.orchard/config.json`
    And `peerproxy.Provider` owns a `name -> {adapter, client, cancelFunc}` map guarded by a mutex
    And each running peer has its own `context.Context` so it can be cancelled independently
    And the daemon entry point owns an fsnotify watcher on `~/.orchard/config.json`
    And the debounce window is ≤2 seconds end-to-end (fsnotify event → reload → first probe)

  # =======================================================================
  # AC1 — Daemon watches the config file and applies peer add/remove live
  # =======================================================================

  @integration
  Scenario: Editing `peers[]` while the daemon runs triggers reload within the debounce window
    Given the daemon has been running for at least 10 seconds with one peer "orchard.boxd.sh"
    And the fsnotify watcher is wired to `~/.orchard/config.json`
    When `orchard config add-peer --name lw-fed-c --address graphql.lw-fed-c.boxd.sh --tls` writes the config
    Then within 2 seconds the daemon has re-parsed the config via `LoadFederationConfig`
    And `Provider.ApplyPeers(...)` has been invoked exactly once for this edit
    And no daemon restart was required

  @integration
  Scenario: Bursty editor saves are coalesced into one reload
    Given the watcher's debounce is ~1 second
    When five fsnotify events fire on `~/.orchard/config.json` within 200ms (typical editor save burst)
    Then `LoadFederationConfig` is invoked at most once for that burst
    And `Provider.ApplyPeers(...)` is invoked at most once for that burst

  @unit
  Scenario: Parse error on reload keeps the last good peer set live
    Given the daemon has `peers: [orchard.boxd.sh]` loaded and probing
    When `~/.orchard/config.json` is rewritten with malformed JSON
    Then the reload returns a parse error
    And `Provider.ApplyPeers(...)` is NOT invoked with an empty peer set
    And `orchard.boxd.sh` continues to probe uninterrupted
    And the error is logged with the config path and line/column

  @integration
  Scenario: macOS atomic-rename writes trigger the watcher
    Given the platform is macOS
    And the watcher uses the documented fsnotify workaround (watch parent directory, filter by filename)
    When `orchard config add-peer` writes the config via `tmp + os.Rename`
    Then the watcher observes the rename event for `~/.orchard/config.json`
    And `Provider.ApplyPeers(...)` is invoked within the debounce window
    # NOTE: macOS fsnotify on a single file path historically misses atomic-rename.
    # This scenario protects the workaround from regressing.

  @unit
  Scenario: Watcher falls back gracefully if `~/.orchard/config.json` is missing at startup
    Given `~/.orchard/config.json` does not exist when the daemon starts
    When the daemon initialises the fsnotify watcher
    Then the watcher attaches to the parent directory and waits for create events
    And no peers are loaded
    And subsequent creation of the file fires `Provider.ApplyPeers(...)`

  # =======================================================================
  # AC2 — `Provider.AddPeer(PeerRow)` and `Provider.RemovePeer(name)` API
  # =======================================================================

  @unit
  Scenario: AddPeer inserts a new peer and starts its probe goroutine
    Given `peerproxy.Provider` has no peer named "lw-fed-c"
    When `Provider.AddPeer(PeerRow{Name: "lw-fed-c", Address: "graphql.lw-fed-c.boxd.sh", TLS: true})` is invoked
    Then a new entry appears in the provider's peer map under key "lw-fed-c"
    And a `runPeer` goroutine is spawned with a fresh per-peer context
    And the goroutine begins issuing `Client.Ping()` calls on the 30s probe cadence
    And the call returns nil error

  @unit
  Scenario: RemovePeer cancels the peer's goroutine and drops it from the map
    Given the provider currently has a peer "lw-fed-c" with an active probe goroutine
    When `Provider.RemovePeer("lw-fed-c")` is invoked
    Then the per-peer context is cancelled
    And the `runPeer` goroutine returns within a short window (≤1s)
    And the entry is removed from the provider's peer map
    And no further `Client.Ping()` calls are emitted for "lw-fed-c"
    And the call returns nil error
    # Verified by `goleak` — no leaked goroutines after removal.

  @unit
  Scenario: AddPeer on an existing name is rejected with a clear error
    Given the provider already has a peer "lw-fed-c"
    When `Provider.AddPeer(PeerRow{Name: "lw-fed-c", ...})` is invoked again
    Then the call returns an error identifying the duplicate name
    And the existing peer's goroutine is undisturbed
    # Mutation through Add/Remove only — replace = remove + add. See ApplyPeers below.

  @unit
  Scenario: RemovePeer on an unknown name returns an error without side effects
    Given the provider has no peer named "ghost"
    When `Provider.RemovePeer("ghost")` is invoked
    Then the call returns an error identifying the missing name
    And no goroutines are cancelled

  @unit
  Scenario: AddPeer / RemovePeer are safe under concurrent access
    Given the provider has 3 peers with active probe goroutines
    When 50 concurrent goroutines each issue a mix of AddPeer and RemovePeer calls for disjoint names
    Then the peer map remains consistent (no torn reads, no double-spawn, no orphan goroutines)
    And `goleak` reports no leaked goroutines after all calls complete

  @unit
  Scenario: `ApplyPeers(cfg FederationConfig)` diffs current vs new and emits the right calls
    Given the provider currently runs peers `["orchard.boxd.sh", "lw-fed-d"]`
    And the new config snapshot has peers `["orchard.boxd.sh", "lw-fed-c"]`
    When `Provider.ApplyPeers(newCfg)` is invoked
    Then `AddPeer("lw-fed-c", ...)` is called exactly once
    And `RemovePeer("lw-fed-d")` is called exactly once
    And `orchard.boxd.sh` is untouched — its goroutine is NOT restarted

  @unit
  Scenario: Address change is treated as remove + add (not in-place mutation)
    Given peer "lw-fed-c" currently has address `graphql.lw-fed-c.boxd.sh`
    When `ApplyPeers` sees the same name with a new address `graphql.lw-fed-c-v2.boxd.sh`
    Then `RemovePeer("lw-fed-c")` is called first
    And `AddPeer("lw-fed-c", new address)` is called next
    And the old goroutine's context was cancelled before the new one was started

  @unit
  Scenario: TLS flag change is treated as remove + add
    Given peer "lw-fed-c" currently has `tls: false`
    When `ApplyPeers` sees the same name with `tls: true`
    Then the peer is remove+add-ed (TLS affects the client's transport, not safely mutable in flight)

  # =======================================================================
  # AC3 — End-to-end: new peer appears in `Host.peers`, removed peer disappears
  # =======================================================================

  @e2e
  Scenario: Adding a peer entry surfaces it in the live `Host.peers` GraphQL query within 2s
    Given the daemon is running with peers `["orchard.boxd.sh"]`
    And a GraphQL client polls `{ host(name: "lw-fed-c") { peers { name reachable } } }`
    When `orchard config add-peer --name lw-fed-c --address graphql.lw-fed-c.boxd.sh --tls` writes the config
    Then within 2 seconds the next poll returns a `Host` entry whose `name == "lw-fed-c"`
    And at least one `Client.Ping()` to `lw-fed-c` has been attempted by then

  @e2e
  Scenario: Removing a peer entry stops probes within the debounce window
    Given the daemon has peers `["orchard.boxd.sh", "lw-fed-c"]` and both are being probed
    And a fake transport records every probe call by peer name
    When the config is rewritten to remove "lw-fed-c"
    Then within 2 seconds no further probes are recorded for "lw-fed-c"
    And `Host.peers` no longer exposes "lw-fed-c" on the next GraphQL query
    And `orchard.boxd.sh` continues to be probed without interruption

  @integration
  Scenario: Reachability outcome of a freshly-added peer is reported truthfully
    Given a freshly-added peer "lw-fed-c" whose VM has NOT had `boxd proxy new graphql ...` run yet
    When the daemon's first probe round runs against it
    Then `Host(name: "lw-fed-c").reachable` is `false`
    And the failure reason surfaces through the existing event/log channel
    # This is the documented operator UX, not a bug — see AC5 docs.

  # =======================================================================
  # AC4 — No regression for the existing `orchard.boxd.sh` peer
  # =======================================================================

  @e2e
  Scenario: `orchard.boxd.sh` keeps probing through repeated config reloads
    Given the daemon starts with `peers: [orchard.boxd.sh]`
    When the config is edited 5 times in succession to add and then remove unrelated peers
    Then `orchard.boxd.sh` is never removed from the peer map by `ApplyPeers`
    And its probe goroutine is the same goroutine (same context) throughout
    And `Host(name: "orchard.boxd.sh").reachable` continues to reflect live probe results

  @integration
  Scenario: Removing and re-adding `orchard.boxd.sh` round-trips cleanly
    Given the daemon has `orchard.boxd.sh` as its only peer
    When the operator edits the config to remove it and then re-adds it within the same minute
    Then a `RemovePeer("orchard.boxd.sh")` happens first
    And then an `AddPeer("orchard.boxd.sh", ...)` happens
    And after the second reload completes, `Host(name: "orchard.boxd.sh").reachable` is `true`
    # Smoke test for the "does the existing peer survive the round-trip" question in /plan.

  # =======================================================================
  # AC5 — Operator documentation states the prerequisite checklist
  # =======================================================================

  @unit
  Scenario: Operator-facing docs name the prerequisite for every peer VM
    Given the operator reads the orchard documentation for `peers[]`
    Then the docs state that each candidate peer VM must:
      | step                                                                            |
      | run `boxd proxy new graphql --vm=<name> --port=7777` to expose the daemon       |
      | have `orchard-daemon` running and listening on `127.0.0.1:7777` inside the VM   |
    And the docs state that adding a peer without these prerequisites is expected to surface as `Host.peers[].reachable == false`
    # The exact doc file is up to /plan, but the content must land. Check via grep
    # for the two `boxd proxy new graphql` and `127.0.0.1:7777` strings in docs/ or
    # the help text of `orchard config add-peer`.

  @unit
  Scenario: `orchard config add-peer --help` mentions the prerequisite
    When the operator runs `orchard config add-peer --help`
    Then the help text references the `boxd proxy new graphql --vm=<name> --port=7777` prerequisite
    And references the `orchard-daemon` inside-the-VM running requirement

  # =======================================================================
  # AC6 — `attend.sh` emits attention events for Claude sessions on a new peer
  # =======================================================================

  @e2e
  Scenario: `attend.sh` surfaces a Claude session on a peer added at runtime
    Given `attend.sh` is running against the local daemon
    And a peer VM "lw-fed-c" hosts a tmux session running a Claude REPL with a `pending input` state
    And "lw-fed-c" is reachable through the peerproxy transport
    When `orchard config add-peer --name lw-fed-c ...` writes the config
    And the daemon hot-reloads within 2 seconds
    Then within one further probe cycle, `attend.sh` emits an attention event whose `peer == "lw-fed-c"`
    And the event payload includes the Claude session id and the pending-input state
    # End-to-end glue test: hot-reload (#566) plumbs through to the operator's primary
    # observability surface. If this fails, the feature isn't actually delivered.

  @integration
  Scenario: Removing a peer stops new attention events from that peer
    Given `attend.sh` is running and "lw-fed-c" had been emitting Claude events
    When "lw-fed-c" is removed from `peers[]` via config edit
    Then within 2 seconds no further `peer == "lw-fed-c"` events are emitted by `attend.sh`
    # Outstanding events that were mid-flight when remove happens may still flush —
    # the assertion is "no NEW events after the debounce window."

  # =======================================================================
  # AC7 — `boxd list` auto-discovery deferred to a follow-up issue
  # =======================================================================

  @unit
  Scenario: A follow-up GitHub issue exists for `boxd list`-driven auto-discovery
    Given issue #566 is closed
    When a reader inspects the closing message or linked issues on #566
    Then there exists a linked GitHub issue on `drewdrewthis/orchardist` whose title or body references "auto-discover" and `boxd list`
    And that issue's body acknowledges the prerequisite gap (daemon not publicly exposed by default)
    And that issue is the canonical home for the deferred work
    # This AC is a workflow gate — verified by the implementer producing the issue
    # URL in the close message of #566. Not enforced by code tests.

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # AC1 "Daemon watches `~/.orchard/config.json` and applies peer additions/removals without restart (≤2s debounce)"
  #   -> "Editing `peers[]` while the daemon runs triggers reload within the debounce window"
  #   -> "Bursty editor saves are coalesced into one reload"
  #   -> "Parse error on reload keeps the last good peer set live"
  #   -> "macOS atomic-rename writes trigger the watcher"
  #   -> "Watcher falls back gracefully if `~/.orchard/config.json` is missing at startup"
  #
  # AC2 "`peerproxy.Provider` exposes `AddPeer(PeerRow) error` and `RemovePeer(name string) error`; fsnotify handler invokes them on diff"
  #   -> "AddPeer inserts a new peer and starts its probe goroutine"
  #   -> "RemovePeer cancels the peer's goroutine and drops it from the map"
  #   -> "AddPeer on an existing name is rejected with a clear error"
  #   -> "RemovePeer on an unknown name returns an error without side effects"
  #   -> "AddPeer / RemovePeer are safe under concurrent access"
  #   -> "`ApplyPeers(cfg FederationConfig)` diffs current vs new and emits the right calls"
  #   -> "Address change is treated as remove + add (not in-place mutation)"
  #   -> "TLS flag change is treated as remove + add"
  #
  # AC3 "Adding a new peer entry while the daemon is running causes that peer to appear in `Host.peers` and be probed within the debounce window. Removing an entry stops probes within the same window."
  #   -> "Adding a peer entry surfaces it in the live `Host.peers` GraphQL query within 2s"
  #   -> "Removing a peer entry stops probes within the debounce window"
  #   -> "Reachability outcome of a freshly-added peer is reported truthfully"
  #
  # AC4 "No regression: `orchard.boxd.sh` (the existing peer) continues to be reachable across reloads"
  #   -> "`orchard.boxd.sh` keeps probing through repeated config reloads"
  #   -> "Removing and re-adding `orchard.boxd.sh` round-trips cleanly"
  #
  # AC5 "Operator documentation states the prerequisite: each peer VM must (a) run `boxd proxy new graphql --vm=<name> --port=7777`, and (b) have `orchard-daemon` running"
  #   -> "Operator-facing docs name the prerequisite for every peer VM"
  #   -> "`orchard config add-peer --help` mentions the prerequisite"
  #
  # AC6 "`attend.sh` running against the local daemon emits attention events for Claude sessions on any peer once it's added"
  #   -> "`attend.sh` surfaces a Claude session on a peer added at runtime"
  #   -> "Removing a peer stops new attention events from that peer"
  #
  # AC7 "The original auto-discovery via `boxd list` idea is filed as a separate follow-up issue"
  #   -> "A follow-up GitHub issue exists for `boxd list`-driven auto-discovery"
