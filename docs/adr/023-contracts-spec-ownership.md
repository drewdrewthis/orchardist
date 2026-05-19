# ADR-023: Contracts spec ownership belongs to the plugin + marketplace

## Status
Accepted.

## Context

The contracts subsystem — agents committing to deliverables and the persisted state machine that tracks them — has shipped breaking changes (most recently the 2026-05-19 collapse from a 9-status enum to the two-status open/closed model). Each shipping consumer (the daemon's `contracts` provider in this repo, the Stop-hook shell script in `orchard-codex`, the CLAUDE.md vocabulary, any future MCP client) discovered the new shape independently by reading the plugin source. There was no spec, no hook contract, no persisted-data contract, no version surface — only an implementation that consumers had to grep.

Three Stop-hook iterations during the 2026-05-19 session were each rejected by the user. Each iteration attempted to encode contract-state policy in the consumer (the hook). Each became an escape path the agent rationalized — soft-nudge "no work, stop allowed", skip-blocked-with-handoff via reasoning scan, `.waiting` sidecar markers. The final design removed all of them: open or closed, no half-finished states.

The pattern: when policy is implicit in the implementation, the consumer is free to invent its own policy that drifts. The cost of fixing this was paid three times in one session.

## Decision

**Spec ownership of the contracts subsystem belongs to the `claude-contracts` plugin + the `drewdrewthis/git-orchard-rs` marketplace.**

Specifically:

1. The plugin source contains a `spec/` directory ([`~/.claude/plugin-sources/claude-contracts/spec/`](https://github.com/drewdrewthis/orchard-codex/tree/main/plugin-sources/claude-contracts/spec)) that defines:
   - Valid status values and the open/closed lifecycle.
   - The on-disk JSONL event shape (every field, what is required, what is read-permissive).
   - Ownership rules (per-session, orphan-and-discoverable).
   - Cron co-location semantics.
   - The Stop-hook contract (block decision, no soft-nudge, session_id-only ownership match).
   - The persisted-data contract (storage layout, file naming, read-path-permissive vs write-path-strict).
   - The daemon-observation contract (what the daemon needs to surface).
   - Version policy (when to bump, mixed-version JSONL tolerance).
2. This repo's `plugins/marketplaces/` is the install surface. The marketplace pins a specific plugin version; fresh hosts install via the marketplace path without first cloning the codex.
3. **Downstream consumers (daemon, hooks, CLI, GUI) adopt the plugin's spec; they do not define it.** When the spec changes, the consumers update; the spec does not change to match a consumer's frozen behavior.

The daemon's `internal/server/providers/contracts/` parses on-disk events according to the spec's `event-shape.md`. The schema's `ContractStatus` enum has exactly the two values defined in `status-and-lifecycle.md`. The filter input is the one defined in `daemon-observation.md`.

## Consequences

### Positive

- A single canonical location to look up what a contract is, what is required on an event, how the Stop-hook behaves. Consumers stop reverse-engineering the implementation.
- Plugin spec changes follow a coordinated path: update spec, bump version, update consumers via PR. Each step is reviewable in isolation.
- The marketplace becomes a real version-pin surface: fresh hosts can install without cloning the codex, and the codex's `plugin-sources/` mirror divergence soft-warn can be retired.
- The daemon can drop multi-status policy code (the 9-value enum) and become a thin observer of the plugin's open/closed model. Less surface area, easier to reason about.

### Negative

- Two repos own pieces of the contracts subsystem: `drewdrewthis/orchard-codex` (plugin source + Stop-hook reference) and `drewdrewthis/git-orchard-rs` (marketplace + daemon). Coordination friction.
- Spec changes require dual commits: codex first (plugin source + spec/), then this repo (daemon + marketplace pin). A new contributor must understand both.
- The codex is currently a public repo with personal-name and absolute-path scrub rules. Adding a heavily-cross-linked spec directory there adds maintenance surface.

### Mitigations

- The spec lives in the plugin source, which lives in the codex. The codex is one git checkout; spec edits are direct-to-main per [orchard-codex ADR-014](https://github.com/drewdrewthis/orchard-codex/blob/main/adrs/014-contracts-plugin-lifecycle.md). The "coordination friction" is one PR in each repo, not a release process.
- The daemon's adoption is a one-time read-path-permissive change. After it lands, mixed-version JSONL (some events v0.6, some v0.7) is normal and no migration is needed.
- The marketplace pin update is a single-file change (`plugins/marketplaces/<name>/plugin.json`). The version-bump flow is documented in `spec/version-policy.md`.

## Alternatives considered

### Daemon owns the spec
**Rejected.** The daemon observes; the plugin defines. When the daemon owned implicit policy (the 9-value enum), spec changes required daemon coordination on every plugin tweak. The 2026-05-19 status collapse would have been a daemon PR before the plugin could ship. That inverts the dependency: the plugin is the policy, the daemon is the read.

### Both own complementary parts
**Rejected.** Split ownership re-introduces the half-finished-state problem at the spec layer. The "what does `blocked` mean now" question would have two answers. The Stop-hook iterations failed precisely because no single source of truth existed; doubling down on split ownership would recreate that failure.

### Codex stays the canonical source, marketplace publishes from there
**Compatible — this is what we're doing.** The plugin source stays in `orchard-codex/plugin-sources/claude-contracts/` per ADR-014. The marketplace in this repo (`plugins/marketplaces/`) is the **install surface**, not a fork. The codex commit is the version-pin reference; the marketplace `plugin.json` points at the codex's tagged commit / branch.

## Implementation

This ADR was committed as part of issue [#640](https://github.com/drewdrewthis/git-orchard-rs/issues/640) along with:

- The `spec/` directory in `drewdrewthis/orchard-codex/plugin-sources/claude-contracts/`.
- A rewrite of `internal/server/providers/contracts/` in this repo to parse v0.7 flat-event JSONL.
- A collapse of the daemon's `ContractStatus` GraphQL enum from 9 values to 2.
- The `plugins/marketplaces/` bootstrap pinning `claude-contracts` to v0.7.0.
- A new `orchard daemon query` CLI subcommand that proxies arbitrary GraphQL queries against the local daemon — the canonical CLI consumer of the new contract surface.

Owner-inherit on `update_contract` (AC #4 of issue #640) is tracked separately at [`drewdrewthis/orchard-codex#26`](https://github.com/drewdrewthis/orchard-codex/issues/26). The fold in this PR inherits owner correctly on read, which covers both the existing null-owner bug and any future fix.

## Open questions

- Should the marketplace surface a `compatible-daemon-version` field next to the plugin pin, so a fresh host knows whether its orchard daemon supports the plugin? Currently no version negotiation exists. Filing as follow-up if it becomes load-bearing.
- Should the spec move out of the codex's `plugin-sources/` and into a dedicated `drewdrewthis/claude-contracts` repo? Cleaner ownership boundary; more git overhead. Defer until the codex's monorepo cost outweighs the consolidation benefit.
