# Workstream A — orchard daemon scaffold

You are an orchard worker session. Your job is to deliver the Workstream A scaffold for the new Go-language `orchard` daemon. This is a **code repo** — you commit on a branch and open a PR; you do NOT commit directly to main.

## Read these documents BEFORE doing anything else

1. **`/Users/USER/workspace/orchard-codex/adrs/011-orchard-node-model-and-providers.md`** — canonical design for what orchard is. The 14-node graph, providers/resolvers/adapters, federation, no mutations.
2. **`/Users/USER/workspace/orchard-codex/plans/2026-05-04-orchard-implementation-guide.md`** — implementation guide. Section "Workstream A — Scaffold" is your AC list. Section "Monorepo layout" is your file tree. Section "CLI shape" is your subcommand structure. Section "Toolchain" is your dependency choices.
3. **`/Users/USER/workspace/orchard-codex/references/contracts.md`** — Contract spec. You don't implement contracts in WS-A but should understand the shape.

If those documents conflict, the ADR wins.

## Repo orientation

You're in `~/workspace/git-orchard-rs/.worktrees/ws-a-scaffold/` on branch `ws-a-scaffold`. The repo root is **polyglot** — Rust workspace already exists (`Cargo.toml`, `crates/`). You are adding the **Go side** alongside, not touching the Rust side.

Do **NOT**:
- Modify any file under `crates/`.
- Modify `Cargo.toml`.
- Touch existing Rust source.

Do **YES**:
- Add `go.mod` and `go.sum` at repo root.
- Add `schema.graphql` at repo root.
- Add `cmd/orchard/main.go`.
- Add `internal/server/...` and `internal/cli/...` per the impl guide layout.
- Add launchd plist + systemd unit under `scripts/init/`.
- Extend the top-level Makefile (or create one if missing — check first) with `daemon`, `generate`, `rust`, `gui`, `all`, `clean`, `install`, `test` targets per the impl guide.
- Update README to note polyglot history (Rust CLI exists; Go daemon added 2026-05-04).

## Acceptance criteria (verbatim from impl guide)

- `go.mod` exists at repo root with module `github.com/drewdrewthis/git-orchard-rs`.
- `schema.graphql` exists at repo root with one type: `type Query { health: Health! }` and `type Health { status: String!, uptimeS: Int! }`.
- `make generate` runs gqlgen successfully; produces files under `internal/server/graphql/` (committed or gitignored per gqlgen convention — pick and document).
- `make daemon` builds with no errors → `bin/orchard`.
- `bin/orchard --help` shows subcommand groups: `daemon`, `config`, `query`.
- `bin/orchard daemon start` listens on `localhost:7777`.
- `curl localhost:7777/health` returns `200 OK { "status":"ok", "uptimeS": <integer> }`.
- `bin/orchard daemon stop` terminates the running daemon cleanly.
- `bin/orchard daemon status` returns up/down + uptime when daemon is running; clean error when it's down.
- `bin/orchard config init` writes a default `~/.config/orchard/config.json` and creates `~/.local/state/orchard/`.
- `bin/orchard query --raw 'query { health { status } }'` returns the health response (proves CLI dispatch works end-to-end).
- launchd plist + systemd unit committed under `scripts/init/`.

## Structural rules (CRITICAL)

- **Schema-first GraphQL.** `schema.graphql` is source of truth. gqlgen reads it. Do NOT do code-first.
- **Top-level `internal/`** per Go convention — prevents external imports.
- **Module path**: `github.com/drewdrewthis/git-orchard-rs` (no `/daemon` suffix).
- **`internal/server/`** is the daemon implementation (GraphQL server body).
- **`internal/cli/{daemon,config,query}/`** are the three cobra subcommand groups. Use cobra's command/subcommand pattern; mount each group from `cmd/orchard/main.go`.
- **`q` is a permitted alias for `query`** at the cobra level.
- **No mutations on the GraphQL surface.** `schema.graphql` has Query + maybe Subscription only; never `Mutation`.
- **No SQLite, no event-sourcing libs, no heavy ORM.**

## Toolchain (locked)

- Go 1.22+
- `99designs/gqlgen` for schema-first GraphQL codegen
- `graph-gophers/dataloader/v7`
- `fsnotify/fsnotify`
- `spf13/cobra` for CLI
- `log/slog` (stdlib) for logging
- stdlib `encoding/json` for config

## How to drive

You will be invoked with `/ralph-wiggum:ralph-loop`. The loop iterates until the completion-promise phrase appears in your output OR max-iterations is reached. Your completion-promise will be **`WSA SCAFFOLD DELIVERED`**.

Per iteration:
1. Read the AC checklist above.
2. Pick the next unmet AC.
3. Implement it.
4. Verify it works (run the actual command, check the actual output).
5. Commit with a clear message.
6. Move on.

When all ACs pass:
1. Run the full AC list end-to-end as a final verification.
2. Open a draft PR titled `Workstream A: orchard daemon scaffold` against `main` of `drewdrewthis/git-orchard-rs`.
3. PR body lists every AC with ✓ next to it and a one-line proof.
4. Output the phrase **`WSA SCAFFOLD DELIVERED`** in your final response.

## Stop conditions (escalate, don't guess)

If any of these happen, **stop and surface to the orchardist** rather than guessing:
- gqlgen schema-first wiring fails in a way the docs don't cover.
- A choice between two equally-plausible package layouts shows up.
- You discover a load-bearing dependency the impl guide didn't anticipate.
- You hit `cgo` / OS-conditional compilation problems.

To surface: write a short message to `~/workspace/git-orchard-rs/.worktrees/ws-a-scaffold/.claude-context/QUESTION.md` describing the blocker, then continue with your best guess if the work can proceed in parallel; the orchardist will read the question and respond.

## Out of scope

- Any provider implementations (B is a separate workstream).
- Resolvers beyond the stub `health` resolver.
- Any node types beyond `Health`.
- Federation, gh, contracts.
- Tests beyond a smoke test that `make daemon && bin/orchard daemon start` works.
