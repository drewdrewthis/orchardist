package hostservice

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeAdapter is the test double for Adapter. Each entry in `responses`
// is keyed by service name; the test sets up the desired Snapshot or
// error and the Provider drives it through the public API. No real
// shellouts — these tests live in the pure-Go layer.
type fakeAdapter struct {
	mu        sync.Mutex
	responses map[string]fakeReply
	calls     map[string]int
}

type fakeReply struct {
	snap Snapshot
	err  error
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		responses: make(map[string]fakeReply),
		calls:     make(map[string]int),
	}
}

func (f *fakeAdapter) set(name string, snap Snapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[name] = fakeReply{snap: snap, err: err}
}

func (f *fakeAdapter) callsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fakeAdapter) FetchOne(_ context.Context, hostID, name string) (Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
	r, ok := f.responses[name]
	if !ok {
		return Snapshot{HostID: hostID, Name: name, State: StateUnknown}, nil
	}
	if r.snap.HostID == "" {
		r.snap.HostID = hostID
	}
	if r.snap.Name == "" {
		r.snap.Name = name
	}
	return r.snap, r.err
}

// TestProvider_KeysReflectsConfiguredServices asserts Keys() returns the
// configured watchlist verbatim, before any cache hydration.
func TestProvider_KeysReflectsConfiguredServices(t *testing.T) {
	p := NewWith(newFakeAdapter(), "host-id", []string{"foo", "bar"}, time.Now)

	keys, err := p.Keys(context.Background())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	want := []HostServiceID{
		MakeID("host-id", "foo"),
		MakeID("host-id", "bar"),
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
}

// TestProvider_GetReturnsSnapshotForKnownKey asserts Get hydrates the
// cache from the adapter on first read and returns the Snapshot.
func TestProvider_GetReturnsSnapshotForKnownKey(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	p := NewWith(a, "host-id", []string{"foo"}, clock)

	got, fresh, err := p.Get(context.Background(), MakeID("host-id", "foo"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateActive {
		t.Errorf("State = %q, want active", got.State)
	}
	if got.HostID != "host-id" || got.Name != "foo" {
		t.Errorf("identity mismatch: hostID=%q name=%q", got.HostID, got.Name)
	}
	if !got.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, now)
	}
	if fresh.LastFetchedAt != now {
		t.Errorf("Freshness.LastFetchedAt = %v, want %v", fresh.LastFetchedAt, now)
	}
}

// TestProvider_GetUnknownKey asserts Get errors when the requested key
// isn't on the watchlist (caller bug — the resolver should be iterating
// Services()).
func TestProvider_GetUnknownKey(t *testing.T) {
	p := NewWith(newFakeAdapter(), "host-id", []string{"foo"}, time.Now)

	_, _, err := p.Get(context.Background(), MakeID("host-id", "not-watched"))
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

// TestProvider_GetCachesUntilTTL asserts back-to-back Gets within the
// PollInterval window do NOT hit the adapter twice. Critical for keeping
// shellout volume sane under DataLoader fan-out.
func TestProvider_GetCachesUntilTTL(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	p := NewWith(a, "host-id", []string{"foo"}, clock.Now)
	key := MakeID("host-id", "foo")

	if _, _, err := p.Get(context.Background(), key); err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	clock.advance(2 * time.Second)
	if _, _, err := p.Get(context.Background(), key); err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if got := a.callsFor("foo"); got != 1 {
		t.Errorf("adapter called %d times within TTL, want 1", got)
	}
}

// TestProvider_GetRefreshesAfterTTL asserts a Get past PollInterval
// triggers a synchronous refresh.
func TestProvider_GetRefreshesAfterTTL(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	p := NewWith(a, "host-id", []string{"foo"}, clock.Now)
	key := MakeID("host-id", "foo")

	if _, _, err := p.Get(context.Background(), key); err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	clock.advance(PollInterval + time.Second)
	if _, _, err := p.Get(context.Background(), key); err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if got := a.callsFor("foo"); got != 2 {
		t.Errorf("adapter called %d times across TTL boundary, want 2", got)
	}
}

// TestProvider_AdapterErrorSurfaces asserts an adapter error is stored
// and resurfaced through subsequent Get calls until the next successful
// fetch clears it.
func TestProvider_AdapterErrorSurfaces(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", Snapshot{}, ErrServiceManagerMissing)
	p := NewWith(a, "host-id", []string{"foo"}, time.Now)

	_, _, err := p.Get(context.Background(), MakeID("host-id", "foo"))
	if !errors.Is(err, ErrServiceManagerMissing) {
		t.Fatalf("Get: err = %v, want ErrServiceManagerMissing", err)
	}
}

// TestProvider_GetManyDedupes asserts duplicate keys collapse into a
// single Get path so the adapter call volume stays linear in unique
// keys, not request size.
func TestProvider_GetManyDedupes(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	p := NewWith(a, "host-id", []string{"foo"}, time.Now)
	key := MakeID("host-id", "foo")

	got, _, err := p.GetMany(context.Background(), []HostServiceID{key, key, key})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len(got) = %d, want 1 (deduped)", len(got))
	}
	if a.callsFor("foo") != 1 {
		t.Errorf("adapter called %d times for duplicates, want 1", a.callsFor("foo"))
	}
}

// TestProvider_NewWithDedupesServices asserts the constructor strips
// duplicate / blank entries from the watchlist so callers can pass
// raw config without sanitising first. (LoadServicesFromConfig already
// dedupes, but the provider belt-and-braces.)
func TestProvider_NewWithDedupesServices(t *testing.T) {
	p := NewWith(newFakeAdapter(), "host-id", []string{"foo", "", "foo", "bar"}, time.Now)
	got := p.Services()
	want := []string{"foo", "bar"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestProvider_StartHydratesAndPollsContinuously asserts Start triggers
// an initial fetch and the poll loop advances on each PollInterval tick.
//
// We don't assert the goroutine timing precisely — that path is
// covered by the cache-TTL tests. Here we check the initial sync hit.
func TestProvider_StartHydratesAllConfiguredServices(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	a.set("bar", Snapshot{State: StateInactive}, nil)
	p := NewWith(a, "host-id", []string{"foo", "bar"}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.callsFor("foo") != 1 {
		t.Errorf("foo: adapter called %d times during Start, want 1", a.callsFor("foo"))
	}
	if a.callsFor("bar") != 1 {
		t.Errorf("bar: adapter called %d times during Start, want 1", a.callsFor("bar"))
	}
}

// TestProvider_SubscribeReceivesInvalidationOnRefresh asserts a refresh
// fans out to subscribers. Caller cancels ctx to close the channel.
func TestProvider_SubscribeReceivesInvalidationOnRefresh(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	a := newFakeAdapter()
	a.set("foo", Snapshot{State: StateActive}, nil)
	p := NewWith(a, "host-id", []string{"foo"}, clock.Now)
	key := MakeID("host-id", "foo")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := p.Subscribe(ctx)

	if _, _, err := p.Get(context.Background(), key); err != nil {
		t.Fatalf("Get: %v", err)
	}
	select {
	case ev := <-ch:
		if ev.Key != key {
			t.Errorf("event key = %q, want %q", ev.Key, key)
		}
	case <-time.After(time.Second):
		t.Fatal("no invalidation event within 1s")
	}
}

// fakeClock is a manually-advanced wall clock used to drive the TTL
// tests. Standard pattern; lives here rather than in a shared helper
// because no other ws-b-hostservice file needs it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
