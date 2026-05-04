// Package adapter defines the contracts every orchard provider implements.
//
// Per ADR-011 §2-§4, the daemon is a read-only join layer over backends.
// Each backend has a Provider that owns cache + watcher + invalidation;
// each Provider holds an Adapter that does the raw I/O.
//
// These types are intentionally tiny — they are the minimum surface
// every provider needs. Providers that want something fancier (snapshots,
// DataLoader hooks) compose extra behaviour locally; this package stays
// boring on purpose.
//
// Workstream B-host is the first provider in the tree, so this is also
// the first place these contracts land. Subsequent providers reuse them.
package adapter

import (
	"context"
	"time"
)

// Key is what a Provider keys its cache by. Any comparable type works;
// most providers use a typed string (e.g. host.HostID).
type Key comparable

// Node is the value type a Provider returns. Concretely a gqlgen-
// generated type from internal/server/graphql, but the contract has no
// interest in the shape — providers move opaque values from adapter to
// resolver.
type Node any

// Provider is the resolver-facing read API for a backend domain.
//
// Resolvers depend on this interface, never on concrete provider types
// — keeps SOLID-D honest and lets WS-C swap providers behind the same
// resolver code (e.g. real adapter under tests, mock at the boundary
// for one specific error-path assertion).
//
// All methods are safe for concurrent use. Watcher state lives inside
// the Provider impl; callers do not own a Watch goroutine.
type Provider[K Key, V Node] interface {
	// Get returns one value by key. Cache-hit when fresh; otherwise
	// the underlying Adapter is invoked and the result cached.
	Get(ctx context.Context, key K) (V, Freshness, error)

	// GetMany returns many values in a single call. Implementations
	// coalesce duplicate keys and may parallelise the underlying
	// adapter calls; DataLoader (WS-C) batches resolver fan-outs into
	// this method. Missing keys are simply absent from the maps.
	GetMany(ctx context.Context, keys []K) (map[K]V, map[K]Freshness, error)

	// Keys returns every key the provider currently has cached. Cold
	// boot returns an empty slice until the watcher hydrates the
	// store. Callers that need a guaranteed snapshot should call
	// GetMany with the keys they expect.
	Keys(ctx context.Context) ([]K, error)

	// Subscribe returns a channel that emits whenever the value for a
	// key may have changed. Resolvers re-fetch on these events;
	// GraphQL Subscriptions (WS-C) push them to clients.
	//
	// The channel closes when ctx is cancelled. Implementations
	// guarantee non-blocking sends — a slow consumer drops events,
	// not the watcher loop.
	Subscribe(ctx context.Context) <-chan InvalidationEvent[K]
}

// FreshnessSource tags how a Freshness value was obtained.
type FreshnessSource string

const (
	// SourceWatcherPush — last update came from the backend's watcher
	// (fsnotify, inotify, websocket subscription, etc.).
	SourceWatcherPush FreshnessSource = "watcher-push"

	// SourcePoll — last update came from the provider's poll loop.
	SourcePoll FreshnessSource = "poll"

	// SourceStaleCache — value is in cache but past its TTL; returned
	// to callers who accept stale reads while a refresh is queued.
	SourceStaleCache FreshnessSource = "stale-cache"
)

// Freshness describes when and how a cached value was last updated.
// Resolvers can surface this to clients (e.g. "this data is 7s old");
// the daemon itself uses it for TTL backstops.
type Freshness struct {
	LastFetchedAt time.Time
	Source        FreshnessSource
}

// InvalidationEvent is the per-key signal that a Provider's value may
// have changed. Watchers emit these on Provider.Subscribe channels.
type InvalidationEvent[K Key] struct {
	Key    K
	Reason string
	At     time.Time
}

// Adapter is the per-provider I/O contract — the thing that actually
// shells out, reads files, or hits HTTP. Stateless on purpose: any
// caching, debouncing, or hydration lives in the Provider above it.
type Adapter[K Key, V Node] interface {
	// Fetch returns one value by key. Errors are wrapped with %w.
	Fetch(ctx context.Context, key K) (V, error)

	// FetchAll returns every value the backend currently exposes.
	// Used at boot to hydrate the Store; providers that cannot
	// enumerate (e.g. lookup-only backends) return an empty map and
	// nil error.
	FetchAll(ctx context.Context) (map[K]V, error)

	// Watch starts a long-running watcher and emits the keys whose
	// values have changed. The channel closes when ctx is cancelled
	// or Close is called.
	Watch(ctx context.Context) (<-chan K, error)

	// Close releases watcher / connection resources.
	Close() error
}
