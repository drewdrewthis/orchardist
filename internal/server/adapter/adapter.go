// Package adapter defines the narrow interface every backend adapter
// implements. Per ADR-011 §3, adapters are stateless I/O — they shell
// out to ps, read a config file, hit an HTTP endpoint, etc. Cache and
// watcher state live in the surrounding provider.
package adapter

import (
	"context"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/provider"
)

// Adapter is the concrete backend implementation a provider wraps.
// Methods are intentionally narrow; per worker-standards §5(I), don't
// add adapter methods that only one provider needs.
type Adapter[K provider.Key, V any] interface {
	// Fetch returns the value for a single key. Used for cache misses
	// and for slow-path opt-in fields.
	Fetch(ctx context.Context, key K) (V, error)

	// FetchAll returns the entire population the adapter knows about.
	// Used at boot and on each watcher tick for poll-based providers.
	FetchAll(ctx context.Context) (map[K]V, error)

	// Watch returns a channel of keys whose values may have changed.
	// The channel closes when ctx is cancelled. For push backends
	// (fsnotify, websocket) this is event-driven; for poll backends
	// it ticks on the poll interval and emits every key that changed
	// vs. the prior tick.
	Watch(ctx context.Context) (<-chan K, error)

	// Close releases any long-lived resources (file watchers, network
	// connections). Idempotent.
	Close() error
}
