# `daemon/ps/`

OS process table — read-only. (pid, ppid, cwd, command, args.)

## Owns

- **Types:** `Process`
- **Inputs:** `ProcessFilter`
- **Subscriptions:** `processes` (snapshot fan-out on invalidation)
- **Query (pass-through, S16):** `ps(tool: PsTool!, args): JSON` — arbitrary `ps` or `lsof` invocation
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Field extensions

- `extend type Host { processes(filter: ProcessFilter): [Process!]! }` — Host owns the field; the resolver here applies the filter against the cached snapshot

## Cross-domain back-edges (declared here, resolved by calling owning service)

| Field | Owning domain |
|---|---|
| `Process.worktree` | [`git`](../git/) (deepest worktree whose `path` prefixes the resolved cwd) |
| `Process.claudeInstance` | [`claude-jsonls`](../claude-jsonls/) (set when this pid is the foreground claude pid) |

## Slow paths are opt-in

- `Process.args` — separate `ps -wwax -o pid,args` lookup
- `Process.cwd` — `lsof` on macOS, `/proc/<pid>/cwd` on Linux. Only fires when in the selection set. Coalesced by the `cwdLoader` into one batched `lsof -p <pids> -F n` per request.

Per [S10](../../RULES.md): hot fields are cheap, expensive fields require explicit opt-in by being in the selection set.

## Current source location (pre-refactor)

- `internal/server/providers/ps/`

## Constitution citations

- [L4](../../RULES.md): query path is in-process; no script exec for reads
- [L9](../../RULES.md): no persisted state — process table is re-observed every poll
- [O10](../../RULES.md): I/O batching at the boundary — multiple cwd lookups → one `lsof` call
- [R3](../../RULES.md): `cwdLoader` exists AND is consumed (the failure mode is "loader exists but field bypasses it" — see #612)
- [O7](../../RULES.md): `Subscription.processes` fan-out is bounded (cached snapshot, not per-process re-fetch)
