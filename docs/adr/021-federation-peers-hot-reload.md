# ADR-021: Federation peers hot-reload

**Status:** Accepted

## Context

Prior to this change the `peers[]` array in `~/.orchard/config.json` was read
once at daemon startup. Adding or removing a peer required restarting the
daemon, which interrupted all in-flight probes and attention events.

## Decision

The daemon owns an `fsnotify` watcher on `~/.orchard/config.json` (parent-dir
strategy to handle macOS atomic-rename writes). On a config change event the
daemon debounces within ≤2 seconds, re-parses via `LoadFederationConfig`, and
calls `peerproxy.Provider.ApplyPeers(newCfg)`.

`ApplyPeers` diffs the running peer map against the new config:

- **Added names** → `AddPeer(PeerRow)` spawns a new probe goroutine.
- **Removed names** → `RemovePeer(name)` cancels the per-peer context and
  drops it from the map.
- **Changed address or TLS** → treated as remove + add (client transport is
  not safely mutable in flight).
- **Unchanged peers** → goroutine and context are untouched (no restart).

Parse errors keep the last good peer set live; `ApplyPeers` is not called.

## Prerequisites for adding a peer

Every candidate peer VM must satisfy both steps before it is added to
`peers[]`:

1. Run `boxd proxy new graphql --vm=<name> --port=7777` to expose the daemon
   over HTTPS from inside the VM.
2. Have `orchard-daemon` running and listening on `127.0.0.1:7777` inside the
   VM.

Peers added before these steps are complete will surface as
`Host.peers[].reachable == false` until both the boxd proxy and the daemon
are in place. This is the expected operator UX, not a bug.

## Out of scope

Auto-discovery via `boxd list` (determining which VMs are federation peers
automatically) is deferred. `boxd list` cannot distinguish federation peers
from ordinary VMs because the daemon is not publicly exposed by default.
This work is tracked in a separate follow-up issue linked from #566.
