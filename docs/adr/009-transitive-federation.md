# ADR-009: Transitive Federation — multi-hop remote discovery

**Status**: Accepted
**Date**: 2026-04-24
**Issue**: #363

---

## Context

ADR-008 introduced single-hop federated discovery: a local orchard instance
SSHes into a directly-configured `orchard-proxy` remote and merges its
`JsonOutput` snapshot. This covers the common case of a developer machine
pulling data from one or two well-known VMs.

The limitation: hosts that are only reachable *through* an intermediate host
are invisible. A three-machine topology — `local → jump → target` — is not
supported without explicit configuration of both `jump` and `target` as
direct remotes in `config.json`. That requirement breaks when the intermediate
host is outside the user's administrative control, or when hosts join/leave
dynamically.

---

## Decision

Extend the read-path with a **transitive BFS walker** that follows remote
advertisements depth-first (actually level-parallel BFS) until no new hosts
are discovered or a configurable depth limit is reached.

Key design choices:

### 1. `list-remotes --json` protocol extension

Each orchard host exposes `orchard-tui list-remotes --json`, returning:

```json
{ "version": 1, "remotes": [{ "name": "...", "host": "...", "kind": "orchard-proxy", "path": "...", "allowTransitive": true }] }
```

`allowTransitive: true` on an advertised remote means "this host consents to
being walked transitively". Hosts with `allowTransitive: false` are fetched
for their snapshot but their own remotes are not followed. This prevents
accidental over-walking when an intermediate host has many unrelated remotes.

### 2. BFS walker with cycle prevention

The walker tracks a **seen-set** keyed by `host_dedup_key()` (best-effort
normalization of SSH targets, e.g. `user@host:port → host`). A host already
in the seen-set is skipped regardless of how many paths lead to it. This
guarantees termination even in the presence of cycles.

Walks are level-parallel: within a BFS level, all hosts are fetched
concurrently (bounded by `per_hop_timeout`). Across levels, work is
sequential so depth limiting and cycle detection are straightforward.

### 3. `discovery_path: Option<Vec<String>>`

Every `WorktreeState` and `SessionState` gains a `discovery_path` field: an
ordered list from `"local"` to the host, e.g. `["local", "jump", "target"]`.
This field is `None` for local worktrees and one-hop remotes that predate
transitive federation.

The `JsonWorktree` and `JsonSession` wire types also carry `discovery_path` so
the field survives cache serialization and cold-start reconstruction.

### 4. `federation_topology.json` persistence

Transitively-discovered hosts and their paths are written to
`~/.cache/orchard/federation_topology.json` after each successful walk. On
cold start, `load_cached_snapshots` reads this file to enumerate transitive
hosts and load their last-known snapshots without running SSH again.

Topology entries that are no longer present after a walk are garbage-collected
(`gc_orphan_snapshots`): their snapshot files are deleted so stale hosts don't
accumulate indefinitely.

### 5. Walker integration points

| Entry point | Walker invoked? |
|-------------|----------------|
| `orchard-tui --json` | No — cache-only (ADR-008 invariant preserved) |
| `orchard-tui refresh` | Yes — `refresh_and_build_with_walker_config` |
| `orchard-tui watch` (full poll) | Yes — `refresh_all_sources → refresh_transitive_federation` |
| `orchard-tui watch` (local poll) | No — local-only refresh |
| TUI cold start | No — reads topology + cached snapshots |

### 6. Write-path chaining (`build_ssh_chain`)

For write operations (create worktree, kill session, transfer) that must reach
a transitively-discovered host, `build_ssh_chain(discovery_path, cmd)` builds
a nested SSH invocation:

```
ssh jump "ssh 'target' 'tmux ls'"
```

The outermost hop is not quoted (it's the subprocess argument). Inner hops and
the command are POSIX single-quote-escaped with `shell_quote()`. This matches
the standard `ProxyJump`-free pattern for nested SSH.

### 7. `transitive_errors` on `OrchardState`

Walker errors (SSH failure, parse failure, timeout) are collected per-host and
surfaced on `OrchardState.transitive_errors: Vec<TransitiveError>`. The TUI
and `--json` output expose these errors so users can diagnose connectivity
issues. Partial results from successful hops are always kept — an error on one
branch does not abort the rest of the walk.

---

## Consequences

**Positive**:
- Multi-hop topologies work with zero config beyond marking the first hop
  as `"type": "orchard-proxy"` and `"allowTransitive": true`.
- Cycle detection guarantees termination; no risk of infinite loops.
- Dashboard reads remain cache-only — ADR-008's latency invariant holds.
- Topology persistence means cold starts reconstruct the full graph without SSH.

**Negative / trade-offs**:
- `discovery_path` is `None` for data loaded from pre-v7 snapshot files (no
  migration path; field appears on next successful refresh).
- The `list-remotes --json` call adds one extra SSH round-trip per intermediate
  host per `orchard-tui refresh` cycle.
- Write-path chaining via `build_ssh_chain` requires the intermediate hosts to
  allow onward SSH (not always guaranteed in locked-down environments).

**Neutral**:
- Depth limit defaults to 8 hops; overridable with `--max-depth` on
  `orchard-tui refresh`. Deep topologies are an operational choice.
- `host_dedup_key` normalization is best-effort. Two aliases for the same
  physical host that resolve differently will still be fetched twice;
  the snapshot for the second alias overwrites the first (last-writer-wins).
