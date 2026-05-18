package hostservices

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeAdapter is the test double for the adapter interface. Keyed by
// service name; tests set up desired snapshots or errors.
type fakeAdapter struct {
	mu        sync.Mutex
	responses map[string]fakeReply
	calls     map[string]int
}

type fakeReply struct {
	snap HostServiceSnapshot
	err  error
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		responses: make(map[string]fakeReply),
		calls:     make(map[string]int),
	}
}

func (f *fakeAdapter) set(name string, snap HostServiceSnapshot, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[name] = fakeReply{snap: snap, err: err}
}

func (f *fakeAdapter) callsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fakeAdapter) fetchOne(_ context.Context, machineID, name string) (HostServiceSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
	r, ok := f.responses[name]
	if !ok {
		return HostServiceSnapshot{MachineID: machineID, Name: name, State: StateUnknown}, nil
	}
	if r.snap.MachineID == "" {
		r.snap.MachineID = machineID
	}
	if r.snap.Name == "" {
		r.snap.Name = name
	}
	return r.snap, r.err
}

// fakeClock is a manually-advanced wall clock for TTL tests.
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

// TestProvider_SnapshotsAfterStart asserts Start hydrates all configured
// services into the cache (T1 — every typed field has a resolver test).
func TestProvider_SnapshotsAfterStart(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", HostServiceSnapshot{State: StateActive}, nil)
	a.set("bar", HostServiceSnapshot{State: StateInactive}, nil)
	p := newProvider(a, "host-id", []string{"foo", "bar"}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	snaps := p.snapshots()
	if len(snaps) != 2 {
		t.Fatalf("snapshots len = %d, want 2", len(snaps))
	}
}

// TestProvider_ByIDReturnsSnapshot asserts byID returns the correct snapshot.
func TestProvider_ByIDReturnsSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := newFakeAdapter()
	a.set("foo", HostServiceSnapshot{State: StateActive}, nil)
	p := newProvider(a, "host-id", []string{"foo"}, clock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	id := MakeID("host-id", "foo")
	snap, err := p.byID(id)
	if err != nil {
		t.Fatalf("byID: %v", err)
	}
	if snap.State != StateActive {
		t.Errorf("State = %q, want active", snap.State)
	}
	if snap.MachineID != "host-id" || snap.Name != "foo" {
		t.Errorf("identity mismatch: machineID=%q name=%q", snap.MachineID, snap.Name)
	}
}

// TestProvider_ByIDUnknownKeyErrors asserts byID errors for unknown IDs.
func TestProvider_ByIDUnknownKeyErrors(t *testing.T) {
	p := newProvider(newFakeAdapter(), "host-id", []string{"foo"}, time.Now)
	_, err := p.byID(MakeID("host-id", "not-watched"))
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

// TestProvider_ByMachineID asserts byMachineID returns all matching
// snapshots.
func TestProvider_ByMachineID(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", HostServiceSnapshot{State: StateActive}, nil)
	a.set("bar", HostServiceSnapshot{State: StateInactive}, nil)
	p := newProvider(a, "machine-1", []string{"foo", "bar"}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	snaps := p.byMachineID("machine-1")
	if len(snaps) != 2 {
		t.Fatalf("byMachineID len = %d, want 2", len(snaps))
	}

	// Different machine returns empty.
	snaps2 := p.byMachineID("other-machine")
	if len(snaps2) != 0 {
		t.Errorf("byMachineID for other-machine = %d, want 0", len(snaps2))
	}
}

// TestProvider_AdapterErrorSurfaces asserts adapter errors surface through
// byID.
func TestProvider_AdapterErrorSurfaces(t *testing.T) {
	a := newFakeAdapter()
	a.set("foo", HostServiceSnapshot{}, ErrServiceManagerMissing)
	p := newProvider(a, "host-id", []string{"foo"}, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	_, err := p.byID(MakeID("host-id", "foo"))
	if !errors.Is(err, ErrServiceManagerMissing) {
		t.Fatalf("byID err = %v, want ErrServiceManagerMissing", err)
	}
}

// TestProvider_DedupesServices asserts duplicate and blank entries are
// stripped from the watchlist.
func TestProvider_DedupesServices(t *testing.T) {
	p := newProvider(newFakeAdapter(), "host-id", []string{"foo", "", "foo", "bar"}, time.Now)
	got := make([]string, len(p.services))
	copy(got, p.services)
	sort.Strings(got)
	want := []string{"bar", "foo"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("services = %v, want %v", got, want)
	}
}

// TestProvider_SubscribeReceivesEvent asserts subscription fan-out fires
// after a cache write (R16).
func TestProvider_SubscribeReceivesEvent(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	a := newFakeAdapter()
	a.set("foo", HostServiceSnapshot{State: StateActive}, nil)
	p := newProvider(a, "host-id", []string{"foo"}, clock.Now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := p.subscribe(ctx)

	// Start triggers refreshAll which emits to subscribers.
	if err := p.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Key != MakeID("host-id", "foo") {
			t.Errorf("event key = %q, want foo key", ev.Key)
		}
	case <-time.After(time.Second):
		t.Fatal("no invalidation event within 1s")
	}
}
