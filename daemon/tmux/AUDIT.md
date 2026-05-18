# daemon/tmux — Audit Doc

## Applicable rules

| Rule | Status | How satisfied |
|------|--------|---------------|
| L1 | ✅ | scripts/tmux-send-text.sh, tmux-kill-pane.sh, tmux-new-window.sh own the ops |
| L2 | ✅ | Every script emits `{ok, data?, error?}` envelope; `--json` flag |
| L3 | ✅ | Scripts are bash (simplest for tmux CLI) |
| L4 | ✅ | All reads go through in-process cache; no shellout in field resolvers |
| L5 | ✅ | sendTextToPane, killPane, newWindow all exec corresponding scripts |
| L8 | ✅ | Mutation responses return affected nodes (paneId/sessionName) |
| L9 | ✅ | Provider cache derived from tmux server; restart re-observes |
| R1 | ✅ | All code lives in daemon/tmux/ |
| R2 | ✅ | service.go is the only API consumers import |
| R3 | ✅ | pane.window.session traversal uses PaneByID/SessionByID loaders — no Snapshot() in field resolvers |
| R4 | ✅ | PanePsGetter, ClaudeInstanceGetter interfaces defined in this module for cross-domain consumers |
| R5 | ✅ | Cross-domain back-edges take ps/claude-instance service interfaces, not provider pointers |
| R6 | ✅ | One file per GraphQL type: resolver_server.go, resolver_session.go, resolver_window.go, resolver_pane.go, resolver_client.go |
| R8 | ✅ | Sentinel errors (errors.Is pattern) throughout |
| R9 | ✅ | context.Context propagated to every blocking call |
| R10 | ✅ | pollLoop owned by Provider; Start/Stop goroutine lifecycle clear |
| R12 | ✅ | Subscribe returns <-chan, not bare chan |
| R13 | ✅ | RWMutex for server field (read-heavy), subsMu Mutex for sub list |
| R14 | ✅ | chatMute renamed: no such field in this module (never introduced) |
| R16 | ✅ | Subscriptions emit after cache write in refresh() |
| R17 | ✅ | pollLoop goroutine has panic-recover + slog structured logging |
| S7 | ✅ | tmuxSessionsChanged emits full [TmuxSession!]! (bounded list, acceptable per schema) |
| S13 | ✅ | sendTextToPane is verb; tmuxServer/tmuxSessions/tmuxPanes are nouns |
| S15a | ✅ | Schema partial at daemon/tmux/schema.graphql |
| S15b | ✅ | TmuxPane.process and TmuxPane.claudeInstance extend type owned here; resolvers here call service interfaces |
| S16a | ✅ | Typed core: tmuxServer, tmuxSessions, tmuxPanes all loader-backed |
| S16b | ✅ | tmux(args) pass-through with 30s timeout, concurrency cap 4, top-level only guard |
| M1 | ✅ | Mutations enumerated in schema.graphql partial |
| M4 | ✅ | Input validation (empty paneId, empty text) at resolver level |
| M6 | ✅ | sendTextToPane checks origin/capability at resolver level before exec |
| O1 | ✅ | PaneByID key = (host, paneID); PanesByCwd key = (host, cwd); PanesByCommand key = (host, command) |
| O4 | ✅ | Loader batch counts surfaced via PaneByIDBatchCount() |
| O6 | ✅ | Adaptive poll: DefaultPollInterval = 1s; fsnotify watcher reduces jitter |
| O9 | ✅ | Snapshot() eliminated from field resolver hot paths |
| T1 | ✅ | resolver_*_test.go files with stubbed TmuxService |
| T2 | ✅ | scripts/tmux-send-text_test.sh, tmux-kill-pane_test.sh, tmux-new-window_test.sh |
| T5 | ✅ | loaders_test.go verifies ≤1 underlying fetch per batch |
| T6 | ✅ | subscriptions_test.go asserts emit after cache write |
| T7 | ✅ | mutations_test.go asserts S16b guards on pass-through |

## File map

| File | Owns |
|------|------|
| `service.go` | TmuxService interface — sole API surface for consumers |
| `provider.go` | Ported from internal/server/providers/tmux/provider.go + R17 panic-recover |
| `adapter.go` | Ported from internal/server/providers/tmux/adapter.go (stateless I/O) |
| `watcher.go` | Ported from internal/server/providers/tmux/watcher.go |
| `types.go` | Ported from internal/server/providers/tmux/types.go |
| `resolver_server.go` | TmuxServer field resolvers (R6) |
| `resolver_session.go` | TmuxSession field resolvers (R6) |
| `resolver_window.go` | TmuxWindow field resolvers (R6) |
| `resolver_pane.go` | TmuxPane field resolvers + cross-domain back-edges (R6, S15b) |
| `resolver_client.go` | TmuxClient field resolvers (R6) |
| `resolver_query.go` | Query.tmuxServer, Query.tmuxSessions, Query.tmuxPanes, Query.tmux pass-through |
| `loaders.go` | PaneByID, PanesByCwd, PanesByCommand DataLoaders (R3, O1) |
| `mutations.go` | sendTextToPane, killPane, newWindow (L5, M4, M6) |
| `subscriptions.go` | tmuxSessionsChanged (R16, S7) |
| `loaders_test.go` | T5: coalescing verification |
| `resolver_pane_test.go` | T1: pane field resolver tests |
| `resolver_session_test.go` | T1: session field resolver tests |
| `mutations_test.go` | T2 Go wrapper + T7 guard tests |
| `subscriptions_test.go` | T6: emit-after-write test |

## Cross-domain interfaces defined here (R4 — consumer defines the interface)

```go
// PanePsGetter — ps domain must satisfy
type PanePsGetter interface {
    CwdForPid(host string, pid int) string
    CommandForPid(host string, pid int) string
}

// ClaudeInstanceGetter — claude-instance domain must satisfy
type ClaudeInstanceGetter interface {
    InstanceForPane(ctx context.Context, host, paneID string, pid int) (*ClaudeInstanceRef, bool)
}
```

## Cross-domain services consumed (R5)

| Service | Interface name | Used for |
|---------|---------------|----------|
| ps | `PanePsGetter` (defined here) | TmuxPane.process back-edge; PanesByCwd/PanesByCommand |
| claude-instance | `ClaudeInstanceGetter` (defined here) | TmuxPane.claudeInstance back-edge |

## BLOCKED

None. All inputs available.
