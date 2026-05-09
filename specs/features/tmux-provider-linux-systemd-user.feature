Feature: tmux provider works under systemd-user on Linux (#464)
  As an orchard user running the daemon as `systemd --user` on Linux
  I want `Query.tmuxSessions` to return the live tmux sessions visible to my user
  And I want the daemon to stop fork-storming `tmux` six times per second
  So that the dashboard reflects reality and the SIGKILL-by-oomd failure mode disappears

  # Scope: daemon-side fixes in `internal/server/providers/tmux/` plus the
  # shipped systemd-user unit at `scripts/init/orchard.service`.
  # Out of scope: Rust TUI in `crates/orchard/`, macOS launchd unit,
  # the federated `peerproxy` provider, and per-call `context.WithTimeout`
  # on `tmux` execs (tracked separately).
  #
  # Why "implementation detail" callouts in scenarios: the issue's Investigation
  # block ranks three concrete root-cause hypotheses (cgroup OOM, fsnotify
  # amplification, PrivateTmp pinning). Each AC names the *structural primitive*
  # that defeats one or more amplifiers; scenarios assert the primitive, not
  # the upstream OS behaviour we cannot reproduce in unit tests.

  Background:
    Given the tmux provider lives at `internal/server/providers/tmux/`
    And the daemon polls the provider on a ticker via `pollLoop` -> `refresh(ctx)` -> `Adapter.FetchAll(ctx)`
    And the shipped systemd-user unit lives at `scripts/init/orchard.service`

  # =======================================================================
  # AC1 — Coalesce the per-tick exec storm: at most 2 `tmux` execs per cycle
  # =======================================================================

  @unit @issue-464
  Scenario: FetchAll issues at most 2 `tmux` execs per poll cycle on a steady-state server
    Given a counting `CommandRunner` test seam wired into the adapter
    And `IsAlive` has been resolved at least once in the current poll interval (cache hit)
    When `Adapter.FetchAll(ctx)` runs one full cycle against a healthy tmux server
    Then the counting runner records at most 2 `tmux` invocations for that cycle
    And one of those invocations is a single `list-panes -a -F` call carrying session+window+pane fields
    And no separate `tmux list-sessions`, `tmux list-windows`, `tmux info`, or `tmux display-message` call is issued in the same cycle

  @unit @issue-464
  Scenario: IsAlive is cached for the poll interval and skipped on the next tick
    Given a counting `CommandRunner` and a poll interval of 1s
    And `IsAlive` resolved successfully in the previous cycle
    When `Adapter.FetchAll(ctx)` runs again before the cache TTL expires
    Then no `tmux` `info` (or equivalent liveness) probe is issued in the second cycle
    And the cached liveness result is reused

  @unit @issue-464
  Scenario: IsAlive cache expires and is re-probed once per interval
    Given the IsAlive cache TTL has elapsed
    When the next `FetchAll` cycle runs
    Then exactly one liveness probe is issued for that cycle
    And subsequent cycles within the new TTL again skip the probe

  @integration @issue-464
  Scenario: serverInfo is derived from the same single source as session/window/pane data
    Given a sandbox tmux server with N sessions, M windows, P panes
    When `Adapter.FetchAll(ctx)` returns its snapshot
    Then `Snapshot.ServerInfo` is populated from the coalesced source (no extra `display-message` call)
    And the session/window/pane counts in the snapshot match the live server (no rows lost vs. the legacy multi-call shape)

  @integration @issue-464
  Scenario: Coalesced parser handles a session with windows but zero panes (race during creation)
    Given a tmux server where one session has a window currently in a zero-panes transient state
    When the coalesced `list-panes -a` output is parsed into a snapshot
    Then either the session/window appears with an empty panes list,
      OR the parser falls back to the legacy multi-call path for that cycle
    And the snapshot does not silently drop the session

  # =======================================================================
  # AC2 — fsnotify socket-event filter only fires on the configured socket basename
  # =======================================================================

  @unit @issue-464
  Scenario: relevantSocketEvent matches the default socket basename
    Given the watcher is configured for the default tmux socket (basename "default")
    When an fsnotify event arrives for path ".../tmux-1000/default"
    Then `relevantSocketEvent` returns true

  @unit @issue-464
  Scenario: relevantSocketEvent ignores other non-hidden files in the socket directory
    Given the watcher is configured for socket basename "default"
    When an fsnotify event arrives for path ".../tmux-1000/some-other-file"
    Then `relevantSocketEvent` returns false
    And no `PokeRefresh()` is triggered for that event

  @unit @issue-464
  Scenario: relevantSocketEvent honours a custom `-S` socket path
    Given the watcher is configured with a custom socket path "/tmp/custom/my-sock"
    When an fsnotify event arrives for path "/tmp/custom/my-sock"
    Then `relevantSocketEvent` returns true
    When an fsnotify event arrives for path "/tmp/custom/default"
    Then `relevantSocketEvent` returns false

  @unit @issue-464
  Scenario: relevantSocketEvent continues to ignore hidden files (existing behaviour preserved)
    Given the watcher is configured for socket basename "default"
    When an fsnotify event arrives for path ".../tmux-1000/.hidden"
    Then `relevantSocketEvent` returns false

  # =======================================================================
  # AC3 — Shipped systemd-user unit: drop hardening + set TMUX_TMPDIR
  # =======================================================================

  @structural @issue-464
  Scenario: Shipped unit no longer sets PrivateTmp=yes
    Given the file `scripts/init/orchard.service`
    When the unit is parsed
    Then there is no active `PrivateTmp=yes` directive
    And any `PrivateTmp=` directive present is set to `no` (or absent entirely)

  @structural @issue-464
  Scenario: Shipped unit no longer sets ProtectHome=read-only
    Given the file `scripts/init/orchard.service`
    When the unit is parsed
    Then there is no active `ProtectHome=read-only` directive
    And any `ProtectHome=` directive present does not restrict reads to the user's tmux socket directory

  @structural @issue-464
  Scenario: Shipped unit sets TMUX_TMPDIR so the daemon can reach the user's socket regardless of namespace
    Given the file `scripts/init/orchard.service`
    When the unit is parsed
    Then it contains an `Environment=TMUX_TMPDIR=...` directive
    And the value is one that survives container/namespace edge cases (e.g. `%t/orchard-tmux` resolving to `XDG_RUNTIME_DIR/orchard-tmux`, or an equivalent that the implementer documents)
    # Open question from /plan: TMUX_TMPDIR may need to point at the user's existing
    # socket dir rather than a daemon-private one. Implementer must close this before shipping.

  @structural @issue-464
  Scenario: README/docs call out the install rename + clean-restart requirement for users on the old unit
    Given a user upgrading from a previously installed unit that shipped `PrivateTmp=yes`
    When they read the README / install docs / unit's leading comment
    Then they are told to perform a clean restart (not `daemon-reload` alone) so the daemon's mount namespace re-binds
    And they are told the legacy hardening directives are intentionally removed

  # =======================================================================
  # AC4 — Diagnostic: log signal name and WaitStatus on signal-terminated execs
  # =======================================================================

  @unit @issue-464
  Scenario: execRunner.Run logs the signal name when the child terminates by signal
    Given an `execRunner` configured to run a fake command that exits via `SIGKILL`
    When `Run` returns
    Then the structured log entry includes a field whose value names the signal (e.g. `SIGKILL`)
    And the log entry is emitted on a path distinct from the generic `exit error` branch

  @unit @issue-464
  Scenario: execRunner.Run logs the signal name for SIGTERM as well
    Given an `execRunner` configured to run a fake command that exits via `SIGTERM`
    When `Run` returns
    Then the structured log entry names the signal as `SIGTERM`

  @unit @issue-464
  Scenario: execRunner.Run preserves existing behaviour for non-zero exit (no signal)
    Given an `execRunner` configured to run a command that exits with code 1 and writes to stderr
    When `Run` returns
    Then the structured log entry uses the existing `exit error` path
    And does not claim a signal terminated the process
    And stderr content is preserved in the log

  @unit @issue-464
  Scenario: execRunner.Run preserves existing behaviour for clean exit (code 0)
    Given an `execRunner` configured to run a command that exits cleanly
    When `Run` returns
    Then no signal/wait-status diagnostic is emitted
    And the existing success path is unchanged

  # =======================================================================
  # AC5 — Linux-only regression test: no SIGKILL across N ticks at fast poll
  # =======================================================================

  @integration @linux-only @issue-464
  Scenario: FetchAll survives N fast ticks against a sandbox tmux without SIGKILL
    Given a Linux build (test gated by `//go:build linux`)
    And a sandbox tmux server in a temp socket directory
    And the provider configured with a 50ms poll interval
    When the test drives 20 consecutive `FetchAll` cycles
    Then no `tmux` exec returns `signal: killed` or any other signal-terminated status
    And every cycle returns a non-empty snapshot consistent with the sandbox server

  @integration @linux-only @issue-464
  Scenario: Regression test fails closed if exec count per tick regresses
    Given the same fast-poll Linux harness
    And a counting `CommandRunner` wrapping the real exec
    When 20 `FetchAll` cycles complete
    Then the recorded `tmux` exec count is consistent with AC1 (≤ 2 per cycle)
    And a regression that re-introduces the 6-exec storm causes the test to fail

  @integration @linux-only @issue-464
  Scenario: Regression test skips with a clear message when cgroup setup is unavailable
    Given a Linux test runner that cannot construct a constrained-cgroup sandbox
    When the test starts
    Then it `t.Skip`s with a message naming the missing capability
    And does not produce a false negative
    # AC5 wording explicitly accepts that this guards the structural cause
    # (fork count per tick), not a faithful systemd-oomd repro.

  # =======================================================================
  # AC6 — Field verification on the boxd machine
  # =======================================================================

  @e2e @manual @issue-464
  Scenario: Vanilla shipped unit on Ubuntu 24.04 boxd VM returns live tmux sessions
    Given a freshly installed daemon built from the merged PR
    And the shipped systemd-user unit is enabled with no user-side overrides
    And the user has 3 attached tmux sessions visible to `tmux ls`
    When the implementer queries `{ tmuxSessions { id name windows { id } } }` against `127.0.0.1:7777/graphql` on the boxd machine
    Then the response lists exactly those 3 sessions with their window children
    And the daemon log for the prior poll cycle does not contain `signal: killed` or `signal=SIGKILL` for the tmux provider
    And both the daemon log excerpt and the GraphQL response are attached to the PR as evidence

  # =======================================================================
  # Out-of-scope guards (matches issue "Out of scope" block)
  # =======================================================================

  @structural @issue-464
  Scenario: No per-call context.WithTimeout is added in this PR
    Given the provider exec path
    When the diff is inspected
    Then no new `context.WithTimeout` wrapper is introduced around `tmux` execs
    # Tracked separately — investigation called this nice-to-have but a
    # different concern from SIGKILL diagnosis.

  @structural @issue-464
  Scenario: No changes are made to the Rust TUI in crates/orchard/
    Given the diff scope
    When file paths are listed
    Then no file under `crates/orchard/` is modified
    And the bug fix is daemon-side only

  @structural @issue-464
  Scenario: No changes are made to the macOS launchd unit
    Given the file `scripts/init/com.gitorchard.orchard.plist`
    When the diff is inspected
    Then it is unchanged

  @structural @issue-464
  Scenario: No changes are made to the federation peerproxy provider
    Given `Host.processes` already works correctly per the original repro
    When the diff is inspected
    Then `internal/server/providers/peerproxy/` is unchanged
    And `internal/server/resolvers/federate_peer_processes.go` is unchanged

  @structural @issue-464
  Scenario: No new rate-limit / backpressure is added inside PokeRefresh beyond the existing 1-buffer non-blocking send
    Given the watcher's `PokeRefresh()` path
    When the diff is inspected
    Then no additional rate-limit or backpressure mechanism is introduced
    And the structural fix relies on AC1 (exec coalesce) plus AC2 (filter tighten) instead

  # --- AC Coverage Map ---
  # AC1: "≤2 tmux execs per tick via list-panes -a coalesce + IsAlive cache + single-source serverInfo"
  #   -> Scenario: FetchAll issues at most 2 `tmux` execs per poll cycle on a steady-state server
  #   -> Scenario: IsAlive is cached for the poll interval and skipped on the next tick
  #   -> Scenario: IsAlive cache expires and is re-probed once per interval
  #   -> Scenario: serverInfo is derived from the same single source as session/window/pane data
  #   -> Scenario: Coalesced parser handles a session with windows but zero panes (race during creation)
  #
  # AC2: "fsnotify filter only fires on configured socket basename"
  #   -> Scenario: relevantSocketEvent matches the default socket basename
  #   -> Scenario: relevantSocketEvent ignores other non-hidden files in the socket directory
  #   -> Scenario: relevantSocketEvent honours a custom `-S` socket path
  #   -> Scenario: relevantSocketEvent continues to ignore hidden files (existing behaviour preserved)
  #
  # AC3: "shipped unit drops PrivateTmp/ProtectHome and sets TMUX_TMPDIR; docs updated"
  #   -> Scenario: Shipped unit no longer sets PrivateTmp=yes
  #   -> Scenario: Shipped unit no longer sets ProtectHome=read-only
  #   -> Scenario: Shipped unit sets TMUX_TMPDIR so the daemon can reach the user's socket regardless of namespace
  #   -> Scenario: README/docs call out the install rename + clean-restart requirement for users on the old unit
  #
  # AC4: "execRunner.Run logs signal name + WaitStatus on signal exits, distinct from exit-error path"
  #   -> Scenario: execRunner.Run logs the signal name when the child terminates by signal
  #   -> Scenario: execRunner.Run logs the signal name for SIGTERM as well
  #   -> Scenario: execRunner.Run preserves existing behaviour for non-zero exit (no signal)
  #   -> Scenario: execRunner.Run preserves existing behaviour for clean exit (code 0)
  #
  # AC5: "Linux-only integration test: FetchAll across N ticks at fast poll, asserts no SIGKILL; fails on fork-storm regression"
  #   -> Scenario: FetchAll survives N fast ticks against a sandbox tmux without SIGKILL
  #   -> Scenario: Regression test fails closed if exec count per tick regresses
  #   -> Scenario: Regression test skips with a clear message when cgroup setup is unavailable
  #
  # AC6: "Vanilla shipped unit on Ubuntu 24.04 boxd VM returns live tmux sessions; daemon log + GraphQL attached to PR"
  #   -> Scenario: Vanilla shipped unit on Ubuntu 24.04 boxd VM returns live tmux sessions
  #
  # Out-of-scope guards (issue "Out of scope" block):
  #   -> Scenario: No per-call context.WithTimeout is added in this PR
  #   -> Scenario: No changes are made to the Rust TUI in crates/orchard/
  #   -> Scenario: No changes are made to the macOS launchd unit
  #   -> Scenario: No changes are made to the federation peerproxy provider
  #   -> Scenario: No new rate-limit / backpressure is added inside PokeRefresh beyond the existing 1-buffer non-blocking send
  #
  # AC count: 6. Mapped scenarios: 6/6. No drops, no gaps.
