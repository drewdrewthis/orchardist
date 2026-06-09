package host

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
	"github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// HostID is the cache key for the Host provider. It wraps the OS-issued
// machine id; v1 only ever holds one value (the local machine).
type HostID string

// LoadTTL is the maximum age of a cached load sample before the next
// Get call refreshes synchronously. The poll loop refreshes on the same
// cadence proactively, so callers normally read from fresh cache.
const LoadTTL = 5 * time.Second

// Provider is the host-domain Provider implementation.
//
// Owns one IdentityReader (read once at boot, cached forever) and one
// LoadReader (re-read every LoadTTL by Start's poll loop). Exposes a
// single key — the local machine's HostID — through the standard
// Provider[K, V] surface so resolvers depend on the interface, not on
// this concrete type.
type Provider struct {
	identityReader IdentityReader
	loadReader     LoadReader
	clock          func() time.Time

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

// New constructs a Provider with the platform-specific readers.
// Callers (the daemon entry point) call this once at startup and pass
// the returned Provider into the resolver root.
func New() *Provider {
	return NewWith(NewIdentityReader(), NewLoadReader(), time.Now)
}

// NewWith is the test-friendly constructor — accepts injected readers
// and a clock so unit tests can drive freshness deterministically. The
// production path (New) uses the OS-native readers + time.Now.
func NewWith(id IdentityReader, load LoadReader, clock func() time.Time) *Provider {
	if clock == nil {
		clock = time.Now
	}
	return &Provider{
		identityReader: id,
		loadReader:     load,
		clock:          clock,
	}
}

// Start hydrates identity (one-shot) and kicks off the load poll loop.
// The loop terminates when ctx is cancelled. Returns the identity error
// if the one-shot read fails so callers can surface it at boot.
//
// Start is idempotent within one process: a second call after ctx is
// cancelled spawns a new loop. v1 only ever calls it once.
func (p *Provider) Start(ctx context.Context) error {
	if err := p.refreshIdentity(ctx); err != nil {
		return err
	}
	// Take an initial sample synchronously so the first Get() returns
	// fresh data instead of an empty snapshot.
	_ = p.refreshLoad(ctx)
	go p.pollLoop(ctx)
	return nil
}

// LocalID returns the cache key for the local machine — useful to
// resolvers that need to call Get(ctx, p.LocalID()).
func (p *Provider) LocalID() HostID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return HostID(p.identity.MachineID)
}

// Get returns the GraphQL Host snapshot for the requested machine. v1
// only knows the local machine; any other key returns ("", _, error).
func (p *Provider) Get(ctx context.Context, key HostID) (*graphql.Host, adapter.Freshness, error) {
	if err := p.ensureFreshLoad(ctx); err != nil {
		// Continue with stale data on transient load failures — identity
		// is still useful and the next tick will retry.
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

	host := buildHost(p.identity, p.load, p.loaded, p.loadAt, p.clock())
	source := adapter.SourcePoll
	if !p.loaded {
		source = adapter.SourceStaleCache
	}
	return host, adapter.Freshness{LastFetchedAt: p.loadAt, Source: source}, nil
}

// GetMany satisfies the Provider interface. v1 collapses to single-key
// lookups; the loop is here so the resolver layer can DataLoader-batch
// without a special case.
func (p *Provider) GetMany(ctx context.Context, keys []HostID) (map[HostID]*graphql.Host, map[HostID]adapter.Freshness, error) {
	hosts := make(map[HostID]*graphql.Host, len(keys))
	fresh := make(map[HostID]adapter.Freshness, len(keys))
	for _, k := range keys {
		h, f, err := p.Get(ctx, k)
		if err != nil {
			continue
		}
		hosts[k] = h
		fresh[k] = f
	}
	return hosts, fresh, nil
}

// Keys returns the single local key once Start has populated identity.
// Cold boot returns an empty slice per the Provider contract.
func (p *Provider) Keys(_ context.Context) ([]HostID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.identity.MachineID == "" {
		return nil, nil
	}
	return []HostID{HostID(p.identity.MachineID)}, nil
}

// Subscribe returns a channel that receives an event each time the
// load sample refreshes. The channel closes when ctx is cancelled.
//
// Sends are non-blocking — a slow consumer drops events rather than
// stalling the watcher loop. Callers that need every tick should buffer
// generously and drain promptly.
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
	p.fanOutInvalidate(now)
	return nil
}

// ensureFreshLoad triggers a synchronous refresh if the cached sample
// is older than LoadTTL. Failures are non-fatal — Get keeps the stale
// value rather than blanking it.
func (p *Provider) ensureFreshLoad(ctx context.Context) error {
	p.mu.RLock()
	stale := !p.loaded || p.clock().Sub(p.loadAt) > LoadTTL
	p.mu.RUnlock()
	if !stale {
		return nil
	}
	return p.refreshLoad(ctx)
}

// fanOutInvalidate broadcasts a "load refreshed" event to all
// subscribers. Caller holds p.mu (write); we drop the lock for the send
// so a slow subscriber cannot deadlock the writer. Sends are best-effort.
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

func (p *Provider) pollLoop(ctx context.Context) {
	t := time.NewTicker(LoadTTL)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = p.refreshLoad(ctx)
		}
	}
}

// buildHost projects internal Identity + Load into the gqlgen Host
// shape. Pure function; all the mutable state stays in Provider.
//
// loadKnown indicates whether the load sample has been populated at
// least once; when false, ResourceLoad is nil and lastSeenAt falls
// back to now() so callers see "I'm here, just no metrics yet".
func buildHost(id Identity, load Load, loadKnown bool, loadAt, now time.Time) *graphql.Host {
	host := &graphql.Host{
		ID:         "Host:" + id.MachineID,
		MachineID:  id.MachineID,
		Hostname:   id.Hostname,
		Os:         id.OS,
		Reachable:  true,
		Peers:      []*graphql.Host{},
		LastSeenAt: now.UTC().Format(time.RFC3339Nano),
	}
	if id.Kernel != "" {
		k := id.Kernel
		host.Kernel = &k
	}
	if loadKnown {
		host.ResourceLoad = &graphql.ResourceLoad{
			CPUPercent:  load.CPUPercent,
			MemPercent:  load.MemPercent,
			DiskPercent: load.DiskPercent,
			LoadAvg1m:   load.LoadAvg1m,
			LoadAvg5m:   load.LoadAvg5m,
			LoadAvg15m:  load.LoadAvg15m,
		}
		host.LastSeenAt = loadAt.UTC().Format(time.RFC3339Nano)
	}
	return host
}

// Compile-time assertion that *Provider satisfies the generic Provider
// interface. Catches signature drift the moment it happens.
var _ adapter.Provider[HostID, *graphql.Host] = (*Provider)(nil)
