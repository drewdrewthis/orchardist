package hostservices

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// provider is the host-service-domain in-process cache and poll loop.
// Internal — not exported; consumers use Service (R2).
//
// It owns the adapter (OS-conditional via build tags), a snapshot cache
// keyed by HostServiceID, and a fan-out of invalidation events to
// subscribers.
//
// v1 always operates against a single machineID (the local machine).
// Federation expands this in a later workstream.
type provider struct {
	adapter   adapter
	machineID string
	services  []string
	clock     func() time.Time

	mu    sync.RWMutex
	cache map[HostServiceID]HostServiceSnapshot
	errs  map[HostServiceID]error

	subsMu sync.Mutex
	subs   []chan InvalidationEvent
}

// newProvider constructs a provider. services is deduped and blank
// entries stripped — callers can pass raw config without sanitising.
func newProvider(a adapter, machineID string, services []string, clock func() time.Time) *provider {
	if clock == nil {
		clock = time.Now
	}
	cleaned := dedupServices(services)
	return &provider{
		adapter:   a,
		machineID: machineID,
		services:  cleaned,
		clock:     clock,
		cache:     make(map[HostServiceID]HostServiceSnapshot, len(cleaned)),
		errs:      make(map[HostServiceID]error, len(cleaned)),
	}
}

// start hydrates the cache once synchronously, then launches the poll
// loop. The loop terminates when ctx is cancelled (R10).
func (p *provider) start(ctx context.Context) error {
	p.refreshAll(ctx)
	go p.pollLoop(ctx)
	return nil
}

// snapshots returns a copy of all current snapshots. Safe for concurrent
// use (R13 — RWMutex for read-heavy access).
func (p *provider) snapshots() []HostServiceSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]HostServiceSnapshot, 0, len(p.cache))
	for _, snap := range p.cache {
		out = append(out, snap)
	}
	return out
}

// byID returns the snapshot for one HostServiceID.
func (p *provider) byID(id HostServiceID) (HostServiceSnapshot, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if errVal := p.errs[id]; errVal != nil {
		return HostServiceSnapshot{}, errVal
	}
	snap, ok := p.cache[id]
	if !ok {
		return HostServiceSnapshot{}, fmt.Errorf("hostservices: no snapshot for %q", id)
	}
	return snap, nil
}

// byMachineID returns all snapshots belonging to machineID.
func (p *provider) byMachineID(machineID string) []HostServiceSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []HostServiceSnapshot
	for _, snap := range p.cache {
		if snap.MachineID == machineID {
			out = append(out, snap)
		}
	}
	return out
}

// subscribe returns a read-only invalidation channel (R12). The channel
// closes when ctx is cancelled (R10).
func (p *provider) subscribe(ctx context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent, 16)
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

func (p *provider) refreshAll(ctx context.Context) {
	for _, name := range p.services {
		key := MakeID(p.machineID, name)
		_ = p.refreshOne(ctx, key, p.machineID, name)
	}
}

func (p *provider) refreshOne(ctx context.Context, key HostServiceID, machineID, name string) error {
	snap, err := p.adapter.fetchOne(ctx, machineID, name)
	now := p.clock()

	p.mu.Lock()
	if err != nil {
		p.errs[key] = err
		p.mu.Unlock()
		return err
	}
	snap.FetchedAt = now
	p.cache[key] = snap
	delete(p.errs, key)
	p.mu.Unlock()

	// Emit AFTER cache write (R16 — subscribers see fresh data).
	p.fanOut(key, now)
	return nil
}

func (p *provider) fanOut(key HostServiceID, at time.Time) {
	ev := InvalidationEvent{
		Key:    key,
		Reason: "poll-refresh",
		At:     at,
	}
	p.subsMu.Lock()
	subs := append([]chan InvalidationEvent(nil), p.subs...)
	p.subsMu.Unlock()
	for _, c := range subs {
		select {
		case c <- ev:
		default: // non-blocking — slow consumer drops events (R10)
		}
	}
}

// pollLoop runs a 5s ticker and refreshes all services on each tick.
// Wraps its top-level loop in a panic-recover + structured log (R17).
func (p *provider) pollLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("hostservices: pollLoop panic", "recovered", r)
		}
	}()

	if len(p.services) == 0 {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshAll(ctx)
		}
	}
}

// pollInterval is the maximum age of a cached snapshot before the next
// Get call refreshes synchronously. Per O6: 5s is fast enough to catch
// service bounces; slow enough not to hammer launchctl/systemctl at idle.
const pollInterval = 5 * time.Second

func dedupServices(services []string) []string {
	out := make([]string, 0, len(services))
	seen := make(map[string]struct{}, len(services))
	for _, s := range services {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
