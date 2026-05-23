// Package hostidentity implements the host-identity domain — machine identity
// (machineId, hostname, os, kernel) and live resource load (CPU/mem/disk/loadavg)
// with a 5s TTL.
//
// Per L4: identity is one-shot at boot (in-process); resource load polls via
// OS-native syscalls/shellouts, never via daemon-exec'd scripts on the resolver
// hot path. Per L9: no persisted state — restart re-reads from the OS.
//
// Build-tag–selected OS implementations live in adapter_darwin.go /
// adapter_linux.go. They provide the NewIdentityReader and NewLoadReader
// constructors; the rest of the package is OS-agnostic.
package hostidentity

import "context"

// HostID is the cache key for the Provider. It wraps the OS-issued machine id;
// v1 only ever holds one value (the local machine).
type HostID string

// Identity is the OS-issued machine identity. Fields do not change across the
// daemon's lifetime — machine id and hostname are boot-stable for v1.
type Identity struct {
	MachineID string
	Hostname  string
	OS        string
	Kernel    string // best-effort; empty when uname unavailable
}

// Load is a snapshot of resource utilisation. Percentages are 0..100.
// Load averages are the kernel-reported 1/5/15-minute moving averages.
type Load struct {
	CPUPercent  float64
	MemPercent  float64
	DiskPercent float64
	LoadAvg1m   float64
	LoadAvg5m   float64
	LoadAvg15m  float64
}

// IdentityReader reads the local machine's identity. Implementations are
// OS-specific (adapter_darwin.go, adapter_linux.go) selected at compile time
// via build tags. Read once at startup; the Provider caches for daemon lifetime.
type IdentityReader interface {
	Read(ctx context.Context) (Identity, error)
}

// LoadReader samples the local machine's resource utilisation. Implementations
// are OS-specific and selected at compile time via build tags.
// Read on a 5s TTL by the Provider's poll loop. ctx must be respected.
type LoadReader interface {
	Read(ctx context.Context) (Load, error)
}

// clampPercent keeps a percentage in 0..100 even when the underlying source
// reports something noisy (e.g. ever-so-slightly negative idle from rounding).
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
