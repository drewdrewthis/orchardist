// Package host implements the Host provider — orchard's reflection of
// the local machine's identity and live resource load.
//
// Per ADR-011 §5.1, Host carries:
//
//   - Identity (machine id, hostname, OS, kernel) — one-shot at startup,
//     cached forever; machine identity does not change at runtime.
//   - Resource load (CPU%, mem%, disk%, loadavg{1,5,15}m) — polled with
//     a 5-second TTL via the OS-native shells / file reads.
//   - Reachability — always true for the local host. Federation
//     (Workstream F) extends this with peer hosts.
//
// The provider exposes a single Host (machineId-keyed) for v1: only
// the machine the daemon runs on. Adapter readers are split by OS via
// build tags — identity_darwin.go / identity_linux.go and
// load_darwin.go / load_linux.go — so each platform pulls only its own
// shellouts and parsers into the binary.
package host
