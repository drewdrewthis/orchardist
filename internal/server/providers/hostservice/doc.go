// Package hostservice implements the HostService provider — orchard's
// reflection of curated launchd (macOS) / systemd-user (Linux) units.
//
// Per ADR-011 §5.1, HostService carries:
//
//   - Identity (host_id, name) — name is config-driven (see config.go).
//   - State (active|inactive|failed|unknown) — what the OS service
//     manager reports right now.
//   - since — RFC 3339 timestamp the unit entered the current state, when
//     the OS surfaces one.
//   - exitCode — most recent exit code of the last completed run.
//   - logTail — last 20 log lines (Linux only; launchd does not stream
//     a per-unit tail in the same shape).
//
// The watchlist (which unit names to surface) is read from
// `~/.config/orchard/config.json` `services` array; defaults to
// `["claude-remote", "orchard", "chezmoi"]` if the key is missing.
// Watched units that don't exist on the host surface as
// `state: unknown` rather than failing the resolver — ADR-011 contract.
//
// State refreshes on a 5-second TTL (ADR-011 §12 — "states change
// quickly when unit cycles"). The Provider's poll loop refreshes on
// the same cadence proactively, so callers normally read fresh cache.
//
// Adapter shellouts are split by OS via build tags —
// adapter_darwin.go (`launchctl list <Label>`) and adapter_linux.go
// (`systemctl --user is-active`, `systemctl --user show`,
// `journalctl --user -u <name>`). No runtime `runtime.GOOS == ...`
// branching outside what build tags already do.
//
// If the OS service-manager binary itself is missing
// (`launchctl` / `systemctl`), the Adapter returns ErrServiceManagerMissing
// and the resolver surfaces a per-field GraphQL error rather than
// erroring the whole resolver. Each watched name is fetched
// independently, so a missing unit can't take out a healthy peer.
package hostservice
