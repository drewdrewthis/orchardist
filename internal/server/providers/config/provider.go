package config

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
)

// FreshnessSource enumerates how the cached value was last populated.
// Defined inline rather than importing a shared package because no other
// provider needs it yet; promote when a second consumer materialises.
type FreshnessSource string

const (
	// SourceWatcherPush — value loaded after a watcher event.
	SourceWatcherPush FreshnessSource = "watcher-push"
	// SourcePoll — value loaded by a periodic refresh (unused by the
	// config provider in v1; reserved for parity with ADR-011 §2).
	SourcePoll FreshnessSource = "poll"
	// SourceStaleCache — value served from a snapshot that has not
	// been re-validated since boot.
	SourceStaleCache FreshnessSource = "stale-cache"
	// SourceCold — first-load population direct from the adapter.
	SourceCold FreshnessSource = "cold-load"
)

// Freshness annotates each cache entry with the time and origin of the
// last successful load. Mirrors ADR-011 §2's Freshness struct.
type Freshness struct {
	LastFetchedAt time.Time
	Source        FreshnessSource
}

// InvalidationEvent is emitted on the provider's Subscribe channel
// whenever a key's value may have changed.
type InvalidationEvent struct {
	Key    RepoID
	Reason string
	At     time.Time
}

// Provider surfaces Repo nodes to the GraphQL resolver layer. It owns
// an in-memory cache, a single fsnotify watcher (via the adapter), and
// a fan-out for Subscribers.
//
// Per ADR-011 §2 the provider exposes only reads — no Put / Delete.
// Writes happen via the CLI editing the config file, and the watcher
// turns those edits into invalidation events.
type Provider struct {
	adapter adapter.Adapter[RepoID, Repo]
	logger  *slog.Logger

	mu     sync.RWMutex
	cache  map[RepoID]Repo
	fresh  map[RepoID]Freshness
	loaded bool

	subMu sync.Mutex
	subs  map[chan InvalidationEvent]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewProvider wires an adapter into a Provider. The provider does not
// touch the adapter until Start is called, so it is safe to construct
// at process boot before the daemon has decided whether to run.
func NewProvider(a adapter.Adapter[RepoID, Repo], logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		adapter: a,
		logger:  logger,
		cache:   map[RepoID]Repo{},
		fresh:   map[RepoID]Freshness{},
		subs:    map[chan InvalidationEvent]struct{}{},
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start hydrates the cache from the adapter and launches the watcher
// goroutine. Subsequent calls are no-ops; one provider, one lifecycle.
func (p *Provider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		if err := p.reload(ctx, "boot", SourceCold); err != nil {
			startErr = fmt.Errorf("hydrate config cache: %w", err)
			return
		}
		ch, err := p.adapter.Watch(ctx)
		if err != nil {
			startErr = fmt.Errorf("start watcher: %w", err)
			return
		}
		go p.run(ctx, ch)
	})
	return startErr
}

// Stop tears down the watcher and any subscribers. Idempotent.
func (p *Provider) Stop() error {
	var err error
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
		err = p.adapter.Close()
		p.subMu.Lock()
		for ch := range p.subs {
			close(ch)
			delete(p.subs, ch)
		}
		p.subMu.Unlock()
	})
	return err
}

// Get returns one project by ID, plus its freshness. Cache hit is the
// common path; on miss the adapter is consulted and the result cached.
func (p *Provider) Get(ctx context.Context, key RepoID) (Repo, Freshness, error) {
	if v, f, ok := p.cacheGet(key); ok {
		return v, f, nil
	}
	v, err := p.adapter.Fetch(ctx, key)
	if err != nil {
		return Repo{}, Freshness{}, err
	}
	f := Freshness{LastFetchedAt: time.Now(), Source: SourceCold}
	p.cachePut(key, v, f)
	return v, f, nil
}

// GetMany is the DataLoader-friendly batch read. Duplicate keys in the
// input slice are coalesced — the result map has at most one entry per
// distinct key. Cache hits avoid the adapter entirely; misses share a
// single FetchAll round-trip when more than one is required.
func (p *Provider) GetMany(ctx context.Context, keys []RepoID) (map[RepoID]Repo, map[RepoID]Freshness, error) {
	out := make(map[RepoID]Repo, len(keys))
	freshness := make(map[RepoID]Freshness, len(keys))

	missing := make([]RepoID, 0, len(keys))
	seen := make(map[RepoID]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if v, f, ok := p.cacheGet(k); ok {
			out[k] = v
			freshness[k] = f
			continue
		}
		missing = append(missing, k)
	}

	if len(missing) == 0 {
		return out, freshness, nil
	}

	// One FetchAll covers all misses — the config file is a single
	// document, so there is no per-key cost saving in fetching them
	// individually.
	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	for _, k := range missing {
		v, ok := all[k]
		if !ok {
			continue
		}
		f := Freshness{LastFetchedAt: now, Source: SourceCold}
		p.cachePut(k, v, f)
		out[k] = v
		freshness[k] = f
	}
	return out, freshness, nil
}

// Keys returns every project ID currently in the cache. Cold boot
// returns an empty slice; the watcher hydrates the cache before any
// resolver calls in well-formed setups.
func (p *Provider) Keys(_ context.Context) ([]RepoID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]RepoID, 0, len(p.cache))
	for k := range p.cache {
		out = append(out, k)
	}
	return out, nil
}

// List returns every cached Repo as a slice. This is what the
// resolver for `Query.projects` calls; List is a thin convenience over
// the read-mutex.
func (p *Provider) List(_ context.Context) ([]Repo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Repo, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	return out, nil
}

// Subscribe returns a buffered channel that receives invalidation
// events for as long as ctx is alive. Closing the returned context (or
// calling Stop) cleans the subscription up.
func (p *Provider) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, 8)
	p.subMu.Lock()
	p.subs[ch] = struct{}{}
	p.subMu.Unlock()

	go func() {
		<-ctx.Done()
		p.subMu.Lock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
		p.subMu.Unlock()
	}()
	return ch
}

// run drains the adapter's Watch channel, reloading the cache on every
// signal and broadcasting invalidations to subscribers.
func (p *Provider) run(ctx context.Context, ch <-chan RepoID) {
	defer close(p.doneCh)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := p.reload(ctx, "fs-modify", SourceWatcherPush); err != nil {
				p.logger.Error("config: reload failed", "err", err)
			}
		}
	}
}

// reload fetches the full set of projects, replaces the cache, and
// broadcasts invalidations for every key whose value changed (or that
// disappeared). Reason is propagated to subscribers verbatim.
func (p *Provider) reload(ctx context.Context, reason string, source FreshnessSource) error {
	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return err
	}
	now := time.Now()

	p.mu.Lock()
	old := p.cache
	p.cache = all
	p.fresh = make(map[RepoID]Freshness, len(all))
	for k := range all {
		p.fresh[k] = Freshness{LastFetchedAt: now, Source: source}
	}
	p.loaded = true
	p.mu.Unlock()

	// Diff and broadcast. We emit one event per added / changed /
	// removed key. Subscribers can coalesce; the daemon does not.
	changed := make([]RepoID, 0)
	for k, v := range all {
		ov, had := old[k]
		if !had || ov != v {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, still := all[k]; !still {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 && reason == "boot" {
		// No-op boot when the file is empty: nothing to broadcast.
		return nil
	}
	p.broadcast(changed, reason, now)
	return nil
}

// broadcast fans an InvalidationEvent out to every subscriber. The
// per-channel buffer is small; we drop on a full buffer rather than
// block the watcher goroutine — subscribers that fall behind miss
// events but stay alive.
func (p *Provider) broadcast(keys []RepoID, reason string, at time.Time) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, k := range keys {
		ev := InvalidationEvent{Key: k, Reason: reason, At: at}
		for ch := range p.subs {
			select {
			case ch <- ev:
			default:
				p.logger.Warn("config: subscriber lagging, dropping event", "key", string(k))
			}
		}
	}
}

func (p *Provider) cacheGet(k RepoID) (Repo, Freshness, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.cache[k]
	if !ok {
		return Repo{}, Freshness{}, false
	}
	return v, p.fresh[k], true
}

func (p *Provider) cachePut(k RepoID, v Repo, f Freshness) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[k] = v
	p.fresh[k] = f
}
