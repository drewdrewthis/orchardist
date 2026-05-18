# Orchard Repo Constitution

The 74 rules below are **load-bearing**: every PR is reviewed against them, every architectural change cites them by ID, every domain refactor must demonstrate conformance.

This is not a style guide. It is the contract.

Rules are numbered with category prefixes so they can be cited in code review, audit reports, and PR descriptions: `R3` (code-pattern rule 3), `S7` (schema rule 7), `O5` (optimization rule 5), `M2` (mutation-coverage rule 2), `L1` (layer rule 1), `T1` (testing rule 1).

**Read this with [docs/architecture.md](docs/architecture.md)** — that doc describes the ecosystem (CLI, daemon, GUI, TUI, scripts) the rules apply to. This doc says what good looks like inside it.

---

## L — Layer rules (the ecosystem contract)

| # | Rule | Why |
|---|---|---|
| **L1** | **Operations live as scripts.** Anything that does an external-world effect (tmux, git, gh, fs) is implemented as a script in `scripts/`. CLI and daemon are both wrappers; neither re-implements the operation. | One source of truth. Bug-fix once. Both consumers pick it up from disk. |
| **L2** | **Scripts have `--json` machine output.** Every script the daemon or CLI execs supports a `--json` flag, with stdout shape `{"ok": bool, "data": <op-specific>?, "error": {"code": str, "message": str}?}`. `ok: true` requires `data` (or an explicit `null`); `ok: false` requires `error`. Scripts exit 0 on `ok: true`, non-zero on `ok: false`. Stderr is free-form for human readers; daemon/CLI parse stdout only. | Wrappers need stable parseable output. A fixed envelope means daemon and CLI share one decoder per script. |
| **L3** | **Scripts pick the right language.** Bash, Python, Rust, Go — whatever's clearest/fastest for the job. Independently executable via shebang. | No language religion. The script's contract is its interface, not its implementation language. |
| **L4** | **Queries are daemon-owned and in-process.** GraphQL reads resolve via the daemon's providers/caches in Go. **No CLI/script exec in field-resolver hot paths.** | Subprocess latency is fatal for per-field reads. 60-second lens loads are the failure mode. |
| **L5** | **Mutations exec scripts.** All GraphQL writes exec the corresponding `scripts/<op>` and project its `--json` output as the response. Daemon does not re-implement mutation logic. | Mutation logic lives once (script); daemon is a thin façade. |
| **L6** | **CLI is standalone.** `orchard <subcommand>` works without the daemon running. CLI is a user-facing shell over the same script library. | Operator ergonomics. CLI is the foundation; daemon stands above. |
| **L7** | **GUI and TUI consume daemon-only via GraphQL.** They do not exec scripts directly, do not import CLI crates, do not call external tools. Everything they need is a daemon query, mutation, or subscription. | Single consumer-facing contract. No drift between two clients re-implementing the same thing. |
| **L8** | **Mutation responses return affected nodes.** Clients update their normalized cache from the mutation response; subscribers see deltas via the normal subscription path. No special "cache invalidate on mutate" logic in the daemon — the GraphQL contract handles it. | GraphQL idiom. Houdini/Apollo apply the mutation response automatically; round-trips drop. |
| **L9** | **No persisted daemon state.** Daemon's caches are projections of external truth (tmux server, git repo, gh API, claude jsonl, fs). Restart re-observes from scratch. | No recovery logic. No state-divergence between daemon and reality. Cheap restarts. |
| **L10** | **Daemon-self commands live under `orchard daemon ...`.** Operations about the daemon itself (start, stop, status, introspect, manual cache rebuild) live in the `daemon` sub-CLI. They are not general orchard verbs. | Clear namespacing. The daemon doesn't escape its own pattern. |
| **L11** | **Scripts do not call the daemon.** Scripts depend only on external truth (filesystem, CLIs, env, args). A script MUST NOT invoke `orchard <subcommand>` or hit `127.0.0.1:7777`. The dependency arrow is `daemon → script`, never `script → daemon`. | Prevents a script→daemon→script cycle that would deadlock under `L5` mutations or loop forever. Scripts are leaves; the daemon and CLI are wrappers. |

---

## R — Code patterns

| # | Rule | What to look for |
|---|---|---|
| **R1** | **Package-by-feature, not by layer.** Code grouped by domain (`daemon/tmux/`), not by technical role (`providers/`, `resolvers/`). | Categorical grouping (`providers/`, `resolvers/`, `loaders/`) forces 4-directory edits for any change. |
| **R2** | **Service layer is the contract.** Every domain module has `service.go` exposing the only API consumers may call. Resolvers and other modules import `daemon/<name>` and do NOT import `provider.go` types directly. | Providers leak in proportion to their surface; the service is the narrowing. |
| **R3** | **DataLoader-shaped reads — defined AND consumed.** Field resolvers go through loaders. Loaders batch and cache per-request. Loaders consume the service. **No `Snapshot()` or full-state clone in a field resolver.** A loader that exists but is bypassed (resolver calls `Snapshot()` instead of `Load(key)`) is the worst case — pays the loader cost without earning the batching. R3 owns the loader-shape contract; O1 owns loader-key correctness. | The 60s lens-load (#612) came from `Snapshot()` per `pane.window.session` traversal AND from a `PanesByCommand` loader that existed but was bypassed. |
| **R4** | **Interface segregation (ISP).** Consumers depend on narrow interfaces defined in their own module, not on broad provider types. | `worktree` should depend on a `PaneReader` interface with 2-3 methods, not `*tmux.Provider`. |
| **R5** | **Anti-corruption layer per integration.** Each module owns its types; cross-module reach happens only at well-defined consumer boundaries (e.g. `worktree` joining tmux + git data via their services). | No "tmux's Pane type leaks into git's worktree resolver." |
| **R6** | **Single Responsibility at the file level.** No god-files. Inside a feature package (R1), each resolver/provider/loader file owns ONE GraphQL type or one logical concern — `resolver_pane.go`, `resolver_session.go`, NOT a single `resolver.go` covering every type the package defines. | Today's `schema.resolvers.go` is 1800+ lines. R1 partitions across domains; R6 partitions within a domain. |
| **R7** | **Open/Closed for extension.** Adding a new GraphQL type or field does not require editing a god-file. New module = new directory; new resolver lives there. | Categorical layout violates this; module layout fixes it. |
| **R8** | **One error style per module.** Pick typed sentinel (`errors.Is`), wrapped, or panic-as-bug — and stick to it. Mixed handling within a module is debt. | Caller can't tell what to do if errors are heterogeneous. |
| **R9** | **Context propagation.** Every blocking call accepts `context.Context` first and respects cancellation. `ctx` reaches the I/O boundary. | Resolver chains that drop `ctx` leak goroutines on client disconnect. |
| **R10** | **Goroutine ownership.** Every spawned goroutine has a clear owner that knows how to stop it. Provider polls run on goroutines; the provider owns shutdown. | Unbounded goroutine growth = slow memory leak. |
| **R11** | **Accept interfaces, return structs (Go idiom).** Public constructors return concrete types; consumers depend on interfaces they define themselves. | Returning interfaces from constructors loses type info and complicates testing. |
| **R12** | **Channel direction in signatures.** Public APIs use `chan<- T` or `<-chan T`, not bare `chan T`. | Bare `chan T` leaks send/receive capability to callers who shouldn't have it. |
| **R13** | **Concurrency primitive choice fits access pattern.** `RWMutex` for read-heavy, `Mutex` for balanced, `sync.Map` for write-heavy with disjoint keys, `atomic` for counters. | Wrong choice = false contention. Audit per shared-state field. |
| **R14** | **Naming honesty.** Type, field, and directory names mean what they say. | `chatMute` consumed by `SessionPane` for Claude REPL reply pings (PR #610) — fix by renaming, not by comments. |
| **R15** | **Boy Scout Rule, file-scoped.** Every PR touching a file leaves that file in better shape than it found it (or doesn't degrade it). | "Surgical edits done incorrectly" is the failure mode this prevents. |
| **R16** | **Subscription emit timing.** Emit AFTER cache write, not before. Subscribers must see fresh data, not stale. | Race-prone if emit precedes write; a fast subscriber re-reads the old value. |
| **R17** | **Goroutine panic-recovery and structured logging.** Every long-running goroutine (provider poll, subscription writer, background task) wraps its top-level loop in a recover-and-log handler. Logs are structured (key=value or zap/slog), not printf. | Untrapped panics inside provider polls kill the daemon's read path silently. Printf logs are unparseable. |

---

## S — Schema design

| # | Rule | What to look for |
|---|---|---|
| **S1** | **Connection / edge pattern (Relay).** Paginated lists use `edges` / `pageInfo` / `cursor`, not raw arrays. Small bounded lists OK as arrays — but heavy lists (worktrees, transcripts) paginate. | Unbounded arrays force clients to materialize everything; subscriptions become enormous. |
| **S2** | **Global Object Identification (Node interface).** Every node-typed thing has a globally unique `id` and implements `Node`. Enables refetching, polymorphic queries, normalized caching. **Houdini's normalized cache REQUIRES this.** | Without unique ids, the client cache fragments. |
| **S3** | **Polymorphism via unions or interfaces, not `kind` discriminators.** `type Result = Worktree | Channel`, not `type Result { kind: String; worktree: …; channel: … }`. | Stringly-typed kind discriminators force defensive null checks everywhere. |
| **S4** | **Input vs output separation.** Mutations take `input: SomeInput!` (single Input object), not positional args. Input types are NOT reused as outputs. | Different shapes for different directions; positional args don't evolve well. |
| **S5** | **Nullability discipline.** Required things are `!`. Optional things are not. No defensive-null on everything. `[Foo!]!` (non-null list of non-null items) is usually right; `[Foo]` is rarely right. | Defensive nullability hides bugs in the schema. |
| **S6** | **Field arguments, not sibling queries.** Filtering happens via arguments on the field, not via N parallel queries (`workView`, `workViewFiltered`, `workViewByHost`). | N parallel queries multiply maintenance and cache fragmentation. |
| **S7** | **Subscription event shape: small addressable deltas.** Subscriptions emit `{nodeId, patch}` shapes, not full re-fetches. Subscribers apply the delta to a normalized cache. | Full re-fetches over a hot subscription are a performance disaster. |
| **S8** | **Mutation return shape: affected nodes.** Mutations return the node(s) they affected so the client cache updates without a follow-up query. (Pairs with L8.) | Boolean returns force a follow-up query for every write. |
| **S9** | **Error union pattern for expected errors.** Rate-limit, permission-denied, validation, conflict — these are typed result unions, not GraphQL `errors[]`. `errors[]` is for system errors. | Expected errors as system errors are unparseable client-side. |
| **S10** | **Field cost discipline.** Hot fields are cheap. Expensive fields require explicit opt-in (deeper query, named field, separate connection). | Cheap top-level fields that hide expensive joins blow up under naive queries. |
| **S11** | **Schema-as-source-of-truth.** Generated client types follow the schema; no client-side typing diverges. | Hand-rolled types drift; the schema must be authoritative. |
| **S12** | **Field stability via `@deprecated`.** Removed fields go through `@deprecated` first. No silent removals. | Silent removal breaks every consumer. |
| **S13** | **Naming consistency.** Mutations are verbs (`createX`, `sendY`); queries are nouns; subscriptions are present-tense. PascalCase types, camelCase fields. | Inconsistent naming = inconsistent ergonomics. |
| **S14** | **One resolver per logical field; composite views delegate, not duplicate.** A piece of data has exactly one canonical resolver implementation. Composite views (e.g. the `extend type` back-edges that wire `Worktree.tmuxPanes` etc.) are allowed and encouraged — they MUST delegate to the per-type resolver, not re-implement the join. Banned: two divergent code paths returning the same data with different semantics (`worktree.pr.checks` AND `worktree.checks`). | Multiple selection paths are fine; multiple implementations are the bug. Note: a dedicated `views/` domain for composite delegation is NOT needed — GraphQL + dataloaders already coalesce cross-domain reads in one query. |
| **S15a** | **Schema partials per domain.** Each domain owns `daemon/<name>/schema.graphql`. gqlgen globs them into one composed schema at build time. There is no monolithic schema file; touching a domain's types means touching that domain's partial. | The schema IS the domain contract; it belongs with the domain. Monolithic schemas drift; per-domain partials are reviewed together with their resolvers/services. |
| **S15b** | **Cross-domain `extend type` placement.** When domain A adds a field to a type owned by domain B (e.g. `git` adds `Worktree.tmuxPanes`), the `extend type` block lives in A's partial and the resolver lives in A. A imports B's service interface (per R5) — never B's provider. | The field is owned by whoever needs the data, not whoever owns the parent type. Keeps each partial self-contained. |
| **S15c** | **Scalars + root operation types declared in root partial only.** `scalar Time`, `scalar JSON`, `type Query`, `type Mutation`, `type Subscription`, and `interface Node` live ONLY in `daemon/schema.graphql`. Domain partials USE these (`extend type Query`, `implements Node`, `firstSeenAt: Time`) — they MUST NEVER re-declare them. | Verified by Phase-0 spike: duplicate scalar declarations silently work in dev and break at codegen with cryptic errors. Hard-coding placement avoids the entire class of error. |
| **S16a** | **Typed core required.** Every shellout-fronting domain (`gh`, `git`, `tmux`, `ps`, `host-services`, `claude-account`) exposes a typed core: cached, loader-batched, R3-clean Node-typed fields covering the 80% read path. | The typed core is where every O-rule earns its keep. |
| **S16b** | **Pass-through escape hatch required, with L4 guards.** Each shellout-fronting domain ALSO exposes `Query.<domain>(...): JSON`. **MANDATORY guards:** (1) pass-through is a TOP-LEVEL query only — never nested inside a list/object resolver, never inside a `Subscription.*` payload; (2) per-call timeout (default 30s) + domain pass-through concurrency cap (default 4); (3) pass-through is NOT cached, NOT loader-batched, NOT subscribable. Clients wanting live updates must use the typed core. | Without guards, pass-through re-creates the #612 60s lens-load with the constitution's blessing. Guards keep pass-through as an escape hatch, not a bypass. |
| **S16c** | **Promote load-bearing pass-through.** When a single pass-through call shape (post-normalisation: same `tool` + flag set ignoring scalar arg values) is observed ≥N times over ≥7 days, the daemon logs a `passthrough.promotion_candidate` event and the domain files a typed-core issue. | Pass-through is the long-tail escape; the constitution refuses to let the long tail become the new hot path. |

---

## O — Optimization

| # | Rule | What to look for |
|---|---|---|
| **O1** | **DataLoader keys are correct and stable.** A loader whose key shape doesn't match the access pattern coalesces nothing — wrong key = N+1 with extra steps. Per [ADR-022](docs/adr/022-graph-modeling.md) the keys are typed by axis (`ByID`, `ByCwd`, `ByCommand`); arity in the name (`PaneByID` → one, `PanesByCommand` → many). R3 owns "loader exists and is consumed"; O1 owns "loader key matches access pattern." | Keying is the optimization concern; existence is the architectural concern. |
| **O2** | **Lazy field resolution.** Fields compute only when requested. Pre-computing the full graph (`workView` building everything when client wants 3 fields) is the failure mode. | GraphQL's promise is lazy-by-default. |
| **O3** | **Subscription delta vs full re-query.** When a node changes, subscribers get a small patch, not a 50KB re-query. The subscription transport is hot per-connection. | Full re-queries on hot subscriptions = death by a thousand cuts. |
| **O4** | **Cache hit attribution / observability.** Every cached field has hit/miss counters surfaced somewhere (debug endpoint, metrics, log line). | Without observability you can't tell if a cache is earning its keep. |
| **O5** | **Cold start cost is measured.** Boot the daemon, measure time-to-first-useful-response. Anything that can be deferred is. | First-paint matters; cold loads ARE the user-visible cost. |
| **O6** | **Steady-state poll cost is bounded.** Idle daemon doesn't poll every external system every second forever. Adaptive polling (slow down when nothing changes). | 1Hz × N providers × 24h = wasted CPU. |
| **O7** | **Subscription fan-out is bounded.** One client subscribes to `workViewChanged`. How many providers fire on a single git change? Fan-out explosion is a smell. | Unbounded fan-out under load is a self-DoS. |
| **O8** | **Per-session memory is bounded.** Long-running session reading a 100MB jsonl uses a tail-window, not memory growth. | Memory leak via cache growth = daemon restart cycle. |
| **O9** | **Hot-path allocation is audited.** Per-request allocations (map clones, string concat in loops). Go's escape analysis catches some; pprof under load surfaces the rest. | The `Store.Snapshot()` map-clone (#612) was an allocation hot path nobody noticed. |
| **O10** | **I/O batching at the boundary.** Multiple `ps` lookups → one `ps` call. Multiple jsonl reads → one open. Batching happens at the I/O edge, not per-resolver. | N+1 at the syscall layer is the same bug as N+1 at the query layer. |
| **O11** | **Read-through vs write-through cache, explicit per module.** Each cached field documents its policy. Not mixed within a module. | Mixed write semantics = race conditions per the EnrichPR (#615) case. |
| **O12** | **Stale-while-revalidate as an explicit pattern.** When external truth is rate-limited or slow, serve stale + refresh in background. Document the staleness contract per field. | The `gh` module already does this ad-hoc for rate-limit. Promote to first-class. |

---

## M — Mutation coverage

| # | Rule | What to look for |
|---|---|---|
| **M1** | **Per-domain schema partial enumerates its mutations.** `daemon/<name>/schema.graphql` is the canonical enumeration — every `Mutation.*` extension lives there and only there. No `AUDIT.md` duplicate. | The partial IS the contract per S15a. Enumeration in a side-file would drift. |
| **M2** | **Every client-side shellout maps to a daemon mutation.** TUI/GUI code that bypasses the daemon and shells out to `tmux`/`git`/`gh`/`claude` directly is a coverage gap. The daemon's mutation surface must be complete for its consumers. | Today's TUI/CLI shells out for most writes — that's why the daemon feels read-only. |
| **M3** | **Mutations are granular.** `setConfig(blob)` is worse than `setConfigKey(k, v)`. Coarse mutations force read-modify-write, which races. | Granular mutations compose; coarse mutations conflict. |
| **M4** | **Mutations validate input.** Bad input fails at the resolver boundary with typed errors, not deep in the script. | Defense-in-depth + better error messages to clients. |
| **M5** | **Mutations declare idempotency or its absence.** Idempotent mutations are safe to retry; non-idempotent ones (sending a chat message, creating a unique resource) are documented and clients know. | Without this, clients either always retry (duplicate sends) or never retry (lose work on transient failures). |
| **M6** | **Mutations gate auth at the resolver, not the script.** Origin / capability / pane-allowlist checks happen before exec. (Today: CheckOrigin gates websocket. Per-mutation gating may follow.) | The script trusts its caller; the resolver is the trust boundary. |
| **M7** | **Every mutation has a corresponding subscription.** If `sendTextToPane` doesn't fire a `paneContentChanged` for that pane, the mutator can't see its own effect without polling. Pairs with S7 + L8. | Mutating without a subscription = client polls = wasted bandwidth + bad UX. |

---

## T — Testing

| # | Rule | What to look for |
|---|---|---|
| **T1** | **Every typed field has a resolver test against a stubbed service.** Resolvers compose service + loader output; tests assert projection correctness with a fake service (per R4 the consumer-owned interface). No production-network/disk in unit tests. | "It compiles" is not "it resolves." |
| **T2** | **Every mutation script has its own test of the `--json` envelope.** Per L2 the envelope is `{ok, data?, error?}` — script tests assert the shape on success AND failure paths. Run from the test file with `bash`/`go test ./scripts/...` harness. | The script is the canonical operation; without its own test, mutation tests have nothing to lean on. |
| **T3** | **No tautological assertions.** Forbidden: `assert true`, `assert.NotNil(x)` on always-constructed values, `expect(typeof x).toBe("number")` when x's type is statically known. Assertions must be capable of failing on the code path they exercise. | #610 shipped `expect(pingCalled).toBeGreaterThanOrEqual(0)` — useless. |
| **T4** | **Cross-domain joins are integration-tested at the GraphQL boundary.** `Worktree.tmuxPanes` is tested via a real GraphQL query against a daemon with real `git` + `tmux` + `ps` providers (or in-process fakes that respect the service interface). NOT at the provider boundary. | Unit-testing the join below the schema misses gqlgen's resolution behaviour and the cross-domain wiring (S15b). |
| **T5** | **Loader coalescing is verified by counting underlying fetches.** A loader test runs N parallel `Load(key)` calls against a service whose adapter records call count; assert ≤1 call per request. | "We have a loader" without "the loader actually batches" is the O1/R3 failure mode. |
| **T6** | **Subscription tests assert "emit after write, not before" (R16).** Test fires a mutation, observes the subscription, asserts the subscriber sees the post-mutation state in its first emission — not the pre-state. | Catches the race R16 forbids. |
| **T7** | **Pass-through tests assert L4 guards (S16b).** Test that pass-through (a) refuses nesting via static gqlgen rejection, (b) honors the timeout, (c) honors the concurrency cap. | Without guard tests, the guards rot. |

---

## How to use this document

- **PR review:** comment with rule IDs. `R3 violation: this resolver calls Snapshot() in a field path. See RULES.md.`
- **Audit reports:** the per-domain refactor PRs include an `AUDIT.md` table mapping each rule to ✅ clean / 🔧 fixed-in-PR / 📋 filed-as-#XXX / ⏭️ deferred-with-reason.
- **New code:** before opening a PR, skim the L-rules + the categories relevant to your change.
- **Architectural change:** if a rule needs to evolve, propose an ADR + amend this file in the same PR. Rules don't change silently.

## Scope

These rules apply to the orchard ecosystem (`crates/`, `internal/server/` (becoming `daemon/`), `scripts/`, `crates/orchard-gui/`, `crates/orchard/`). They do not apply to vendored code, generated code (gqlgen output, Houdini codegen), or test fixtures.

## Provenance

These rules were adopted in [ADR-023](docs/adr/023-repo-constitution.md) after the PR #610 review surfaced cross-cutting patterns that no single PR-level review was catching. Citation IDs are stable; rule TEXT may be refined via PR + ADR amendment.
