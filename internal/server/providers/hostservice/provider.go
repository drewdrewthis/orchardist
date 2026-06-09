package hostservice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
)

// Provider is the host-service-domain Provider implementation.
//
// Owns the Adapter (OS-conditional via build tags) plus an in-memory
// store of the latest Snapshot per (host, name). The poll loop refreshes
// every PollInterval seconds; per-key TTL backstops Get when a caller
// reads between ticks.
//
// v1 always operates against a single hostID — the local machine —
// because Workstream F (federation) hasn't lit up yet. The hostID is
// supplied by the daemon at New() time so identity logic stays in one
// place (the host provider).
type Provider struct {
	adapter  Adapter
	hostID   string
	services []string
	clock    func() time.Time

	mu    sync.RWMutex
	cache map[HostServiceID]Snapshot
	errs  map[HostServiceID]error

	subsMu sync.Mutex
	subs   []chan adapter.InvalidationEvent[HostServiceID]
}

// New constructs a Provider with the platform-specific adapter and
// the resolved watchlist + hostID.
//
// services may be empty — Provider.Start is a no-op and Get returns the
// "no such key" error consistently. Callers loading the watchlist from
// config should fall back to DefaultServices before calling New.
func New(hostID string, services []string) *Provider {
	return NewWith(NewAdapter(), hostID, services, time.Now)
}

// NewWith is the test-friendly constructor — accepts an injected
// Adapter and clock so unit tests can drive freshness deterministically
// and stub the OS shellouts.
func NewWith(a Adapter, hostID string, services []string, clock func() time.Time) *Provider {
	if clock == nil {
		clock = time.Now
	}
	cleaned := make([]string, 0, len(services))
	seen := make(map[string]struct{}, len(services))
	for _, s := range services {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		cleaned = append(cleaned, s)
	}
	return &Provider{
		adapter:  a,
		hostID:   hostID,
		services: cleaned,
		clock:    clock,
		cache:    make(map[HostServiceID]Snapshot, len(cleaned)),
		errs:     make(map[HostServiceID]error, len(cleaned)),
	}
}

// Start hydrates the cache once synchronously, then kicks off the
// poll loop. The loop terminates when ctx is cancelled.
//
// Start is best-effort: per-service errors are stored on the Provider
// and surfaced through Get; only a totally empty watchlist is
// considered a no-op. Returning nil on partial failure mirrors the
// resolver contract — one missing service shouldn't fail the resolver.
func (p *Provider) Start(ctx context.Context) error {
	p.refreshAll(ctx)
	go p.pollLoop(ctx)
	return nil
}

// HostID exposes the hostID this Provider was constructed with so the
// resolver can compose `HostService:<host>:<name>` ids without
// re-deriving identity.
func (p *Provider) HostID() string { return p.hostID }

// Services returns a copy of the watchlist in the order it was
// configured. Resolvers iterate over this to populate
// Host.hostServices.
func (p *Provider) Services() []string {
	out := make([]string, len(p.services))
	copy(out, p.services)
	return out
}

// Keys returns every key the Provider currently tracks. Cold boot
// returns the configured set unconditionally so callers can drive
// GetMany even before Start has hydrated the cache.
func (p *Provider) Keys(_ context.Context) ([]HostServiceID, error) {
	out := make([]HostServiceID, 0, len(p.services))
	for _, s := range p.services {
		out = append(out, MakeID(p.hostID, s))
	}
	return out, nil
}

// Get returns the Snapshot for one (host, name). Synchronously refreshes
// when the cached value is past PollInterval.
//
// Returns an error only when:
//   - the key isn't on the watchlist (caller bug);
//   - the OS service manager is missing (ErrServiceManagerMissing);
//   - a previous fetch failed for an OS-specific reason that wasn't
//     "unit not present" — because the adapter contract reports
//     "unit not present" as state=unknown, not as an error.
//
// "Unit not present" is NEVER an error — the Snapshot has
// State == StateUnknown and the rest of the fields are nil.
func (p *Provider) Get(ctx context.Context, key HostServiceID) (Snapshot, adapter.Freshness, error) {
	hostID, name, ok := p.splitKey(key)
	if !ok {
		return Snapshot{}, adapter.Freshness{}, fmt.Errorf("unknown HostServiceID %q (not on watchlist)", key)
	}

	if err := p.ensureFresh(ctx, key, hostID, name); err != nil {
		// Continue with stale data on transient failures; the next tick will retry.
		_ = err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if errVal, ok := p.errs[key]; ok && errVal != nil {
		// Surface the most recent fetch error — the resolver may want
		// to mark the field as a per-field GraphQL error.
		return Snapshot{}, adapter.Freshness{}, errVal
	}
	snap, ok := p.cache[key]
	if !ok {
		return Snapshot{}, adapter.Freshness{}, fmt.Errorf("no snapshot yet for %q", key)
	}
	source := adapter.SourcePoll
	if p.clock().Sub(snap.FetchedAt) > PollInterval {
		source = adapter.SourceStaleCache
	}
	return snap, adapter.Freshness{LastFetchedAt: snap.FetchedAt, Source: source}, nil
}

// GetMany satisfies the Provider interface. Coalesces duplicate keys
// and shares the cache across calls — DataLoader (WS-C) batches resolver
// fan-outs into this method.
func (p *Provider) GetMany(ctx context.Context, keys []HostServiceID) (map[HostServiceID]Snapshot, map[HostServiceID]adapter.Freshness, error) {
	seen := make(map[HostServiceID]struct{}, len(keys))
	out := make(map[HostServiceID]Snapshot, len(keys))
	fresh := make(map[HostServiceID]adapter.Freshness, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		s, f, err := p.Get(ctx, k)
		if err != nil {
			continue
		}
		out[k] = s
		fresh[k] = f
	}
	return out, fresh, nil
}

// Subscribe returns a channel that receives an event each time a watched
// service refreshes. Channel closes when ctx is cancelled.
//
// Sends are non-blocking — a slow consumer drops events rather than
// stalling the watcher loop.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[HostServiceID] {
	ch := make(chan adapter.InvalidationEvent[HostServiceID], 16)
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
				close(ch)
				return
			}
		}
	}()
	return ch
}

// LastError returns the most recent fetch error for a key, if any. The
// resolver consults this to decide whether to surface a per-field
// GraphQL error vs returning the cached value.
func (p *Provider) LastError(key HostServiceID) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.errs[key]
}

func (p *Provider) splitKey(key HostServiceID) (hostID, name string, ok bool) {
	want := string(key)
	for _, s := range p.services {
		candidate := MakeID(p.hostID, s)
		if HostServiceID(candidate) == HostServiceID(want) {
			return p.hostID, s, true
		}
	}
	return "", "", false
}

func (p *Provider) refreshAll(ctx context.Context) {
	for _, name := range p.services {
		key := MakeID(p.hostID, name)
		_ = p.refreshOne(ctx, key, p.hostID, name)
	}
}

func (p *Provider) ensureFresh(ctx context.Context, key HostServiceID, hostID, name string) error {
	p.mu.RLock()
	snap, hit := p.cache[key]
	stale := !hit || p.clock().Sub(snap.FetchedAt) > PollInterval
	p.mu.RUnlock()
	if !stale {
		return nil
	}
	return p.refreshOne(ctx, key, hostID, name)
}

func (p *Provider) refreshOne(ctx context.Context, key HostServiceID, hostID, name string) error {
	snap, err := p.adapter.FetchOne(ctx, hostID, name)
	now := p.clock()

	p.mu.Lock()
	if err != nil {
		p.errs[key] = err
		p.mu.Unlock()
		return err
	}
	snap.FetchedAt = now
	p.cache[key] = snap
	delete(p.errs, key)
	p.mu.Unlock()

	p.fanOutInvalidate(key, now)
	return nil
}

func (p *Provider) fanOutInvalidate(key HostServiceID, at time.Time) {
	ev := adapter.InvalidationEvent[HostServiceID]{
		Key:    key,
		Reason: "poll-refresh",
		At:     at,
	}
	p.subsMu.Lock()
	subs := append([]chan adapter.InvalidationEvent[HostServiceID](nil), p.subs...)
	p.subsMu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default:
		}
	}
}

func (p *Provider) pollLoop(ctx context.Context) {
	if len(p.services) == 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshAll(ctx)
		}
	}
}

// Compile-time assertion that *Provider satisfies the generic Provider
// interface. Catches signature drift the moment it happens.
var _ adapter.Provider[HostServiceID, Snapshot] = (*Provider)(nil)
