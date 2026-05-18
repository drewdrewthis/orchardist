Expert Rust engineer: async CLI, TUI (ratatui), process orchestration (git/tmux/ssh), cache pipelines.

# Git Orchard

Worktree/session dashboard. Aggregates git, tmux, GitHub, SSH into TUI + JSON API. Binary: `~/Library/pnpm/orchard` → `target/release/orchard`.

## Architecture

See @docs/architecture.md. Enforce:
- Functional core, imperative shell — pure `build_state()` joins, shell fetches
- SRP, files ≤ 300 lines
- `OrchardState` = single data model (TUI + `--json`)
- ADRs in `docs/adr/`

**Repo constitution: [RULES.md](RULES.md).** 61 numbered rules (L/R/S/O/M). Read before any architectural change. Cite rule IDs in PR review (e.g. `R3 violation`). Codified in ADR-023.

Coordination: @AGENTS.md

## Rules

### GraphQL is the protocol (ADR-016, 017, 018)

Clients call daemon GraphQL. No client-side `git`/`gh`/`tmux` exec. No client-side joins or caches — daemon owns state. Subscriptions drive live updates. File a daemon issue for gaps; don't paper over client-side.

Exception: title fallback chain (`agentName → customTitle → branch → cwd → uuid`) stays client-side; GraphQL doesn't fluent-coalesce.

### Data + graph modeling (ADR-022)

Before any new resolver, provider method, fragment, or dataloader, run the 3-step gate:

1. **Name the node.** `Pane`, `Worktree`, `Conversation`, `ClaudeInstance`, `PullRequest`, ... If none fits, the first deliverable is naming a new one — not a wrapper around an old one.
2. **Name the lookup axes.** `ByID`, `ByCwd`, `ByCommand`, `BySession`. Arity in the name (`PaneByID` → one, `PanesByCommand` → many).
3. **Wire provider → dataloader → resolver.** Provider exposes typed by-axis methods that build indices on its snapshot. Loader batches per request. Resolver is a thin `Load(key)` + projection.

Smells (stop, redesign): `*Seeder` / `*Synthesizer` / `*Adapter` that emits type X to fit a reader of type Y; resolver bodies with `for` loops over provider snapshots; provider methods named `For<SpecificCaller>` instead of `By<Axis>`; the same data served through two paths.

### Tmux session ≠ Claude session

| Concept | Type | Identity | Lifetime |
|---|---|---|---|
| Tmux | `TmuxSession` | `<host>:<name>` | tmux server kill |
| Claude | `ClaudeInstance` | `<host>:<claudePid>` live, `sessionUuid` transcript | REPL exit |

Use `Worktree.tmuxPanes` / `Worktree.tmuxSession` / `Worktree.claudeInstances`. Never `Worktree.sessions`.

### Verify live daemon schema before client queries

Don't assume a field exists in the running daemon. Introspect:

```bash
curl -s -X POST http://127.0.0.1:7777/graphql \
  -H 'Content-Type: application/json' \
  -d '{"query":"{__type(name:\"<Type>\"){fields{name type{name}}}}"}'
```

"Verified live" requires daemon restart after rebuild. Test-pass ≠ deployed.

### gqlgen traps

1. **Resolver/field name collision**: type in `gqlgen.yml` `models:` generates `Resolver.<TypeName>()` method. Rename clashing provider fields (e.g. `r.ClaudeInstance` → `r.ClaudeInstanceProvider`) before adding.
2. **Stale code rescue**: snapshot `schema.resolvers.go` to `/tmp/` before every `make generate`; revert on explosion.
3. **`sortKey` rewrite**: regen overwrites our `sortKey` rename. Re-rename `sort` → `sortKey` in `internal/server/resolvers/schema.resolvers.go` after every regen.

### Heavy fields excluded from Conversation (ADR-016)

`Conversation` excludes transcripts/message bodies. Heavy reads: `GET /v1/conversations/<uuid>/jsonl`. Don't add `Conversation.transcript`.

### Config writers — CLI only

`~/.orchard/config.json` mutated only by `orchard config init|write` and `orchard init` wizard. Never written from TUI startup. New silent writer = file issue.

### Houdini cache, no client layers (ADR-019)

Use Houdini's normalized cache + `defaultCachePolicy`. No module-level Maps. No custom invalidation. Subscriptions update the cache.

### Tailwind-first (ADR-020, #544)

No new scoped CSS in orchard-gui. Tailwind classes only. Opportunistic migration on touched components.

### No hand-rolling

Use the tool. Examples: gqlgen `resolver: true`, Houdini cache, CSS flex/grid (no margin math), `#[serde(rename_all)]`, Go stdlib (`errors.Is`, `slices`, `cmp`), `@tanstack/svelte-virtual`, Tailwind.

Tool fighting you = you're using it wrong.

### GUI work requires rendering

UI changes verified by rendering, not diff inspection. Playwright snapshot or browser check before claiming done.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **git-orchard-rs** (2984 symbols, 7553 relationships, 259 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## When Debugging

1. `gitnexus_query({query: "<error or symptom>"})` — find execution flows related to the issue
2. `gitnexus_context({name: "<suspect function>"})` — see all callers, callees, and process participation
3. `READ gitnexus://repo/git-orchard-rs/process/{processName}` — trace the full execution flow step by step
4. For regressions: `gitnexus_detect_changes({scope: "compare", base_ref: "main"})` — see what your branch changed

## When Refactoring

- **Renaming**: MUST use `gitnexus_rename({symbol_name: "old", new_name: "new", dry_run: true})` first. Review the preview — graph edits are safe, text_search edits need manual review. Then run with `dry_run: false`.
- **Extracting/Splitting**: MUST run `gitnexus_context({name: "target"})` to see all incoming/outgoing refs, then `gitnexus_impact({target: "target", direction: "upstream"})` to find all external callers before moving code.
- After any refactor: run `gitnexus_detect_changes({scope: "all"})` to verify only expected files changed.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Tools Quick Reference

| Tool | When to use | Command |
|------|-------------|---------|
| `query` | Find code by concept | `gitnexus_query({query: "auth validation"})` |
| `context` | 360-degree view of one symbol | `gitnexus_context({name: "validateUser"})` |
| `impact` | Blast radius before editing | `gitnexus_impact({target: "X", direction: "upstream"})` |
| `detect_changes` | Pre-commit scope check | `gitnexus_detect_changes({scope: "staged"})` |
| `rename` | Safe multi-file rename | `gitnexus_rename({symbol_name: "old", new_name: "new", dry_run: true})` |
| `cypher` | Custom graph queries | `gitnexus_cypher({query: "MATCH ..."})` |

## Impact Risk Levels

| Depth | Meaning | Action |
|-------|---------|--------|
| d=1 | WILL BREAK — direct callers/importers | MUST update these |
| d=2 | LIKELY AFFECTED — indirect deps | Should test |
| d=3 | MAY NEED TESTING — transitive | Test if critical path |

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/git-orchard-rs/context` | Codebase overview, check index freshness |
| `gitnexus://repo/git-orchard-rs/clusters` | All functional areas |
| `gitnexus://repo/git-orchard-rs/processes` | All execution flows |
| `gitnexus://repo/git-orchard-rs/process/{name}` | Step-by-step execution trace |

## Self-Check Before Finishing

Before completing any code modification task, verify:
1. `gitnexus_impact` was run for all modified symbols
2. No HIGH/CRITICAL risk warnings were ignored
3. `gitnexus_detect_changes()` confirms changes match expected scope
4. All d=1 (WILL BREAK) dependents were updated

## Keeping the Index Fresh

After committing code changes, the GitNexus index becomes stale. Re-run analyze to update it:

```bash
npx gitnexus analyze
```

If the index previously included embeddings, preserve them by adding `--embeddings`:

```bash
npx gitnexus analyze --embeddings
```

To check whether embeddings exist, inspect `.gitnexus/meta.json` — the `stats.embeddings` field shows the count (0 means no embeddings). **Running analyze without `--embeddings` will delete any previously generated embeddings.**

> Claude Code users: A PostToolUse hook handles this automatically after `git commit` and `git merge`.

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
