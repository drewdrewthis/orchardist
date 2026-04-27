# ADR-010: `--json` is the live read; the TUI keeps the cached fast path

## Status

Accepted

## Date

2026-04-27

## Context

Two consumers read orchard's unified data model:

1. **The TUI dashboard** — long-lived, redrawn many times per second, shown
   to a human. It must start instantly and tolerate stale data while a
   background refresh catches up; the human can wait a few seconds for the
   PR review counts to update without losing flow.
2. **External scripts and other agents** — `orchard --json`, the new
   `orchard sessions --json`, the orchardist's `/prune` skill, ad-hoc
   pipelines. Each invocation is a one-shot call whose answer is then acted
   on by code: `gh pr close`, `tmux kill-session`, `git worktree remove`.

These two consumers have **opposite freshness requirements**.

Pre-#374 we had ADR-008's wire protocol and a single freshness contract:
both the TUI and `--json` used `build_state_with_cached_snapshots`. That
broke `/prune`. After a `git worktree remove` the worktree lingered in
`orchard --json` until either `orchard refresh` ran or the watch daemon
caught up — anywhere from a few seconds to a few minutes. The orchardist
had no way to know which, so it gave up on the data plane and shelled out
to per-host `ssh` queries to verify state. That defeated the point of
shipping a unified CLI: every consumer was inventing its own freshness
strategy.

Issues #374 and #375 made the contract explicit: `orchard --json` must
reflect a `git worktree remove` (or any local mutation) within ~5 seconds
of the next invocation, with no manual `orchard refresh` step between them.

Three approaches we considered:

1. **Keep `--json` cache-only, add a `--refresh` flag.** Smaller diff but
   pushes the freshness decision onto every caller; `/prune` ends up wrapping
   `orchard refresh && orchard --json` everywhere, and forgetting the
   refresh is silent corruption.
2. **Watcher-driven cache invalidation.** A filesystem watcher would notice
   `git worktree remove` and re-stat. Solves the local case but doesn't
   handle remote SSH targets and adds a long-lived background process to a
   one-shot CLI. Out of scope for this PR.
3. **Make `--json` a live read.** Whatever the cost — SSH probes, local git
   re-stat, GitHub fetch — pay it on the call. Simple contract, no opt-in,
   no per-script refresh discipline.

We picked option 3. The architecture already supports it:
`build_state::refresh_and_build` is the same code path `orchard refresh`
uses. The `--json` handler now calls it, then serialises to `JsonOutput`.

## Decision

`orchard --json` and `orchard sessions --json` are **live reads**. They
synchronously refresh every reachable source — `git worktree list` per
configured repo, `tmux list-panes` locally and on every reachable remote
SSH target, `gh` for issues and PRs — before serialising. Cache files
written by previous invocations are overwritten as a side effect.

The TUI keeps the cache-fast path: cold-start renders from
`build_state_with_cached_snapshots`, then a background refresh catches up
and re-renders. None of this changes for #374.

### Mental model: cache files are the inter-service queue

Each source service writes its own cache file
(`~/.cache/orchard/{owner}_{repo}_worktrees.json`,
`~/.cache/orchard/{host}_tmux_sessions.json`, etc.). The cache is the
shared datastore between services and consumers:

- **TUI** reads the datastore at read time.
- **`--json`** tells every service to update its file, then reads.

This is the natural "update then return" pattern Drew called out — the
files are durable state, services are responsible for keeping their slice
fresh, and `--json` orchestrates the refresh fan-out across them.

### Concrete changes

- `main::build_output()` calls `build_state::refresh_and_build` instead of
  `merge_remote::build_state_with_cached_snapshots`.
- New `orchard sessions --json` subcommand routes through the same
  `refresh_and_build` path before invoking `sessions_index::build_sessions_index`.
- `--help` documents the live-read contract for both subcommands.
- `architecture.md` is updated to describe the two modes correctly.

### Latency contract

`--json` latency tracks the slowest reachable host's SSH round-trip plus
the GitHub API. Unreachable hosts are bounded by the reachability-probe
timeout in `sources::hosts`. Empty config (no remotes, no repos) returns
in tens of milliseconds — the AC7 test suite proves this and now also
guards "empty config → zero SSH calls".

If a caller needs a low-latency feed (sub-second polling, dashboards),
they should use `orchard watch` and tail `events.jsonl`, not
`orchard --json`.

## Consequences

### Pros

- **`/prune` works without ceremony.** A user-driven `git worktree remove`
  followed immediately by `orchard --json` shows the post-state correctly,
  with no `orchard refresh` step.
- **Single freshness contract per binary.** Every external consumer can
  trust `--json` without knowing about `refresh`, `watch`, or cache TTLs.
- **`--json` warms caches as a side effect.** Subsequent TUI cold-starts
  see fresher data, since `refresh_and_build` writes every cache file it
  touches.

### Cons

- **`--json` can block on SSH.** A flaky remote slows down every script
  that calls `--json`. Mitigations: per-host probe timeouts, parallel
  fan-out, no fallback (an unreachable host is reported as unreachable
  rather than retried in-band).
- **GitHub API budget.** Each `--json` triggers a `gh` call per repo.
  Heavy scripted use could exhaust the rate limit. Mitigation: `gh` caches
  responses; orchard's per-source SWR layer further damps re-fetches when
  cache is fresh.
- **Test isolation.** Tests that pre-seed the local tmux cache to assert
  classification logic stop working — the live refresh overwrites the
  seed. Tests that need fixed tmux state must spin up an isolated tmux
  socket (see `tests/json_freshness_integration.rs`'s `TmuxHarness`).

### Reversal

If the latency cost proves too high in practice, we can re-introduce a
`--cached` opt-out flag on `--json` without changing the default. That
gives sophisticated callers an escape hatch while keeping `/prune` and
ad-hoc consumers correct by default.

## Related

- ADR-001: cache architecture (per-source files, no computed state on disk).
- ADR-008: federated discovery — remote `orchard --json` is the wire protocol
  for cross-machine reads. ADR-008's freshness assumption is now consistent
  with this ADR: a remote `orchard --json` invocation is itself live,
  so a local `orchard --json` calling out to it gets a live remote view.
- Issues #374, #375.
