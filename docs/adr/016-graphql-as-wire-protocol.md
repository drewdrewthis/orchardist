# ADR-016: GraphQL Is the Wire Protocol

## Status
Accepted.

## Decision
GraphQL (served by the daemon at `127.0.0.1:7777/graphql`) is the wire protocol for all client-daemon communication. Clients do not exec local processes (`git`, `gh`, `tmux`) for state queries or mutations. JSON dumps over HTTP and SSH-piped CLI output (the `--json` legacy path) remain only for cross-daemon federation, not for client→daemon.

## Why
- Clients are heterogeneous: orchard-tui (Rust), orchard-gui (Svelte+Tauri), orchard-worktree CLI, mobile, future web. A single typed protocol prevents N×M adapters.
- Schema-first contracts catch breakage at codegen, not at runtime.
- Subscriptions enable real-time UX (transcript live-refresh, session activity) without polling.
- The daemon is already the join authority (ADR-008); it should be the only API surface.

## How
- Schema lives in `internal/server/graphql/schema.graphql`; codegen via gqlgen.
- All queries, mutations, and subscriptions are defined here.
- Federation between daemons uses the same schema (one daemon proxies to another).
- Heavy reads (full transcripts) flow through HTTP REST endpoints documented in the schema; not GraphQL fields.

## What is NOT GraphQL
- Heavy file streams (jsonl tail reads): HTTP `GET /v1/conversations/<uuid>/jsonl`.
- PTY data: WebSocket per-pane.
- Cross-daemon federation: `--json` snapshot over SSH (legacy ADR-008/009).

## Consequences
- New client features: schema first, then resolver, then UI. No shortcut paths.
- gqlgen and Houdini codegen are part of the build.
- Schema drift is caught by codegen failures, not runtime errors.
