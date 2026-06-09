package chat

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/drewdrewthis/orchardist/internal/server/adapter"
)

// Provider exposes Chat rooms and ChatMessage events to the GraphQL
// resolver layer.
//
// Owns:
//   - one [Adapter] (JSONL read).
//   - one [Watcher] (fsnotify on the chat directory).
//   - the in-memory fold result, keyed by RoomID.
//   - the per-key Subscribe fan-out for invalidation events.
//
// Per ADR-011 §2 the surface is read-only. Writes happen out-of-band
// via chat-core's `orchard chat send` CLI; the watcher turns those
// writes into invalidation events, and the next read picks up the
// fresh fold.
type Provider struct {
	adapterIO *Adapter
	watcher   *Watcher
	logger    *slog.Logger

	mu      sync.RWMutex
	rooms   map[RoomID]Room
	loaded  bool
	offsets map[string]int64

	subMu sync.Mutex
	subs  map[chan adapter.InvalidationEvent[RoomID]]struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	// notifyOverride is for tests — replaces the watcher's channel.
	notifyOverride <-chan struct{}
}

// New constructs a Provider using DefaultDir.
func New(logger *slog.Logger) *Provider {
	return NewWithPath(DefaultDir(), logger)
}

// NewWithPath is the test-friendly constructor.
func NewWithPath(dir string, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		adapterIO: NewAdapter(dir),
		watcher:   NewWatcher(dir, logger),
		logger:    logger,
		rooms:     map[RoomID]Room{},
		offsets:   map[string]int64{},
		subs:      map[chan adapter.InvalidationEvent[RoomID]]struct{}{},
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// LogPath returns the absolute path the Provider reads from.
func (p *Provider) LogPath() string { return p.adapterIO.Dir() }

// Start hydrates from a Snapshot, then launches the watcher loop.
// Subsequent calls are no-ops.
func (p *Provider) Start(ctx context.Context) error {
	var startErr error
	p.startOnce.Do(func() {
		startErr = p.hydrate(ctx)
		go p.run(ctx)
	})
	return startErr
}

func (p *Provider) hydrate(ctx context.Context) error {
	rooms, offsets, err := p.adapterIO.Snapshot(ctx)
	if err != nil {
		p.logger.Warn("chat provider: hydrate snapshot failed", "err", err)
	}
	folded := make(map[RoomID]Room, len(rooms))
	for room, evs := range rooms {
		folded[room] = Fold(room, evs)
	}
	p.mu.Lock()
	p.rooms = folded
	p.offsets = offsets
	p.loaded = true
	p.mu.Unlock()
	return nil
}

func (p *Provider) run(ctx context.Context) {
	defer close(p.doneCh)
	if p.notifyOverride == nil {
		go func() {
			if err := p.watcher.Run(ctx); err != nil {
				p.logger.Warn("chat provider: watcher exited", "err", err)
			}
		}()
	}
	notify := p.notifyOverride
	if notify == nil {
		notify = p.watcher.Notifications()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case _, ok := <-notify:
			if !ok {
				return
			}
			p.refresh(ctx)
		}
	}
}

func (p *Provider) refresh(ctx context.Context) {
	rooms, offsets, err := p.adapterIO.Snapshot(ctx)
	if err != nil {
		p.logger.Warn("chat provider: refresh snapshot failed", "err", err)
	}
	folded := make(map[RoomID]Room, len(rooms))
	for room, evs := range rooms {
		folded[room] = Fold(room, evs)
	}
	p.mu.Lock()
	prev := p.rooms
	p.rooms = folded
	p.offsets = offsets
	p.mu.Unlock()
	// Publish for any room that's new or changed (compare timestamps).
	for room, r := range folded {
		old, existed := prev[room]
		if !existed || !old.LastEventAt.Equal(r.LastEventAt) || len(old.Messages) != len(r.Messages) || len(old.Members) != len(r.Members) {
			p.publish(room)
		}
	}
}

// Subscribe returns a channel that receives an InvalidationEvent each
// time a room changes.
func (p *Provider) Subscribe(ctx context.Context) <-chan adapter.InvalidationEvent[RoomID] {
	ch := make(chan adapter.InvalidationEvent[RoomID], 16)
	p.subMu.Lock()
	p.subs[ch] = struct{}{}
	p.subMu.Unlock()
	go func() {
		<-ctx.Done()
		p.subMu.Lock()
		delete(p.subs, ch)
		p.subMu.Unlock()
		close(ch)
	}()
	return ch
}

func (p *Provider) publish(room RoomID) {
	ev := adapter.InvalidationEvent[RoomID]{Key: room}
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for ch := range p.subs {
		select {
		case ch <- ev:
		default:
			// drop slow subscribers
		}
	}
}

// Rooms returns a snapshot of every room currently cached.
func (p *Provider) Rooms() []Room {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Room, 0, len(p.rooms))
	for _, r := range p.rooms {
		out = append(out, cloneRoom(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Room returns one room's folded state.
func (p *Provider) Room(id RoomID) (Room, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.rooms[id]
	if !ok {
		return Room{}, false
	}
	return cloneRoom(r), true
}

// Stop cancels the run loop. Idempotent.
func (p *Provider) Stop() { p.stopOnce.Do(func() { close(p.stopCh) }) }

// Done returns a channel closed when the run loop exits.
func (p *Provider) Done() <-chan struct{} { return p.doneCh }

func cloneRoom(r Room) Room {
	out := Room{ID: r.ID, LastEventAt: r.LastEventAt}
	if len(r.Messages) > 0 {
		out.Messages = make([]Message, len(r.Messages))
		copy(out.Messages, r.Messages)
	}
	if len(r.Members) > 0 {
		out.Members = make([]Member, len(r.Members))
		copy(out.Members, r.Members)
	}
	return out
}

