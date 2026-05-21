package contracts

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ContractsService is the only API consumers (resolvers, loaders, views) may
// call. Resolvers import this interface — never the concrete Provider (R2, R4).
//
// Read-only: contracts are authored exclusively by the claude-contracts plugin.
// Orchard reads them via this service, never writes.
type ContractsService interface {
	// Get returns one contract by id. Returns nil when not found.
	Get(ctx context.Context, id ContractID) (*Contract, error)

	// GetMany returns multiple contracts in a single call. Missing keys are
	// absent from the result map. This is the batch endpoint DataLoaders use.
	GetMany(ctx context.Context, ids []ContractID) (map[ContractID]*Contract, error)

	// List returns every cached Contract, sorted descending by LastEventAt.
	// Optionally filtered by ContractFilter (nil matches everything).
	List(ctx context.Context, filter *ContractFilter) ([]*Contract, error)

	// Subscribe returns a channel that receives one event per contract whose
	// folded value just changed. The channel closes when ctx is cancelled or
	// [Provider.Stop] runs.
	Subscribe(ctx context.Context) <-chan InvalidationEvent
}

// ClaudeJSONLSReader is the interface this domain defines for consuming the
// claude-jsonls service (R4 — consumer defines the interface in its own
// module). The concrete claude-jsonls service implements this; the contracts
// Provider is wired to it at startup.
//
// Currently unused in the Provider implementation because the contracts domain
// reads JSONL files directly via its own Adapter (the claude-jsonls service
// did not expose a record-level streaming API at the time of this refactor).
// This interface is here to satisfy R4 and signal the intended consumption
// pattern for future integration.
type ClaudeJSONLSReader interface {
	// ContractRecords returns the raw contract event records from the JSONL
	// stream. Returns an empty slice when none are available.
	ContractRecords(ctx context.Context) ([]Event, error)
}

// Provider exposes Contract nodes to the GraphQL resolver layer. It is the
// concrete implementation of [ContractsService].
//
// Owns:
//   - one [Adapter] (JSONL read).
//   - one [Watcher] (fsnotify).
//   - the in-memory fold result, keyed by ContractID.
//   - the per-key Subscribe fan-out for invalidation events.
//
// State is a projection of JSONL event records — nothing is persisted (L9).
type Provider struct {
	adapterIO *Adapter
	watcher   *Watcher
	logger    *slog.Logger
	clock     func() time.Time

	mu     sync.RWMutex // R13: RWMutex for read-heavy cache
	state  map[ContractID]Contract
	loaded bool
	last   time.Time

	// offsets is the per-file byte position the Provider has folded up to.
	// Keyed by file basename (e.g. `C-2026-04-27-0398e48e.jsonl`).
	offsets map[string]int64

	subMu sync.Mutex // R13: Mutex for balanced subscriber map
	subs  map[chan InvalidationEvent]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider using the platform-default log directory.
func New(logger *slog.Logger) *Provider {
	return NewWithPath(DefaultLogDir(), logger)
}

// NewWithPath is the test-friendly constructor — accepts an explicit log
// directory so unit tests can point at a t.TempDir().
func NewWithPath(dir string, logger *slog.Logger) *Provider {
	return NewForTest(dir, logger, time.Now)
}

// NewForTest is the constructor with every dependency injectable. Production
// callers use [New] / [NewWithPath]; test callers use this to drive the
// provider with a fake clock.
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
		subs:      map[chan InvalidationEvent]struct{}{},
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// LogPath returns the absolute path the Provider reads from.
func (p *Provider) LogPath() string {
	return p.adapterIO.Dir()
}

// Start hydrates the cache from a Snapshot read, then launches the watcher
// loop. Subsequent calls are no-ops.
//
// A missing log directory is not an error — Start returns nil and the watcher
// waits for the directory to be created.
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
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error("contracts watcher goroutine panicked",
						"recover", fmt.Sprintf("%v", r))
				}
				close(p.doneCh)
			}()
			if runErr := p.watcher.Run(ctx); runErr != nil {
				p.logger.Warn("contracts watcher exited", "err", runErr)
			}
		}()
		go p.consume(ctx) // R17: consume has its own panic-recover
		startErr = nil
	})
	return startErr
}

// Stop tears down the watcher and closes every Subscribe channel.
// Idempotent. Safe to call before Start.
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

// Get returns one contract by id (ContractsService impl).
func (p *Provider) Get(_ context.Context, key ContractID) (*Contract, error) {
	p.mu.RLock()
	c, ok := p.state[key]
	loaded := p.loaded
	p.mu.RUnlock()
	if !loaded {
		return nil, fmt.Errorf("contracts provider not started")
	}
	if !ok {
		return nil, nil // not found — nil, nil per service contract
	}
	cp := c
	return &cp, nil
}

// GetMany returns multiple contracts in a single call (ContractsService impl).
// Missing keys are absent from the result map.
func (p *Provider) GetMany(_ context.Context, keys []ContractID) (map[ContractID]*Contract, error) {
	out := make(map[ContractID]*Contract, len(keys))
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.loaded {
		return out, fmt.Errorf("contracts provider not started")
	}
	for _, k := range keys {
		c, ok := p.state[k]
		if !ok {
			continue
		}
		cp := c
		out[k] = &cp
	}
	return out, nil
}

// List returns every cached Contract sorted descending by LastEventAt (ContractsService impl).
func (p *Provider) List(_ context.Context, filter *ContractFilter) ([]*Contract, error) {
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
		ti := contracts[i].LastEventAt
		tj := contracts[j].LastEventAt
		if ti.Equal(tj) {
			return contracts[i].ID < contracts[j].ID
		}
		return ti.After(tj)
	})

	out := make([]*Contract, 0, len(contracts))
	for i := range contracts {
		c := contracts[i]
		if filter != nil && !matchesFilter(&c, filter) {
			continue
		}
		cp := c
		out = append(out, &cp)
	}
	return out, nil
}

// Subscribe returns a channel that receives one event per contract whose
// folded value just changed (ContractsService impl).
func (p *Provider) Subscribe(ctx context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, 16)
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

// consume drives the fold loop. Each watcher tick triggers a tail read from
// the saved offset; new events are merged into the existing state and
// invalidations are fanned out for whichever ids changed.
func (p *Provider) consume(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("contracts consume goroutine panicked",
				"recover", fmt.Sprintf("%v", r)) // R17
		}
	}()
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

// refresh tails every jsonl in the dir from the saved per-file offsets,
// applies new events to the in-memory state, advances the offsets, and fans
// out InvalidationEvents for every id touched.
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
		if ev.ID != "" {
			touched[ContractID(ev.ID)] = struct{}{}
		}
	}
	p.offsets = advanced
	p.last = now
	p.mu.Unlock()

	// R16: emit AFTER cache write, not before.
	if len(touched) == 0 {
		return
	}
	for id := range touched {
		p.fanOut(InvalidationEvent{
			Key:    id,
			Reason: "watcher-push",
			At:     now,
		})
	}
}

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
// Sends are best-effort; a slow consumer drops events.
func (p *Provider) fanOut(ev InvalidationEvent) {
	p.subMu.Lock()
	subs := make([]chan InvalidationEvent, 0, len(p.subs))
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

// matchesFilter applies a ContractFilter to a single Contract. All filter
// fields are AND-combined; nil/empty fields match everything.
func matchesFilter(c *Contract, f *ContractFilter) bool {
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
	if f.ParentContractID != nil && *f.ParentContractID != "" && c.ParentContractID != *f.ParentContractID {
		return false
	}
	return true
}

// Compile-time assertion that Provider satisfies ContractsService.
var _ ContractsService = (*Provider)(nil)
