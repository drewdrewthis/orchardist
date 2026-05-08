Feature: Boxd as first-class backend — hexagonal RemoteWorktreeService with universal SWR
  As an orchardist using Boxd fork-per-issue VMs
  I want Boxd VMs to appear in orchard's data pipeline without hand-edited config
  And I want every outbound call (SSH exec, `gh api`, `ssh boxd.sh list`) to return cached instantly and revalidate in the background
  So that fresh forks show up within one refresh, `orchard --json` stays fast with warm caches, and the adapter port is not hollowed out by blocking probes

  # Related issues folded in: #264 (discovery by remote type), #248 (remote PR/issue enrichment),
  # #246 (hard SSH timeout — remaining scope).
  # Scope rule: SWR is the framework for ALL outbound calls, not Boxd-specific.

  Background:
    Given a global config at "~/.orchard/config.json"
    And the per-repo `remotes[]` array contains zero or more remote entries
    And each remote entry has fields: name, host, path, type
    And the cache directory is "~/.cache/orchard/"

  # =======================================================================
  # AC1 — Hexagonal RemoteWorktreeService port
  # =======================================================================

  @unit
  Scenario: RemoteWorktreeService port defines a minimal stable surface
    Then a port named "RemoteWorktreeService" is defined (implementation choice: trait OR `enum RemoteAdapter` with match, per CLAUDE.md polymorphism guidance)
    And it exposes the methods:
      | method           | purpose                                                |
      | list_worktrees   | return Vec<CachedWorktree> for this remote             |
      | list_sessions    | return Vec<CachedTmuxSession> for this remote          |
      | probe            | return reachability + optional metadata for this remote|
    And each method returns a typed Result so the core can handle adapter errors uniformly
    And if a trait object is used, the justification is recorded (e.g., required for test doubles) — otherwise prefer an enum + match to match the codebase's "traits only for genuinely polymorphic behavior" rule

  @unit
  Scenario: SSH exec is injectable so adapters are unit-testable without the network
    Then each adapter (Remmy, BoxdShared, BoxdFork) takes its SSH/command runner through a seam (function pointer, closure, or trait)
    And unit tests construct adapters with a fake runner that returns canned stdout/stderr/exit-code
    And no adapter `@unit` scenario in this feature is executed against a real `ssh` subprocess
    And the seam lives in the adapter module, not bolted onto each test

  @unit
  Scenario: Adapter dispatch selects implementation from RemoteConfig.type
    Given a RemoteConfig with type "remmy"
    When the core constructs a RemoteWorktreeService for it
    Then a RemmyAdapter is returned
    Given a RemoteConfig with type "boxd-shared"
    When the core constructs a RemoteWorktreeService for it
    Then a BoxdSharedAdapter is returned
    Given a RemoteConfig with type "boxd-fork"
    When the core constructs a RemoteWorktreeService for it
    Then a BoxdForkAdapter is returned

  @unit
  Scenario: Unknown remote type fails fast with actionable error
    Given a RemoteConfig with type "gcp-vm"
    When the core tries to construct an adapter
    Then an error is returned identifying the unknown type
    And the error names the supported types: remmy, boxd-shared, boxd-fork

  # =======================================================================
  # AC2 — Three adapters implement the port
  # =======================================================================

  @unit
  Scenario: RemmyAdapter wraps current universal-git-worktree-list-over-SSH behavior
    Given a remote of type "remmy" with host "ubuntu@10.0.3.56" and path "~/langwatch-workspace"
    And the adapter is constructed with a fake SSH exec runner (per the injection scenario above)
    And the fake runner, when invoked with `ssh ubuntu@10.0.3.56 'git -C ~/langwatch-workspace worktree list --porcelain'`, returns
      """
      worktree /home/ubuntu/langwatch-workspace
      bare

      worktree /home/ubuntu/langwatch-workspace/worktrees/feat-x
      HEAD abc123
      branch refs/heads/feat-x
      """
    When RemmyAdapter.list_worktrees() is called
    Then it returns exactly 1 non-bare CachedWorktree with branch "feat-x" and host "ubuntu@10.0.3.56"
    And each entry has layout "bare"

  @unit
  Scenario: BoxdSharedAdapter preserves current single-VM-with-worktrees behavior
    Given a remote of type "boxd-shared" with host "boxd@orchard-rs.boxd.sh" and path "~/git-orchard-rs"
    And the adapter is constructed with a fake SSH exec runner
    And the fake runner, when invoked with `ssh boxd@orchard-rs.boxd.sh 'git -C ~/git-orchard-rs worktree list --porcelain'`, returns
      """
      worktree /home/boxd/git-orchard-rs
      bare

      worktree /home/boxd/git-orchard-rs/worktrees/issue240
      HEAD def456
      branch refs/heads/issue240/smart-sorting
      """
    When BoxdSharedAdapter.list_worktrees() is called
    Then exactly 1 non-bare CachedWorktree is returned
    And it carries host "boxd@orchard-rs.boxd.sh"
    And its layout is "bare"
    And its branch is "issue240/smart-sorting"

  @unit
  Scenario: BoxdForkAdapter returns a flat-clone worktree for each live fork
    Given BoxdForkAdapter is configured with golden host "boxd.sh"
    And the adapter is constructed with a fake SSH exec runner
    And the fake runner, when invoked with `ssh boxd.sh list --json`, returns
      """
      [
        {"name": "issue3155", "host": "issue3155.boxd.sh", "status": "running"},
        {"name": "issue3152", "host": "issue3152.boxd.sh", "status": "running"}
      ]
      """
    And the fake runner, when invoked with `ssh boxd@issue3155.boxd.sh 'cd ~/langwatch && git rev-parse --abbrev-ref HEAD'`, returns "issue3155/custom-evaluator-input-field-race"
    And the fake runner, when invoked with `ssh boxd@issue3152.boxd.sh 'cd ~/langwatch && git rev-parse --abbrev-ref HEAD'`, returns "issue3152/foo"
    When BoxdForkAdapter.list_worktrees() is called
    Then 2 CachedWorktrees are returned
    And each entry has layout "flat"
    And each entry's path is "~/langwatch" (the single checkout), not a per-worktree subdirectory
    And the hosts are "boxd@issue3155.boxd.sh" and "boxd@issue3152.boxd.sh" respectively

  # =======================================================================
  # AC3 — BoxdForkAdapter specifics
  # =======================================================================

  @unit
  Scenario: Dynamic host enumeration via ssh boxd.sh list --json
    Given no per-fork entries in the `remotes[]` config array
    And a single `boxd-fork` remote with golden host "boxd.sh"
    When discovery runs
    Then the set of fork hosts is obtained by parsing `ssh boxd.sh list --json`
    And the operator is NOT required to edit "~/.orchard/config.json" for any individual fork

  @unit
  Scenario: Flat-clone layout is parsed without bare-repo assumption
    Given a fork host exposing one checkout at "~/langwatch" with branch "issue3155/foo"
    And no bare repo at "~/langwatch.git"
    When the BoxdForkAdapter parses worktree data for that host
    Then a single CachedWorktree is produced
    And layout is "flat"
    And no bare-repo `git worktree list --porcelain` parsing is attempted
    And the code paths in `git.rs` that assume "bare + worktrees" are not invoked for flat layout

  @unit
  Scenario: CachedWorktree model carries layout flag
    Then CachedWorktree has a field "layout" with allowed values "flat" and "bare"
    And WorktreeState exposes the same layout field through build_state
    And JsonOutput includes "layout" on each worktree entry
    And the JsonOutput schema version is bumped (additive change: new optional field)
    And existing JSON consumers that ignore unknown fields continue to parse
    And legacy local/remmy/boxd-shared worktrees default to layout="bare" so existing output is unchanged except for the new field

  @unit
  Scenario: JsonOutput version is bumped and field is documented
    Given an OrchardState with one local worktree (layout="bare") and one boxd-fork worktree (layout="flat")
    When JsonOutput is derived from the OrchardState
    Then the top-level "version" field reflects the new schema version (bumped from v2 to v3, or current+1)
    And each worktree entry includes "layout" as a non-null string
    And a snapshot test captures the v3 schema shape so future regressions are caught

  @integration
  Scenario: BoxdForkAdapter batches per-fork exec calls where possible
    Given 3 live fork VMs returned by `ssh boxd.sh list --json`
    And a fake SSH exec runner that records call ordering and timing
    When BoxdForkAdapter.list_worktrees() enumerates them
    Then the fake runner observes the 3 per-fork calls started before any of them completed (concurrent, not sequential)
    And each fork requires at most one SSH exec round-trip to collect branch + session data
    And no two per-fork calls are strictly ordered (no call's start-time is after another's end-time)

  @unit
  Scenario: Half-down fork — listed by boxd.sh but own SSH unreachable — surfaces as degraded, not removed
    Given `ssh boxd.sh list --json` returns fork "issue3155" as running
    But `ssh boxd@issue3155.boxd.sh ...` times out or fails authentication
    When BoxdForkAdapter.list_worktrees() is called
    Then a CachedWorktree is still emitted for issue3155 with host "boxd@issue3155.boxd.sh"
    And the entry carries a degraded flag (e.g. `reachable: false` or `status: "unreachable"`) but is NOT omitted
    And no "worktree_gone" event is emitted (the VM is not destroyed, only unreachable)
    And the branch field falls back to the fork name ("issue3155") if `git rev-parse` could not be executed

  @unit
  Scenario: Race — VM destroyed between `boxd.sh list` and per-fork exec
    Given `ssh boxd.sh list --json` returned fork "issue3155"
    And the per-fork `git rev-parse` then fails with "no such host" / immediate TCP RST
    When BoxdForkAdapter.list_worktrees() processes that fork
    Then the adapter emits the fork with degraded state (same as half-down) rather than crashing
    And the destruction is detected and handled by the NEXT revalidation (which will see it absent from `boxd.sh list`)
    And no duplicate or contradictory "worktree_gone" event is emitted during the racing refresh

  @unit
  Scenario: Malformed JSON from `ssh boxd.sh list --json` degrades, does not crash
    Given `ssh boxd.sh list --json` returns invalid JSON or a truncated payload
    When BoxdForkAdapter.list_worktrees() is called
    Then the adapter returns an error tagged "boxd.sh list parse failure"
    And the error is logged with the raw payload (redacted if large)
    And the pipeline continues: other adapters still run, cached forks remain visible
    And the SWR cache retains the prior fresh/stale value rather than being cleared

  @unit
  Scenario: boxd.sh unreachable entirely — only the boxd-fork adapter is affected
    Given `ssh boxd.sh ...` fails (network partition / auth)
    When `refresh_remote_worktrees` runs with remmy + boxd-shared + boxd-fork configured
    Then the remmy and boxd-shared adapters still complete normally
    And the boxd-fork adapter returns its error and its prior cache (if any) continues to serve
    And no pipeline-level stall occurs

  @unit
  Scenario: Detached HEAD on a flat clone reports the commit, not the literal "HEAD"
    Given a fork at "~/langwatch" has detached HEAD at commit abc123
    When BoxdForkAdapter runs `git rev-parse --abbrev-ref HEAD`
    Then the adapter detects the literal "HEAD" string
    And falls back to `git rev-parse --short HEAD` to record the commit
    And the CachedWorktree branch field is set to "(detached: abc123)" (or equivalent unambiguous marker)
    And the entry is not silently dropped

  # =======================================================================
  # AC4 — Explicit `type` field on RemoteConfig (migration from name-based dispatch)
  # =======================================================================

  @unit
  Scenario: RemoteConfig schema requires a `type` field
    Then RemoteConfig has a required field "type" of enum kind
    And allowed values are: remmy, boxd-shared, boxd-fork
    And the field is serialized/deserialized as a lowercase-hyphenated string

  @unit
  Scenario: Missing type field produces a clear validation error
    Given a config entry with `name: "gpu"` and `host: "ubuntu@10.0.0.1"` but no `type`
    When the config is loaded
    Then loading fails with an error pointing at the entry missing `type`
    And the error suggests adding `"type": "remmy"` (or similar) with examples

  @unit
  Scenario: Legacy configs without a type are not silently auto-detected by name
    Given a config entry whose `name` contains the substring "boxd"
    And the entry has no `type` field
    When the config is loaded
    Then the loader does NOT infer a type from the name
    And the loader returns the same missing-`type` validation error as any other entry

  # =======================================================================
  # AC5 — Stale-while-revalidate (SWR) framework for ALL outbound calls
  #
  # Rule: every outbound call (SSH exec, `gh api`, `ssh boxd.sh list`) returns
  # the cached value instantly and revalidates in the background. The port
  # is hollow if probes still block; SWR is the framework, not a Boxd-only
  # cache. In-scope callers:
  #   - BoxdForkAdapter: list_vms, list_sessions, per-fork git rev-parse
  #   - RemmyAdapter / BoxdSharedAdapter: list_worktrees, list_sessions
  #   - sources::hosts::probe_reachability (per-host)
  #   - sources::github: gh api (PRs, issues) per repo
  # =======================================================================

  @unit
  Scenario: Fresh cache entry is returned instantly without revalidation
    Given a remote-worktree cache entry written 5 seconds ago
    And the configured TTL is 60 seconds
    When the core requests remote worktrees for that remote
    Then the cached value is returned immediately
    And no adapter call is made
    And no background revalidation is spawned

  @unit
  Scenario: Stale cache entry is returned instantly AND triggers background revalidation
    Given a remote-worktree cache entry written 120 seconds ago
    And the configured TTL is 60 seconds
    When the core requests remote worktrees for that remote
    Then the cached value is returned immediately (no blocking wait)
    And a background revalidation task is spawned to call the adapter
    And the cache is updated atomically when the adapter returns

  @unit
  Scenario: Concurrent stale reads do not spawn duplicate revalidations
    Given a stale cache entry and no in-flight revalidation
    When two callers request the same remote's worktrees simultaneously
    Then exactly one revalidation task is spawned
    And both callers receive the cached value instantly

  @unit
  Scenario: Cache miss (no prior entry) falls back to synchronous adapter call
    Given no cache entry exists for a remote
    When the core requests remote worktrees for that remote
    Then the adapter is called synchronously (blocking this caller)
    And the result is written to the cache with a fresh timestamp
    And subsequent calls within TTL hit the fast path

  @integration
  Scenario: Cold-start synchronous adapter calls across remotes run concurrently
    Given a config with 3 remotes and empty caches for all of them
    And fake adapters that record their invocation start/end instants
    When `refresh_remote_worktrees` runs
    Then all 3 adapter invocations have overlapping lifetimes (no call's start is after another's end)
    And the orchestration uses one task per remote, not a sequential loop
    # Wall-clock bound ("<5s when all unreachable") is asserted by the @e2e scenario below,
    # not here — @integration verifies structure only.

  @unit
  Scenario: TTL is configurable per outbound kind with a global default
    Then the global config exposes a default TTL in the range [30, 120] seconds
    And each outbound kind has an optional override:
      | kind              | default TTL | rationale                                   |
      | remote_worktrees  | 60s         | remote worktree list changes on fork churn  |
      | remote_sessions   | 30s         | sessions start/stop frequently              |
      | host_reachable    | 30s         | fast feedback when a host goes down         |
      | boxd_list_vms     | 30s         | fork-per-issue churns hourly                |
      | github_prs        | 60s         | gh api is rate-limited                      |
      | github_issues     | 120s        | issues change less often than PRs           |
    And a per-remote optional "swr_ttl_secs" field overrides the adapter kinds for that entry
    And max_age (the stale-cap before transitioning to Expired) is configurable per kind, default 24h

  @integration
  Scenario: Cached-but-gone — revalidation sees VM destroyed and emits state-change event
    Given a cached worktree for fork host "issue3155.boxd.sh" exists
    And the VM has been destroyed so `ssh boxd.sh list --json` no longer includes "issue3155"
    When SWR revalidation runs for the boxd-fork remote
    Then the cache entry for that fork is marked stale immediately (not waiting for TTL)
    And a state-change event "worktree_gone" is appended to "events.jsonl"
    And the event includes the host, branch, and prior cached worktree path
    And the monitor/watch daemon can react to the event without polling

  @unit
  Scenario: "worktree_gone" event is emitted from the shell/refresh layer, not the pure compositor
    Then `build_state()` remains a pure function: no filesystem writes, no event emission
    And the `worktree_gone` event is appended by the SWR revalidation task (shell layer)
    And no call to `events.jsonl` writing appears inside `build_state.rs` or `join.rs`
    And this preserves ADR-004 (functional core, imperative shell)

  @unit
  Scenario: "worktree_gone" event shape is specified
    Then the event JSON line contains at minimum these fields:
      | field          | type   | example                                           |
      | event          | string | "worktree.remote_lost"                            |
      | timestamp      | string | ISO 8601 UTC                                      |
      | repo           | string | "langwatch/langwatch"                             |
      | remote_name    | string | "boxd-fork-langwatch"                             |
      | remote_type    | string | "boxd-fork"                                       |
      | host           | string | "boxd@issue3155.boxd.sh"                          |
      | branch         | string | "issue3155/custom-evaluator-input-field-race"     |
      | path           | string | prior cached worktree path                        |
    And the event name follows the existing `*.` dotted-namespace convention (task.created, webhook.*)

  @integration
  Scenario: `orchard --json` with fresh caches issues zero remote adapter calls
    Given cache entries for all configured remotes are fresh
    And the adapters are fake implementations that count invocations
    When the refresh pipeline runs in --json mode
    Then the invocation count for every remote adapter is 0
    And the pipeline completes without blocking on any network task

  @integration
  Scenario: SWR round-trip against a real temp-dir cache file with a fake adapter
    Given a temp directory used as the orchard cache root
    And a fake adapter that returns a known Vec<CachedWorktree>
    When `refresh_remote_worktrees` is invoked and the cache is cold
    Then the adapter is called once synchronously
    And a JSON cache file is written at the expected path with a fresh `last_refreshed`
    When `refresh_remote_worktrees` is invoked again within TTL
    Then the adapter is NOT called (invocation count remains 1)
    And the returned worktrees match the cached file byte-for-byte in identity
    When the cache file's `last_refreshed` is rewritten to be older than TTL
    And `refresh_remote_worktrees` is invoked
    Then the cached value is returned to the caller instantly
    And a background revalidation runs, after which the adapter's count reaches 2
    And the cache file is rewritten with a new `last_refreshed`

  @unit
  Scenario: SWR is a generic wrapper, not a remote-worktree-specific cache
    Then the SWR layer is implemented as a generic wrapper parameterized over:
      | parameter     | purpose                                                     |
      | cache key     | (kind, scope) tuple, e.g. ("remote_worktrees", remote_name) |
      | value type    | the Serialize + Deserialize payload                         |
      | fetcher       | a closure/function producing a fresh value on revalidation  |
      | ttl           | per-call TTL (falls back to global default)                 |
      | max_age       | upper bound before Expired transition                       |
    And the same wrapper is used for:
      | caller                                     | kind                    | scope                 |
      | RemoteWorktreeService.list_worktrees       | "remote_worktrees"      | (repo, remote_name)   |
      | RemoteWorktreeService.list_sessions        | "remote_sessions"       | (repo, remote_name)   |
      | BoxdForkAdapter `ssh boxd.sh list --json`  | "boxd_list_vms"         | (golden_host)         |
      | sources::hosts::probe_reachability         | "host_reachable"        | (host)                |
      | sources::github PRs                        | "github_prs"            | (repo_slug)           |
      | sources::github issues                     | "github_issues"         | (repo_slug)           |
    And no caller reimplements SWR semantics inline

  @unit
  Scenario: Host probes flow through SWR — no blocking probe on cache hit
    Given a host "boxd@orchard-rs.boxd.sh" was last probed 10 seconds ago and is reachable
    And the configured probe TTL is 30 seconds
    When `probe_reachability_all` is invoked for a set including that host
    Then the cached reachability value is returned immediately for that host
    And no `ssh host true` subprocess is spawned for it
    And a background revalidation is spawned only if the entry is stale (not when it is fresh)

  @unit
  Scenario: gh api calls flow through SWR — PR/issue enrichment from cached values on fast path
    Given a PRs cache for repo "langwatch/langwatch" was written 20 seconds ago
    And the configured gh-api TTL is 60 seconds
    When the refresh pipeline requests PRs for that repo
    Then the cached PR list is returned immediately
    And no `gh api graphql` subprocess is spawned
    And a background revalidation is spawned if and only if the entry is stale

  @unit
  Scenario: Cold-start SWR falls back to synchronous fetch uniformly across caller kinds
    Given no cache entries exist for any outbound kind (remote_worktrees, host_reachable, github_prs, github_issues, boxd_list_vms)
    When the refresh pipeline runs
    Then every caller blocks on its first fetch
    And all first fetches across different kinds run concurrently (no serial waterfall)
    And subsequent invocations within TTL return from cache with zero outbound calls

  @integration
  Scenario: `orchard --json` with warm caches across all outbound kinds issues zero subprocesses
    Given fresh caches exist for remote_worktrees (all remotes), host_reachable (all hosts), github_prs and github_issues (all repos), boxd_list_vms (golden host)
    And fake runners count every SSH / gh / boxd invocation
    When the refresh pipeline runs in --json mode
    Then the combined invocation count across all fake runners is 0
    And the pipeline returns the joined OrchardState purely from on-disk caches

  @unit
  Scenario: SWR state machine transitions are well-defined
    Then the SWR cache entry exists in exactly one of these states at any time:
      | state            | meaning                                                  |
      | Missing          | no cache entry on disk                                   |
      | Fresh            | on-disk entry with age <= ttl                            |
      | Stale            | on-disk entry with ttl < age <= max_age, no revalidation |
      | Revalidating    | on-disk entry, a background revalidation is in flight    |
      | Expired          | on-disk entry with age > max_age                         |
    And transitions are:
      | from         | trigger                          | to              |
      | Missing      | caller requests, adapter returns | Fresh           |
      | Fresh        | age exceeds ttl                  | Stale           |
      | Stale        | caller requests                  | Revalidating    |
      | Revalidating | adapter returns Ok               | Fresh           |
      | Revalidating | adapter returns listed-but-gone  | Fresh (empty)   |
      | Revalidating | adapter returns Err              | Stale (retry_at = now + backoff) |
      | Stale        | age exceeds max_age              | Expired         |
      | Expired      | caller requests                  | Missing (treated as cold start) |

  @unit
  Scenario: Revalidation failure serves stale with bounded retry, not forever
    Given a stale cache entry and an adapter that returns an error
    When SWR revalidation runs
    Then the cache entry remains (stale) but its retry_at is set to now + backoff
    And subsequent callers within the backoff window receive the stale value without re-spawning revalidation
    And after N consecutive failed revalidations (N configurable, default 5) the entry transitions to Expired
    And once Expired the next caller is forced through the cold-start synchronous path

  @unit
  Scenario: TTL comparison uses a monotonic clock, not wall-clock
    Given a cache entry with a recorded monotonic-instant timestamp
    When the TTL comparison runs
    Then it uses a monotonic clock source (e.g. `std::time::Instant`) for age arithmetic, not `SystemTime`
    And wall-clock jumps (NTP adjustments, DST) do not flip Fresh to Stale or vice versa
    And if the process restarts, the on-disk `last_refreshed` wall-clock timestamp is treated defensively: a negative duration_since is clamped to "stale" rather than panicking

  @unit
  Scenario: Corrupted cache file is treated as Missing, not a crash
    Given a cache file containing invalid JSON or a truncated write
    When the SWR layer attempts to read it
    Then the read returns Missing (not an error that stalls the pipeline)
    And a warning is logged with the file path
    And the next caller proceeds through the cold-start synchronous path

  @unit
  Scenario: Cache key is (repo_slug, remote_name, remote_type)
    Then SWR cache files are keyed by (repo_slug, remote_name, remote_type)
    And renaming a remote invalidates the old cache (different key)
    And changing a remote's type from "boxd-shared" to "boxd-fork" invalidates the old cache (different key)
    And two repos sharing a remote name do not cross-contaminate caches

  # =======================================================================
  # AC6 — Hard timeout on adapter calls (absorbs remaining #246 scope)
  # =======================================================================

  @unit
  Scenario: Adapter SSH subprocess is killed after hard deadline
    Given a RemmyAdapter with a configured per-call hard timeout of 5 seconds
    And an SSH subprocess that does not exit on its own
    When list_worktrees() is invoked
    Then the subprocess is killed after 5 seconds (not relying on ssh ConnectTimeout alone)
    And the adapter returns a timeout error, not a hang

  @unit
  Scenario: Hard timeout applies to all three adapters uniformly
    Then RemmyAdapter, BoxdSharedAdapter, and BoxdForkAdapter all enforce the same hard-timeout contract
    And each documents its timeout in the per-adapter module doc comment

  @e2e
  Scenario: `orchard --json` completes in <5s when all remote hosts are unreachable
    Given 3 remotes configured and all hosts are unreachable
    When the user runs "orchard --json" with empty caches
    Then the command exits cleanly with per-remote errors surfaced in the output
    And the total wall-clock time is under 5 seconds

  @unit
  Scenario: Hard timeout fires within the configured deadline under a fake executor
    Given an adapter configured with a 5s hard timeout
    And a fake executor whose virtual clock can be advanced
    When list_worktrees() is invoked and the fake SSH runner never completes
    And the virtual clock advances past the deadline
    Then the adapter returns a timeout error
    And the subprocess-kill hook was invoked before the timeout error was returned

  # =======================================================================
  # AC7 — Discovery by remote type (fold-in of #264)
  # =======================================================================

  @integration
  Scenario: Discovery enumerates each remotes[] entry by its `type`, not by path shape
    Given a config with 3 remotes: one "remmy", one "boxd-shared", and one "boxd-fork"
    When `refresh_remote_worktrees` runs
    Then each remote is dispatched to its adapter based on the `type` field
    And no code path inspects the `path` value to guess the remote kind

  @integration
  Scenario: boxd-fork discovery enumerates live VMs and matches by branch
    Given a "boxd-fork" remote is configured
    And `ssh boxd.sh list --json` lists fork "issue3155" with branch "issue3155/foo"
    When discovery runs
    Then a CachedWorktree is produced with branch "issue3155/foo" and host "boxd@issue3155.boxd.sh"
    And it is merged into the repo's worktree set by `collect_repo_caches`

  @integration
  Scenario: boxd-shared and remmy retain current behavior
    Given a "boxd-shared" remote and a "remmy" remote, each reachable
    When discovery runs
    Then the boxd-shared remote produces the same CachedWorktrees it produced before this refactor
    And the remmy remote produces the same CachedWorktrees it produced before this refactor
    And a regression snapshot test confirms byte-identical cache output for both

  @unit
  Scenario: Adding a new remote type does not require editing the core discovery loop
    Given the core discovery loop iterates `remotes[]` and dispatches via a type -> adapter registry
    When a hypothetical new adapter "GcpVmAdapter" is added for type "gcp-vm"
    Then only the adapter module and the registry entry are added
    And no line in `refresh_remote_worktrees` changes

  @e2e
  Scenario: Fresh fork appears in `orchard --json` within one refresh
    Given a boxd-fork remote is configured for "langwatch/langwatch"
    And a new fork VM "issue4242" is launched with branch "issue4242/new-feature"
    And an active tmux session and a PR #4242 exist on the fork
    When the user runs "orchard --json" (cache cold) on the next invocation
    Then the output contains a worktree for branch "issue4242/new-feature" on host "boxd@issue4242.boxd.sh"
    And the worktree has the tmux session attached
    And the worktree has PR #4242 attached

  @e2e
  Scenario: Destroyed fork disappears from `orchard --json` on next revalidation
    Given a boxd-fork remote with a fresh cached worktree for "issue4242"
    And the fork VM has been destroyed (no longer listed by `ssh boxd.sh list --json`)
    When the cache becomes stale and `orchard --json` is invoked
    Then SWR revalidation detects the fork is gone
    And a "worktree.remote_lost" event is appended to "events.jsonl"
    And the subsequent `orchard --json` output does not include a worktree for that fork
    And the total invocation time stays under the "<5s all unreachable" bound

  # =======================================================================
  # AC8 — Remote worktree PR/issue enrichment (fold-in of #248)
  # =======================================================================

  @unit
  Scenario: Remote worktree branch with issue number resolves issue field
    Given a remote CachedWorktree on branch "issue240/smart-sorting-priority-indicators"
    And an issues cache containing issue #240 with state "closed"
    When build_state joins the data
    Then the resulting WorktreeState has `issue.number == 240`
    And `issue.state == "closed"`

  @unit
  Scenario: Remote worktree with a PR head branch resolves pr field
    Given a remote CachedWorktree on branch "issue240/smart-sorting-priority-indicators"
    And a PRs cache containing PR #244 for that branch with state "merged"
    When build_state joins the data
    Then the resulting WorktreeState has `pr.number == 244`
    And `pr.state == "merged"`

  @unit
  Scenario: Issue/PR state fields carry through all valid states
    Then a remote worktree's issue.state takes values from {"open", "closed"}
    And a remote worktree's pr.state takes values from {"open", "closed", "merged"}
    And the join logic distinguishes "closed" from "merged" for PRs

  @integration
  Scenario: TUI displays merged/closed badges on remote worktrees same as local
    Given an OrchardState containing a local worktree and a remote worktree, both on branches tied to merged PRs
    When the TUI list view renders
    Then both rows display the merged badge
    And both rows display the closed-issue badge if the issue is closed
    And no rendering branch in the TUI discriminates by worktree.host for these badges

  @integration
  Scenario: JSON output is consistent between local and remote worktrees for the same branch
    Given a local and a remote worktree both on branch "issue240/smart-sorting-priority-indicators"
    When "orchard --json" runs
    Then both worktrees' JSON entries include identical `issue` and `pr` blocks
    And the only differences are host, path, and layout

  # =======================================================================
  # Cross-cutting — data model + architecture invariants
  # =======================================================================

  @unit
  Scenario: OrchardState remains the single unified data model
    Then remote data flows through `collect_repo_caches` into `build_state`
    And no separate "RemoteOrchardState" type exists
    And JsonOutput is derived solely from OrchardState (per ADR-004)

  @unit
  Scenario: `host: Option<String>` continues to distinguish local from remote
    Then WorktreeState.host is Some(_) for remote adapters (all three)
    And WorktreeState.host is None for local worktrees
    And `join.rs` branch-matching logic already respects this

  @unit
  Scenario: SWR layer respects ADR-001 — filesystem remains the source of truth
    Then on-disk cache files under "~/.cache/orchard/" remain the durable source of truth for remote data
    And the SWR layer's in-memory state (single-flight locks, retry_at, in-flight tasks) is ephemeral — rebuildable from on-disk files on process restart
    And cold start of a new process reads on-disk caches and derives Fresh/Stale/Expired purely from file timestamps
    And no computed/derived state (display_group, issue enrichment) is written to the SWR cache files — only raw adapter output
    And ADR-001 is extended (or a new ADR noted) to document SWR as an on-top layer, not a replacement

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # Issue #267 AC1 "Hexagonal RemoteWorktreeService port"
  #   -> "RemoteWorktreeService port defines a minimal stable surface"
  #   -> "SSH exec is injectable so adapters are unit-testable without the network"
  #   -> "Adapter dispatch selects implementation from RemoteConfig.type"
  #   -> "Unknown remote type fails fast with actionable error"
  # Issue #267 AC2 "Three adapters (Remmy, BoxdShared, BoxdFork)"
  #   -> "RemmyAdapter wraps current universal-git-worktree-list-over-SSH behavior"
  #   -> "BoxdSharedAdapter preserves current single-VM-with-worktrees behavior"
  #   -> "BoxdForkAdapter returns a flat-clone worktree for each live fork"
  # Issue #267 AC3 "BoxdForkAdapter specifics (dynamic hosts, flat-clone, batching)"
  #   -> "Dynamic host enumeration via ssh boxd.sh list --json"
  #   -> "Flat-clone layout is parsed without bare-repo assumption"
  #   -> "CachedWorktree model carries layout flag"
  #   -> "JsonOutput version is bumped and field is documented"
  #   -> "BoxdForkAdapter batches per-fork exec calls where possible"
  #   -> "Half-down fork — listed by boxd.sh but own SSH unreachable — surfaces as degraded, not removed"
  #   -> "Race — VM destroyed between `boxd.sh list` and per-fork exec"
  #   -> "Malformed JSON from `ssh boxd.sh list --json` degrades, does not crash"
  #   -> "boxd.sh unreachable entirely — only the boxd-fork adapter is affected"
  #   -> "Detached HEAD on a flat clone reports the commit, not the literal 'HEAD'"
  # Issue #267 AC4 "Explicit `type` field on RemoteConfig"
  #   -> "RemoteConfig schema requires a `type` field"
  #   -> "Missing type field produces a clear validation error"
  #   -> "Legacy configs without a type are not silently auto-detected by name"
  # Issue #267 AC5 "SWR framework wrapping ALL outbound calls"
  #   Generic wrapper applied to adapters, host probes, and gh api:
  #   -> "SWR is a generic wrapper, not a remote-worktree-specific cache"
  #   -> "Host probes flow through SWR — no blocking probe on cache hit"
  #   -> "gh api calls flow through SWR — PR/issue enrichment from cached values on fast path"
  #   -> "Cold-start SWR falls back to synchronous fetch uniformly across caller kinds"
  #   -> "`orchard --json` with warm caches across all outbound kinds issues zero subprocesses"
  #   Core SWR semantics:
  #   -> "Fresh cache entry is returned instantly without revalidation"
  #   -> "Stale cache entry is returned instantly AND triggers background revalidation"
  #   -> "Concurrent stale reads do not spawn duplicate revalidations"
  #   -> "Cache miss (no prior entry) falls back to synchronous adapter call"
  #   -> "Cold-start synchronous adapter calls across remotes run concurrently"
  #   -> "TTL is configurable per outbound kind with a global default"
  #   -> "Cached-but-gone — revalidation sees VM destroyed and emits state-change event"
  #   -> "'worktree_gone' event is emitted from the shell/refresh layer, not the pure compositor"
  #   -> "'worktree_gone' event shape is specified"
  #   -> "SWR state machine transitions are well-defined"
  #   -> "Revalidation failure serves stale with bounded retry, not forever"
  #   -> "TTL comparison uses a monotonic clock, not wall-clock"
  #   -> "Corrupted cache file is treated as Missing, not a crash"
  #   -> "Cache key is (repo_slug, remote_name, remote_type)"
  #   -> "`orchard --json` with fresh caches issues zero remote adapter calls"
  #   -> "SWR round-trip against a real temp-dir cache file with a fake adapter"
  #   -> "SWR layer respects ADR-001 — filesystem remains the source of truth"
  # Issue #267 AC6 "Hard timeout on adapter calls (absorbs remaining #246 scope)"
  #   -> "Adapter SSH subprocess is killed after hard deadline"
  #   -> "Hard timeout fires within the configured deadline under a fake executor"
  #   -> "Hard timeout applies to all three adapters uniformly"
  #   -> "`orchard --json` completes in <5s when all remote hosts are unreachable" (@e2e)
  # Issue #267 AC7 = Issue #264 (discovery by remote type)
  #   #264 AC-a "Discovery enumerates remotes[] by type, not hardcoded remmy"
  #     -> "Discovery enumerates each remotes[] entry by its `type`, not by path shape"
  #   #264 AC-b "boxd-fork: list live VMs, match by branch, surface as worktrees"
  #     -> "boxd-fork discovery enumerates live VMs and matches by branch"
  #   #264 AC-c "boxd-shared: keep current behavior"
  #     -> "boxd-shared and remmy retain current behavior"
  #   #264 AC-d "remmy: keep current behavior"
  #     -> "boxd-shared and remmy retain current behavior"
  #   #264 AC-e "Each remote type has adapter; adding new type doesn't touch core discovery loop"
  #     -> "Adding a new remote type does not require editing the core discovery loop"
  #   #264 AC-f "Verified: fresh fork → branch+session+PR in `orchard --json` within one refresh"
  #     -> "Fresh fork appears in `orchard --json` within one refresh"
  #     -> "Destroyed fork disappears from `orchard --json` on next revalidation"
  # Issue #267 AC8 = Issue #248 (remote PR/issue enrichment)
  #   #248 AC-a "Remote worktrees have `issue` populated when branch contains issue number"
  #     -> "Remote worktree branch with issue number resolves issue field"
  #   #248 AC-b "Remote worktrees have `pr` populated when a PR exists"
  #     -> "Remote worktree with a PR head branch resolves pr field"
  #   #248 AC-c "Issue/PR state accurate (open/closed/merged)"
  #     -> "Issue/PR state fields carry through all valid states"
  #   #248 AC-d "TUI displays merged/closed badges on remote same as local"
  #     -> "TUI displays merged/closed badges on remote worktrees same as local"
  #   #248 AC-e "`orchard --json` consistent between local and remote"
  #     -> "JSON output is consistent between local and remote worktrees for the same branch"
  #
  # Architecture invariants (not numbered ACs, but named in scope):
  #   "OrchardState as single unified model (ADR-004)"
  #     -> "OrchardState remains the single unified data model"
  #   "host: Option<String> already present on data model"
  #     -> "`host: Option<String>` continues to distinguish local from remote"
  #
  # Out of scope (explicitly split to follow-up issue):
  #   - TUI Enter on remote row blocking when host_reachable lacks entry
  #     (reachability-guard bug in TUI action layer; NOT a discovery AC)
