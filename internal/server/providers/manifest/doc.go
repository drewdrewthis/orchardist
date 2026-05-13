// Package manifest reflects `references/fleet-manifest.yaml` into the
// daemon as `Host`-shaped metadata.
//
// Provider responsibility (issue #584):
//
//   - At startup, read `$FLEET_MANIFEST` (default
//     `~/.claude/references/fleet-manifest.yaml`).
//   - Re-read on a configurable interval (default 60s) so manifest edits
//     propagate without restarting the daemon.
//   - Parse errors are non-fatal: the provider keeps the previous snapshot
//     and surfaces the error through `Status()` so the `health` resolver
//     can expose it. The daemon never crashes on a bad manifest.
//   - Concurrency: `Snapshot()` and `Status()` are safe to call from any
//     goroutine; the resolver holds them for the lifetime of one request.
//
// The provider does not write to the manifest — the YAML file is owned by
// human edits + the existing `fleet-verify.sh` script.
package manifest
