Feature: Transitive federation — walk the orchard graph past one hop
  As an orchardist running many machines where some hosts spawn their own forks
  I want registering a single remote to expose its whole subtree of orchards
  So that dynamically-spawned children (e.g. boxd spinning up a VM fork) become visible
  And write operations (send-keys, session launch, delete) work on transitively-discovered
  nodes the same as on directly-registered remotes — without per-child local config changes

  # Scope: transitive READ-path discovery + WRITE-path chaining. The motivating case is
  # Mac -> boxd -> scenario-voice-agents.boxd.sh (private-network fork); the Mac cannot
  # SSH the grandchild directly, so write ops must route through the discovery chain.
  #
  # Per the plan:
  #   - JsonOutput wire version is NOT bumped (exact-whitelist check would break mixed fleets).
  #     A new subcommand `orchard list-remotes --json` decouples topology from enrichment.
  #   - Traversal is opt-in per root via `RemoteConfig.allow_transitive: bool` (default false).
  #   - Dedup key is best-effort `host_dedup_key()` (NOT "canonicalize_ssh_target");
  #     SSH aliases / ProxyJump chains cannot be resolved from the string alone.
  #   - Walker runs ONLY in `orchard refresh` / `orchard watch`. Dashboard reads stay
  #     cache-only (<100ms invariant from ADR-008).
  #   - Topology persisted at `~/.cache/orchard/federation_topology.json` so cold-start
  #     `orchard --json` returns transitive rows after the first successful walk.

  Background:
    Given a per-repo `RemoteConfig` may include `"type": "orchard-proxy"` entries
    And `RemoteConfig` has an optional field `allow_transitive: bool` (default `false`)
    And the new subcommand `orchard list-remotes --json` returns `{ name, host, kind, path, allow_transitive }` entries
    And `orchard list-remotes --json` has its OWN independent version constant (lower-bound check, not exact whitelist)
    And the walker runs only inside `orchard refresh` and `orchard watch`
    And dashboard reads (`orchard --json`, TUI) remain cache-only
    And the federation topology index lives at `~/.cache/orchard/federation_topology.json`

  # =======================================================================
  # AC1 — `orchard --json` returns the transitive closure of reachable nodes
  # =======================================================================

  @integration
  Scenario: After refresh, orchard --json surfaces depth-2 nodes discovered via a depth-1 root
    Given a root remote "boxd" configured locally with `allow_transitive: true`
    And "boxd" advertises one child "scenario-voice-agents.boxd.sh" via `orchard list-remotes --json`
    And a prior `orchard refresh` has written both snapshots and `federation_topology.json`
    When the user runs `orchard --json`
    Then the output includes worktrees from "boxd"
    And the output includes worktrees from "scenario-voice-agents.boxd.sh"
    And no SSH process is spawned during the `orchard --json` invocation

  @integration
  Scenario: Depth-3 transitive closure appears after refresh
    Given a chain A -> B -> C -> D where each root forwards `allow_transitive: true`
    And `orchard refresh` has completed one successful walk
    When `orchard --json` runs locally
    Then the output includes worktrees sourced from B, C, and D
    And each node's snapshot was loaded from its cached `{safe_host}_orchard_snapshot.json`

  # =======================================================================
  # AC2 — Each node is tagged with its discovery path
  # =======================================================================

  @unit
  Scenario: WorktreeState carries discovery_path from root to origin
    Given a JsonOutput fetched from host "scenario-voice-agents.boxd.sh" via parent "boxd"
    And the walker's discovery_path for that hop is `["local", "boxd", "scenario-voice-agents.boxd.sh"]`
    When the merge folds the snapshot into OrchardState
    Then each WorktreeState sourced from that snapshot carries `discovery_path == ["local", "boxd", "scenario-voice-agents.boxd.sh"]`
    And each SessionState sourced from that snapshot carries the same discovery_path
    And each HostState entry carries the same discovery_path

  @unit
  Scenario: Directly-registered (depth-1) remote has a single-parent discovery_path
    Given a root remote "boxd" with `allow_transitive: false`
    When a successful walk merges its snapshot
    Then worktrees from "boxd" carry `discovery_path == ["local", "boxd"]`

  # =======================================================================
  # AC3 — Cycle prevention via seen-set (silent skip, no error, no infinite loop)
  # =======================================================================

  @unit
  Scenario: Already-visited host is skipped silently during BFS
    Given a cycle graph A -> B -> A (B advertises A as one of its remotes)
    And `allow_transitive: true` on both hops
    When the walker traverses from root A
    Then the walker visits A once and B once — A is not re-visited
    And no error or warning is emitted for the cycle
    And the walk terminates

  @unit
  Scenario: Diamond graph — same grandchild reached via two parents yields one snapshot fetch
    Given a diamond A -> {B, C} -> D where both B and C advertise D
    And `allow_transitive: true` everywhere
    When the walker traverses from A
    Then D is fetched exactly once within the refresh (one OrchardProxyAdapter instance keyed by `host_dedup_key(D)`)
    And D appears in the topology under both B's and C's subtrees via discovery_path, not as a duplicate snapshot fetch

  # =======================================================================
  # AC4 — Host identifier dedup via best-effort `host_dedup_key()`
  # =======================================================================

  @unit
  Scenario: host_dedup_key case-folds the host portion but preserves user case
    Given strings "boxd@VM.Boxd.Sh" and "boxd@vm.boxd.sh"
    When `host_dedup_key` is applied to each
    Then the two keys are equal

  @unit
  Scenario: host_dedup_key treats default SSH port as implicit
    Given strings "boxd@vm.boxd.sh" and "boxd@vm.boxd.sh:22"
    When `host_dedup_key` is applied to each
    Then the two keys are equal
    And "boxd@vm.boxd.sh:2222" produces a DIFFERENT key from both

  @unit
  Scenario: host_dedup_key strips a trailing dot on the hostname
    Given strings "boxd@vm.boxd.sh." and "boxd@vm.boxd.sh"
    When `host_dedup_key` is applied to each
    Then the two keys are equal

  @unit
  Scenario: host_dedup_key normalizes IPv6 brackets consistently
    Given "boxd@[2001:db8::1]" and "boxd@[2001:db8::1]:22"
    When `host_dedup_key` is applied
    Then the two keys are equal
    And bracketed-with-nondefault-port "boxd@[2001:db8::1]:2222" is a DIFFERENT key

  @unit
  Scenario: host_dedup_key preserves distinct users on the same host
    Given strings "alice@vm.boxd.sh" and "bob@vm.boxd.sh"
    When `host_dedup_key` is applied to each
    Then the two keys are NOT equal
    And this is by design — SSH treats them as distinct identities

  @unit
  Scenario: host_dedup_key rejects malformed strings
    Given the string "boxd@vm.boxd.sh/evil"
    When `host_dedup_key` is applied
    Then the call returns an error (reject paths, whitespace, backslashes)

  @unit
  Scenario: first-seen host emits federation.discovered_host event for collision visibility
    Given the walker discovers "Boxd@VM.Boxd.Sh" for the first time in a refresh
    When the dedup key is computed
    Then a `federation.discovered_host` event is appended to `events.jsonl` with both the raw input and the computed key
    And a subsequent discovery of "boxd@vm.boxd.sh" (same key) does NOT emit a new event in the same refresh

  # =======================================================================
  # AC5 — Write operations work on transitive nodes via jump-host chaining
  # =======================================================================

  @unit
  Scenario: build_ssh_chain produces `ssh parent ssh child <cmd>` for a depth-2 target
    Given a discovery_path `["local", "boxd", "scenario-voice-agents.boxd.sh"]`
    And a raw command `tmux send-keys -t session:0 "hello" Enter`
    When `build_ssh_chain(discovery_path, cmd)` is called
    Then the result is a single shell command string that, when executed locally, runs the raw command on the leaf host via nested SSH through "boxd"
    And arguments are shell-quoted at each nesting level so special characters survive both layers

  @unit
  Scenario: build_ssh_chain handles nested shell-metacharacter payloads
    Given a payload containing `$`, backtick, double-quote, backslash, newline, and a Unicode codepoint outside ASCII
    And a two-hop discovery_path
    When `build_ssh_chain` is applied
    Then the resulting command, when executed, delivers the payload byte-identical to the leaf host (no shell interpolation, no truncation)

  @unit
  Scenario: Depth-1 direct remote uses single SSH (unchanged behavior)
    Given a discovery_path `["local", "boxd"]`
    When `build_ssh_chain` is called for a send-keys command
    Then the result is a single-level `ssh boxd <cmd>` — no nested layer, no regression on direct-remote paths

  @integration
  Scenario: send-keys to a transitively-discovered session routes through the parent chain
    Given a recorded walker topology where leaf "scenario-voice-agents.boxd.sh" was discovered via "boxd"
    And a fake SSH runner that records every invocation
    When a send-keys message is dispatched to a session on the leaf host
    Then the runner records exactly one `ssh boxd ...` invocation whose inner argv is an `ssh scenario-voice-agents.boxd.sh ...` command
    And no direct `ssh scenario-voice-agents.boxd.sh ...` invocation is attempted from local

  @integration
  Scenario: Session launch on a transitive node uses the chain
    Given a transitively-discovered leaf host reached through one parent
    When the TUI Enter action launches a tmux session on a worktree at that host
    Then the launch command executes via `ssh parent ssh leaf tmux new-session ...`
    And on success a SessionState for that session appears in the next refresh with the correct discovery_path

  @integration
  Scenario: Worktree delete on a transitive node uses the chain
    Given a transitively-discovered worktree two hops from local
    When the TUI `d` action triggers `git worktree remove` on that worktree
    Then the resulting command executes as `ssh parent ssh leaf git worktree remove ...`
    And the worktree path is shell-quoted correctly across both SSH layers (paths with spaces, quotes, dollar-signs survive)

  @integration
  Scenario: RemoteConfig.allow_transitive = false suppresses transitive traversal for that root
    Given a root remote "boxd" with `allow_transitive: false`
    And "boxd" advertises one child via `orchard list-remotes --json`
    When `orchard refresh` runs
    Then the walker fetches "boxd" but does NOT fetch its children
    And `federation_topology.json` records only "boxd" under that root
    And no snapshot file is written for the child

  # =======================================================================
  # AC6 — Seen-set is the primary termination; optional `--max-depth N`
  # =======================================================================

  @unit
  Scenario: `--max-depth` flag halts traversal at the specified depth
    Given a chain A -> B -> C -> D with `allow_transitive: true` everywhere
    And `orchard refresh --max-depth 2` is invoked
    When the walker runs
    Then D is NOT fetched (depth 3 exceeds the cap)
    And C is fetched (depth 2 included)
    And a diagnostic is logged naming D as "max-depth reached"

  @unit
  Scenario: Default max depth is a belt-and-suspenders cap (plan: 8)
    Given a chain of 10 orchards A -> B -> ... -> J with `allow_transitive: true`
    And `orchard refresh` is invoked WITHOUT `--max-depth`
    When the walker runs
    Then traversal stops at depth 8 (default cap from the plan — seen-set is primary guard, depth is belt-and-suspenders)
    And hosts at depth > 8 are not fetched
    # NOTE: issue AC6 says "no depth bound by default"; plan overrides with default 8 to guard
    # against canonicalization misses and operator misconfig. Reconciled in /plan.

  @unit
  Scenario: Seen-set alone terminates a cycle even without --max-depth
    Given a cycle A -> B -> A with `allow_transitive: true`
    And no `--max-depth` is specified
    When the walker runs
    Then traversal terminates (seen-set short-circuits the revisit of A)
    And the walker does NOT rely on the depth cap to terminate this cycle

  # =======================================================================
  # AC7 — Aggregate failures: one bad hop doesn't break the tree
  # =======================================================================

  @unit
  Scenario: Middle-hop failure does not abort the walk
    Given a tree A -> {B, C}, B -> D, and C is unreachable
    And the fake SSH runner returns exit 255 for any `ssh C ...` invocation
    When the walker runs from root A
    Then B is fetched successfully
    And D is fetched successfully (via B)
    And the walk result contains a `TransitiveError` for C with `{ key, discovery_path, root, reason, phase }`
    And the walker did NOT abort on C's failure

  @unit
  Scenario: TransitiveError carries discovery_path and root for debuggability
    Given a failure at host "evals-v3-debug" reached via root "boxd"
    When the walker records the error
    Then the emitted TransitiveError has `root == "boxd"`
    And `discovery_path == ["local", "boxd", "evals-v3-debug"]`
    And `phase` identifies whether the failure was during `list-remotes` or `--json` fetch

  @unit
  Scenario: `orchard --json` surfaces a top-level errors[] array grouped for the operator
    Given a refresh that produced two TransitiveErrors under different roots
    When `orchard --json` reads the resulting OrchardState
    Then the output contains an errors array at the top level
    And each error includes `{ key, discovery_path, root, reason, phase }`
    And errors are distinguishable by root for per-subtree triage

  @integration
  Scenario: Exit 127 from a list-remotes call is treated as a LEAF, not a failure
    Given a remote running a v6 orchard (no `list-remotes` subcommand)
    And the walker invokes `ssh host orchard list-remotes --json` and receives exit 127
    When the walker classifies the result
    Then the host is recorded as a leaf (no children discovered)
    And NO `TransitiveError` is emitted for this host
    And the host's own `orchard --json` snapshot is still fetched and merged

  @integration
  Scenario: Unreachable host leaves last-known snapshot visible
    Given a prior successful snapshot at "~/.cache/orchard/scenario-voice-agents_boxd_sh_orchard_snapshot.json"
    And on the next refresh the host is unreachable (exit 255)
    When the walker completes
    Then the cached snapshot on disk is NOT deleted
    And `federation_topology.json` still lists the host under its root
    And the dashboard renders the stale rows with the unchanged `discovery_path`

  # =======================================================================
  # AC8 — Per-hop timeout (default 10s, configurable)
  # =======================================================================

  @unit
  Scenario: Per-hop timeout defaults to 10 seconds
    Given `orchard refresh` is invoked WITHOUT a timeout flag
    When the walker probes a hop that never responds
    Then the per-hop operation is cancelled after 10 seconds
    And a TransitiveError with reason "timeout (10s)" is recorded
    And the walker proceeds to sibling hops

  @unit
  Scenario: `--per-hop-timeout` flag overrides the default
    Given `orchard refresh --per-hop-timeout 3` is invoked
    When the walker probes a slow hop
    Then the hop is cancelled at 3 seconds
    And the emitted TransitiveError reason reflects the 3s budget

  @unit
  Scenario: Level-parallel BFS bounds total wall-clock to depth * per-hop timeout
    Given a tree with 5 depth-1 hops and 5 depth-2 hops under each, all slow (just under timeout)
    And default per-hop timeout of 10s
    When `orchard refresh` runs
    Then the walker fetches depth-1 hosts in parallel (one level at a time)
    And total wall-clock is bounded by 2 * 10s (depth * per-hop), NOT by 25 * 10s (nodes * per-hop)

  # =======================================================================
  # AC9 — End-to-end regression: 2-hop, 3-hop, cycle all terminate correctly
  # =======================================================================

  @e2e
  Scenario: Two-hop graph A -> B -> C terminates with full merged state
    Given a live topology A (local) -> B (orchard-proxy, `allow_transitive: true`) -> C (orchard-proxy, leaf)
    And C has one worktree "issue999/demo" with a tmux session
    When `orchard refresh` is invoked on A
    And `orchard --json` is invoked on A
    Then the output contains the worktree from C with `discovery_path == ["local", "B", "C"]`
    And enrichment (pr, issue, claude, check_state, display_group) on the C worktree is preserved from C's JsonOutput (not re-derived locally)
    And no errors[] entries are present for this walk

  @e2e
  Scenario: Three-hop graph A -> B -> C -> D terminates with full merged state
    Given a chain of 4 orchards with `allow_transitive: true` at every non-leaf
    And D has one worktree and one session
    When `orchard refresh` is invoked on A
    And `orchard --json` is invoked on A
    Then the output includes D's worktree tagged with `discovery_path == ["local", "B", "C", "D"]`
    And D's snapshot file exists on disk at its `host_dedup_key` path

  @e2e
  Scenario: Cycle graph A -> B -> A terminates and returns correct data
    Given a live cycle: A lists B as a transitive-allowed remote, and B lists A
    When `orchard refresh` is invoked on A
    Then the walker terminates
    And each host is fetched exactly once
    And `orchard --json` on A returns a merged state containing both A's and B's worktrees without duplication

  # =======================================================================
  # Topology persistence + orphan GC + TTL  (plan section — not a numbered AC
  # but load-bearing for cold-start correctness; coverage-mapped under AC1)
  # =======================================================================

  @unit
  Scenario: federation_topology.json is written after a successful walk
    Given a successful walk from root "boxd" discovers children `["scenario-voice-agents.boxd.sh", "evals-v3-debug"]`
    When the walker completes
    Then `~/.cache/orchard/federation_topology.json` exists
    And it contains a mapping `{ "boxd": { "descendants": [...], "discovery_paths": {...} } }`
    And the file was written atomically (tmp -> rename)

  @unit
  Scenario: load_cached_snapshots consults federation_topology.json in addition to config.repos
    Given `federation_topology.json` lists "scenario-voice-agents.boxd.sh" under root "boxd"
    And only "boxd" is in local `config.repos`
    And both hosts have snapshot files on disk
    When the TUI cold-starts and calls `load_cached_snapshots(config)`
    Then both snapshots are loaded into the initial OrchardState
    And the transitive host's worktrees appear in the first render (no SSH required)

  @unit
  Scenario: Orphan snapshot files are GC'd at the end of a refresh
    Given a snapshot file "abandoned-host_orchard_snapshot.json" exists on disk
    And `federation_topology.json` does NOT list "abandoned-host"
    When `orchard refresh` completes its walk
    Then the orphan snapshot file is deleted
    And the post-refresh topology index reflects only currently-reachable hosts

  @unit
  Scenario: 7-day soft TTL on snapshot files
    Given a snapshot file older than 7 days whose host is still in the topology
    When `orchard refresh` runs and the host is unreachable for this refresh
    Then the stale-but-in-topology file is NOT deleted (kept for observability)
    And a diagnostic is logged noting the stale age

  # =======================================================================
  # list-remotes subcommand — independent versioning (plan section)
  # =======================================================================

  @unit
  Scenario: `orchard list-remotes --json` emits its own independent version field
    Given the new subcommand exists
    When the user invokes `orchard list-remotes --json` locally
    Then the output is a JSON object with `version: <u32>` and `remotes: [JsonRemoteConfig, ...]`
    And each `JsonRemoteConfig` has `{ name, host, kind, path, allow_transitive }`
    And the `version` constant is declared independently of `JsonOutput` (not the same constant)

  @unit
  Scenario: list-remotes version check is lower-bound, not exact-whitelist
    Given a future version N+1 is emitted by a newer remote
    And the local orchard declares a lower-bound of N
    When the local walker parses the response
    Then parsing succeeds (additive-only fields tolerated)
    And any unknown fields are ignored
    # This is the lesson learned from JsonOutput's exact-whitelist skew trap.

  @unit
  Scenario: v6 remote without list-remotes degrades as LEAF via exit 127
    Given a remote on a pre-transitive orchard build (no `list-remotes` subcommand)
    When the walker invokes `ssh host orchard list-remotes --json`
    Then the SSH call returns exit 127
    And the walker treats the host as a LEAF (no descendants)
    And no `TransitiveError` is emitted
    And the host's own `orchard --json` snapshot is still fetched and merged

  # =======================================================================
  # Global adapter dedup (plan section)
  # =======================================================================

  @unit
  Scenario: Walker uses one OrchardProxyAdapter per host_dedup_key per refresh
    Given a diamond topology where two discovery paths reach the same leaf
    And the walker is wired with a factory that records adapter constructions
    When the walker traverses the graph
    Then the factory produced exactly ONE adapter for the leaf's `host_dedup_key`
    And that adapter's internal OnceLock was used to share the single SSH round-trip

  # =======================================================================
  # Dashboard invariant (plan section — from ADR-008)
  # =======================================================================

  @e2e
  Scenario: `orchard --json` stays cache-only even with transitive topology
    Given a configured root with `allow_transitive: true` and a populated topology
    And all transitive hosts are unreachable at this instant
    When the user runs `orchard --json`
    Then the command completes in under 100ms wall-clock
    And NO SSH process is spawned during its execution
    And the output contains the last-known cached state for every host in the topology

  # =======================================================================
  # AC Coverage Map
  # =======================================================================

  # --- AC Coverage Map ---
  # AC1 "orchard --json returns the transitive closure of all reachable nodes, not just direct remotes"
  #   -> "After refresh, orchard --json surfaces depth-2 nodes discovered via a depth-1 root"
  #   -> "Depth-3 transitive closure appears after refresh"
  #   -> "federation_topology.json is written after a successful walk"
  #   -> "load_cached_snapshots consults federation_topology.json in addition to config.repos"
  # AC2 "Each node in the response is tagged with its path (local -> boxd -> scenario-voice-agents)"
  #   -> "WorktreeState carries discovery_path from root to origin"
  #   -> "Directly-registered (depth-1) remote has a single-parent discovery_path"
  # AC3 "Cycle prevention via seen-set — walker maintains visited set; already-seen hosts skipped silently"
  #   -> "Already-visited host is skipped silently during BFS"
  #   -> "Diamond graph — same grandchild reached via two parents yields one snapshot fetch"
  #   -> "Seen-set alone terminates a cycle even without --max-depth"
  # AC4 "Host identifier for dedup: canonical form of SSH destination"
  #   -> "host_dedup_key case-folds the host portion but preserves user case"
  #   -> "host_dedup_key treats default SSH port as implicit"
  #   -> "host_dedup_key strips a trailing dot on the hostname"
  #   -> "host_dedup_key normalizes IPv6 brackets consistently"
  #   -> "host_dedup_key preserves distinct users on the same host"
  #   -> "host_dedup_key rejects malformed strings"
  #   -> "first-seen host emits federation.discovered_host event for collision visibility"
  # AC5 "Write operations (send-keys, session launch, etc.) work on transitively-discovered nodes the same as direct remotes"
  #   -> "build_ssh_chain produces `ssh parent ssh child <cmd>` for a depth-2 target"
  #   -> "build_ssh_chain handles nested shell-metacharacter payloads"
  #   -> "Depth-1 direct remote uses single SSH (unchanged behavior)"
  #   -> "send-keys to a transitively-discovered session routes through the parent chain"
  #   -> "Session launch on a transitive node uses the chain"
  #   -> "Worktree delete on a transitive node uses the chain"
  #   -> "RemoteConfig.allow_transitive = false suppresses transitive traversal for that root"
  # AC6 "No depth bound by default (seen-set is the only termination condition); optional --max-depth N flag for diagnostic use"
  #   -> "`--max-depth` flag halts traversal at the specified depth"
  #   -> "Default max depth is a belt-and-suspenders cap (plan: 8)"    # RECONCILED: plan overrides issue default; seen-set remains primary termination
  #   -> "Seen-set alone terminates a cycle even without --max-depth"
  # AC7 "Aggregate failures: one unreachable remote does not break the whole query — response includes errors[] per-node"
  #   -> "Middle-hop failure does not abort the walk"
  #   -> "TransitiveError carries discovery_path and root for debuggability"
  #   -> "`orchard --json` surfaces a top-level errors[] array grouped for the operator"
  #   -> "Exit 127 from a list-remotes call is treated as a LEAF, not a failure"
  #   -> "Unreachable host leaves last-known snapshot visible"
  # AC8 "Timeout per hop (configurable, default 10s) so a slow remote doesn't stall the whole query"
  #   -> "Per-hop timeout defaults to 10 seconds"
  #   -> "`--per-hop-timeout` flag overrides the default"
  #   -> "Level-parallel BFS bounds total wall-clock to depth * per-hop timeout"
  # AC9 "Regression test: two-hop (A -> B -> C), three-hop (A -> B -> C -> D), cycle (A -> B -> A) all terminate and return correct data"
  #   -> "Two-hop graph A -> B -> C terminates with full merged state"
  #   -> "Three-hop graph A -> B -> C -> D terminates with full merged state"
  #   -> "Cycle graph A -> B -> A terminates and returns correct data"
  #
  # Additional plan-driven scenarios (not numbered ACs but load-bearing; referenced above):
  #   - list-remotes subcommand with independent lower-bound version check (guards against
  #     the JsonOutput exact-whitelist skew trap)
  #   - Global adapter dedup by host_dedup_key (prevents duplicate SSHes across diamond paths)
  #   - 7-day soft TTL + orphan GC on snapshot files (keeps cache honest)
  #   - Dashboard-never-blocks invariant from ADR-008 (preserved under transitive topology)
