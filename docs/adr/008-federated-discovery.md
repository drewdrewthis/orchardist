# ADR-008: Federated discovery — remote orchard is the authority

## Status

Accepted

## Date

2026-04-21

## Context

Orchard aggregates worktree, session, PR, issue, and Claude state across
machines. Pre-federation, the local orchard reached each remote over SSH and
ran the join pipeline itself: `git worktree list --porcelain`, `tmux
list-sessions`, plus `gh` calls for PR/issue enrichment, then folded
everything together in `build_state()`.

That worked when only one machine ran orchard. As more machines run orchard
(Boxd VMs, dev boxes, shepherd hosts), each remote is already computing the
same joins for its own dashboard — and the local orchard re-does the same
work over SSH, paying latency for every enrichment lookup.

Three pressures surfaced together:

1. **Wasted work.** The remote already knows its PRs, issues, claude state,
   CI check status, and display group. Recomputing them locally is a second
   round of `gh api` calls plus local cache reads for the same answers.
2. **Latency budget.** Raw `git worktree list --porcelain` plus per-repo
   GitHub calls scales linearly with remote count. Proxying through a
   remote's `--json` collapses that to one SSH round-trip per host.
3. **Trust gap.** Only the remote can observe its own claude hook files
   and local tmux state. The local orchard was approximating — sometimes
   incorrectly — what the remote actually sees.

## Decision

The remote orchard is the **authority** on its own worktrees, sessions, and
enrichment. The local orchard proxies read-path discovery through
`ssh host orchard-tui --json` and trusts the returned `JsonOutput`.

### Wire protocol

`JsonOutput` (defined in `crates/orchard/src/json_output_types.rs`) is the
protocol. It is:

- **Versioned** — `version: u32`. Local code checks against
  `SUPPORTED_JSON_OUTPUT_VERSIONS`; unknown → `AdapterError::ParseFailure`
  → fallback.
- **Already joined** — carries `pr`, `issue`, `claude`, `check_state`,
  `display_group` fields computed by the remote.
- **`Deserialize`-able** locally — the same derives that support
  `--json` output now also support ingest.

### Merge invariant

`merge_remote_snapshot()` in `crates/orchard/src/merge_remote.rs` folds
remote snapshots into `OrchardState` **without** calling `derive_all_repos`
or any PR/issue/claude join function over remote-sourced worktrees. Remote
entries are tagged with `host`; duplicates collapse by `(host, path)`
with preference `proxy > legacy`.

### Transport

SSH. Not a new HTTP endpoint, not a unix socket, not a custom RPC — `ssh
host orchard-tui --json`. The existing `SshExec` seam (`ProcessSshExec` +
`FakeSshExec`) is reused; `OrchardProxyAdapter` wraps one adapter-scoped
`OnceLock<Result<JsonOutput, _>>` so `list_worktrees()` + `list_sessions()`
share one round-trip.

### Reachability

`OrchardProxy` probes with `orchard-tui --version` (not `true`) bounded by
`PROBE_TIMEOUT = 3s`. A host that accepts SSH but lacks orchard fails this
probe and falls back.

### Failure handling (no silent fallback)

OrchardProxy failures surface explicitly. On any `AdapterError::{FetchFailure,
ParseFailure}` — missing binary (exit 127), SSH failure (exit 255),
malformed JSON, version skew — the adapter:

1. Returns the error up the refresh pipeline (does **not** invoke any
   other adapter kind).
2. Writes a `remote_adapter.proxy_failure` event to `events.jsonl` with
   the host, reason, and (for parse failures) a bounded UTF-8-safe
   snippet of the payload.
3. Leaves the last-known `{host}_orchard_snapshot.json` on disk so
   `build_state_with_cached_snapshots` still surfaces its contents to
   the caller. The dashboard shows stale data rather than nothing.

There is no silent downgrade to legacy shell-discovery. If a user wants
legacy behaviour for a specific host, they reconfigure that remote as
`"type": "remmy"` — an explicit opt-out, not a hidden default. This:

- Keeps the upgrade path honest: un-upgraded remotes surface as errors,
  not as "looks fine but subtly different enrichment."
- Eliminates `FallbackAdapter` / `RemoteConfig.fallback_kind` as
  permanent code debt carried for a transitional concern.
- Prevents papering over real remote failures (crashed orchard, broken
  SSH) with silently different data.

### Caching

`~/.cache/orchard/{safe_host}_orchard_snapshot.json` persists the raw
`JsonOutput` on every successful fetch. Atomic tmp→rename write. Cold
start reads and pre-populates `OrchardState` before SSH completes.
Version-skew snapshots are treated as absent on read and overwritten by
the next refresh.

### Scope: read path only

This ADR covers discovery (`list_worktrees`, `list_sessions`). Mutating
operations — create worktree, kill session, transfer — continue to flow
through the existing Remmy / BoxdShared / BoxdFork adapter paths. A future
ADR may extend federation to mutations, but the blast radius and failure
modes are different (idempotency, two-phase commit, partial failure) and
warrant a separate decision.

### Webhook federation

Out of scope. Each machine runs its own `webhook-serve` daemon; federating
webhook event streams between machines is a different problem.

## Consequences

### Positive

- Remote enrichment is computed once, by the authority. Local orchard
  never makes `gh api` calls about remote repos.
- SSH round-trips per remote collapse from N (one per source) to 1.
- Per-host snapshot cache means TUI cold start is instant; remote rows
  render from cache, then refresh in background via `orchard-tui watch` or
  `orchard-tui refresh`.
- Dashboard reads (`orchard-tui --json`, TUI render) never block on network.
  Unreachable hosts cannot delay a read, regardless of probe timeouts.
- Adding a new source type on the remote (e.g. future claude enrichment
  fields) requires no local change — it rides the existing `JsonOutput`.
- No `FallbackAdapter` / `fallback_kind` code debt. Failures surface
  explicitly; users opt into legacy behaviour per-host with
  `"type": "remmy"` if they want it.

### Negative

- Version skew must be handled. A remote on an older schema emits an
  unknown `version`; local emits a `remote_adapter.proxy_failure` event
  and keeps the last-known snapshot visible. A remote on a newer schema
  that adds a field local doesn't understand — serde's `#[serde(default)]`
  on new fields is the mitigation, but schema additions require care.
- Remote orchard must be available and working for fresh data. If it's
  down, users see the last-known snapshot plus an error event — never
  silently stale-but-plausible data from a different code path.
- Users running `orchard-tui --json` get cache-only output. For fresh data
  they must run `orchard-tui refresh` first (or have `orchard-tui watch`
  running). This is a deliberate trade — reads are instant, freshness
  is explicit.
- `OrchardProxyAdapter` holds a `OnceLock` snapshot per instance; callers
  that want a fresh snapshot must construct a new adapter. The existing
  refresh pipeline does this implicitly; new call sites need to know.

### Neutral

- The `--json` schema is now a cross-machine interface. The existing
  `schemars`-generated schema is already a commit-tracked artifact; this
  ADR promotes that artifact's stability from "nice to have" to
  "contract."

## Alternatives considered

### A. HTTP RPC between orchards

Running an HTTP server on each orchard machine and hitting it over
`localhost`-forwarded TCP or an internal hostname. Rejected:

- Adds a daemon lifecycle (listen port, auth, TLS, restart).
- SSH already provides auth, multiplexing, and timeouts via the existing
  `ProcessSshExec` path.
- `--json` already exists and is production-grade.

### B. Unix socket per machine

Each orchard binary exposes a socket; a fleet control plane forwards over
SSH tunnels. Rejected for the same lifecycle reasons — plus, `ssh host
orchard-tui --json` works today without any new socket.

### C. Continue recomputing locally

The status-quo path. Rejected because it doesn't scale past one or two
remotes and silently diverges from the remote's own dashboard.

### D. Federate mutations too, in this PR

Kill-session, create-worktree, transfer would all route through
`ssh host orchard-tui --<cmd>`. Deferred: read-path failures are safe
(missing rows), mutation failures are not (orphaned resources, dual
writes). Mutations deserve their own ADR and their own rollout.

## Structural invariants

The following source-level invariants enforce the "no silent fallback" decision
and are pinned by `crates/orchard/tests/ac6_no_fallback.rs`. If a future
refactor re-introduces either symbol, those tests fail and point the author at
this ADR.

- **No `FallbackAdapter` type** — the type was removed as part of AC6.
  A future adapter for a different remote kind must be named explicitly;
  `Fallback` implies an automatic downgrade that this ADR explicitly rejects.
- **No `fallback_kind:` field on `RemoteConfig`** — the field was removed
  alongside `FallbackAdapter`. Users who want legacy behaviour for a specific
  host set `"type": "remmy"` explicitly; there is no per-host implicit
  downgrade.
- **`OrchardProxyAdapter` has no `fallback` field** — the proxy adapter either
  succeeds or returns an `AdapterError`. It never silently delegates to another
  adapter kind.

These invariants are documented here (prose) and enforced in
`crates/orchard/tests/ac6_no_fallback.rs` (machine-checked). The BDD feature
file (`specs/features/federated-orchard-discovery.feature`, AC6 section)
references this section rather than duplicating the scenario as a grep test.

## Related

- ADR-001 — cache architecture (snapshot cache extends this pattern)
- ADR-004 — unified data model (the `JsonOutput` that this ADR promotes
  to a wire protocol)
- Issue #329 — implementation
