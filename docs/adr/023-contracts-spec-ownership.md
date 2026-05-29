# ADR-023: Contracts spec ownership belongs to the plugin + marketplace

## Status
**Superseded (2026-05-29):** the conversation-contracts plugin was retired entirely in favor of a single Stop hook (`audit-promise-stop.sh`, two terminal states: `<i-am-done>` | `<waiting-for>`) plus native TaskList. The marketplace is now empty. Rationale: the plugin became a 10-PR discipline-gateway thrash (v0.9→v0.12.2) reinventing what TaskList + one hook do natively; zero external consumers of its sentinels (surveyed 2026-05-29). See codex `research/2028-2026-05-29-replace-contracts-with-tasklist.md`. Retained as historical record of the spec-ownership principle, which still holds for any future plugin.

Accepted (2026-05-19). Amended 2026-05-28 (PR #666): the daemon-side `contracts` provider was deleted entirely — the v0.9 plugin emits sentinels into the session jsonl and folds them locally via `scripts/fold-contracts.sh`, with no daemon participation. The principle this ADR articulates is unchanged and now stronger: the plugin is the sole owner of contract spec, lifecycle, and on-disk shape. If cross-session observability is later wanted, it should ride on top of `ClaudeSession` (an "enhanced session" derived field), not a separate node type.

## Context

The contracts subsystem — agents committing to deliverables and the persisted state machine that tracks them — has shipped breaking changes (most recently the 2026-05-19 collapse from a 9-status enum to the two-status open/closed model). Each shipping consumer (the daemon's `contracts` provider in this repo, the Stop-hook shell script in `orchard-codex`, the CLAUDE.md vocabulary, any future MCP client) discovered the new shape independently by reading the plugin source. There was no spec, no hook contract, no persisted-data contract, no version surface — only an implementation that consumers had to grep.

Three Stop-hook iterations during the 2026-05-19 session were each rejected by the user. Each iteration attempted to encode contract-state policy in the consumer (the hook). Each became an escape path the agent rationalized — soft-nudge "no work, stop allowed", skip-blocked-with-handoff via reasoning scan, `.waiting` sidecar markers. The final design removed all of them: open or closed, no half-finished states.

The pattern: when policy is implicit in the implementation, the consumer is free to invent its own policy that drifts. The cost of fixing this was paid three times in one session.

## Decision

**Spec ownership of the contracts subsystem belongs to the `conversation-contracts` plugin + the `drewdrewthis/git-orchard-rs` marketplace.**

Specifically:

1. The plugin source contains a `spec/` directory inside `plugins/conversation-contracts/spec/` that defines:
   - Valid status values and the open/closed lifecycle.
   - The on-disk JSONL event shape (every field, what is required, what is read-permissive).
   - Ownership rules (per-session, orphan-and-discoverable).
   - Cron co-location semantics.
   - The Stop-hook contract (block decision, no soft-nudge, session_id-only ownership match).
   - The persisted-data contract (storage layout, file naming, read-path-permissive vs write-path-strict).
   - The daemon-observation contract (what the daemon needs to surface).
   - Version policy (when to bump, mixed-version JSONL tolerance).
2. This repo's `.claude-plugin/marketplace.json` is the install surface. The marketplace pins a specific plugin version; fresh hosts install via the marketplace path without first cloning any codex.
3. **Downstream consumers (daemon, hooks, CLI, GUI) adopt the plugin's spec; they do not define it.** When the spec changes, the consumers update; the spec does not change to match a consumer's frozen behavior.

The daemon's `internal/server/providers/contracts/` parses on-disk events according to the spec's `event-shape.md`. The schema's `ContractStatus` enum has exactly the two values defined in `status-and-lifecycle.md`. The filter input is the one defined in `daemon-observation.md`.

## Consequences

### Positive

- A single canonical location to look up what a contract is, what is required on an event, how the Stop-hook behaves. Consumers stop reverse-engineering the implementation.
- Plugin spec changes follow a coordinated path: update spec, bump version, update consumers via PR. Each step is reviewable in isolation.
- The marketplace in this repo (`.claude-plugin/marketplace.json`) is a real version-pin surface: fresh hosts can install without cloning any codex.
- The daemon can drop multi-status policy code (the 9-value enum) and become a thin observer of the plugin's open/closed model. Less surface area, easier to reason about.

### Negative

- Spec and daemon code live in the same repo but in distinct directories. Contributors must understand both `plugins/conversation-contracts/` and `internal/server/providers/contracts/`.
- Spec changes require coordinated updates: plugin source first, then daemon + consumer code in the same PR.

### Mitigations

- Plugin source and daemon live in the same git checkout. Spec edits and daemon updates land in the same PR. Coordination friction is one review, not two repos.
- The daemon's adoption is a one-time read-path change to the session-JSONL fold; no migration of historic events is needed.
- The marketplace pin update is a single-file change (`.claude-plugin/marketplace.json`). The version-bump flow is documented in `spec/version-policy.md`.

## Alternatives considered

### Daemon owns the spec
**Rejected.** The daemon observes; the plugin defines. When the daemon owned implicit policy (the 9-value enum), spec changes required daemon coordination on every plugin tweak. That inverts the dependency: the plugin is the policy, the daemon is the read.

### Both own complementary parts
**Rejected.** Split ownership re-introduces the half-finished-state problem at the spec layer. The Stop-hook iterations failed precisely because no single source of truth existed; doubling down on split ownership would recreate that failure.

### orchard-codex stays the canonical source
**Rejected for this plugin.** The `conversation-contracts` plugin (v0.8) is orchard-specific: it uses the orchard daemon's GraphQL to check for open contracts, and its ContractFold projection lives in the daemon. Placing the source outside this repo would break the "plugin + daemon update in one PR" invariant. The orchard-codex copy of `claude-contracts` is retired; this repo is now the canonical home.

## Implementation

This ADR was committed as part of issue [#650](https://github.com/drewdrewthis/git-orchard-rs/issues/650) along with:

- The `plugins/conversation-contracts/` plugin scaffold (marketplace.json, plugin.json, hooks.json).
- The `.claude-plugin/marketplace.json` marketplace bootstrap at the repo root.
- A rewrite of `internal/server/providers/contracts/` to the v0.8 jsonl-fold model.
- A collapse of the daemon's `ContractStatus` GraphQL enum from 9 values to 2 (SIGNED, CLOSED).
- The ContractFold projection scanning `~/.claude/projects/*/*.jsonl` for `open_contract` and `close_contract` tool_use events.

## Open questions

- Should the marketplace surface a `compatible-daemon-version` field next to the plugin pin, so a fresh host knows whether its orchard daemon supports the plugin? Currently no version negotiation exists. Filing as follow-up if it becomes load-bearing.
