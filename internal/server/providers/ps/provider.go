package ps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/provider"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/store"
)

// Provider is the orchard-side facade over PsAdapter. Owns the cache,
// the watcher loop, and the invalidation fanout. Resolvers depend on
// this concrete type via its Provider[ProcessID, Process] interface
// satisfaction (worker-standards §5(D)).
type Provider struct {
	adapter *PsAdapter
	store   *store.Store[ProcessID, Process]
	logger  *slog.Logger

	// subs tracks live Subscribe channels for fanout. The provider drops
	// events on slow consumers (best-effort push per ADR-011 §2).
	subsMu sync.Mutex
	subs   []chan provider.InvalidationEvent[ProcessID]

	// argsLoader and cwdLoader are per-resolver-call DataLoaders for the
	// slow-path opt-in fields. Each call to LoadArgs / LoadCwd batches
	// over a short window. Future Workstream C will swap these for
	// proper per-request DataLoaders.
	argsLoader *batchLoader[int, []string]
	cwdLoader  *batchLoader[int, string]
}

// New constructs a Provider for the given host. The watcher starts when
// Start is called — letting tests construct without spawning goroutines.
func New(adapter *PsAdapter, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		adapter: adapter,
		store:   store.New[ProcessID, Process](),
		logger:  logger,
	}
	p.argsLoader = newBatchLoader[int, []string](20*time.Millisecond, 512, func(ctx context.Context, pids []int) (map[int][]string, error) {
		return adapter.FetchArgs(ctx, pids)
	})
	p.cwdLoader = newBatchLoader[int, string](20*time.Millisecond, 128, func(ctx context.Context, pids []int) (map[int]string, error) {
		return adapter.FetchCwds(ctx, pids)
	})
	return p
}

// HostID exposes the host id the provider materialises ProcessIDs for.
// Resolvers use this to construct host nodes.
func (p *Provider) HostID() string { return p.adapter.HostID() }

// Start launches the watcher loop. It blocks until the first
// FetchAll returns (so resolvers don't see an empty cache on cold boot)
// or until ctx fires. Subsequent ticks run in the background.
//
// Returns the adapter's first-fetch error so callers can fail fast on
// fundamentally-broken environments (no `ps` on PATH, etc.).
func (p *Provider) Start(ctx context.Context) error {
	// Synchronous initial fetch so the cache is warm before resolvers run.
	first, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return fmt.Errorf("ps provider: initial fetch: %w", err)
	}
	p.store.ReplaceAll(first, provider.SourcePoll, processEqualsHotPath)

	// Background watcher.
	ch, err := p.adapter.Watch(ctx)
	if err != nil {
		return fmt.Errorf("ps provider: watch: %w", err)
	}
	go p.consumeWatcher(ctx, ch)
	return nil
}

// consumeWatcher drains the adapter's Watch channel, refreshes the
// store, and fans out InvalidationEvents to subscribers.
func (p *Provider) consumeWatcher(ctx context.Context, in <-chan ProcessID) {
	for range in {
		// Watcher is "something changed" — re-fetch full state and let
		// the store compute the precise delta.
		all, err := p.adapter.FetchAll(ctx)
		if err != nil {
			p.logger.Warn("ps provider: refetch failed", "err", err)
			continue
		}
		changed := p.store.ReplaceAll(all, provider.SourcePoll, processEqualsHotPath)
		now := time.Now()
		for _, k := range changed {
			p.fanout(provider.InvalidationEvent[ProcessID]{Key: k, Reason: "poll", At: now})
		}
		// Drain any pending events the adapter pushed in the same tick.
		// We've already refetched; consuming them prevents redundant
		// FetchAlls without losing data.
		for {
			select {
			case <-in:
				continue
			default:
			}
			break
		}
	}
}

// fanout pushes an event to every subscriber. Slow consumers get
// dropped — orchard's "subscribers MUST drain promptly" contract.
func (p *Provider) fanout(ev provider.InvalidationEvent[ProcessID]) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for _, ch := range p.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Get returns a Process from the store, falling back to the adapter on
// a miss. Freshness reflects the store's record for cache hits, or
// SourcePoll for adapter-served misses.
func (p *Provider) Get(ctx context.Context, key ProcessID) (Process, provider.Freshness, error) {
	if v, f, ok := p.store.Get(key); ok {
		return v, f, nil
	}
	v, err := p.adapter.Fetch(ctx, key)
	if err != nil {
		return Process{}, provider.Freshness{}, err
	}
	p.store.Put(key, v, provider.SourcePoll)
	v2, f, ok := p.store.Get(key)
	if !ok {
		return v, provider.Freshness{LastFetchedAt: time.Now(), Source: provider.SourcePoll}, nil
	}
	return v2, f, nil
}

// GetMany batches a multi-key read against the cache. Misses are not
// fetched individually — for the ps provider, a missing key means the
// process exited; returning a hole is more honest than re-shelling
// to ps for each one. Resolvers can call Get for a single missing key
// if they really care.
func (p *Provider) GetMany(ctx context.Context, keys []ProcessID) (map[ProcessID]Process, map[ProcessID]provider.Freshness, error) {
	values := make(map[ProcessID]Process, len(keys))
	freshness := make(map[ProcessID]provider.Freshness, len(keys))
	for _, k := range keys {
		if v, f, ok := p.store.Get(k); ok {
			values[k] = v
			freshness[k] = f
		}
	}
	return values, freshness, nil
}

// Keys returns every process currently in the cache. Resolvers that
// want the full population (e.g. Host.processes with no filter) call
// this and then Snapshot via List.
func (p *Provider) Keys(ctx context.Context) ([]ProcessID, error) {
	return p.store.Keys(), nil
}

// List returns a snapshot of every cached Process. Convenience method
// that the resolver layer prefers over Keys + GetMany.
func (p *Provider) List() []Process {
	snap := p.store.Snapshot()
	out := make([]Process, 0, len(snap))
	for _, v := range snap {
		out = append(out, v)
	}
	return out
}

// Subscribe registers a new fanout channel and returns it. The channel
// closes when ctx is cancelled.
func (p *Provider) Subscribe(ctx context.Context) <-chan provider.InvalidationEvent[ProcessID] {
	ch := make(chan provider.InvalidationEvent[ProcessID], 32)
	p.subsMu.Lock()
	p.subs = append(p.subs, ch)
	p.subsMu.Unlock()
	go func() {
		<-ctx.Done()
		p.subsMu.Lock()
		defer p.subsMu.Unlock()
		for i, c := range p.subs {
			if c == ch {
				p.subs = append(p.subs[:i], p.subs[i+1:]...)
				break
			}
		}
		close(ch)
	}()
	return ch
}

// Refresh forces an immediate poll cycle. Useful for tests and for the
// CLI's `query processes` command which wants fresh data on each call.
func (p *Provider) Refresh(ctx context.Context) error {
	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return err
	}
	changed := p.store.ReplaceAll(all, provider.SourcePoll, processEqualsHotPath)
	now := time.Now()
	for _, k := range changed {
		p.fanout(provider.InvalidationEvent[ProcessID]{Key: k, Reason: "refresh", At: now})
	}
	return nil
}

// LoadArgs returns argv for the given pids. Batches via the per-provider
// loader so a query selecting `args` on N processes makes one shellout.
func (p *Provider) LoadArgs(ctx context.Context, pids []int) (map[int][]string, error) {
	return p.argsLoader.LoadMany(ctx, pids)
}

// LoadCwd returns cwd for a single pid. The resolver typically asks one
// at a time as it walks the result list; the loader coalesces them.
func (p *Provider) LoadCwd(ctx context.Context, pid int) (string, error) {
	return p.cwdLoader.Load(ctx, pid)
}

// LoadCwds returns cwd for multiple pids. Used by the CLI's
// --by-cwd filter, which forces resolution before applying the prefix.
func (p *Provider) LoadCwds(ctx context.Context, pids []int) (map[int]string, error) {
	return p.cwdLoader.LoadMany(ctx, pids)
}

// ErrNotFound is returned by Get when the key is missing AND the
// adapter cannot fetch it (e.g. process gone). Callers can use
// errors.Is to distinguish from other errors.
var ErrNotFound = errors.New("ps: process not found")
