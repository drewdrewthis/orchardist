# ADR-023: Repo constitution (ecosystem model + design rules)

**Status:** Accepted
**Date:** 2026-05-18

## Context

PR #740 (sidebar redesign) shipped with bundled work spanning 5 domains: ADR-022 pane-first refactor, batched PR enrichment, `sendTextToPane` mutation, PWA shell, mobile layout. The post-merge `/review` surfaced 16 concerns — most of them cross-cutting patterns that no PR-level review had caught:

- **Snapshot() in field resolvers** caused ~60s cold lens loads (#742)
- **Duplicate code paths** for single-vs-batch enrichment in `gh` (#745)
- **Half-removed subsystems** (chat) hydrating on boot with no UI (#746)
- **Mutations shipped without auth gating** (`sendTextToPane`)
- **Schema fields with dishonest names** (`chatMute` consumed for Claude REPL pings)
- **Tautological tests** (assertions that always pass)

The pattern across all of these: the daemon's **categorical layout** (`internal/server/{providers,resolvers,loaders,graphql}/`) made every change a 4-directory edit, with no module-level owner to enforce cross-cutting quality. Each pattern violation was a true bug, but none was a single-PR review finding — they only became visible when reading across files.

The deeper observation: the daemon, CLI, GUI, and TUI grew without a shared dependency model. The TUI and CLI bypassed the daemon for any write (because the daemon's mutation surface was thin), which meant tmux/git logic was reimplemented per consumer, and drift followed.

## Decision

Adopt two repo-level documents as **authoritative**:

1. **[docs/architecture.md](../architecture.md)** — describes the ecosystem (CLI, daemon, GUI, TUI, scripts), the dependency invariants, and each daemon domain.
2. **[RULES.md](../../RULES.md)** — the 76-rule constitution: 12 layer rules (L1–L11 + L2c), 17 code patterns (R1–R17), 20 schema rules (S1–S14 + S15a/b/c + S16a/b/c), 12 optimization rules (O1–O12), 7 mutation-coverage rules (M1–M7), 8 testing rules (T1–T8). Rules are numbered with stable IDs for citation.

These documents are **load-bearing**, not aspirational. Every PR is reviewed against them. Architectural changes cite the relevant rule IDs. Domain refactors must demonstrate conformance.

The ecosystem model has 4 citizens:

- **`scripts/`** is the canonical home for operations (tmux send-keys, worktree create). Picks the right language per script.
- **CLI** wraps scripts. Standalone (works without daemon).
- **Daemon** wraps scripts for mutations; resolves queries in-process from cached projections of external truth.
- **GUI + TUI** consume the daemon only, via GraphQL.

The daemon does not persist state. Its caches are rebuildable projections of external truth.

GraphQL mutation semantics carry their own contract: mutations return affected nodes, clients update their normalized cache from the response, subscribers see deltas via subscriptions. No special daemon-side cache invalidation on mutate.

## Consequences

**Positive:**

- New PRs have a fixed rubric to be reviewed against. "This violates R3 (no Snapshot in field resolvers)" is faster than "this looks wrong."
- The daemon module refactor (#743) has a concrete spec: bring each domain up to constitution conformance. The 74 rules ARE the acceptance criteria.
- Cross-cutting smells get a name and a stable citation. Audits compose: if `tmux` violates R3, `gh` might too, and the fix shape is the same.
- The 4-citizen model collapses the "where does this logic live?" question. Scripts. Always scripts. CLI and daemon wrap.
- GUI/TUI duplication eliminated by construction (they consume daemon-only).

**Negative / costs:**

- 74 rules is a lot to internalize. Mitigated by stable IDs and category structure: most PRs only touch ~5 relevant rules at a time.
- The script-as-canonical pattern requires extracting current inline logic into standalone scripts. That's its own refactor, parallel to #743.
- "No persisted daemon state" rules out some optimizations (warm-start caches). Cold start cost becomes load-bearing (O5).
- Future schema or layer changes that need to evolve a rule require an ADR + RULES.md amendment in the same PR. More ceremony for architectural changes.

**Neutral / explicit non-goals:**

- These rules apply to the orchard ecosystem only. Vendored code, generated code (gqlgen, Houdini), and test fixtures are out of scope.
- Cross-host federation rules are not codified here. Federation patterns (peer auth, schema-version compat across hosts) deserve their own ADR once the federation work matures.

## Alternatives considered

1. **Document patterns informally in CLAUDE.md.** Rejected: CLAUDE.md is loaded into every Claude session — adding 74 rules bloats every conversation's context. Rules belong in `RULES.md` (loaded on demand), cited from CLAUDE.md.

2. **Per-domain rules in each module.** Rejected: cross-cutting rules are cross-cutting. A repo-level constitution is the right scope; per-module specs (`.feature` files, `AUDIT.md`) layer ON TOP of the constitution.

3. **Wait until daemon module refactor lands.** Rejected: refactor agents need a written target to refactor TOWARD. The constitution must precede the refactor.

4. **Skip the audit checklist; just refactor.** Rejected — see #740's `/review` findings. Without a written rubric, "refactor" is unbounded.

## References

- [docs/architecture.md](../architecture.md) — the ecosystem and domain descriptions
- [RULES.md](../../RULES.md) — the 76-rule constitution
- #743 — daemon module refactor (the first major application of these rules)
- #740 — sidebar redesign PR; `/review` of it surfaced the patterns this ADR codifies
- ADR-013 — orchard CLI ecosystem (predecessor; defined the multi-binary `orchard` shape)
- ADR-016 — daemon as protocol (GraphQL-only; predecessor of the "GUI/TUI consume daemon-only" rule)
- ADR-022 — data and graph modeling (pane-first; immediately predates this ADR)
