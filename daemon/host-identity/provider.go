package hostidentity

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
)

// LoadTTL is the maximum age of a cached load sample. The poll loop refreshes
// on this cadence proactively; callers normally read from fresh cache.
const LoadTTL = 5 * time.Second

// Provider is the in-process cache for the host-identity domain.
//
// Owns one IdentityReader (read once at boot, cached forever) and one
// LoadReader (re-read every LoadTTL by Start's poll loop). The single key
// is the local machine's HostID.
//
// Per R10: the poll goroutine is owned by Provider; Start launches it and
// ctx cancellation stops it.
// Per R13: sync.RWMutex chosen because reads (per-request resolver calls)
// vastly outnumber writes (one write per 5s poll tick).
type Provider struct {
	identityReader IdentityReader
	loadReader     LoadReader
	clock          func() time.Time
	log            *slog.Logger

	mu          sync.RWMutex
	identity    Identity
	identityErr error
	loaded      bool
	load        Load
	loadAt      time.Time
	loadErr     error

	subsMu sync.Mutex
	subs   []chan adapter.InvalidationEvent[HostID]
}

// NewProvider constructs a Provider with the platform-specific readers.
// Callers (the daemon entry point) call this once at startup.
func NewProvider() *Provider {
	return NewProviderWith(NewIdentityReader(), NewLoadReader(), time.Now, slog.Default())
}

// NewProviderWith is the test-friendly constructor — accepts injected readers
// and a clock so unit tests can drive freshness deterministically.
func NewProviderWith(id IdentityReader, load LoadReader, clock func() time.Time, log *slog.Logger) *Provider {
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Provider{
		identityReader: id,
		loadReader:     load,
		clock:          clock,
		log:            log,
	}
}

// Start hydrates identity (one-shot) and kicks off the load poll loop.
// The loop terminates when ctx is cancelled. Returns an error if the
// one-shot identity read fails so callers can surface it at boot.
func (p *Provider) Start(ctx context.Context) error {
	if err := p.refreshIdentity(ctx); err != nil {
		return err
	}
	// Take an initial sample synchronously so the first Get returns fresh data.
	_ = p.refreshLoad(ctx)
	go p.pollLoop(ctx)
	return nil
}

// LocalID returns the cache key for the local machine. Useful to resolvers
// that need to call Get(ctx, p.LocalID()).
func (p *Provider) LocalID() HostID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return HostID(p.identity.MachineID)
}

// GetIdentity returns the cached identity. Returns an error if Start has not
// been called.
func (p *Provider) GetIdentity() (Identity, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.identity.MachineID == "" {
		return Identity{}, fmt.Errorf("host provider not started")
	}
	return p.identity, nil
}

// GetLoad returns the cached load sample and whether a sample is available.
func (p *Provider) GetLoad() (Load, bool, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.load, p.loaded, p.loadAt
}

// Get returns the Host snapshot for the requested machine. v1 only knows the
// local machine; any other key returns an error.
func (p *Provider) Get(ctx context.Context, key HostID) (*HostSnapshot, adapter.Freshness, error) {
	if err := p.ensureFreshLoad(ctx); err != nil {
		// Continue with stale data on transient load failures — identity is
		// still useful and the next tick will retry.
		_ = err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.identity.MachineID == "" {
		return nil, adapter.Freshness{}, fmt.Errorf("host provider not started")
	}
	if string(key) != p.identity.MachineID {
		return nil, adapter.Freshness{}, fmt.Errorf("unknown host key %q (v1 only knows %q)", key, p.identity.MachineID)
	}

	snap := buildSnapshot(p.identity, p.load, p.loaded, p.loadAt, p.clock())
	source := adapter.SourcePoll
	if !p.loaded {
		source = adapter.SourceStaleCache
	}
	return snap, adapter.Freshness{LastFetchedAt: p.loadAt, Source: source}, nil
}

// GetMany satisfies batched lookups. v1 collapses to single-key lookups.
func (p *Provider) GetMany(ctx context.Context, keys []HostID) (map[HostID]*HostSnapshot, map[HostID]adapter.Freshness, error) {
	snaps := make(map[HostID]*HostSnapshot, len(keys))
	fresh := make(map[HostID]adapter.Freshness, len(keys))
	for _, k := range keys {
		s, f, err := p.Get(ctx, k)
		if err != nil {
			continue
		}
		snaps[k] = s
		fresh[k] = f
	}
	return snaps, fresh, nil
}

// Keys returns the local machine's key once Start has populated identity.
func (p *Provider) Keys(_ context.Context) ([]HostID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.identity.MachineID == "" {
		return nil, nil
	}
	return []HostID{HostID(p.identity.MachineID)}, nil
}

// Subscribe returns a channel that receives an event each time the load sample
// refreshes. The channel closes when ctx is cancelled.
//
// Per R12: returns receive-only channel.
// Per R16: events emit after cache write.
// Sends are non-blocking — a slow consumer drops events rather than
// stalling the poll loop.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[HostID] {
	ch := make(chan adapter.InvalidationEvent[HostID], 8)
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

func (p *Provider) refreshIdentity(ctx context.Context) error {
	id, err := p.identityReader.Read(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.identityErr = err
		return fmt.Errorf("read identity: %w", err)
	}
	p.identity = id
	p.identityErr = nil
	return nil
}

func (p *Provider) refreshLoad(ctx context.Context) error {
	load, err := p.loadReader.Read(ctx)
	now := p.clock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.loadErr = err
		return err
	}
	p.load = load
	p.loadAt = now
	p.loaded = true
	p.loadErr = nil
	// Emit AFTER cache write — per R16.
	p.fanOutInvalidate(now)
	return nil
}

// ensureFreshLoad triggers a synchronous refresh if the cached sample is older
// than LoadTTL. Failures are non-fatal — Get keeps the stale value.
func (p *Provider) ensureFreshLoad(ctx context.Context) error {
	p.mu.RLock()
	stale := !p.loaded || p.clock().Sub(p.loadAt) > LoadTTL
	p.mu.RUnlock()
	if !stale {
		return nil
	}
	return p.refreshLoad(ctx)
}

// fanOutInvalidate broadcasts a "load refreshed" event to all subscribers.
// Caller MUST hold p.mu (write) to ensure cache is written before this fires.
// We drop the lock for the send so a slow subscriber cannot deadlock the writer.
// Sends are best-effort (drop on full buffer) per non-blocking contract.
func (p *Provider) fanOutInvalidate(at time.Time) {
	if p.identity.MachineID == "" {
		return
	}
	ev := adapter.InvalidationEvent[HostID]{
		Key:    HostID(p.identity.MachineID),
		Reason: "load-refresh",
		At:     at,
	}
	p.subsMu.Lock()
	subs := append([]chan adapter.InvalidationEvent[HostID](nil), p.subs...)
	p.subsMu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default:
		}
	}
}

// pollLoop runs on its own goroutine (per R10) and refreshes load every
// LoadTTL. Per R17: panic-recover + structured logging.
func (p *Provider) pollLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("host-identity poll loop panicked", "panic", r)
		}
	}()

	t := time.NewTicker(LoadTTL)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.refreshLoad(ctx); err != nil {
				p.log.Warn("host-identity load refresh failed", "err", err)
			}
		}
	}
}

// HostSnapshot is the projection the service and resolvers work with.
// It carries identity + load in a single value so resolvers never read
// partial state.
type HostSnapshot struct {
	Identity   Identity
	Load       Load
	LoadKnown  bool
	LoadAt     time.Time
	SampledAt  time.Time // clock() at projection time
}

// buildSnapshot projects Identity + Load into a HostSnapshot. Pure function.
func buildSnapshot(id Identity, load Load, loadKnown bool, loadAt, now time.Time) *HostSnapshot {
	return &HostSnapshot{
		Identity:  id,
		Load:      load,
		LoadKnown: loadKnown,
		LoadAt:    loadAt,
		SampledAt: now,
	}
}
