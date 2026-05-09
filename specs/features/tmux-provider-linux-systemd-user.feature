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
  # AC1 — Coalesce the per-tick exec storm: at most 3 `tmux` execs per cycle
  # (Amended 2026-05-09 from "≤2" → "≤3" after /review found that dropping
  # `list-clients` silently broke five GraphQL fields. See issue body AC1.)
  # =======================================================================

  @unit @issue-464
  Scenario: FetchAll issues at most 3 `tmux` execs per poll cycle on a steady-state server
    Given a counting `CommandRunner` test seam wired into the adapter
    And `IsAlive` is permitted to fire once per cycle (cache hit means 0 execs)
    When `Adapter.FetchAll(ctx)` runs one full cycle against a healthy tmux server
    Then the counting runner records at most 3 `tmux` invocations for that cycle:
      `tmux info` (cached liveness probe), `tmux list-panes -a -F <combined>` (sessions+windows+panes),
      and `tmux list-clients -F <client-format>` (preserves the client subgraph: tmuxServer.clients,
      tmuxSession.activeAttached, tmuxPane.attachedClients, tmuxWindow.watchingClients,
      subscribeTmuxClientChanged)
    And no separate `tmux list-sessions`, `tmux list-windows`, or `tmux display-message` call is issued in the same cycle

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
  Scenario: ServerInfo is populated without a separate display-message exec
    Given a sandbox tmux server with N sessions, M windows, P panes
    When `Adapter.FetchAll(ctx)` returns its snapshot
    Then `Snapshot.Server` carries `SocketPath` and `Alive=true` directly
    And `Snapshot.Server.Pid` is 0 (resolved lazily on demand by resolvers, not eagerly)
    And the session/window/pane counts in the snapshot match the live server (no rows lost vs. the legacy multi-call shape)

  @integration @issue-464
  Scenario: Coalesced parser handles a session with windows but zero panes (race during creation)
    Given a tmux server where one session has a window currently in a zero-panes transient state
    When the coalesced `list-panes -a` output is parsed into a snapshot
    Then the session/window with zero panes does not appear in the snapshot for THAT cycle
      (no fallback to the legacy multi-call path is provided — see adapter.go listAll)
    And the next poll cycle (≤1s later) picks up the session once a pane exists
    # Documented limitation: tmux always creates a default pane on session
    # creation, so the worst-case staleness window is one poll tick.

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
  # AC3 — Shipped systemd-user unit: drop hardening; TMUX_TMPDIR handled
  # explicitly (intentionally NOT set + documented per /plan resolution)
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
  Scenario: Shipped unit handles TMUX_TMPDIR explicitly so the daemon can reach the user's socket regardless of namespace
    Given the file `scripts/init/orchard.service`
    When the unit is parsed
    Then either:
      - the unit contains an `Environment=TMUX_TMPDIR=...` directive whose value points at the user's tmux socket dir (matching the user's interactive `$TMPDIR`), or
      - the unit intentionally does NOT set TMUX_TMPDIR and the leading comment block documents (1) that TMUX_TMPDIR is intentionally absent and (2) why (so the daemon inherits the user's $TMPDIR and connects to the same socket the user's interactive sessions use)
    # Open question from /plan resolved 2026-05-09: setting TMUX_TMPDIR=%t/orchard-tmux
    # would isolate the daemon from the user's tmux socket (the opposite of the fix).
    # The implementer chose intentionally-NOT-set with documented rationale.

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
  Scenario: execRunner.Run names the signal in its returned error when the child terminates by signal
    Given an `execRunner` running a fake command that exits via `SIGKILL`
    When `Run` returns
    Then the returned error string contains the canonical signal name (e.g. `signal: SIGKILL`)
    And the signal-naming branch is distinct from the generic `exit error` branch
    And the diagnostic is the returned error itself, not a separately-emitted log entry
      (callers — `pollLoop`, resolvers — surface this via `logger.Warn(... "err", err)`)

  @unit @issue-464
  Scenario: execRunner.Run names the signal as SIGTERM when the child receives SIGTERM
    Given an `execRunner` running a fake command that exits via `SIGTERM`
    When `Run` returns
    Then the returned error string contains `signal: SIGTERM`

  @unit @issue-464
  Scenario: execRunner.Run preserves existing behaviour for non-zero exit (no signal)
    Given an `execRunner` running a command that exits with code 1 and writes to stderr
    When `Run` returns
    Then the returned error wraps the original `exec.ExitError` (so callers can `errors.Is` / `errors.As` it)
    And the error string contains `exit status 1`
    And does not claim a signal terminated the process
    And the stderr content is preserved in the error string

  @unit @issue-464
  Scenario: execRunner.Run preserves existing behaviour for clean exit (code 0)
    Given an `execRunner` running a command that exits cleanly
    When `Run` returns
    Then it returns `(stdout, nil)` — no signal/wait-status diagnostic is added
    And the existing success path is unchanged

  @unit @issue-464
  Scenario: execRunner.Run distinguishes ctx-cancel kills from external SIGKILL
    Given an `execRunner` whose context is cancelled while the child is running
    When `Run` returns
    Then the returned error wraps `ctx.Err()` (e.g. `context.Canceled`, `context.DeadlineExceeded`)
    And the returned error string does NOT contain `signal: SIGKILL`
      (otherwise ctx-cancel kills would be conflated with the oomd kills the diagnostic exists to flag)
    # CodeRabbit follow-up on PR #507: exec.CommandContext invokes Process.Kill
    # (SIGKILL) on context cancellation; ctx.Err() check disambiguates.

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
    Then the recorded `tmux` exec count is consistent with AC1 (≤ 3 per cycle)
    And a regression that re-introduces the 6-exec storm causes the test to fail
    # Note: the ≤3-execs guarantee is also asserted cross-platform by
    # TestRegression_FetchAllExecCount_Issue464 in exec_count_test.go using
    # a fake CommandRunner — this Linux test exercises the same invariant
    # against a real tmux server.

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
  # AC1 (amended ≤2→≤3): "≤3 tmux execs per tick — info + list-panes -a + list-clients;
  #                       IsAlive cached; single-source ServerInfo without display-message"
  #   -> Scenario: FetchAll issues at most 3 `tmux` execs per poll cycle on a steady-state server
  #   -> Scenario: IsAlive is cached for the poll interval and skipped on the next tick
  #   -> Scenario: IsAlive cache expires and is re-probed once per interval
  #   -> Scenario: ServerInfo is populated without a separate display-message exec
  #   -> Scenario: Coalesced parser handles a session with windows but zero panes (race during creation)
  #
  # AC2: "fsnotify filter only fires on configured socket basename + relevant Op bits"
  #   -> Scenario: relevantSocketEvent matches the default socket basename
  #   -> Scenario: relevantSocketEvent ignores other non-hidden files in the socket directory
  #   -> Scenario: relevantSocketEvent honours a custom `-S` socket path
  #   -> Scenario: relevantSocketEvent continues to ignore hidden files (existing behaviour preserved)
  #   (Plus Go-level coverage of Chmod/Rename masking — see watcher_test.go::TestRelevantSocketEvent_IgnoresChmodAndRename)
  #
  # AC3 (amended): "shipped unit drops PrivateTmp/ProtectHome; TMUX_TMPDIR handled explicitly
  #                 (intentionally NOT set with documented rationale); upgrade docs updated"
  #   -> Scenario: Shipped unit no longer sets PrivateTmp=yes
  #   -> Scenario: Shipped unit no longer sets ProtectHome=read-only
  #   -> Scenario: Shipped unit handles TMUX_TMPDIR explicitly so the daemon can reach the user's socket regardless of namespace
  #   -> Scenario: README/docs call out the install rename + clean-restart requirement for users on the old unit
  #
  # AC4: "execRunner.Run names signal in returned error on signal exits, distinct from exit-error path,
  #       and ctx-cancel kills surface ctx.Err() not signal: SIGKILL"
  #   -> Scenario: execRunner.Run names the signal in its returned error when the child terminates by signal
  #   -> Scenario: execRunner.Run names the signal as SIGTERM when the child receives SIGTERM
  #   -> Scenario: execRunner.Run preserves existing behaviour for non-zero exit (no signal)
  #   -> Scenario: execRunner.Run preserves existing behaviour for clean exit (code 0)
  #   -> Scenario: execRunner.Run distinguishes ctx-cancel kills from external SIGKILL
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
