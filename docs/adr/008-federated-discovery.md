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
`ssh host orchard --json` and trusts the returned `JsonOutput`.

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
host orchard --json`. The existing `SshExec` seam (`ProcessSshExec` +
`FakeSshExec`) is reused; `OrchardProxyAdapter` wraps one adapter-scoped
`OnceLock<Result<JsonOutput, _>>` so `list_worktrees()` + `list_sessions()`
share one round-trip.

### Reachability

`OrchardProxy` probes with `orchard --version` (not `true`) bounded by
`PROBE_TIMEOUT = 3s`. A host that accepts SSH but lacks orchard fails this
probe and falls back.

### Fallback (un-upgraded remotes)

Every OrchardProxy call is wrapped so any `AdapterError::{FetchFailure,
ParseFailure}` — missing binary (exit 127), SSH failure (exit 255),
malformed JSON, version skew — transparently dispatches to the configured
legacy kind (`RemoteConfig.fallback_kind`, default `Remmy`). Every fallback
writes a `remote_adapter.fallback` event with host + reason. Callers see
the legacy result with no bubbled error.

The fallback is **required** for the compat story: an orchard upgrade
must not break remotes that haven't been upgraded yet.

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
- Per-host snapshot cache means TUI cold start is instant even with slow
  SSH; remote rows render from cache, then refresh in background.
- Adding a new source type on the remote (e.g. future claude enrichment
  fields) requires no local change — it rides the existing `JsonOutput`.

### Negative

- Version skew must be handled. A remote on an older schema emits an
  unknown `version`; local falls back. A remote on a newer schema that
  adds a field local doesn't understand — serde's `#[serde(default)]` on
  new fields is the mitigation, but schema additions require care.
- Remote orchard must be available and working for the fast path.
  Upgrading orchard now involves keeping the `--json` schema
  backward-compatible across rollout windows. The fallback path absorbs
  the window, but it is slower and less rich.
- `OrchardProxyAdapter` holds a `OnceLock` snapshot per instance; callers
  that want a fresh snapshot must construct a new adapter. The existing
  cache-refresh pipeline does this implicitly, but new call sites need to
  know.

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
orchard --json` works today without any new socket.

### C. Continue recomputing locally

The status-quo path. Rejected because it doesn't scale past one or two
remotes and silently diverges from the remote's own dashboard.

### D. Federate mutations too, in this PR

Kill-session, create-worktree, transfer would all route through
`ssh host orchard --<cmd>`. Deferred: read-path failures are safe
(missing rows), mutation failures are not (orphaned resources, dual
writes). Mutations deserve their own ADR and their own rollout.

## Related

- ADR-001 — cache architecture (snapshot cache extends this pattern)
- ADR-004 — unified data model (the `JsonOutput` that this ADR promotes
  to a wire protocol)
- Issue #329 — implementation
