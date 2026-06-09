package claudejsonls

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"sync"
	"time"

	gql "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// HeartbeatThreshold is the cutoff for `open: Boolean!`. A
// Conversation is reported open when its last record was written within
// this window. Default 60s — large enough that human typing pauses do
// not flicker the field, small enough that an abandoned session settles
// to closed within a minute.
const HeartbeatThreshold = 60 * time.Second

// Provider implements Service. It holds:
//   - an FSAdapter for disk access
//   - an in-memory cache keyed by ConversationID
//   - a subscriber fan-out for invalidation events
//
// Per L9, the cache is a projection of external truth. Restart
// re-observes from scratch.
type Provider struct {
	adapter   *FSAdapter
	logger    *slog.Logger
	clock     func() time.Time
	heartbeat time.Duration

	// mu guards cache + fresh. RWMutex: reads are dominant (per R13).
	mu     sync.RWMutex
	cache  map[ConversationID]Conversation
	loaded bool

	// subMu guards subs. Plain Mutex because subscribe and unsubscribe
	// are infrequent relative to reads.
	subMu sync.Mutex
	subs  map[chan InvalidationEvent]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

var _ Service = (*Provider)(nil)

// New constructs a Provider with production defaults: real filesystem
// adapter, real wall-clock, default heartbeat threshold.
func New(root, hostID string, logger *slog.Logger) *Provider {
	return NewWith(NewFSAdapter(root, hostID, logger), logger, time.Now, HeartbeatThreshold)
}

// NewWith is the test-friendly constructor. Callers inject the adapter,
// clock, and heartbeat so unit tests drive openness deterministically
// without touching the real filesystem.
func NewWith(a *FSAdapter, logger *slog.Logger, clock func() time.Time, heartbeat time.Duration) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	if heartbeat <= 0 {
		heartbeat = HeartbeatThreshold
	}
	return &Provider{
		adapter:   a,
		logger:    logger,
		clock:     clock,
		heartbeat: heartbeat,
		cache:     map[ConversationID]Conversation{},
		subs:      map[chan InvalidationEvent]struct{}{},
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start hydrates the cache from the adapter and launches the watcher
// goroutine. Subsequent calls are no-ops (one provider, one lifecycle).
func (p *Provider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		if err := p.reload(ctx, "boot"); err != nil {
			startErr = fmt.Errorf("hydrate claude-jsonls cache: %w", err)
			return
		}
		if p.adapter == nil {
			// Test mode: no adapter, no watcher.
			go func() { close(p.doneCh) }()
			return
		}
		ch, err := p.adapter.Watch(ctx)
		if err != nil {
			startErr = fmt.Errorf("start watcher: %w", err)
			return
		}
		go p.run(ctx, ch)
	})
	return startErr
}

// Stop tears down the watcher and all subscribers. Idempotent.
func (p *Provider) Stop() error {
	var err error
	p.stopOnce.Do(func() {
		close(p.stopCh)
		<-p.doneCh
		if p.adapter != nil {
			err = p.adapter.Close()
		}
		p.subMu.Lock()
		for ch := range p.subs {
			close(ch)
			delete(p.subs, ch)
		}
		p.subMu.Unlock()
	})
	return err
}

// Get returns one Conversation by ID (Service implementation). Cache
// hit is the common path; on miss the adapter reads only that file.
func (p *Provider) Get(ctx context.Context, key ConversationID) (Conversation, error) {
	if v, ok := p.cacheGet(key); ok {
		return v, nil
	}
	if p.adapter == nil {
		return Conversation{}, fmt.Errorf("conversation %q: %w", key.SessionUUID, fs.ErrNotExist)
	}
	v, err := p.adapter.Fetch(ctx, key)
	if err != nil {
		return Conversation{}, err
	}
	p.cachePut(key, v)
	return v, nil
}

// GetMany is the DataLoader-friendly batch read. Unique keys hit the
// cache first; misses share a single FetchAll call (O10).
func (p *Provider) GetMany(ctx context.Context, keys []ConversationID) (map[ConversationID]Conversation, error) {
	out := make(map[ConversationID]Conversation, len(keys))

	missing := make([]ConversationID, 0)
	seen := make(map[ConversationID]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if v, ok := p.cacheGet(k); ok {
			out[k] = v
			continue
		}
		missing = append(missing, k)
	}
	if len(missing) == 0 {
		return out, nil
	}

	if p.adapter == nil {
		return out, nil
	}
	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range missing {
		v, ok := all[k]
		if !ok {
			continue
		}
		p.cachePut(k, v)
		out[k] = v
	}
	return out, nil
}

// Keys returns every cached ConversationID.
func (p *Provider) Keys(_ context.Context) ([]ConversationID, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ConversationID, 0, len(p.cache))
	for k := range p.cache {
		out = append(out, k)
	}
	return out, nil
}

// List returns every cached Conversation sorted descending by
// LastSeenAt (most-recently active first). Conversations with nil
// LastSeenAt sort to the end.
func (p *Provider) List(_ context.Context) ([]Conversation, error) {
	p.mu.RLock()
	out := make([]Conversation, 0, len(p.cache))
	for _, v := range p.cache {
		out = append(out, v)
	}
	p.mu.RUnlock()

	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].LastSeenAt, out[j].LastSeenAt
		switch {
		case ti == nil && tj == nil:
			return out[i].ID.SessionUUID < out[j].ID.SessionUUID
		case ti == nil:
			return false
		case tj == nil:
			return true
		default:
			return ti.After(*tj)
		}
	})
	return out, nil
}

// IsOpen returns whether a conversation is "open" (heartbeat). A
// Conversation is open when its LastSeenAt is within HeartbeatThreshold
// of the provider's current clock. Conversations with nil LastSeenAt
// (an empty JSONL) are closed.
func (p *Provider) IsOpen(c Conversation) bool {
	if c.LastSeenAt == nil {
		return false
	}
	return p.clock().Sub(*c.LastSeenAt) < p.heartbeat
}

// ToGraphQL maps an in-memory Conversation onto the wire-level type.
// recap is always nil in v1.
//
// TODO(plugin): recap will be populated by the conversations plugin.
func (p *Provider) ToGraphQL(c Conversation) *gql.Conversation {
	return &gql.Conversation{
		ID:           c.ID.GraphQLID(),
		SessionUUID:  c.ID.SessionUUID,
		Cwd:          c.Cwd,
		FirstSeenAt:  c.FirstSeenAt,
		LastSeenAt:   c.LastSeenAt,
		MessageCount: c.MessageCount,
		Open:         p.IsOpen(c),
		Recap:        nil,
		JsonlPath:    c.Path,
		CustomTitle:  c.CustomTitle,
		AgentName:    c.AgentName,
	}
}

// PathForSessionUUID returns the on-disk JSONL path for the given
// sessionUUID from the current in-memory cache. Returns ("", false) on
// a cache miss.
func (p *Provider) PathForSessionUUID(_ context.Context, uuid string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, c := range p.cache {
		if id.SessionUUID == uuid {
			return c.Path, true
		}
	}
	return "", false
}

// GetBySessionUUID returns the cached Conversation whose JSONL filename
// matches uuid. Returns (zero, false) on a miss.
func (p *Provider) GetBySessionUUID(_ context.Context, uuid string) (Conversation, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for id, c := range p.cache {
		if id.SessionUUID == uuid {
			return c, true
		}
	}
	return Conversation{}, false
}

// Subscribe returns a buffered channel that receives InvalidationEvents
// for as long as ctx is alive. Closing ctx (or calling Stop) cleans up.
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

// Refresh forces a full re-walk of the projects root.
func (p *Provider) Refresh(ctx context.Context) error {
	return p.reload(ctx, "manual-refresh")
}

// run drains the adapter's Watch channel, refreshes affected entries,
// and broadcasts invalidations to subscribers. Per R17 it wraps its
// loop in a panic-recover handler.
func (p *Provider) run(ctx context.Context, ch <-chan ConversationID) {
	defer close(p.doneCh)
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("claude-jsonls: provider goroutine panicked",
				"recover", r)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case key, ok := <-ch:
			if !ok {
				return
			}
			if err := p.refreshOne(ctx, key); err != nil {
				p.logger.Warn("claude-jsonls: refresh failed",
					"session_uuid", key.SessionUUID, "err", err)
			}
		}
	}
}

// refreshOne re-reads metadata for one conversation after a watcher
// event. On fs.ErrNotExist the entry is dropped and an invalidation
// broadcast is sent (after the cache write per R16).
func (p *Provider) refreshOne(ctx context.Context, key ConversationID) error {
	p.mu.RLock()
	cached, hit := p.cache[key]
	p.mu.RUnlock()

	now := p.clock()

	if !hit {
		return p.reload(ctx, "watcher-create")
	}

	if p.adapter == nil {
		return nil
	}
	v, err := p.adapter.FetchOne(ctx, cached.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Cache write first, then broadcast (R16).
			p.mu.Lock()
			delete(p.cache, key)
			p.mu.Unlock()
			p.broadcast([]ConversationID{key}, "watcher-remove", now)
			return nil
		}
		return err
	}
	// Cache write first, then broadcast (R16).
	p.cachePut(key, v)
	p.broadcast([]ConversationID{key}, "watcher-write", now)
	return nil
}

// reload fetches every conversation, replaces the cache, and broadcasts
// invalidations for added/changed/removed keys. Cache write precedes
// broadcast per R16.
func (p *Provider) reload(ctx context.Context, reason string) error {
	if p.adapter == nil {
		p.mu.Lock()
		p.loaded = true
		p.mu.Unlock()
		return nil
	}

	all, err := p.adapter.FetchAll(ctx)
	if err != nil {
		return err
	}
	now := p.clock()

	p.mu.Lock()
	old := p.cache
	p.cache = all
	p.loaded = true
	p.mu.Unlock()

	// Determine changed keys.
	changed := make([]ConversationID, 0)
	for k, v := range all {
		ov, had := old[k]
		if !had || !conversationsEqual(ov, v) {
			changed = append(changed, k)
		}
	}
	for k := range old {
		if _, still := all[k]; !still {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 && reason == "boot" {
		return nil
	}
	p.broadcast(changed, reason, now)
	return nil
}

// conversationsEqual returns true when two Conversations have identical
// metadata. Used to suppress unnecessary broadcasts on no-op refreshes.
func conversationsEqual(a, b Conversation) bool {
	if a.ID != b.ID || a.Path != b.Path || a.MessageCount != b.MessageCount {
		return false
	}
	if !timesEqual(a.FirstSeenAt, b.FirstSeenAt) {
		return false
	}
	if !timesEqual(a.LastSeenAt, b.LastSeenAt) {
		return false
	}
	if !stringsEqual(a.Cwd, b.Cwd) {
		return false
	}
	return true
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

func stringsEqual(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// broadcast fans an InvalidationEvent out to every subscriber. Sends
// are non-blocking — a slow subscriber drops events but stays alive
// (per R12 the channel is directed; per O7 fan-out is bounded by
// subscriber count).
func (p *Provider) broadcast(keys []ConversationID, reason string, at time.Time) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, k := range keys {
		ev := InvalidationEvent{Key: k, Reason: reason, At: at}
		for ch := range p.subs {
			select {
			case ch <- ev:
			default:
				p.logger.Warn("claude-jsonls: subscriber lagging, dropping broadcast",
					"session_uuid", k.SessionUUID)
			}
		}
	}
}

func (p *Provider) cacheGet(k ConversationID) (Conversation, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.cache[k]
	return v, ok
}

func (p *Provider) cachePut(k ConversationID, v Conversation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[k] = v
}
