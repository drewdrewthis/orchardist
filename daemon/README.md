# `daemon/`

The orchard daemon. Module-per-domain layout per [RULES.md R1, R2](../RULES.md) and [docs/architecture.md](../docs/architecture.md).

## Layout

```
daemon/
├── README.md                       ← this file
├── schema.graphql                  ← root: Node interface, scalars, empty Query/Mutation/Subscription
├── server.go                       ← HTTP/WebSocket wiring, origin gating, gqlgen composition
├── transport/                      ← federation (peer proxy, websocket subprotocol)
├── node.go                         ← Query.node(id) prefix-registry dispatcher
├── loaders.go                      ← cross-domain dataloader composer
├── graphql/                        ← gqlgen-generated; not authored
│
├── git/                            ← Local git: Repo, Worktree
├── gh/                             ← GitHub API: PullRequest, Issue, WorkflowRun, gh pass-through
├── tmux/                           ← tmux server: TmuxServer, TmuxSession, TmuxWindow, TmuxPane, TmuxClient
├── ps/                             ← OS process table: Process
├── host-identity/                  ← Machine identity + resource load: Host, ResourceLoad
├── host-services/                  ← launchd/systemd watchlist: HostService
├── claude-jsonls/                  ← Claude Code JSONLs (raw): Conversation
├── claude-instance/                ← Live REPL derivation (jsonl + tmux + ps): ClaudeInstance
├── claude-account/                 ← `claude auth status`: ClaudeAccount
├── contracts/                      ← Agent delivery commitments: Contract
├── daemon-self/                    ← Liveness, version, schemaSDL, node-id dispatcher
└── daemon-meta/                    ← Provider rollup, freshness counters, Meta envelope
```

**12 domains total.** Previous 9-domain shape (#618 first draft) split per devils-advocate review:
- `claude-jsonls` → `claude-jsonls` + `claude-instance` + `contracts` (three independent lifecycles)
- `daemon-self` → `daemon-self` + `daemon-meta` (R6 god-module split)

> 13-domain interim split also created `views/` for composite WorkView; subsequently deleted — GraphQL + dataloaders already provide round-trip coalescing without a composite delegator.

## Canonical per-domain layout

Each `daemon/<name>/` has the following files (omit any that don't apply):

```
daemon/<name>/
├── README.md            ← domain summary, current source path, RULES.md citations
├── schema.graphql       ← S15: schema partial owned by this domain
├── service.go           ← R2: the only API consumers may import
├── provider.go          ← internal cache + source-of-truth poll/watch
├── adapter.go           ← external-world I/O (exec, syscall, http)
├── resolver.go          ← R3: thin Load(key) + projection per field
├── loaders.go           ← per-domain dataloaders (composed at shell)
├── mutations.go         ← L5: each mutation execs `scripts/<op>` and projects --json
├── subscriptions.go     ← R16: emit AFTER cache write
└── *_test.go            ← unit tests; integration tests live at daemon/<name>/integration/
```

## Schema composition

The root `daemon/schema.graphql` declares `interface Node`, the `Time` and `JSON` scalars, and the empty `type Query`, `type Mutation`, `type Subscription` shells.

Each domain's `daemon/<name>/schema.graphql` is a **partial** that uses `extend type Query`, `extend type Mutation`, `extend type Subscription` to add its fields, and declares its own object types, inputs, and enums. gqlgen globs all partials into one composed schema at build time — there is no monolithic schema file to edit.

Cross-domain field types (e.g. `TmuxPane.claudeInstance: ClaudeInstance`) are declared in the consuming domain's partial; the resolver lives there and calls the owning domain's service through its interface.

## Status

**Skeleton scaffolded; #613 is the refactor that fills it in.**

The current daemon code still lives under `internal/server/{providers,resolvers,loaders,graphql}/`. This `daemon/` tree is the migration target. Each domain README links to its current source path.
