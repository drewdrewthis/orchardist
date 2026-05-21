package claudeaccount

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PollInterval is the default watcher tick rate. Quota changes slowly;
// 60s avoids over-polling (O6).
const PollInterval = 60 * time.Second

// CacheTTL is the maximum age of a cached value before Get triggers a
// synchronous refresh. Defaults to PollInterval.
const CacheTTL = 60 * time.Second

// freshnessSource tags how a value was obtained.
type freshnessSource string

const (
	sourcePoll freshnessSource = "poll"
)

// freshness records when and how a cached value was last updated.
type freshness struct {
	lastFetchedAt time.Time
	source        freshnessSource
}

// InvalidationEvent is the per-key signal that a value may have changed (R16).
// Exported so external subscribers (resolvers, subscription writers) can
// receive and inspect events.
type InvalidationEvent struct {
	key    AccountID
	reason string
	at     time.Time
}

// Key returns the AccountID that changed.
func (e InvalidationEvent) Key() AccountID { return e.key }

// Reason returns why the invalidation was emitted.
func (e InvalidationEvent) Reason() string { return e.reason }

// At returns when the invalidation occurred.
func (e InvalidationEvent) At() time.Time { return e.at }

// Provider is the resolver-facing cache for the ClaudeAccount domain.
//
// Owns:
//   - one ShellAdapter (raw shellouts).
//   - an in-memory cache keyed by AccountID.
//   - a poll loop on PollInterval.
//   - a fan-out of invalidation events for Subscribers.
//
// Read-only surface per the domain README. Mutations (login/logout) are
// out of scope in v1.
//
// R17: pollLoop is the only long-running goroutine; it wraps with recover.
type Provider struct {
	adapter      *ShellAdapter
	logger       *slog.Logger
	clock        func() time.Time
	pollInterval time.Duration
	ttl          time.Duration

	mu        sync.RWMutex
	cache     map[AccountID]Account
	fresh     map[AccountID]freshness
	loaded    bool
	lastErr   error
	lastErrAt time.Time

	subMu sync.Mutex
	subs  map[chan InvalidationEvent]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider with production defaults.
func New(hostID string, logger *slog.Logger) *Provider {
	return NewWith(NewShellAdapter(hostID, logger), logger, time.Now, PollInterval, CacheTTL)
}

// NewWith is the test-friendly constructor — accepts injected adapter,
// clock, poll interval, and TTL.
func NewWith(a *ShellAdapter, logger *slog.Logger, clock func() time.Time, poll, ttl time.Duration) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	if poll <= 0 {
		poll = PollInterval
	}
	if ttl <= 0 {
		ttl = CacheTTL
	}
	return &Provider{
		adapter:      a,
		logger:       logger,
		clock:        clock,
		pollInterval: poll,
		ttl:          ttl,
		cache:        map[AccountID]Account{},
		fresh:        map[AccountID]freshness{},
		subs:         map[chan InvalidationEvent]struct{}{},
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start hydrates the cache and launches the poll loop. Subsequent calls
// are no-ops. A first-load failure does NOT block Start — the loop keeps
// retrying. Per-field GraphQL errors surface on read until the tool is
// installed.
func (p *Provider) Start(ctx context.Context) error {
	p.startOnce.Do(func() {
		_ = p.refresh(ctx, "boot")
		go p.pollLoop(ctx)
	})
	return nil
}

// Stop tears down the poll loop and all subscribers. Idempotent.
func (p *Provider) Stop() error {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
		p.subMu.Lock()
		for ch := range p.subs {
			close(ch)
			delete(p.subs, ch)
		}
		p.subMu.Unlock()
	})
	return nil
}

// Get returns one Account by ID plus its freshness indicator. On cache miss
// or stale entry, triggers a synchronous refresh.
func (p *Provider) Get(ctx context.Context, key AccountID) (Account, bool, error) {
	if v, f, ok := p.cacheGet(key); ok && p.isFresh(f) {
		return v, true, nil
	}
	if err := p.refresh(ctx, "get-stale"); err != nil {
		if v, _, ok := p.cacheGet(key); ok {
			return v, false, nil
		}
		return Account{}, false, err
	}
	v, _, ok := p.cacheGet(key)
	if !ok {
		return Account{}, false, fmt.Errorf("claudeaccount: account %s not found after refresh", key.GraphQLID())
	}
	return v, true, nil
}

// List returns every cached Account, refreshing when the cache is empty or
// stale. If the refresh fails with ErrToolNotInstalled and the cache is
// empty, the error is returned verbatim so the resolver can surface a
// per-field GraphQL error rather than returning [].
func (p *Provider) List(ctx context.Context) ([]Account, error) {
	if err := p.ensureFresh(ctx); err != nil {
		if p.cacheEmpty() {
			return nil, err
		}
		p.logger.Warn("claudeaccount: serving stale cache after refresh failure", "err", err)
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Account, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	return out, nil
}

// Subscribe returns a buffered channel that receives InvalidationEvents for
// as long as ctx is alive. Closing ctx (or calling Stop) cleans up the
// subscription.
//
// Channel direction is receive-only per R12.
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

// LastError returns the most recent refresh error and the time it happened.
// Time is zero when err is nil. Used by the resolver for per-field errors.
func (p *Provider) LastError() (time.Time, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastErrAt, p.lastErr
}

// Adapter exposes the underlying ShellAdapter for the pass-through resolver.
func (p *Provider) Adapter() *ShellAdapter { return p.adapter }

// pollLoop refreshes the cache every pollInterval.
// R17: wrapped in recover to prevent daemon death on panics.
func (p *Provider) pollLoop(ctx context.Context) {
	defer close(p.doneCh)
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("claudeaccount: poll loop panicked", "panic", r)
		}
	}()
	t := time.NewTicker(p.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-t.C:
			_ = p.refresh(ctx, "poll-tick")
		}
	}
}

// ensureFresh triggers a synchronous refresh if cache is empty or stale.
func (p *Provider) ensureFresh(ctx context.Context) error {
	p.mu.RLock()
	stale := !p.loaded
	if !stale {
		newest := time.Time{}
		for _, f := range p.fresh {
			if f.lastFetchedAt.After(newest) {
				newest = f.lastFetchedAt
			}
		}
		stale = p.clock().Sub(newest) > p.ttl
	}
	p.mu.RUnlock()
	if !stale {
		return nil
	}
	return p.refresh(ctx, "ensure-fresh")
}

// refresh fetches every account, replaces the cache, and broadcasts
// invalidation events AFTER the cache write (R16).
func (p *Provider) refresh(ctx context.Context, reason string) error {
	all, err := p.adapter.FetchAll(ctx)
	now := p.clock()

	p.mu.Lock()
	p.lastErr = err
	if err != nil {
		p.lastErrAt = now
	}
	if err != nil && len(all) == 0 {
		p.mu.Unlock()
		return err
	}
	old := p.cache
	if all != nil {
		p.cache = all
		p.fresh = make(map[AccountID]freshness, len(all))
		for k := range all {
			p.fresh[k] = freshness{lastFetchedAt: now, source: sourcePoll}
		}
		p.loaded = true
	}
	// Cache write complete BEFORE broadcast (R16).
	p.mu.Unlock()

	changed := make([]AccountID, 0)
	for k, v := range all {
		ov, had := old[k]
		if !had || !accountsEqual(ov, v) {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, still := all[k]; !still {
			changed = append(changed, k)
		}
	}
	if len(changed) > 0 || reason != "boot" {
		p.broadcast(changed, reason, now)
	}
	return err
}

// isFresh returns true when f is within p.ttl of the provider's clock.
func (p *Provider) isFresh(f freshness) bool {
	return p.clock().Sub(f.lastFetchedAt) <= p.ttl
}

func (p *Provider) cacheGet(k AccountID) (Account, freshness, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.cache[k]
	if !ok {
		return Account{}, freshness{}, false
	}
	return v, p.fresh[k], true
}

func (p *Provider) cacheEmpty() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.cache) == 0
}

// accountsEqual returns true when two Accounts have identical fields.
// Used to suppress invalidations for no-op refreshes.
func accountsEqual(a, b Account) bool {
	if a.ID != b.ID || a.QuotaEstimated != b.QuotaEstimated {
		return false
	}
	if !floatPtrsEqual(a.QuotaUsed, b.QuotaUsed) {
		return false
	}
	if !floatPtrsEqual(a.QuotaCap, b.QuotaCap) {
		return false
	}
	if !timePtrsEqual(a.QuotaResetsAt, b.QuotaResetsAt) {
		return false
	}
	return true
}

func floatPtrsEqual(a, b *float64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func timePtrsEqual(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

// broadcast fans an InvalidationEvent out to every subscriber.
// Drops on a full buffer rather than blocking the poll goroutine.
func (p *Provider) broadcast(keys []AccountID, reason string, at time.Time) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, k := range keys {
		ev := InvalidationEvent{key: k, reason: reason, at: at}
		for ch := range p.subs {
			select {
			case ch <- ev:
			default:
				p.logger.Warn("claudeaccount: subscriber lagging, dropping event",
					"account_id", k.GraphQLID())
			}
		}
	}
}
