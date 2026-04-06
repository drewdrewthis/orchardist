# Agent Guidelines

## Common Mistakes & Corrections

| Mistake | Correct Behavior |
|---------|-----------------|
| Running `cargo clean` — this deletes the release binary that the user's symlink points to (`~/Library/pnpm/git-orchard` → `target/release/git-orchard`). The user's live command breaks until `cargo build --release` runs. | Never run `cargo clean` unless explicitly asked. If you must, immediately `cargo build --release` after. |
| Agents finishing at different times overwrite each other's changes to shared files (e.g., `tui/list.rs`, `tui/mod.rs`). Late-arriving agents revert fixes made after earlier agents completed. | Never run two agents that modify the same file in parallel. Sequence them, or have the second agent read the file fresh before editing. |
| Test assertions that are wrong (e.g., `backlog_pagination_second_page` expected 15 items but correct answer is 10). Agent writes the test, orchestrator trusts it, but the test fails. | Orchestrator must run `cargo test` after EACH agent completes, not just at the end. Fix failures before launching the next agent. |
| Tmux runtime bindings (`tmux bind-key`) take precedence over config-file bindings. Running `tmux source-file` does NOT override a runtime binding. | Always `tmux unbind-key <key>` before `tmux source-file` when replacing a binding. |
| Tmux hooks from the old architecture (`session-closed[99]`) persist across config reloads and can undo new bindings when legacy sessions die. | Explicitly `set-hook -gu session-closed[99]` during cleanup. |
| Adding `mod foo;` to `main.rs` but the agent's code has compile errors — the build breaks for everyone. | Agents should produce compilable code. Orchestrator must verify `cargo build` after each agent, not batch. |
| Multiple agents both add `mod` declarations to `main.rs`, creating duplicates or conflicts. | Only one agent should touch `main.rs` at a time, or a single agent should handle all `mod` declarations. |
| Agent creates methods referenced in `mod.rs` but puts them in a file (`dialogs.rs`) that the calling code can't see due to missing `impl` blocks or imports. | Agent must verify that all referenced methods exist in the correct `impl` block and are importable from the call site. |
| Dead code warnings accumulate because new modules aren't wired into the app yet. | Expected during staged implementation. Don't treat warnings as errors during development, but clean them up before the final review. |
| `#[serde(rename_all = "snake_case")]` on enums — Rust's `InProgress` serializes as `in_progress` via serde, but `format!("{:?}", status)` gives `InProgress`. Using debug formatting for event logging produces wrong strings. | Use a dedicated `fn status_str(s: TaskStatus) -> &'static str` for display/logging, not `Debug` formatting. |
| Leaving superseded code in place during refactors ("it still compiles, tests pass"). Led to two parallel PR pipelines and three separate issue fetch paths doing overlapping work. | Every refactor that introduces a replacement must include a kill list. Old code is removed in the same PR or a follow-up issue is created and linked. Dead code paths compound into architectural debt. |
| Copying legacy behavior into new code "for consistency" without questioning it. `issue_state` was suppressed when a PR existed because the old collector did it — perpetuating a bug across both pipelines. | "Mirrors legacy behavior" is a red flag, not a justification. Always ask: was the old behavior correct? Data layers must collect all available data — filtering and priority are display-layer concerns. |
| Code reviews that only check "does the new code work?" without checking for dead code, unused data, or duplicate pipelines. `CachedPr.linked_issue` was fetched via GraphQL but never consumed by derive. | Reviews must audit: (1) Does this change supersede existing code? Is the old code removed? (2) Is all fetched/stored data actually consumed downstream? (3) Are data-layer functions suppressing information that other layers need? |

## Architecture Notes

### Popup Model (current)
- The binary launches a TUI directly — no dedicated tmux session
- On Enter: creates worktree/session if needed, prints session name to stdout, exits
- A wrapper script (`~/.local/bin/orchard-popup`) captures stdout and runs `tmux switch-client`
- The tmux keybinding is: `bind-key o display-popup -E -w 90% -h 80% "$HOME/.local/bin/orchard-popup"`
- `q`/`Escape` exits with empty stdout (no switch)

### State System
- `~/.local/state/git-orchard/state.json` — persistent task state (atomic writes)
- `~/.local/state/git-orchard/events.jsonl` — structured event log (append-only, rotates at 50MB)
- `~/.local/state/git-orchard/status.txt` — tmux status bar segment (written on each refresh)
- `~/.local/state/git-orchard/debug.log` — low-level debug output

### File Ownership (which agents can touch which files)
- `src/main.rs` — orchestrator only (mod declarations, CLI dispatch)
- `src/tui/mod.rs` — one agent at a time (App struct, event loop)
- `src/tui/list.rs` — one agent at a time (rendering, key handlers)
- `src/tui/dialogs.rs` — one agent at a time (dialog rendering)
- `src/tui/state.rs` — one agent at a time (ViewState enum)
- `src/state.rs`, `src/events.rs`, `src/issue_sync.rs`, `src/session_discovery.rs` — independent, safe to parallelize
- `src/tmux.rs`, `src/remote.rs`, `src/git.rs`, `src/github.rs` — independent, safe to parallelize
- `Cargo.toml` — orchestrator only (dependency changes)

## Testing Rules

- Run `cargo test && cargo build --release` after EVERY agent completes
- Fix failures immediately before launching the next agent
- Never trust agent-written test assertions without verifying the logic
- Dead code warnings are OK during staged implementation but must be zero at final review

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **git-orchard-rs** (2267 symbols, 5909 relationships, 198 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

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
