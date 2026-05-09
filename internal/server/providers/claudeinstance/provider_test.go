package claudeinstance

import (
	"context"
	"testing"
	"time"
)

// staticReader is a HeartbeatReader fake whose ReadAll returns a
// caller-controlled slice. Used to drive Provider deterministically.
type staticReader struct {
	dir        string
	heartbeats []Heartbeat
}

func (s *staticReader) ReadAll(_ context.Context) ([]Heartbeat, error) {
	out := make([]Heartbeat, len(s.heartbeats))
	copy(out, s.heartbeats)
	return out, nil
}

func (s *staticReader) Dir() string { return s.dir }

// TestProvider_List_Empty asserts a fresh provider with no heartbeats
// returns an empty list cleanly — never an error. Briefing AC: "missing
// instance is normal, not a per-field error".
func TestProvider_List_Empty(t *testing.T) {
	now := time.Now()
	r := &staticReader{}
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{}, nil, func() time.Time { return now }, HeartbeatStaleAfter)
	p := NewWith("local", r, c, func() time.Time { return now })

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 from empty reader", len(got))
	}
}

// TestProvider_Refresh_PopulatesCache asserts a refresh hydrates the
// cache and List then returns the composed instances.
func TestProvider_Refresh_PopulatesCache(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hbs := []Heartbeat{{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}}
	r := &staticReader{heartbeats: hbs}
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{alive: map[int]bool{42100: true}}, nil, func() time.Time { return now }, HeartbeatStaleAfter)
	p := NewWith("local", r, c, func() time.Time { return now })

	if err := p.Refresh(context.Background(), "test"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	got, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}

	keys, err := p.Keys(context.Background())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("Keys() = %v, want 1 entry", keys)
	}
	if keys[0].ClaudePid != 42100 {
		t.Errorf("Keys[0].ClaudePid = %d, want 42100", keys[0].ClaudePid)
	}
}

// TestProvider_Subscribe_FiresOnChange asserts a subscriber receives a
// non-nil InvalidationEvent when a refresh produces a different value.
// Suppression of unchanged-value events stops the watcher from
// flooding subscribers every poll cycle.
func TestProvider_Subscribe_FiresOnChange(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	r := &staticReader{}
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{alive: map[int]bool{42100: true}}, nil, clock, HeartbeatStaleAfter)
	p := NewWith("local", r, c, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := p.Subscribe(ctx)

	// First refresh: empty → still empty. No events.
	if err := p.Refresh(ctx, "boot"); err != nil {
		t.Fatalf("Refresh1: %v", err)
	}

	// Add a heartbeat; refresh should emit an event for the new key.
	r.heartbeats = []Heartbeat{{
		TmuxSession:     "alpha",
		State:           "working",
		ClaudePid:       42100,
		Timestamp:       now.Add(-1 * time.Second),
		LastHeartbeatAt: now.Add(-1 * time.Second),
	}}
	if err := p.Refresh(ctx, "add"); err != nil {
		t.Fatalf("Refresh2: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Reason != "add" {
			t.Errorf("event reason = %q, want add", ev.Reason)
		}
		if ev.Key.ClaudePid != 42100 {
			t.Errorf("event pid = %d, want 42100", ev.Key.ClaudePid)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive invalidation event after add")
	}

	// Re-refresh with the SAME data: no event (suppressed).
	if err := p.Refresh(ctx, "noop"); err != nil {
		t.Fatalf("Refresh3: %v", err)
	}
	select {
	case ev := <-events:
		t.Errorf("unexpected event for unchanged refresh: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// No event — desired.
	}
}

// TestProvider_Get_Unknown asserts an unknown id surfaces a typed error
// so resolvers can distinguish "no such id" from "stale cache".
func TestProvider_Get_Unknown(t *testing.T) {
	r := &staticReader{}
	now := time.Now()
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{}, nil, func() time.Time { return now }, HeartbeatStaleAfter)
	p := NewWith("local", r, c, func() time.Time { return now })
	_, _, err := p.Get(context.Background(), InstanceID{HostID: "local", ClaudePid: 99999})
	if err == nil {
		t.Error("Get on unknown key err = nil, want non-nil")
	}
}
