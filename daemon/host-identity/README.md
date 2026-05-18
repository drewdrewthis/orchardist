# `daemon/host-identity/`

Machine identity + resource load (CPU/mem/disk/loadavg, 5s TTL).

## Owns

- **Types:** `Host`, `ResourceLoad`
- **Queries:** `host`, `hosts`, `peers`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Why split from `host-services`

Different fetch path and OS adapters from `host-services` (launchd/systemd watchlist). Sharing a `host-` prefix is the only thing they have in common per the design call:

> "host" was identity (`Host`, `ResourceLoad`) — fast cache, 5s TTL, machine-id source. "host-services" is a curated launchd/systemd watchlist — different fetch path, different OS adapter, config-driven. Two domains.

## Field extensions hosted by other domains

| Field added by | Field |
|---|---|
| [`ps`](../ps/) | `Host.processes(filter: ProcessFilter): [Process!]!` |
| [`host-services`](../host-services/) | `Host.hostServices: [HostService!]!` |

## Federation

- v1: `Host.peers` returns `[]` for local; resource fields only for local
- Federation: peers populated via the [`daemon/transport/`](../) shell layer (not a domain)
- `Host.lastSeenAt` is the empty string for "never seen live"; pair with `reachable=false`

## Current source location (pre-refactor)

- `internal/server/providers/host/`

## Constitution citations

- [L4](../../RULES.md): identity is one-shot at boot; resource load polls on 5s TTL — both in-process, no script exec on read
- [L9](../../RULES.md): no persisted state — restart re-observes machineId from `/etc/machine-id` or `IOPlatformUUID`
- [O6](../../RULES.md): 5s TTL is bounded; no per-second polling
- [S5](../../RULES.md): nullability discipline — `resourceLoad` is nullable for the brief cold-boot window before the first sample
