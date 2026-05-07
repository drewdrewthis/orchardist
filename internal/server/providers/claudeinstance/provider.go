package claudeinstance

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/adapter"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// Provider is the resolver-facing read API for the ClaudeInstance node
// per ADR-011 §5.1. Owns:
//   - one HeartbeatReader (raw filesystem I/O).
//   - one Composer (stateless join over sibling providers).
//   - a small in-memory cache keyed by InstanceID.
//   - a Watcher (fsnotify + 5s poll fallback) to invalidate the cache.
//   - subscriber fan-out for GraphQL Subscriptions (wired by ws-c).
//
// The cross-provider edges (TmuxPane, Process, ClaudeAccount) are owned
// by their respective providers; this provider asks for them through
// dependency-injected interfaces so it neither imports them nor holds
// duplicate copies of their state.
type Provider struct {
	hostID   string
	reader   HeartbeatReader
	composer *Composer
	clock    func() time.Time

	mu     sync.RWMutex
	cache  map[InstanceID]*graphql.ClaudeInstance
	fresh  map[InstanceID]adapter.Freshness
	loaded bool

	subsMu sync.Mutex
	subs   []chan adapter.InvalidationEvent[InstanceID]

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// New constructs a Provider with the production HeartbeatReader (file
// glob over the resolved heartbeat dir) and the supplied composer.
//
// hostID identifies the local host; the daemon entry point passes the
// host provider's LocalID so cross-host federation can disambiguate
// instance ids when ws-f lands.
func New(hostID string, composer *Composer) *Provider {
	return NewWith(hostID, NewFileReader(""), composer, time.Now)
}

// NewWith is the test-friendly constructor. Tests inject a fixture
// HeartbeatReader and clock so freshness assertions converge in
// milliseconds and the file system stays untouched.
func NewWith(hostID string, reader HeartbeatReader, composer *Composer, clock func() time.Time) *Provider {
	if clock == nil {
		clock = time.Now
	}
	return &Provider{
		hostID:   hostID,
		reader:   reader,
		composer: composer,
		clock:    clock,
		cache:    map[InstanceID]*graphql.ClaudeInstance{},
		fresh:    map[InstanceID]adapter.Freshness{},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start hydrates the cache with an initial sweep and is idempotent.
// The watcher loop is owned separately by NewWatcher().Run(); Start
// itself does not spawn goroutines so callers can compose lifecycles
// freely (the daemon entry point pairs Start with Watcher.Run).
//
// A first-load failure does NOT block Start — the heartbeat directory
// may simply not exist yet. Subsequent watcher refreshes pick up files
// as they are written.
func (p *Provider) Start(ctx context.Context) error {
	var err error
	p.startOnce.Do(func() {
		err = p.refreshLocked(ctx, "boot")
	})
	return err
}

// Stop tears down subscribers. Idempotent; safe to call multiple times.
// The watcher's Run loop is stopped via its own context — callers
// cancel that ctx separately.
func (p *Provider) Stop() error {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		close(p.doneCh)
		p.subsMu.Lock()
		for _, ch := range p.subs {
			close(ch)
		}
		p.subs = nil
		p.subsMu.Unlock()
	})
	return nil
}

// Get returns one ClaudeInstance by InstanceID. Pure cache read; the
// watcher (or callers calling Refresh directly) is responsible for
// keeping the cache fresh.
//
// Unknown keys produce a typed error so resolvers can map it to a
// per-field GraphQL error rather than nil-with-no-data.
func (p *Provider) Get(_ context.Context, key InstanceID) (*graphql.ClaudeInstance, adapter.Freshness, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.cache[key]; ok {
		return v, p.fresh[key], nil
	}
	return nil, adapter.Freshness{}, fmt.Errorf("claudeinstance: unknown id %s:%d", key.HostID, key.ClaudePid)
}

// GetMany satisfies adapter.Provider. Missing keys are simply absent
// from the returned maps — the caller distinguishes hit/miss by map
// presence, never by zero values.
func (p *Provider) GetMany(_ context.Context, keys []InstanceID) (map[InstanceID]*graphql.ClaudeInstance, map[InstanceID]adapter.Freshness, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[InstanceID]*graphql.ClaudeInstance, len(keys))
	freshness := make(map[InstanceID]adapter.Freshness, len(keys))
	for _, k := range keys {
		if v, ok := p.cache[k]; ok {
			out[k] = v
			freshness[k] = p.fresh[k]
		}
	}
	return out, freshness, nil
}

// Keys returns every cached InstanceID. Cold boot (before the first
// successful Refresh) returns an empty slice per the Provider contract.
func (p *Provider) Keys(_ context.Context) ([]InstanceID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]InstanceID, 0, len(p.cache))
	for k := range p.cache {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostID != out[j].HostID {
			return out[i].HostID < out[j].HostID
		}
		return out[i].ClaudePid < out[j].ClaudePid
	})
	return out, nil
}

// List returns every cached ClaudeInstance, refreshed if the cache has
// never been hydrated. The resolver for `Query.claudeInstances` calls
// this; it returns [] (empty list) cleanly when no heartbeats exist —
// per ADR-011 §6 a missing instance is normal, not a per-field error.
func (p *Provider) List(ctx context.Context) ([]*graphql.ClaudeInstance, error) {
	p.mu.RLock()
	loaded := p.loaded
	p.mu.RUnlock()
	if !loaded {
		if err := p.Refresh(ctx, "lazy-load"); err != nil {
			return nil, err
		}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*graphql.ClaudeInstance, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Subscribe returns a channel that emits an invalidation event each
// time Refresh observes a change. Sends are non-blocking — slow
// consumers drop events rather than stalling the watcher loop.
//
// The channel closes when ctx is cancelled. Callers that want a
// guaranteed every-event delivery must buffer and drain promptly.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[InstanceID] {
	ch := make(chan adapter.InvalidationEvent[InstanceID], 8)
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

// Refresh re-reads heartbeats and re-runs the composer, broadcasting
// invalidation events for any keys whose value changed (including
// newly-added or newly-removed keys). Used by Watcher; safe to call
// directly from tests.
func (p *Provider) Refresh(ctx context.Context, reason string) error {
	return p.refreshLocked(ctx, reason)
}

func (p *Provider) refreshLocked(ctx context.Context, reason string) error {
	hbs, err := p.reader.ReadAll(ctx)
	if err != nil {
		return fmt.Errorf("claudeinstance read heartbeats: %w", err)
	}
	now := p.clock()
	composed := p.composer.Compose(ctx, hbs)

	p.mu.Lock()
	prev := p.cache
	next := make(map[InstanceID]*graphql.ClaudeInstance, len(composed))
	freshNext := make(map[InstanceID]adapter.Freshness, len(composed))
	for _, inst := range composed {
		host, pid, ok := parseInstanceID(inst.ID)
		key := InstanceID{HostID: host, ClaudePid: pid}
		if !ok {
			// Session-keyed (no pid) — still cache, using the synthesised
			// host id. Multiple session-keyed instances on one host MUST
			// have distinct tmux session names; the parser returns pid==0
			// for all of them, so we layer the session into the key by
			// stuffing the negated hash into ClaudePid.
			key = InstanceID{HostID: host, ClaudePid: -hashSession(inst.ID)}
		}
		next[key] = inst
		freshNext[key] = adapter.Freshness{LastFetchedAt: now, Source: adapter.SourcePoll}
	}
	p.cache = next
	p.fresh = freshNext
	p.loaded = true
	p.mu.Unlock()

	p.fanOutInvalidations(prev, next, reason, now)
	return nil
}

// fanOutInvalidations broadcasts InvalidationEvents for every key that
// changed between the previous and next cache snapshots. New keys,
// removed keys, and value changes all emit events; unchanged keys are
// silent so subscribers do not get a spurious flood after every poll.
func (p *Provider) fanOutInvalidations(prev, next map[InstanceID]*graphql.ClaudeInstance, reason string, at time.Time) {
	keys := make(map[InstanceID]struct{}, len(prev)+len(next))
	for k := range prev {
		keys[k] = struct{}{}
	}
	for k := range next {
		keys[k] = struct{}{}
	}

	p.subsMu.Lock()
	subs := append([]chan adapter.InvalidationEvent[InstanceID](nil), p.subs...)
	p.subsMu.Unlock()
	if len(subs) == 0 {
		return
	}

	for k := range keys {
		if instancesEqual(prev[k], next[k]) {
			continue
		}
		ev := adapter.InvalidationEvent[InstanceID]{
			Key:    k,
			Reason: reason,
			At:     at,
		}
		for _, c := range subs {
			select {
			case c <- ev:
			default:
			}
		}
	}
}

// instancesEqual returns true when two ClaudeInstances are
// observationally equal for the fields a resolver would expose. Used to
// suppress noise events when the heartbeat sweep produces an
// identical instance to the one already cached.
func instancesEqual(a, b *graphql.ClaudeInstance) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.ID != b.ID {
		return false
	}
	if a.State != b.State {
		return false
	}
	if a.RcEnabled != b.RcEnabled {
		return false
	}
	if !ptrStringEqual(a.RcURL, b.RcURL) {
		return false
	}
	if !ptrStringEqual(a.SessionUUID, b.SessionUUID) {
		return false
	}
	if !ptrStringEqual(a.StartedAt, b.StartedAt) {
		return false
	}
	if !ptrStringEqual(a.LastActivityAt, b.LastActivityAt) {
		return false
	}
	return true
}

func ptrStringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// hashSession is a tiny FNV-1a-ish hash so session-keyed instances get
// stable map keys. We do not need cryptographic strength — collisions
// would be visible as one instance overwriting another, which would
// surface as a bug in tests instantly.
func hashSession(s string) int {
	h := 2166136261
	for _, c := range []byte(s) {
		h ^= int(c)
		h *= 16777619
	}
	if h < 0 {
		h = -h
	}
	if h == 0 {
		h = 1 // avoid (0, 0) collision with a real pid==0 key
	}
	return h
}

// Compile-time assertion that *Provider satisfies the generic Provider
// interface for ClaudeInstance.
var _ adapter.Provider[InstanceID, *graphql.ClaudeInstance] = (*Provider)(nil)
