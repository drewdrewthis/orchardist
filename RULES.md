# Orchard Repo Constitution

The 55 rules below are **load-bearing**: every PR is reviewed against them, every architectural change cites them by ID, every domain refactor must demonstrate conformance.

This is not a style guide. It is the contract.

Rules are numbered with category prefixes so they can be cited in code review, audit reports, and PR descriptions: `R3` (code-pattern rule 3), `S7` (schema rule 7), `O5` (optimization rule 5), `M2` (mutation-coverage rule 2), `L1` (layer rule 1).

**Read this with [docs/architecture.md](docs/architecture.md)** — that doc describes the ecosystem (CLI, daemon, GUI, TUI, scripts) the rules apply to. This doc says what good looks like inside it.

---

## L — Layer rules (the ecosystem contract)

| # | Rule | Why |
|---|---|---|
| **L1** | **Operations live as scripts.** Anything that does an external-world effect (tmux, git, gh, fs) is implemented as a script in `scripts/`. CLI and daemon are both wrappers; neither re-implements the operation. | One source of truth. Bug-fix once. Both consumers pick it up from disk. |
| **L2** | **Scripts have `--json` machine output.** Every script the daemon or CLI execs supports a `--json` flag. | Wrappers need stable parseable output. Stdout/stderr ad-hoc text is not a contract. |
| **L3** | **Scripts pick the right language.** Bash, Python, Rust, Go — whatever's clearest/fastest for the job. Independently executable via shebang. | No language religion. The script's contract is its interface, not its implementation language. |
| **L4** | **Queries are daemon-owned and in-process.** GraphQL reads resolve via the daemon's providers/caches in Go. **No CLI/script exec in field-resolver hot paths.** | Subprocess latency is fatal for per-field reads. 60-second lens loads are the failure mode. |
| **L5** | **Mutations exec scripts.** All GraphQL writes exec the corresponding `scripts/<op>` and project its `--json` output as the response. Daemon does not re-implement mutation logic. | Mutation logic lives once (script); daemon is a thin façade. |
| **L6** | **CLI is standalone.** `orchard <subcommand>` works without the daemon running. CLI is a user-facing shell over the same script library. | Operator ergonomics. CLI is the foundation; daemon stands above. |
| **L7** | **GUI and TUI consume daemon-only via GraphQL.** They do not exec scripts directly, do not import CLI crates, do not call external tools. Everything they need is a daemon query, mutation, or subscription. | Single consumer-facing contract. No drift between two clients re-implementing the same thing. |
| **L8** | **Mutation responses return affected nodes.** Clients update their normalized cache from the mutation response; subscribers see deltas via the normal subscription path. No special "cache invalidate on mutate" logic in the daemon — the GraphQL contract handles it. | GraphQL idiom. Houdini/Apollo apply the mutation response automatically; round-trips drop. |
| **L9** | **No persisted daemon state.** Daemon's caches are projections of external truth (tmux server, git repo, gh API, claude jsonl, fs). Restart re-observes from scratch. | No recovery logic. No state-divergence between daemon and reality. Cheap restarts. |
| **L10** | **Daemon-self commands live under `orchard daemon ...`.** Operations about the daemon itself (start, stop, status, introspect, manual cache rebuild) live in the `daemon` sub-CLI. They are not general orchard verbs. | Clear namespacing. The daemon doesn't escape its own pattern. |

---

## R — Code patterns

| # | Rule | What to look for |
|---|---|---|
| **R1** | **Package-by-feature, not by layer.** Code grouped by domain (`daemon/tmux/`), not by technical role (`providers/`, `resolvers/`). | Categorical grouping (`providers/`, `resolvers/`, `loaders/`) forces 4-directory edits for any change. |
| **R2** | **Service layer is the contract.** Every domain module has `service.go` exposing the only API consumers may call. Resolvers and other modules import `daemon/<name>` and do NOT import `provider.go` types directly. | Providers leak in proportion to their surface; the service is the narrowing. |
| **R3** | **DataLoader-shaped reads.** Field resolvers go through loaders. Loaders batch and cache per-request. Loaders consume the service. **No `Snapshot()` or full-state clone in a field resolver.** | The 60s lens-load (#612) came from `Snapshot()` per `pane.window.session` traversal. |
| **R4** | **Interface segregation (ISP).** Consumers depend on narrow interfaces defined in their own module, not on broad provider types. | `worktree` should depend on a `PaneReader` interface with 2-3 methods, not `*tmux.Provider`. |
| **R5** | **Anti-corruption layer per integration.** Each module owns its types; cross-module reach happens only at well-defined consumer boundaries (e.g. `worktree` joining tmux + git data via their services). | No "tmux's Pane type leaks into git's worktree resolver." |
| **R6** | **Single Responsibility at the file level.** No god-files. A resolver file owns one type's fields, not every type. | Today's `schema.resolvers.go` is 1800+ lines. |
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
| **S14** | **One thing, one place.** Same data accessible via exactly one path. No `worktree.pr.checks` AND `worktree.checks` returning the same data differently. | Two paths to the same data = two cache entries = two bugs. |

---

## O — Optimization

| # | Rule | What to look for |
|---|---|---|
| **O1** | **DataLoader coalescing actually works.** Loaders are both defined AND consumed. A loader that exists but is bypassed is worst-of-both. Loaders are keyed correctly — wrong key shape = no coalescing. | PR #610 surfaced: PanesByCommand loader existed but `pane.window.session` resolvers bypassed it. |
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
| **M1** | **Every module enumerates its mutations.** Module's `AUDIT.md` (or per-module spec) lists every `Mutation.*` resolver it owns. | Without enumeration, gaps are invisible. |
| **M2** | **Every client-side shellout maps to a daemon mutation.** TUI/GUI code that bypasses the daemon and shells out to `tmux`/`git`/`gh`/`claude` directly is a coverage gap. The daemon's mutation surface must be complete for its consumers. | Today's TUI/CLI shells out for most writes — that's why the daemon feels read-only. |
| **M3** | **Mutations are granular.** `setConfig(blob)` is worse than `setConfigKey(k, v)`. Coarse mutations force read-modify-write, which races. | Granular mutations compose; coarse mutations conflict. |
| **M4** | **Mutations validate input.** Bad input fails at the resolver boundary with typed errors, not deep in the script. | Defense-in-depth + better error messages to clients. |
| **M5** | **Mutations declare idempotency or its absence.** Idempotent mutations are safe to retry; non-idempotent ones (sending a chat message, creating a unique resource) are documented and clients know. | Without this, clients either always retry (duplicate sends) or never retry (lose work on transient failures). |
| **M6** | **Mutations gate auth at the resolver, not the script.** Origin / capability / pane-allowlist checks happen before exec. (Today: CheckOrigin gates websocket. Per-mutation gating may follow.) | The script trusts its caller; the resolver is the trust boundary. |
| **M7** | **Every mutation has a corresponding subscription.** If `sendTextToPane` doesn't fire a `paneContentChanged` for that pane, the mutator can't see its own effect without polling. Pairs with S7 + L8. | Mutating without a subscription = client polls = wasted bandwidth + bad UX. |

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
