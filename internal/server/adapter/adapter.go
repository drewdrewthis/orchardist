// Package adapter declares the cross-provider interfaces from ADR-011 §2/§3.
//
// Every provider implements [Provider]; every provider has a single
// concrete [Adapter] that performs the I/O. Resolvers depend on
// [Provider]; providers depend on [Adapter]. No-one depends on a
// concrete adapter type — the dependency-inversion edge per ADR-011.
//
// The interfaces are intentionally small. New per-backend behaviour
// belongs on the concrete adapter, not the interface — see SOLID-I in
// the worker standards (plans/2026-05-04-orchard-worker-standards.md).
//
// This package is owned by Workstream A's tail (shared utility, per
// scope-discipline §8) and is filled in by the first provider that
// needs it — Workstream B-git.
package adapter

import (
	"context"
	"time"
)

// FreshnessSource indicates how a cached value's last update arrived.
// "watcher-push" — fsnotify / subscription pushed an invalidation.
// "poll"          — provider re-fetched on a timer.
// "stale-cache"   — loaded from a snapshot at boot, not yet refreshed.
type FreshnessSource string

const (
	FreshnessWatcherPush FreshnessSource = "watcher-push"
	FreshnessPoll        FreshnessSource = "poll"
	FreshnessStaleCache  FreshnessSource = "stale-cache"
)

// Freshness describes when and how a cached value was last refreshed.
type Freshness struct {
	LastFetchedAt time.Time
	Source        FreshnessSource
}

// InvalidationEvent is emitted by a provider's Subscribe channel when a
// keyed value may have changed. Resolvers and GraphQL Subscriptions
// listen for these.
type InvalidationEvent[K comparable] struct {
	Key    K
	Reason string
	At     time.Time
}

// Provider is the read-only cache interface every node-backing provider
// implements. There are no Put/Delete methods — orchard authors
// nothing (ADR-011 §1).
type Provider[K comparable, V any] interface {
	// Get reads a single value. Implementations decide whether to serve
	// the cache or re-fetch on miss / staleness.
	Get(ctx context.Context, key K) (V, Freshness, error)

	// GetMany batches reads. DataLoader uses this; implementations must
	// coalesce duplicate keys.
	GetMany(ctx context.Context, keys []K) (map[K]V, map[K]Freshness, error)

	// Keys lists currently-cached keys. Cold-boot returns empty until
	// the watcher hydrates the store.
	Keys(ctx context.Context) ([]K, error)

	// Subscribe returns a channel that emits invalidations for keys
	// whose cached value may have changed. Closes when ctx is cancelled.
	Subscribe(ctx context.Context) <-chan InvalidationEvent[K]
}

// Adapter is the per-backend I/O surface. Stateless; the provider holds
// cache + watcher state and orchestrates lifecycle.
type Adapter[K comparable, V any] interface {
	Fetch(ctx context.Context, key K) (V, error)
	FetchAll(ctx context.Context) (map[K]V, error)
	Watch(ctx context.Context) (<-chan K, error)
	Close() error
}
