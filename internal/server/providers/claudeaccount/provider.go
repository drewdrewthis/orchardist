package claudeaccount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// PollInterval is the watcher's tick rate. Briefing AC: 60s is fine,
// quota changes slowly. Tests can shrink it via NewWith for snappier
// convergence.
const PollInterval = 60 * time.Second

// CacheTTL is the maximum age of a cached value before Get triggers a
// synchronous refresh. Defaults to PollInterval so a steady-state
// caller never sees a sync shellout.
const CacheTTL = 60 * time.Second

// Provider is the resolver-facing read API for the ClaudeAccount node.
//
// Owns:
//   - one ShellAdapter (raw shellouts).
//   - an in-memory cache keyed by AccountID.
//   - a poll loop on PollInterval.
//   - a fan-out for Subscribers.
//
// Per ADR-011 §2 the surface is read-only — clients write to the
// backend (`claude auth login`, `bunx ccusage`) and the next poll
// picks up the changes.
type Provider struct {
	adapter      *ShellAdapter
	logger       *slog.Logger
	clock        func() time.Time
	pollInterval time.Duration
	ttl          time.Duration

	mu         sync.RWMutex
	cache      map[AccountID]Account
	fresh      map[AccountID]adapter.Freshness
	loaded     bool
	lastErr    error
	lastErrAt  time.Time

	subMu sync.Mutex
	subs  map[chan adapter.InvalidationEvent[AccountID]]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider with production defaults: the real shell
// adapter, the wall clock, and the standard PollInterval.
func New(hostID string, logger *slog.Logger) *Provider {
	return NewWith(NewShellAdapter(hostID, logger), logger, time.Now, PollInterval, CacheTTL)
}

// NewWith is the test-friendly constructor — accepts injected adapter,
// clock, poll interval, and TTL. Production callers use New.
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
		fresh:        map[AccountID]adapter.Freshness{},
		subs:         map[chan adapter.InvalidationEvent[AccountID]]struct{}{},
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
}

// Start hydrates the cache from the adapter and launches the poll
// loop. Subsequent calls are no-ops; one provider, one lifecycle.
//
// A first-load failure is reported but does NOT block Start —
// `claude` may simply not be installed yet, in which case the daemon
// boots cleanly and per-field errors surface on every read until the
// user installs it. The loop keeps trying.
func (p *Provider) Start(ctx context.Context) error {
	p.startOnce.Do(func() {
		_ = p.refresh(ctx, "boot", adapter.SourcePoll)
		go p.pollLoop(ctx)
	})
	return nil
}

// Stop tears down the poll loop and any subscribers. Idempotent.
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

// Get returns one Account by ID plus its freshness. On cache miss or
// stale entry, triggers a synchronous refresh.
//
// If the most recent refresh failed with ErrToolNotInstalled and the
// cache is empty, the error is returned verbatim so the resolver can
// map it to a per-field GraphQL error.
func (p *Provider) Get(ctx context.Context, key AccountID) (Account, adapter.Freshness, error) {
	if v, f, ok := p.cacheGet(key); ok && p.fresh_(f) {
		return v, f, nil
	}
	if err := p.refresh(ctx, "get-stale", adapter.SourcePoll); err != nil {
		// Surface the error if we have nothing cached for the caller.
		if v, f, ok := p.cacheGet(key); ok {
			return v, f, nil
		}
		return Account{}, adapter.Freshness{}, err
	}
	v, f, ok := p.cacheGet(key)
	if !ok {
		return Account{}, adapter.Freshness{}, fmt.Errorf("claudeaccount: account %s not found after refresh", key.GraphQLID())
	}
	return v, f, nil
}

// GetMany satisfies adapter.Provider. v1 collapses to single-key
// reads since `claude auth status` reports one account per call.
func (p *Provider) GetMany(ctx context.Context, keys []AccountID) (map[AccountID]Account, map[AccountID]adapter.Freshness, error) {
	out := make(map[AccountID]Account, len(keys))
	freshness := make(map[AccountID]adapter.Freshness, len(keys))
	for _, k := range keys {
		v, f, err := p.Get(ctx, k)
		if err != nil {
			continue
		}
		out[k] = v
		freshness[k] = f
	}
	return out, freshness, nil
}

// Keys returns every cached AccountID. Cold boot returns an empty
// slice. Callers that need a guaranteed snapshot should call
// List(ctx) instead — it forces a refresh when stale.
func (p *Provider) Keys(_ context.Context) ([]AccountID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]AccountID, 0, len(p.cache))
	for k := range p.cache {
		out = append(out, k)
	}
	return out, nil
}

// List returns every cached Account, refreshing if the cache is empty
// or stale. Used by the resolver for `Query.claudeAccounts`.
//
// If the refresh fails with ErrToolNotInstalled and the cache is
// empty, the error is returned verbatim so the resolver can surface
// a per-field GraphQL error rather than returning [].
func (p *Provider) List(ctx context.Context) ([]Account, error) {
	if err := p.ensureFresh(ctx); err != nil {
		// Cache-empty → propagate. Cache-not-empty → log and serve
		// stale; one transient ccusage failure should not blank the
		// dashboard.
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

// Subscribe returns a buffered channel that receives invalidation
// events for as long as ctx is alive. Closing ctx (or calling Stop)
// cleans the subscription up.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[AccountID] {
	ch := make(chan adapter.InvalidationEvent[AccountID], 8)
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

// LastError returns the most recent refresh error and the time it
// happened. Useful to the resolver when it needs to map an
// ErrToolNotInstalled to a per-field error without holding a fresh
// shellout. Time is the zero value when err is nil.
func (p *Provider) LastError() (time.Time, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastErrAt, p.lastErr
}

// ToGraphQL maps an in-memory Account onto the wire-level
// graphql.ClaudeAccount type the resolver returns. The host edge is
// populated with a stub Host carrying only its id; the full Host type
// lands with Workstream B-host.
//
// instances is always [] in v1 — Workstream B-claudeinstance will
// populate it via cross-provider composition in the resolver.
func (p *Provider) ToGraphQL(a Account) *graphql.ClaudeAccount {
	return &graphql.ClaudeAccount{
		ID:             a.ID.GraphQLID(),
		Email:          a.ID.Email,
		QuotaUsed:      a.QuotaUsed,
		QuotaCap:       a.QuotaCap,
		QuotaEstimated: a.QuotaEstimated,
		QuotaResetsAt:  a.QuotaResetsAt,
		Host: &graphql.Host{
			ID: "Host:" + a.ID.HostID,
		},
		Instances: []*graphql.ClaudeInstance{},
	}
}

// pollLoop refreshes the cache every pollInterval. Exits on stopCh or
// ctx cancellation.
func (p *Provider) pollLoop(ctx context.Context) {
	defer close(p.doneCh)
	t := time.NewTicker(p.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-t.C:
			_ = p.refresh(ctx, "poll-tick", adapter.SourcePoll)
		}
	}
}

// ensureFresh triggers a synchronous refresh if the cache is empty or
// the newest entry is older than ttl. No-op when fresh.
func (p *Provider) ensureFresh(ctx context.Context) error {
	p.mu.RLock()
	stale := !p.loaded
	if !stale {
		newest := time.Time{}
		for _, f := range p.fresh {
			if f.LastFetchedAt.After(newest) {
				newest = f.LastFetchedAt
			}
		}
		stale = p.clock().Sub(newest) > p.ttl
	}
	p.mu.RUnlock()
	if !stale {
		return nil
	}
	return p.refresh(ctx, "ensure-fresh", adapter.SourcePoll)
}

// refresh fetches every account, replaces the cache, and broadcasts
// invalidations for added/changed/removed keys.
//
// Errors are recorded on the provider so List/Get can surface them
// when the cache is empty. ToolNotInstalledError is treated as a
// "soft" failure on the cache (we keep what we had) but a hard error
// for an empty-cache caller — the resolver decides what to do.
func (p *Provider) refresh(ctx context.Context, reason string, source adapter.FreshnessSource) error {
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
		p.fresh = make(map[AccountID]adapter.Freshness, len(all))
		for k := range all {
			p.fresh[k] = adapter.Freshness{LastFetchedAt: now, Source: source}
		}
		p.loaded = true
	}
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
	// A partial result with an error (e.g. claude OK, ccusage missing)
	// still propagates the error so the resolver can render a
	// per-field GraphQL error for the quota fields.
	return err
}

// fresh_ returns true when f is within p.ttl of the provider's clock.
// Underscored to dodge the field shadow.
func (p *Provider) fresh_(f adapter.Freshness) bool {
	return p.clock().Sub(f.LastFetchedAt) <= p.ttl
}

func (p *Provider) cacheGet(k AccountID) (Account, adapter.Freshness, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.cache[k]
	if !ok {
		return Account{}, adapter.Freshness{}, false
	}
	return v, p.fresh[k], true
}

func (p *Provider) cacheEmpty() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.cache) == 0
}

// accountsEqual returns true when two Accounts have identical
// fields. Used to suppress invalidations for no-op refreshes.
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
	if !timesEqual(a.QuotaResetsAt, b.QuotaResetsAt) {
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

func timesEqual(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}

// broadcast fans an InvalidationEvent out to every subscriber. The
// per-channel buffer is small; we drop on a full buffer rather than
// block the watcher goroutine.
func (p *Provider) broadcast(keys []AccountID, reason string, at time.Time) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, k := range keys {
		ev := adapter.InvalidationEvent[AccountID]{Key: k, Reason: reason, At: at}
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

// Compile-time assertion that *Provider satisfies the adapter contract.
// Catches signature drift the moment it happens.
var _ adapter.Provider[AccountID, Account] = (*Provider)(nil)

// Sentinel for compile-time error import — keeps `errors` referenced
// in builds where the rest of the file does not exercise it.
var _ = errors.Is
