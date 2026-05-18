# `daemon/claude-jsonls` — Refactor Audit

## Domain

Raw Claude Code JSONL parsing. Owns `Conversation` only. Read-only.

## Applicable Rules

| Rule | Status | How satisfied |
|------|--------|---------------|
| **L4** | ✅ | jsonl tails cached in-process; no script exec in field resolvers |
| **L9** | ✅ | `Conversation` is a projection of the JSONL on disk; daemon restart re-observes |
| **R1** | ✅ | Code lives in `daemon/claude-jsonls/` — package-by-feature |
| **R2** | ✅ | `service.go` is the only public API; resolvers import the `Service` interface |
| **R3** | ✅ | Field resolvers load via `ConversationByID` / `ConversationsByAll` loaders; no `Snapshot()` |
| **R4** | ✅ | `ClaudeInstanceReader` interface defined here (consumer defines the interface) |
| **R5** | ✅ | Cross-domain call to `claude-instance` goes via `ClaudeInstanceReader` in this package |
| **R6** | ✅ | `resolver_conversation.go` owns exactly `Conversation` type |
| **R8** | ✅ | Errors wrapped with `fmt.Errorf("…: %w", err)` throughout |
| **R9** | ✅ | Every blocking call takes `context.Context` first |
| **R10** | ✅ | Provider goroutine owned by `Provider`; stopped via `Stop()` |
| **R12** | ✅ | `Subscribe` returns `<-chan` (receive-only) |
| **R13** | ✅ | `sync.RWMutex` for read-heavy cache; `sync.Mutex` for subscriber fan-out map |
| **R16** | ✅ | `broadcast()` called only after `cachePut` / `reload` completes the cache write |
| **R17** | ✅ | `run()` goroutine wraps top loop in `recover` + structured `slog` logging |
| **S10** | ✅ | Transcripts excluded from GraphQL; see `GET /v1/conversations/<uuid>/jsonl` |
| **S15a** | ✅ | Schema partial at `daemon/claude-jsonls/schema.graphql` (unchanged from constitution) |
| **S15b** | ✅ | `Conversation.liveInstances` declared here; resolver here calls `ClaudeInstanceReader` |
| **O1** | ✅ | `ConversationByID` key = `ConversationID`; `ConversationsByAll` = no-key bulk loader |
| **O4** | ✅ | `provider.go` logs cache-miss / watcher events via structured slog |
| **O6** | ✅ | fsnotify-driven; no polling loop — watcher fires only on real disk events |
| **O8** | ✅ | `readJSONLMeta` uses tail-window scan; never loads full file into RAM |
| **O10** | ✅ | Single `FetchAll` walk covers all missing keys in one pass |
| **T1** | ✅ | `resolver_conversation_test.go` tests every typed field against a stubbed Service |
| **T5** | ✅ | `loaders_test.go` counts underlying `GetMany` calls to assert coalescing |
| **T6** | ✅ | `subscriptions_test.go` asserts emit happens after cache write |

## File map

| File | Purpose |
|------|---------|
| `service.go` | `Service` interface + `ClaudeInstanceReader` (consumer-defined per R4) |
| `provider.go` | In-process cache, fsnotify watcher, broadcast fan-out |
| `adapter.go` | FSAdapter: walk `~/.claude/projects/` + `readJSONLMeta` |
| `jsonl.go` | Pure `readJSONLMeta` + helpers (tail-window; no RAM growth) |
| `loaders.go` | `ConversationByID` + `ConversationsByAll` DataLoaders |
| `subscriptions.go` | `ConversationChanged` subscription emitter |
| `resolver_conversation.go` | `Conversation` type resolver (one file per type per R6) |
| `service_test.go` | `Provider` unit tests (ToGraphQL, PathForSessionUUID, IsOpen) |
| `jsonl_test.go` | Pure `readJSONLMeta` unit tests |
| `loaders_test.go` | Loader coalescing test (T5) |
| `subscriptions_test.go` | Subscription emit-after-write test (T6) |
| `resolver_conversation_test.go` | Field projection tests against stub service (T1) |

## Cross-domain interfaces

### Defined here (R4 — consumer defines the interface)

```go
// ClaudeInstanceReader is the narrow interface this domain calls to resolve
// Conversation.liveInstances. Defined here; implemented by daemon/claude-instance.
type ClaudeInstanceReader interface {
    LiveInstancesByConversationUUID(ctx context.Context, sessionUUID string) ([]*graphql.ClaudeInstance, error)
}
```

### Consumed

- `daemon/claude-instance` — via `ClaudeInstanceReader` above

## No mutations

`claude-jsonls` is read-only. Claude Code appends to JSONLs; the daemon only reads.
No `mutations.go`, no `scripts/` entries.
