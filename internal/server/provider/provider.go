// Package provider defines the load-bearing Provider interface that every
// orchard data backend (config, git, tmux, ps, …) implements.
//
// Per ADR-011 §2 the Provider is the seam resolvers depend on: never on
// concrete adapter types or shellout helpers. One Provider per backend
// domain. Each provider owns its cache, freshness policy, watcher, and
// invalidation stream. Adapters underneath are stateless I/O.
//
// **No mutations.** No `Put`, no `Delete`, no `Mutate`. Backends that
// happen to support writes are written by clients talking to the
// backend directly; orchard is read-only.
package provider

import (
	"context"
	"time"
)

// Key is the constraint for provider key types. Keys must be comparable
// so they can be used as map keys in the Store.
type Key interface {
	comparable
}

// Provider is the read-only access surface every backend exposes. It
// presents a cache that resolvers read from and a Subscribe channel that
// pushes invalidations into the GraphQL Subscription layer (Workstream
// C) and into peer-proxy adapters (Workstream F).
type Provider[K Key, V any] interface {
	// Get returns the value for key, falling back to the Adapter when
	// the cache is missing or stale. Returns the value's freshness so
	// callers can reason about staleness if they care.
	Get(ctx context.Context, key K) (V, Freshness, error)

	// GetMany returns values for the given keys in one call. The
	// returned maps are keyed by the input key. DataLoader (Workstream
	// C) batches resolver lookups through this method to avoid N+1.
	GetMany(ctx context.Context, keys []K) (map[K]V, map[K]Freshness, error)

	// Keys returns every key the provider currently knows about. Cold
	// boot returns an empty slice until the Watcher hydrates.
	Keys(ctx context.Context) ([]K, error)

	// Subscribe returns a channel of invalidation events. The channel
	// closes when ctx is cancelled. Subscribers MUST drain the channel
	// promptly; the provider drops events on a slow consumer.
	Subscribe(ctx context.Context) <-chan InvalidationEvent[K]
}

// Freshness describes when a value was fetched and from where. Resolvers
// can ignore this; tools (like `orchard daemon status`) use it to surface
// "this cell is stale" indicators to the user.
type Freshness struct {
	LastFetchedAt time.Time
	Source        FreshnessSource
}

// FreshnessSource identifies how a cached value got there. The set is
// closed; new entries require an ADR.
type FreshnessSource string

const (
	// SourceWatcherPush — value arrived via a push from the backend
	// (fsnotify event, websocket frame, webhook, etc.).
	SourceWatcherPush FreshnessSource = "watcher-push"

	// SourcePoll — value was refreshed by a periodic poll cycle.
	SourcePoll FreshnessSource = "poll"

	// SourceStaleCache — value loaded from a snapshot file at boot;
	// not yet refreshed by a poll or push.
	SourceStaleCache FreshnessSource = "stale-cache"
)

// InvalidationEvent is emitted on a provider's Subscribe channel when
// the value for Key may have changed. Subscribers re-read through Get /
// GetMany; the event itself does not carry the new value.
type InvalidationEvent[K Key] struct {
	Key    K
	Reason string
	At     time.Time
}
