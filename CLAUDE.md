You are an expert Rust engineer. pecialty: async CLI tools, TUI (ratatui), process orchestration (git/tmux/ssh), and cache-driven data pipelines.

# Git Orchard

Worktree/session dashboard: aggregates git, tmux, GitHub, and SSH into a unified TUI and JSON API.

Release binary symlinked: `~/Library/pnpm/orchard` → `target/release/orchard`.

## Architecture

See @docs/architecture.md for the full picture. Constraints to enforce:

- **Functional Core, Imperative Shell** — pure `build_state()` joins data, shell modules fetch it
- **SRP** — files ≤ 300 lines, one responsibility per module/function
- **`OrchardState`** is the single unified data model for TUI and `--json`
- ADRs in `docs/adr/`

Agent coordination rules and common mistakes: @AGENTS.md

## Project-Specific Rules (learned the hard way)

### Daemon-first joins — don't double-build on the frontend

**Rule**: If a join belongs to the daemon (cwd→worktree, sessionUuid→Conversation, lastSeenAt→ClaudeInstance), expose it on the daemon. Do NOT re-implement the join in TS lens projections.

- Bad pattern: `const byUuid = new Map(); for (c of conversations) ...` repeated across 5 lens files.
- Good pattern: query `claudeInstance { worktree { ... } conversation { ... } }` — one daemon trip, no client-side glue.
- The frontend stays a render layer. GraphQL is "ask for what you need," not "fetch slices and re-join."
- Title fallback chain (agentName → customTitle → branch → cwd → uuid) is the rare exception that stays client-side — GraphQL doesn't fluent-coalesce.

### Tmux session ≠ Claude session

These are distinct concepts in the schema and must NOT be conflated in field names:

| Concept | Type | Identity | Lifetime |
|---|---|---|---|
| Tmux session | `TmuxSession` | `<host>:<name>` | Until tmux server kills it |
| Claude session | `ClaudeInstance` | `<host>:<claudePid>` (live) + `sessionUuid` (transcript) | Until Claude REPL exits |

A worktree can carry both, neither, or several of each. Don't add `Worktree.sessions` — use `Worktree.tmuxPanes` / `Worktree.tmuxSession` (already exists, #511) and `Worktree.claudeInstances` (when added).

### Verify the live daemon schema before writing client queries

Don't assume a field exists because it's in `schema.graphql` — the daemon may not have been regenerated/restarted. Verify with introspection:

```bash
curl -s -X POST http://127.0.0.1:7777/graphql \
  -H 'Content-Type: application/json' \
  -d '{"query":"{__type(name:\"<Type>\"){fields{name type{name}}}}"}'
```

Daemon-first hook fires on every `gh` call — check `~/.claude/references/orchard-daemon.md` for daemon coverage. File an issue when there's a gap rather than papering over it client-side.

### gqlgen traps

1. **Resolver-method/provider-field collision**: Adding a type to `gqlgen.yml` under `models:` makes gqlgen generate a `func (r *Resolver) <TypeName>() <TypeName>Resolver` method. If `Resolver` already has a *field* of the same name (e.g. `ClaudeInstance *claudeinstance.Provider`), build breaks with "field and method with the same name." Fix: rename the provider field (`r.ClaudeInstance` → `r.ClaudeInstanceProvider`) before adding the type to gqlgen.yml, OR avoid the resolver-method codegen by binding the field directly.

2. **Stale-code "rescue"** — when you delete a resolver function before regen, gqlgen may dump its body as raw text into `schema.resolvers.go` outside any function, breaking the build. Mitigation: snapshot `schema.resolvers.go` to `/tmp/` before every `make generate`. If the regen explodes, revert.

3. **`sortKey` arg rewrite**: gqlgen regenerates the parameter named `sort` (matching schema) on every run. Our resolver renames it to `sortKey` to avoid shadowing the imported `sort` package. After every regen, re-rename `sort` → `sortKey` in the generated resolver signature. Search `internal/server/resolvers/schema.resolvers.go` for `sort *graphql1.TmuxSessionSort` after regen.

### Heavy fields excluded from Conversation by design

Per schema docs (lines 882–887): `Conversation` deliberately excludes full transcripts and message bodies. Heavy reads go through `GET /v1/conversations/<sessionUuid>/jsonl` (HTTP, last-N bytes from end of file). Don't add a `Conversation.transcript` GraphQL field — it would violate the v1 contract. Virtualize the existing reader instead.

### Config writers must be explicit CLI/manual ONLY

`~/.orchard/config.json` may only be mutated by:
1. `orchard config init` / `orchard config write` (CLI in `internal/cli/config/config.go`)
2. `orchard init` wizard (CLI in `crates/orchard/src/shell.rs`)

`orchard-tui` startup must NOT write the config. The earlier `register_cwd_repo_if_new` call from `App::new` was removed (#545). If you find a new silent writer, file an issue.

### No-handrolling rule (Drew, 2026-05-10)

If a tool/library does the job, use it. Don't hand-roll:
- gqlgen has `resolver: true` per field — use it instead of writing your own dispatch.
- Houdini has `defaultCachePolicy` — use it instead of caching layers.
- CSS gap/flex — use it instead of margin math.
- Rust `#[serde(rename_all)]` — use it instead of bespoke `Deserialize`.
- Go stdlib utilities (`gofmt`, `errors.Is`, `slices`, `cmp`) — use them.
- Virtualization (`@tanstack/svelte-virtual`) — use it instead of manual visible-window math.
- Tailwind (when adopted, #544) over hand-rolled scoped CSS.

When the tool fights you, slow down — likely you're using it wrong, not the tool being broken. (cf. the gqlgen schema-drift fight that wasted a session.)

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
