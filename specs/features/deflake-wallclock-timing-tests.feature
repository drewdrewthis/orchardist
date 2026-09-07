Feature: De-flake the remaining wall-clock timing tests in internal/server (#818)
  As an orchard maintainer
  I want the flaky wall-clock waits in the daemon's Go tests replaced with real synchronization
  So that the suite passes deterministically under -race contention instead of racing timers

  # Every flagged site is a wall-clock wait standing in for a missing synchronization
  # primitive. The fix removes the timing dependency at each site rather than widening the
  # window. Three seam shapes cover the surface — all additive, test-only where possible,
  # production defaults unchanged:
  #   * a peerproxy provider probe-completion hook (fires after every probe attempt, incl. failures)
  #   * per-provider subscriber-registration hooks/counters (tmux, ps, claudeprojects)
  #   * the existing FakeClock/reload-hook debounce seam, adopted at two watcher call sites
  #   * a test-only loader options override for deterministic batch-capacity dispatch
  # The two client keepalive tests are deliberate wall-clock read-deadline simulations and
  # are OUT OF SCOPE. See the issue's Plan §"Key decisions" for the rationale.

  Background:
    Given `internal/server` contains the daemon's Go test suite
    And the flaky sites are wall-clock waits standing in for missing synchronization primitives
    And every new seam is additive and leaves production defaults unchanged

  # =======================================================================
  # AC1 — Every named site synchronizes on a real signal, not a wall-clock wait
  # =======================================================================

  @integration
  Scenario: Named test sites wait on an explicit signal instead of a wall-clock wait
    Given each test site named in the issue Problem except `client_test.go:207,248`
    When the site needs to wait for a precondition
    Then it synchronizes on an explicit signal — a channel close, a hook callback, or a `WithFakeClockForTest` advance
    And it no longer sleeps for a fixed duration to absorb that precondition
    And the unified diff of each touched file shows the sleep→signal swap

  @unit
  Scenario: Debounce-path tests drive the existing FakeClock/reload-hook seam
    Given the debounce tests at `watcher_test.go:197` and `watcher_test.go:431`
    When each test is rewritten
    Then it constructs the watcher with `WithFakeClockForTest` and `WithReloadHookForTest`
    And it drives the debounce timer via the fake clock instead of `WithDebounce` plus a real `time.Sleep`
    And no new production seam is added for these two sites

  # =======================================================================
  # AC2 — No sleeps, retry-around-assert loops, or skips remain in touched files
  # =======================================================================

  @unit
  Scenario: No time.Sleep, retry-around-assert loop, or t.Skip remains in the touched test files
    Given the set of touched test files under `internal/server`
    When the change lands
    Then no `time.Sleep` remains except the `client_test.go` keepalive sleeps
    And no `t.Skip` remains except the pre-existing platform skip at `watcher_test.go:267`
    And no retry-around-assert loop remains
    And none of these are newly introduced
    And `git diff -U0 origin/main -- internal/server | grep '^+' | grep -cE 'time\.Sleep|t\.Skip'` prints `0`

  # =======================================================================
  # AC3 — Previously-flaky tests pass 50/50 under -race contention
  # =======================================================================

  @integration
  Scenario: The previously-flaky tests pass 50/50 under -race run concurrently
    Given two shells running concurrently to create contention
    When one runs `go test -race -count=50 -run 'TestNodeChanged_DispatchesByPrefix|TestHostVersion_PeerVersionFerried' ./internal/server/resolvers/`
    And the other runs `go test -race -count=50 ./internal/server/providers/peerproxy/ ./internal/server/loaders/`
    Then both commands print an `ok` tail with 0 failures
    And a single `go test -race -count=1 ./internal/server/...` whole-package run also passes green

  # =======================================================================
  # AC4 — Each de-flaked test still catches its regression
  # =======================================================================

  @integration
  Scenario: A de-flaked test still fails when its guarded behavior is broken
    Given a one-line local mutation that forces the `loaders_test.go` batch to split
    When the batch tests run against the mutation
    Then the affected batch test turns red
    And when the probe/subscriber hook call is removed instead
    Then the corresponding hook test fails or times out
    And the failing run is captured before the mutation is reverted

  # =======================================================================
  # AC5 — Production defaults are unchanged
  # =======================================================================

  @unit
  Scenario: Production construction paths are unchanged when test options are not passed
    Given `NewLoaders`, `NewProvider`, and `NewConfigWatcher` called without the new test options
    When the daemon runs in production
    Then the behavior of each constructor is unchanged from origin/main
    And the diff shows the new options are additive with nil/zero defaults
    And the whole-package pass from AC3 confirms no production behavior drift

  # --- AC Coverage Map ---
  # AC1: "Each test site named in the Problem (except client_test.go:207,248) synchronizes on an
  #       explicit signal (channel close, hook callback, or WithFakeClockForTest advance) instead
  #       of a wall-clock wait; debounce-path tests (watcher_test.go:197,431) drive the existing
  #       FakeClock/reload-hook seam."
  #   -> @integration "Named test sites wait on an explicit signal instead of a wall-clock wait"
  #   -> @unit "Debounce-path tests drive the existing FakeClock/reload-hook seam"
  #
  # AC2: "No time.Sleep, no retry-around-assert loop, and no t.Skip remain in the touched test
  #       files (except the pre-existing platform t.Skip at watcher_test.go:267 and the
  #       client_test.go keepalive sleeps), and none are newly introduced."
  #   -> @unit "No time.Sleep, retry-around-assert loop, or t.Skip remains in the touched test files"
  #
  # AC3: "The previously-flaky tests pass 50/50 under -race, run concurrently in separate shells
  #       (contention), plus one whole-package -race pass."
  #   -> @integration "The previously-flaky tests pass 50/50 under -race run concurrently"
  #
  # AC4: "Each de-flaked test still FAILS when its guarded behavior is broken (loaders batch split
  #       mutation; removing the probe/subscriber hook call)."
  #   -> @integration "A de-flaked test still fails when its guarded behavior is broken"
  #
  # AC5: "Production defaults unchanged: no change in behavior of NewLoaders, NewProvider,
  #       NewConfigWatcher when the test options are not passed."
  #   -> @unit "Production construction paths are unchanged when test options are not passed"
  #
  # Total ACs in issue plan: 5. All 5 mapped above.
