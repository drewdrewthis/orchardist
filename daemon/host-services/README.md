# `daemon/host-services/`

Curated launchd (macOS) / systemd (Linux) unit watchlist.

The watchlist is config-driven — `services` in `~/.orchard/config.json`. Watched services that don't exist on the host surface as `state: not_installed` rather than failing the resolver.

## Owns

- **Types:** `HostService`
- **Enums + inputs:** `HostServiceState`, `HostServiceFilter`
- **Queries:** `hostServices`
- **Field extensions on:** `Host.hostServices`
- **Schema partial:** [`schema.graphql`](./schema.graphql) per [S15](../../RULES.md)

## Why split from `host-identity`

Different fetch path (shellout to `launchctl` / `systemctl --user`) and different OS adapter from `host-identity` (machineId + sysctl/proc). Sharing the `host-` prefix is the only thing they have in common.

## Cross-domain back-edges

- `HostService.host: Host!` — owned by [`host-identity`](../host-identity/), resolver here calls that service.

## Current source location (pre-refactor)

- `internal/server/providers/hostservice/`

## Constitution citations

- [L4](../../RULES.md): query path cached in-process; the `launchctl print` / `systemctl status` shellout polls outside the request lifecycle
- [O6](../../RULES.md): adaptive polling — services don't bounce every second
- [S5](../../RULES.md): `state: not_installed` and `state: unknown` are first-class enum values, not nulls — the daemon distinguishes "no unit on this host" from "service-manager output we couldn't interpret"
- [R14](../../RULES.md): `not_installed` ≠ `unknown` — naming is honest about which one we mean
