package contracts

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// Provider exposes Contract nodes to the GraphQL resolver layer.
//
// Owns:
//
//   - one [Adapter] (JSONL read).
//   - one [Watcher] (fsnotify).
//   - the in-memory fold result, keyed by ContractID.
//   - the per-key Subscribe fan-out for invalidation events.
//
// Per ADR-011 §2 the surface is read-only. Writes happen out-of-band
// (the claude-contracts plugin appends to the JSONL); the watcher
// turns those writes into invalidation events, the next read picks up
// the fresh fold.
type Provider struct {
	adapterIO *Adapter
	watcher   *Watcher
	logger    *slog.Logger
	clock     func() time.Time

	mu     sync.RWMutex
	state  map[ContractID]Contract
	loaded bool
	last   time.Time

	// offsets is the per-file byte position the Provider has folded up
	// to. Keyed by file basename (e.g. `C-2026-04-27-0398e48e.jsonl`).
	// Tail reads resume from these offsets.
	offsets map[string]int64

	subMu sync.Mutex
	subs  map[chan adapter.InvalidationEvent[ContractID]]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider using the platform-default log directory
// resolved by [DefaultLogDir].
func New(logger *slog.Logger) *Provider {
	return NewWithPath(DefaultLogDir(), logger)
}

// NewWithPath is the test-friendly constructor — accepts an explicit
// log directory so unit tests can point at a t.TempDir(). The clock is
// fixed to time.Now in production; NewForTest swaps it for callers
// that need deterministic timestamps.
func NewWithPath(dir string, logger *slog.Logger) *Provider {
	return NewForTest(dir, logger, time.Now)
}

// NewForTest is the constructor with every dependency injectable.
// Production callers use [New] / [NewWithPath]; test callers use this
// to drive the provider with a fake clock.
func NewForTest(dir string, logger *slog.Logger, clock func() time.Time) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	return &Provider{
		adapterIO: NewAdapter(dir),
		watcher:   NewWatcher(dir, logger),
		logger:    logger,
		clock:     clock,
		state:     map[ContractID]Contract{},
		offsets:   map[string]int64{},
		subs:      map[chan adapter.InvalidationEvent[ContractID]]struct{}{},
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// LogPath returns the absolute path the Provider reads from.
func (p *Provider) LogPath() string {
	return p.adapterIO.Dir()
}

// Start hydrates the cache from a Snapshot read, then launches the
// watcher loop. Subsequent calls are no-ops.
func (p *Provider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		events, offsets, err := p.adapterIO.Snapshot(ctx)
		if err != nil {
			p.logger.Warn("contracts provider: snapshot read failed",
				"dir", p.adapterIO.Dir(), "err", err)
		}
		p.mu.Lock()
		p.state = Fold(events)
		p.offsets = offsets
		p.loaded = true
		p.last = p.clock()
		p.mu.Unlock()

		go func() {
			defer close(p.doneCh)
			if runErr := p.watcher.Run(ctx); runErr != nil {
				p.logger.Warn("contracts watcher exited", "err", runErr)
			}
		}()
		go p.consume(ctx)
		startErr = nil
	})
	return startErr
}

// Stop tears down the watcher and closes every Subscribe channel.
// Idempotent. Safe to call before Start (no-op).
func (p *Provider) Stop() error {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		p.watcher.Stop()
		<-p.watcher.Done()
		p.subMu.Lock()
		for ch := range p.subs {
			close(ch)
			delete(p.subs, ch)
		}
		p.subMu.Unlock()
	})
	return nil
}

// Get returns one contract by id.
func (p *Provider) Get(_ context.Context, key ContractID) (*graphql.Contract, adapter.Freshness, error) {
	p.mu.RLock()
	c, ok := p.state[key]
	loaded := p.loaded
	last := p.last
	p.mu.RUnlock()
	if !ok {
		if !loaded {
			return nil, adapter.Freshness{}, fmt.Errorf("contracts provider not started")
		}
		return nil, adapter.Freshness{}, fmt.Errorf("unknown contract %q", key)
	}
	return toGraphQL(c), adapter.Freshness{LastFetchedAt: last, Source: adapter.SourceWatcherPush}, nil
}

// GetMany returns multiple contracts in a single call. Missing keys
// are simply absent from the result maps per the Provider contract.
func (p *Provider) GetMany(_ context.Context, keys []ContractID) (map[ContractID]*graphql.Contract, map[ContractID]adapter.Freshness, error) {
	out := make(map[ContractID]*graphql.Contract, len(keys))
	freshness := make(map[ContractID]adapter.Freshness, len(keys))
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.loaded {
		return out, freshness, fmt.Errorf("contracts provider not started")
	}
	for _, k := range keys {
		c, ok := p.state[k]
		if !ok {
			continue
		}
		out[k] = toGraphQL(c)
		freshness[k] = adapter.Freshness{LastFetchedAt: p.last, Source: adapter.SourceWatcherPush}
	}
	return out, freshness, nil
}

// Keys returns every contract id currently in the cache.
func (p *Provider) Keys(_ context.Context) ([]ContractID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ContractID, 0, len(p.state))
	for k := range p.state {
		out = append(out, k)
	}
	return out, nil
}

// List returns every cached Contract, sorted descending by
// LastEventAt — most recently active contracts first. Optionally
// filtered by [ContractFilter].
func (p *Provider) List(_ context.Context, filter *graphql.ContractFilter) ([]*graphql.Contract, error) {
	p.mu.RLock()
	contracts := make([]Contract, 0, len(p.state))
	for _, c := range p.state {
		contracts = append(contracts, c)
	}
	loaded := p.loaded
	p.mu.RUnlock()
	if !loaded {
		return nil, fmt.Errorf("contracts provider not started")
	}

	sort.Slice(contracts, func(i, j int) bool {
		// Descending — most-recent first.
		ti := contracts[i].LastEventAt
		tj := contracts[j].LastEventAt
		if ti.Equal(tj) {
			return contracts[i].ID < contracts[j].ID
		}
		return ti.After(tj)
	})

	out := make([]*graphql.Contract, 0, len(contracts))
	for _, c := range contracts {
		gc := toGraphQL(c)
		if filter != nil && !matches(gc, filter) {
			continue
		}
		out = append(out, gc)
	}
	return out, nil
}

// Subscribe returns a channel that receives one event per contract
// whose folded value just changed.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[ContractID] {
	ch := make(chan adapter.InvalidationEvent[ContractID], 16)
	p.subMu.Lock()
	p.subs[ch] = struct{}{}
	p.subMu.Unlock()

	go func() {
		<-ctx.Done()
		p.subMu.Lock()
		defer p.subMu.Unlock()
		if _, ok := p.subs[ch]; ok {
			delete(p.subs, ch)
			close(ch)
		}
	}()
	return ch
}

// consume drives the fold loop. Each watcher tick triggers a tail read.
func (p *Provider) consume(ctx context.Context) {
	notifications := p.watcher.Notifications()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case _, ok := <-notifications:
			if !ok {
				return
			}
			p.refresh(ctx)
		}
	}
}

// refresh tails every jsonl in the dir from the saved per-file
// offsets, applies new events to the in-memory state, and fans out
// InvalidationEvents for every id touched.
func (p *Provider) refresh(ctx context.Context) {
	p.mu.RLock()
	from := make(map[string]int64, len(p.offsets))
	for k, v := range p.offsets {
		from[k] = v
	}
	p.mu.RUnlock()

	events, advanced, err := p.adapterIO.FollowFromOffsets(ctx, from)
	if err != nil {
		p.logger.Warn("contracts provider: tail read failed",
			"dir", p.adapterIO.Dir(), "err", err)
	}

	if len(events) == 0 && offsetsEqual(advanced, from) {
		return
	}

	touched := make(map[ContractID]struct{}, len(events))
	now := p.clock()
	p.mu.Lock()
	for _, ev := range events {
		applyEvent(p.state, ev)
		if ev.ContractID != "" {
			touched[ContractID(ev.ContractID)] = struct{}{}
		}
	}
	p.offsets = advanced
	p.last = now
	p.mu.Unlock()

	if len(touched) == 0 {
		return
	}
	for id := range touched {
		p.fanOut(adapter.InvalidationEvent[ContractID]{
			Key:    id,
			Reason: "watcher-push",
			At:     now,
		})
	}
}

// offsetsEqual compares two per-file offset maps for exact equality.
func offsetsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// fanOut broadcasts an invalidation event to every active subscriber.
func (p *Provider) fanOut(ev adapter.InvalidationEvent[ContractID]) {
	p.subMu.Lock()
	subs := make([]chan adapter.InvalidationEvent[ContractID], 0, len(p.subs))
	for ch := range p.subs {
		subs = append(subs, ch)
	}
	p.subMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// matches applies a ContractFilter to a single Contract. All filter
// fields are AND-combined; nil/empty fields match everything.
func matches(c *graphql.Contract, f *graphql.ContractFilter) bool {
	if f == nil {
		return true
	}
	if len(f.Statuses) > 0 {
		ok := false
		for _, s := range f.Statuses {
			if s == c.Status {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.OwnerSessionID != nil && *f.OwnerSessionID != "" && c.OwnerSessionID != *f.OwnerSessionID {
		return false
	}
	if f.OwnerAgentName != nil && *f.OwnerAgentName != "" && c.OwnerAgentName != *f.OwnerAgentName {
		return false
	}
	if f.OwnerContains != nil && *f.OwnerContains != "" {
		if !strings.Contains(c.OwnerSessionID, *f.OwnerContains) {
			return false
		}
	}
	return true
}

// toGraphQL projects an internal Contract onto the GraphQL shape. Pure;
// the resolver layer calls it after every read.
func toGraphQL(c Contract) *graphql.Contract {
	out := &graphql.Contract{
		ID:             "Contract:" + string(c.ID),
		ContractID:     string(c.ID),
		Summary:        c.Summary,
		OwnerSessionID: c.OwnerSessionID,
		OwnerAgentName: c.OwnerAgentName,
		Status:         mapStatus(c.Status),
		Reasoning:      c.Reasoning,
		CreatedBy:      c.CreatedBy,
		CreatedAt:      formatTime(c.CreatedAt),
		UpdatedAt:      formatTime(c.UpdatedAt),
		LastEventAt:    formatTime(c.LastEventAt),
	}
	if c.Source != "" {
		s := c.Source
		out.Source = &s
	}
	return out
}

// mapStatus maps the plugin's raw status string to the 2-value schema
// enum. "delivered" → DELIVERED; everything else (including legacy
// "blocked" and "started") → OPEN.
func mapStatus(s string) graphql.ContractStatus {
	switch s {
	case "delivered":
		return graphql.ContractStatusDelivered
	default:
		return graphql.ContractStatusOpen
	}
}

// formatTime renders a time as RFC 3339 with nanosecond precision.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// Compile-time assertion that Provider satisfies the generic
// Provider interface from internal/server/adapter.
var _ adapter.Provider[ContractID, *graphql.Contract] = (*Provider)(nil)
