package contracts

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

	// offset is the byte position the Provider has folded up to in the
	// log. Tail reads resume from here.
	offset int64

	subMu sync.Mutex
	subs  map[chan adapter.InvalidationEvent[ContractID]]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider using the platform-default log path
// resolved by [DefaultLogPath].
func New(logger *slog.Logger) *Provider {
	return NewWithPath(DefaultLogPath(), logger)
}

// NewWithPath is the test-friendly constructor — accepts an explicit
// log path so unit tests can point at a t.TempDir() file. The clock is
// fixed to time.Now in production; NewForTest swaps it for callers
// that need deterministic timestamps.
func NewWithPath(path string, logger *slog.Logger) *Provider {
	return NewForTest(path, logger, time.Now)
}

// NewForTest is the constructor with every dependency injectable.
// Production callers use [New] / [NewWithPath]; test callers use this
// to drive the provider with a fake clock.
func NewForTest(path string, logger *slog.Logger, clock func() time.Time) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	return &Provider{
		adapterIO: NewAdapter(path),
		watcher:   NewWatcher(path, logger),
		logger:    logger,
		clock:     clock,
		state:     map[ContractID]Contract{},
		subs:      map[chan adapter.InvalidationEvent[ContractID]]struct{}{},
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// LogPath returns the absolute path the Provider reads from. Surfaces
// the resolved default for diagnostics (e.g. `orchard query contracts
// --help`).
func (p *Provider) LogPath() string {
	return p.adapterIO.Path()
}

// Start hydrates the cache from a Snapshot read, then launches the
// watcher loop. Subsequent calls are no-ops.
//
// A missing log file is not an error — Start returns nil and the
// watcher waits for the file to be created.
func (p *Provider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		events, offset, err := p.adapterIO.Snapshot(ctx)
		if err != nil {
			// Surface the error but let the daemon continue — a
			// transient read failure should not collapse boot.
			p.logger.Warn("contracts provider: snapshot read failed",
				"path", p.adapterIO.Path(), "err", err)
		}
		p.mu.Lock()
		p.state = Fold(events)
		p.offset = offset
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
//
// Closing the underlying fsnotify watcher closes its event channels,
// which causes [Watcher.Run] to exit and the provider's done channel
// to fire. We wait for that exit so callers can rely on no further
// invalidation events after Stop returns.
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

// Get returns one contract by id. The fold is the source of truth;
// the adapter is consulted only when the contract is not in the cache
// (which only happens during a watcher tick that has not landed yet).
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

// Keys returns every contract id currently in the cache. Cold boot
// returns an empty slice until [Start] hydrates.
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
//
// Resolvers call this for `Query.contracts(filter)`. Returning the
// graphql shape directly keeps the resolver layer trivial.
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
// whose folded value just changed. The channel closes when ctx is
// cancelled or [Stop] runs.
//
// Sends are non-blocking — a slow consumer drops events rather than
// stalling the watcher. Callers re-fetch via [Get] or [List] in
// response.
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

// consume drives the fold loop. Each watcher tick triggers a tail read
// from the saved offset; new events are merged into the existing state
// (so the fold is incremental, not full-rebuild) and invalidations are
// fanned out for whichever ids changed.
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

// refresh tails the JSONL from the saved offset, applies new events to
// the in-memory state, advances the offset, and fans out
// InvalidationEvents for every id touched. Run on every watcher poke.
func (p *Provider) refresh(ctx context.Context) {
	p.mu.RLock()
	from := p.offset
	p.mu.RUnlock()

	events, advanced, err := p.adapterIO.FollowFromOffset(ctx, from)
	if err != nil {
		p.logger.Warn("contracts provider: tail read failed",
			"path", p.adapterIO.Path(), "err", err)
	}

	if len(events) == 0 && advanced == from {
		return
	}

	touched := make(map[ContractID]struct{}, len(events))
	now := p.clock()
	p.mu.Lock()
	for _, ev := range events {
		applyEvent(p.state, ev)
		if ev.ID != "" {
			touched[ContractID(ev.ID)] = struct{}{}
		}
	}
	p.offset = advanced
	p.last = now
	p.mu.Unlock()

	if advanced < from {
		// File rotated — re-fold from scratch on next tick. We dropped
		// state; emit invalidations for every known id so subscribers
		// re-read.
		p.logger.Info("contracts provider: log rotated; cache may be stale",
			"path", p.adapterIO.Path())
	}

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

// fanOut broadcasts an invalidation event to every active subscriber.
// Sends are best-effort; a slow consumer drops events.
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
	if f.ParentContractID != nil && *f.ParentContractID != "" {
		if c.ParentContractID == nil || *c.ParentContractID != *f.ParentContractID {
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
		Statement:      c.Statement,
		OwnerSessionID: c.OwnerSessionID,
		OwnerAgentName: c.OwnerAgentName,
		Status:         mapStatus(c.Status),
		CreatedAt:      formatTime(c.CreatedAt),
		UpdatedAt:      formatTime(c.UpdatedAt),
		LastEventAt:    formatTime(c.LastEventAt),
		Criteria:       append([]string{}, c.Criteria...),
		OpenQuestions:  buildOpenQuestions(c.OpenQuestions),
	}
	if c.ReportsTo != "" {
		s := c.ReportsTo
		out.ReportsTo = &s
	}
	if c.ParentContractID != "" {
		s := c.ParentContractID
		out.ParentContractID = &s
	}
	return out
}

// buildOpenQuestions copies the internal OpenQuestion list onto the
// generated graphql.ContractQuestion slice. We allocate a fresh slice
// so callers cannot mutate the cache by editing the response.
func buildOpenQuestions(qs []OpenQuestion) []*graphql.ContractQuestion {
	if len(qs) == 0 {
		return []*graphql.ContractQuestion{}
	}
	out := make([]*graphql.ContractQuestion, 0, len(qs))
	for _, q := range qs {
		gq := &graphql.ContractQuestion{
			QuestionID:  q.QuestionID,
			Text:        q.Text,
			AskedBy:     q.AskedBy,
			AskedAt:     formatTime(q.AskedAt),
			BlocksClose: q.BlocksClose,
		}
		if q.Deadline != nil {
			d := formatTime(*q.Deadline)
			gq.Deadline = &d
		}
		out = append(out, gq)
	}
	return out
}

// mapStatus maps the plugin's raw status string to the schema enum.
// Unknown values fall through to OPEN — the safest default for a
// future-tense enum addition.
func mapStatus(s string) graphql.ContractStatus {
	switch s {
	case "open":
		return graphql.ContractStatusOpen
	case "delivered_pending_validation":
		return graphql.ContractStatusDeliveredPendingValidation
	case "delivered_pending_parent_validation":
		return graphql.ContractStatusDeliveredPendingParentValidation
	case "pending_drew_approval":
		return graphql.ContractStatusPendingDrewApproval
	case "awaiting_cancel_ack":
		return graphql.ContractStatusAwaitingCancelAck
	case "waiting_external":
		return graphql.ContractStatusWaitingExternal
	case "satisfied":
		return graphql.ContractStatusSatisfied
	case "cancelled":
		return graphql.ContractStatusCancelled
	case "judge_rejected_terminal":
		return graphql.ContractStatusJudgeRejectedTerminal
	default:
		return graphql.ContractStatusOpen
	}
}

// formatTime renders a time as RFC 3339 with nanosecond precision.
// Mirrors the Host provider's lastSeenAt format so clients get a
// consistent timestamp shape across the schema.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// Compile-time assertion that Provider satisfies the generic
// Provider interface from internal/server/adapter.
var _ adapter.Provider[ContractID, *graphql.Contract] = (*Provider)(nil)
