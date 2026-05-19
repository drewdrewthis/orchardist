// Provider holds per-provider freshness counters for the daemon-meta domain.
// This file is internal — consumers use service.go (R2).
//
// Note: daemon-meta itself has no poll loop. It reads freshness counters
// from other domains' providers via ProviderRegistry. The "provider" here is
// the counter-store for domain-meta's own observability.
package daemonmeta

import (
	"sync/atomic"
)

// HealthCounter tracks refresh and failure counts for a single provider.
// Providers in other domains embed or reference this to implement ProviderRegistry.
//
// All methods are safe for concurrent use. Uses atomic operations per R13
// (atomic for counters, no mutex needed for increment-only counters).
type HealthCounter struct {
	refreshCount atomic.Int64
	failureCount atomic.Int64
}

// RecordSuccess increments the refresh counter and returns the new count.
func (h *HealthCounter) RecordSuccess() int64 {
	return h.refreshCount.Add(1)
}

// RecordFailure increments the failure counter and returns the new count.
func (h *HealthCounter) RecordFailure() int64 {
	return h.failureCount.Add(1)
}

// RefreshCount returns the total successful refreshes.
func (h *HealthCounter) RefreshCount() int64 {
	return h.refreshCount.Load()
}

// FailureCount returns the total failures.
func (h *HealthCounter) FailureCount() int64 {
	return h.failureCount.Load()
}
