package ps

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// invalidationEvent is the per-key signal that a cached process may have
// changed. The subscription layer fans these out to GraphQL subscribers.
type invalidationEvent struct {
	Key    ProcessID
	Reason string
	At     time.Time
}

// Provider is the cache + poll loop for the ps domain. It owns the
// in-memory process table and notifies subscribers on changes (R10).
// Consumers call the Service interface (R2) — never this type directly.
type Provider struct {
	adapter *Adapter
	logger  *slog.Logger

	mu      sync.RWMutex
	cache   map[ProcessID]Process
	updated time.Time

	// subsMu guards the subscriber list. RWMutex not needed since
	// writes (subscribe/unsubscribe) are infrequent.
	subsMu sync.Mutex
	subs   []chan invalidationEvent

	// argsLoader and cwdLoader are DataLoader-shaped batch loaders for
	// slow-path opt-in fields (S10, R3, O10).
	argsLoader *batchLoader[int, []string]
	cwdLoader  *batchLoader[int, string]
}

// NewProvider constructs a Provider. The poll loop starts when Start is
// called — constructing without starting lets tests inspect state.
func NewProvider(adapter *Adapter, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		adapter: adapter,
		logger:  logger,
		cache:   make(map[ProcessID]Process),
	}
	p.argsLoader = newBatchLoader[int, []string](20*time.Millisecond, 512, func(ctx context.Context, pids []int) (map[int][]string, error) {
		return adapter.FetchArgs(ctx, pids)
	})
	p.cwdLoader = newBatchLoader[int, string](20*time.Millisecond, 128, func(ctx context.Context, pids []int) (map[int]string, error) {
		return adapter.FetchCwds(ctx, pids)
	})
	return p
}

// Start hydrates the cache with the first FetchAll, then launches the
// background poll loop. Blocks until the first fetch completes so
// resolvers don't see an empty cache on cold boot.
func (p *Provider) Start(ctx context.Context) error {
	first, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return fmt.Errorf("ps provider: initial fetch: %w", err)
	}
	p.replaceAll(first)

	ch, err := p.adapter.Watch(ctx)
	if err != nil {
		return fmt.Errorf("ps provider: watch: %w", err)
	}
	go p.consumeWatcher(ctx, ch)
	return nil
}

// consumeWatcher drains the adapter's Watch channel, refreshes the
// cache, and fans out invalidation events. Per R17: panic-recover + log.
func (p *Provider) consumeWatcher(ctx context.Context, in <-chan ProcessID) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("ps provider: consumeWatcher panic", "panic", r)
		}
	}()
	for range in {
		all, err := p.adapter.FetchAll(ctx)
		if err != nil {
			p.logger.Warn("ps provider: refetch failed", "err", err)
			continue
		}
		changed := p.replaceAll(all)
		now := time.Now()
		for _, k := range changed {
			p.fanout(invalidationEvent{Key: k, Reason: "poll", At: now})
		}
		// Drain remaining events from the same tick to avoid redundant fetches.
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

// replaceAll atomically swaps the entire cache to next. Returns the set
// of keys that changed (added, removed, or value-modified). Emit AFTER
// this write (R16).
func (p *Provider) replaceAll(next map[ProcessID]Process) []ProcessID {
	p.mu.Lock()
	defer p.mu.Unlock()

	changed := make([]ProcessID, 0)
	now := time.Now()

	for k, v := range next {
		prior, ok := p.cache[k]
		if !ok || !processEqualsHotPath(prior, v) {
			changed = append(changed, k)
		}
		p.cache[k] = v
	}
	for k := range p.cache {
		if _, ok := next[k]; !ok {
			delete(p.cache, k)
			changed = append(changed, k)
		}
	}
	p.updated = now
	return changed
}

// fanout pushes an event to every subscriber. Slow consumers get dropped
// (best-effort push).
func (p *Provider) fanout(ev invalidationEvent) {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for _, ch := range p.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// List returns a snapshot of every cached Process. Used by Host.processes
// resolver (not Snapshot() in a field path — this is the collection entrypoint).
func (p *Provider) List() []Process {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Process, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	return out
}

// Get returns a single Process by key.
func (p *Provider) Get(key ProcessID) (Process, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.cache[key]
	return v, ok
}

// Subscribe registers a new fanout channel. The channel closes when ctx
// is cancelled (R12: returns receive-only channel).
func (p *Provider) Subscribe(ctx context.Context) <-chan invalidationEvent {
	ch := make(chan invalidationEvent, 32)
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

// HostID returns the host id this provider materialises ProcessIDs for.
func (p *Provider) HostID() string { return p.adapter.HostID() }

// LoadArgs returns argv for the given pids via the batched loader (R3, O10).
func (p *Provider) LoadArgs(ctx context.Context, pids []int) (map[int][]string, error) {
	return p.argsLoader.LoadMany(ctx, pids)
}

// LoadCwd returns cwd for a single pid via the batched loader (R3, O10).
func (p *Provider) LoadCwd(ctx context.Context, pid int) (string, error) {
	return p.cwdLoader.Load(ctx, pid)
}

// LoadCwds returns cwd for multiple pids (used by cwdPrefix filter).
func (p *Provider) LoadCwds(ctx context.Context, pids []int) (map[int]string, error) {
	return p.cwdLoader.LoadMany(ctx, pids)
}

// Refresh forces an immediate poll cycle (useful for tests).
func (p *Provider) Refresh(ctx context.Context) error {
	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return err
	}
	changed := p.replaceAll(all)
	now := time.Now()
	for _, k := range changed {
		p.fanout(invalidationEvent{Key: k, Reason: "refresh", At: now})
	}
	return nil
}
