---
date: 2026-05-19
situation_tags: [testing, parity, principle, langwatch-pattern]
resolve_after: 2026-05-26
status: resolved
---
# Daemon feature files cover ONLY daemon-boundary scenarios; no @unimplemented escape hatch

## Goal
Stated: pick (a) or (b) for the Gherkin rebuild PR.
Real: keep the testing philosophy honest. User explicitly said "we should not use unimplemented, we should just implement" — that rules out (a) (write 172 tests, many flagged unimplemented) AND rules out godog's "pending" trick. The only honest path is to shrink the daemon's spec surface to what the daemon actually owns, then test every one of those.

## Values protocol
risk tolerance: low for principle erosion, high for scope re-cuts · time horizon: long (testing discipline is structural) · reversibility weight: very reversible (move scenarios between crate-side and daemon-side later) · whose welfare: future engineers reading the test suite

## Chosen path
(b) — agent rebuilds PR #637 with:
- godog removed; one Go test fn per scenario with `// @scenario <title>` annotation
- daemon/features/ shrinks to daemon-boundary scenarios only (~50-80 estimated, vs 172)
- TUI/GUI consumer scenarios migrate to crates/orchard/features/ and crates/orchard-gui/features/ respectively, each with its own parity checker in the appropriate runner (cargo test, Vitest/Playwright)
- bash parity checker per-tree; CI-enforced; no @unimplemented, no LEGACY_UNBOUND
- docs/testing-philosophy.md mirrors langwatch's source minus those escape hatches
- T8 in RULES.md: hard parity, no opt-out

## Autonomy verdict
decided-and-acting

## Consequences foreseen
- The agent's PR will be smaller per-tree (daemon owns ~80 tests, TUI/GUI inherit the rest)
- Some scenarios will be genuinely ambiguous about ownership boundary — fallback: client-tested wins (consumers own their own contracts; daemon only owns what it serves)
- TUI/GUI feature-parity work becomes follow-up tickets (their tests don't exist yet; this dispatch just creates the parity scaffolding in those crates)
- Removing @unimplemented means the parity checker will fail green-build the moment we're aware of an unbacked scenario — that's the intended pressure

## Consequences that materialized
- Daemon ended up with 33 scenarios (vs ~50-80 estimated) — the daemon boundary is genuinely narrower than expected
- TUI: 73 scenarios, GUI: 99 scenarios — both in client trees with parity scaffolding
- `make check-feature-parity` exits 0: daemon 33/33 PASS; TUI/GUI report gaps informally
- The scenario count dropped because the agent correctly identified that most "consumer spec" scenarios test client behavior, not GraphQL boundary behavior
- Ambiguity cases resolved: defaulted to client-tested (health query → TUI tree, lens response shapes → daemon tree)

## Outcome
PR #638 opened (https://github.com/drewdrewthis/git-orchard-rs/pull/638). PR #637 closed.

## Process-soundness
Decision was correctly framed. The key insight that "daemon owns what the GraphQL boundary serves, clients own what they do with it" was the right cut. Zero ambiguity about the principle — execution followed cleanly.

## Regrets
None. The estimate of ~50-80 daemon scenarios was high but the spirit was right. 33 clean, tested scenarios beats 172 pending ones every time.
