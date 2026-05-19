---
date: 2026-05-19
situation_tags: [testing, triage, boundary, ownership]
resolve_after: 2026-05-26
status: decided-and-acting
---
# Daemon vs client feature triage — how we split the 172 original scenarios

## Decision

We split the original 172 scenarios (24 feature files) into three trees:
- **daemon/features/daemon/** — 33 scenarios, 9 feature files (pure GraphQL boundary assertions)
- **crates/orchard/features/** — 73 scenarios, 12 feature files (TUI projection/rendering)
- **crates/orchard-gui/features/** — 99 scenarios, 12 feature files (GUI Svelte/Houdini rendering)

## Triage rule applied

For each scenario, the question was: "Who is the unit under test?"

- **Daemon**: the GraphQL resolver returns the correct shape, latency, subscription event. The daemon owns this behavior regardless of who calls it. Daemon tests run against a real `httptest.Server` with real providers.
- **TUI**: the Ratatui binary's behavior (projection, rendering, error handling, CLI mutations). These assertions are about what the Rust binary *does* with data the daemon sends.
- **GUI**: the Svelte component behavior (rendering, Houdini cache, Tauri bridge, optimistic UI state). These assertions are about what the browser/Tauri app *does* with data.

## Ambiguity resolution

The instructions said: "for genuinely ambiguous scenarios: default to CLIENT-SIDE."

Applied to:
- `gui-sidebar-boot`: has lens response shapes (daemon-testable) AND Houdini cache behavior (GUI). → We extracted the response shape scenarios into daemon feature files and left the cache/render behavior in the GUI tree.
- `tui-health-probe`: health query contract (daemon) AND ORCHARD_DAEMON_URL override (TUI client behavior). → We put health query in daemon features, ORCHARD_DAEMON_URL behavior stays in TUI features.
- `tui-dashboard-render`: workView response shape (daemon) AND TUI rendering/snapshot (TUI). → We put the response shape scenarios in `work-view-query.feature` and left rendering/snapshot in TUI features.

## Consequence: 33 daemon scenarios vs 172 original

The daemon's feature surface is genuinely smaller than the original PR assumed. The godog PR marked 163/172 as "pending" because it tried to test consumer-side behavior from the daemon side. We avoid this entirely by moving those scenarios to the consumer trees.

## Follow-up issues to file

- `feat(tui): implement @scenario tests for TUI feature parity` — 73 scenarios
- `feat(gui): implement @scenario tests for GUI feature parity` — 99 scenarios
