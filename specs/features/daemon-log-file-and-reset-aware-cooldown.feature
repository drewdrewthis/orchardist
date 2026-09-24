Feature: Daemon-owned log file and reset-aware GraphQL rate-limit cooldown

  As an operator running the orchard daemon under launchd, systemd, or manually
  I want the daemon to always write a log file under its state dir, and its
  GraphQL rate-limit backoff to honour GitHub's own reset time
  So that I can diagnose a daemon regardless of how it was launched, and the
  daemon recovers from a 403 rate limit as soon as GitHub says it may, instead
  of waiting a fixed 5 minutes or (worse) never backing off at all

  # Issue #768 — two independent slices left over from #749/#763:
  #   AC1-AC5:  daemon.log opened under the state dir, alongside stderr,
  #             append-mode, level-consistent, and non-fatal to open.
  #   AC6-AC11: EnrichPullRequest/BatchEnrichPullRequests arm the rate-limit
  #             cooldown from the response's X-RateLimit-Reset epoch instead
  #             of (or in addition to, on failure) a fixed 5-minute window.
  #
  # Test-tree note: AC1-AC5 scenarios are bound by
  # internal/cli/daemon/daemonlog_test.go; AC6-AC11 scenarios are bound by
  # internal/server/providers/gh/graphql_cooldown_test.go.

  Background:
    Given the daemon resolves its state dir via orchpaths.StateDir(), honouring XDG_STATE_HOME
    And "<StateDir>/daemon.log" denotes that resolved directory joined with "daemon.log"
    And the gh provider's EnrichPullRequest and BatchEnrichPullRequests already carry a rate-limit cooldown gated by a fixed 5-minute constant
    And GitHub's GraphQL 403 response already carries X-RateLimit-Reset as a typed *ErrRateLimitedT{ResetAt} out of graphql.go

  # =======================================================================
  # AC1 — daemon.log opened regardless of launcher
  # =======================================================================

  @integration @issue-768
  Scenario: The daemon writes its slog output to daemon.log under the state dir
    Given a state dir resolved from XDG_STATE_HOME that does not yet exist on disk
    When the daemon starts, independent of launcher (launchd, systemd, or manual "orchard daemon daemon start")
    Then "<StateDir>/daemon.log" exists and is non-empty
    And "<StateDir>/daemon.log" contains the daemon's bind/listening log line

  # =======================================================================
  # AC2 — stderr sink preserved (regression)
  # =======================================================================

  @integration @issue-768 @regression
  Scenario: Daemon output reaches both stderr and daemon.log, not one in place of the other
    Given the daemon's stderr is captured to a file
    When the daemon starts and logs its bind/listening line
    Then the captured stderr file contains the bind/listening line
    And "<StateDir>/daemon.log" also contains the bind/listening line

  # =======================================================================
  # AC3 — append-mode; restart preserves prior contents
  # =======================================================================

  @integration @issue-768
  Scenario: A daemon restart against the same state dir appends rather than truncates
    Given a state dir that already has one prior run's daemon.log with one bind/listening line in it
    When the daemon starts again against the same state dir and logs its own bind/listening line
    Then "<StateDir>/daemon.log" contains the bind/listening line twice
    And none of the prior run's content was removed

  # =======================================================================
  # AC4 — log level honoured identically by the file sink (consistency/regression)
  # =======================================================================

  @unit @issue-768 @regression
  Scenario Outline: The file sink filters by level exactly like stderr, via one shared handler
    Given the daemon logger is built at level "<level>" over both stderr and daemon.log
    When a Debug-level record and an Info-level record are both logged
    Then "<StateDir>/daemon.log" contains the Debug-level record: <debug_present>
    And "<StateDir>/daemon.log" always contains the Info-level record

    Examples:
      | level | debug_present |
      | debug | yes           |
      | info  | no            |

  # =======================================================================
  # AC5 — unwritable state dir/log file never fails startup (failure mode)
  # =======================================================================

  @unit @issue-768
  Scenario: An unwritable state dir degrades to stderr-only logging without failing startup
    Given a state dir path whose parent component is a regular file, so mkdir/open fails deterministically
    When the daemon builds its log sink against that state dir
    Then the daemon still starts and serves, logging to stderr only
    And exactly one Warn line is emitted on stderr naming the intended "<StateDir>/daemon.log" path
    And the daemon does not exit non-zero for this reason

  # =======================================================================
  # AC6 — header-403 in EnrichPullRequest arms the cooldown
  # =======================================================================

  @integration @issue-768
  Scenario: A 403 with X-RateLimit-Remaining:0 on EnrichPullRequest arms the cooldown and suppresses the next call
    Given an httptest GraphQL server that returns 403 with "X-RateLimit-Remaining: 0" and a future "X-RateLimit-Reset" epoch
    When EnrichPullRequest is called for a PR key
    Then a Warn line "gh: rate-limit cooldown engaged" is emitted
    And a second EnrichPullRequest call for the same key, issued before the reset epoch, does not increment the server's GraphQL hit-counter
    But on the current (unfixed) code, no cooldown line is emitted on the header-403 path and the second call DOES increment the hit-counter

  # =======================================================================
  # AC7 — cooldown backs off until the reset epoch, both enrich paths
  # =======================================================================

  @integration @issue-768
  Scenario Outline: The cooldown arms until the GitHub-reported reset epoch, not a fixed 5 minutes
    Given an injected clock fixed at "now"
    And an httptest GraphQL server that returns 403 with "X-RateLimit-Remaining: 0" and "X-RateLimit-Reset" set to now+42min
    When "<enrich_path>" is called and receives the 403
    Then the "gh: rate-limit cooldown engaged" Warn line's "until" attribute equals the RFC3339 formatting of now+42min

    Examples:
      | enrich_path             |
      | EnrichPullRequest       |
      | BatchEnrichPullRequests |

  # =======================================================================
  # AC8 — missing/unparseable reset header falls back to 5 minutes (failure mode)
  # =======================================================================

  @integration @issue-768
  Scenario: A header-403 with no X-RateLimit-Reset header falls back to a 5-minute cooldown
    Given an injected clock fixed at "now"
    And an httptest GraphQL server that returns 403 with "X-RateLimit-Remaining: 0" and no "X-RateLimit-Reset" header
    When EnrichPullRequest is called and receives the 403
    Then the "gh: rate-limit cooldown engaged" Warn line's "until" attribute equals the RFC3339 formatting of now+5min

  # =======================================================================
  # AC9 — reset epoch already in the past floors to 5 minutes (edge)
  # =======================================================================

  @integration @issue-768
  Scenario: A reset epoch in the past floors the cooldown to 5 minutes instead of arming a zero/negative window
    Given an injected clock fixed at "now"
    And an httptest GraphQL server that returns 403 with "X-RateLimit-Remaining: 0" and "X-RateLimit-Reset" set to a past epoch
    When EnrichPullRequest is called and receives the 403
    Then the "gh: rate-limit cooldown engaged" Warn line's "until" attribute equals the RFC3339 formatting of now+5min
    And a second EnrichPullRequest call for the same key, issued within that 5-minute window, does not increment the server's GraphQL hit-counter

  # =======================================================================
  # AC10 — cooldown expires promptly at the reset epoch; refetch resumes
  # =======================================================================

  @integration @issue-768
  Scenario: Once the clock passes the armed reset epoch, the next enrich call issues a fresh GraphQL request
    Given the cooldown was armed via a 403 with "X-RateLimit-Reset" set to now+42min
    When the injected clock is advanced past that reset epoch and EnrichPullRequest is called again for the same key
    Then the call increments the server's GraphQL hit-counter, i.e. a real GraphQL request is issued

  # =======================================================================
  # AC11 — GraphQL-body "rate limit" string path unchanged (regression)
  # =======================================================================

  @integration @issue-768 @regression
  Scenario: A 200 response with a rate-limit message in errors[] still arms the cooldown using the 5-minute fallback
    Given an injected clock fixed at "now"
    And an httptest GraphQL server that returns 200 with body {"errors":[{"message":"API rate limit exceeded"}]} and no data, and no rate-limit headers
    When EnrichPullRequest is called and observes the GraphQL-body error
    Then a "gh: rate-limit cooldown engaged" Warn line is emitted
    And its "until" attribute equals the RFC3339 formatting of now+5min

  # --- AC Coverage Map ---
  # AC1: "daemon.log opened regardless of launcher"
  #   -> @integration "The daemon writes its slog output to daemon.log under the state dir"
  #
  # AC2: "stderr sink preserved (regression)"
  #   -> @integration @regression "Daemon output reaches both stderr and daemon.log, not one in place of the other"
  #
  # AC3: "daemon.log is append-mode; a restart preserves prior contents"
  #   -> @integration "A daemon restart against the same state dir appends rather than truncates"
  #
  # AC4: "log level honoured by the file sink (consistency/regression)"
  #   -> @unit @regression "The file sink filters by level exactly like stderr, via one shared handler"
  #
  # AC5: "state dir / log file unwritable -> daemon still starts (failure mode)"
  #   -> @unit "An unwritable state dir degrades to stderr-only logging without failing startup"
  #
  # AC6: "header-403 in EnrichPullRequest arms the cooldown"
  #   -> @integration "A 403 with X-RateLimit-Remaining:0 on EnrichPullRequest arms the cooldown and suppresses the next call"
  #
  # AC7: "cooldown backs off until the reset epoch, not a fixed 5 minutes (both paths)"
  #   -> @integration "The cooldown arms until the GitHub-reported reset epoch, not a fixed 5 minutes" (Examples: EnrichPullRequest, BatchEnrichPullRequests)
  #
  # AC8: "missing / unparseable reset header -> 5-minute fallback (failure mode)"
  #   -> @integration "A header-403 with no X-RateLimit-Reset header falls back to a 5-minute cooldown"
  #
  # AC9: "reset epoch already in the past -> 5-minute floor (edge)"
  #   -> @integration "A reset epoch in the past floors the cooldown to 5 minutes instead of arming a zero/negative window"
  #
  # AC10: "cooldown expires promptly at the reset epoch and refetch resumes (feature payoff)"
  #   -> @integration "Once the clock passes the armed reset epoch, the next enrich call issues a fresh GraphQL request"
  #
  # AC11: "GraphQL-body \"rate limit\" string path unchanged (regression)"
  #   -> @integration @regression "A 200 response with a rate-limit message in errors[] still arms the cooldown using the 5-minute fallback"
