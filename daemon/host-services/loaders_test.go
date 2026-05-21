package hostservices

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// countingService wraps a ServiceReader and counts how many times
// Snapshots is called. Used to verify loader coalescing (T5).
type countingService struct {
	mu             sync.Mutex
	snaps          []HostServiceSnapshot
	snapshotsCalls atomic.Int64
}

func newCountingServiceFromSnaps(snaps []HostServiceSnapshot) *countingService {
	return &countingService{snaps: snaps}
}

func (c *countingService) Snapshots(_ context.Context) ([]HostServiceSnapshot, error) {
	c.snapshotsCalls.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]HostServiceSnapshot, len(c.snaps))
	copy(out, c.snaps)
	return out, nil
}

func (c *countingService) ByID(_ context.Context, id HostServiceID) (HostServiceSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.snaps {
		if MakeID(s.MachineID, s.Name) == id {
			return s, nil
		}
	}
	return HostServiceSnapshot{}, nil
}

func (c *countingService) ByMachineID(_ context.Context, machineID string) ([]HostServiceSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []HostServiceSnapshot
	for _, s := range c.snaps {
		if s.MachineID == machineID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *countingService) Subscribe(_ context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent)
	close(ch)
	return ch
}

func (c *countingService) MachineID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snaps) > 0 {
		return c.snaps[0].MachineID
	}
	return ""
}

// TestLoaderByID_CoalescesParallelLoads asserts N parallel Load(sameKey)
// calls result in exactly 1 underlying Snapshots call (T5).
func TestLoaderByID_CoalescesParallelLoads(t *testing.T) {
	id := MakeID("host-1", "foo")
	cs := newCountingServiceFromSnaps([]HostServiceSnapshot{
		{MachineID: "host-1", Name: "foo", State: StateActive},
	})

	loader := NewLoaderByID(cs)
	ctx := context.Background()

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := loader.Load(ctx, id); err != nil {
				t.Errorf("Load: %v", err)
			}
		}()
	}
	wg.Wait()

	// once.Do ensures exactly 1 Snapshots call regardless of concurrency.
	if got := cs.snapshotsCalls.Load(); got != 1 {
		t.Errorf("Snapshots called %d times for %d parallel loads; want 1 (coalesced)", got, n)
	}
}

// TestLoaderByID_ReturnsCorrectSnap asserts the loader returns the right
// snapshot for a given ID.
func TestLoaderByID_ReturnsCorrectSnap(t *testing.T) {
	id := MakeID("host-1", "bar")
	cs := newCountingServiceFromSnaps([]HostServiceSnapshot{
		{MachineID: "host-1", Name: "bar", State: StateInactive},
	})

	loader := NewLoaderByID(cs)
	snap, err := loader.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.State != StateInactive {
		t.Errorf("State = %q, want inactive", snap.State)
	}
}

// TestLoaderByMachineID_CoalescesParallelLoads asserts N parallel
// Load(sameMachineID) calls result in exactly 1 underlying Snapshots call
// (T5).
func TestLoaderByMachineID_CoalescesParallelLoads(t *testing.T) {
	mid := "host-42"
	cs := newCountingServiceFromSnaps([]HostServiceSnapshot{
		{MachineID: mid, Name: "svc1", State: StateActive},
		{MachineID: mid, Name: "svc2", State: StateInactive},
	})

	loader := NewLoaderByMachineID(cs)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := loader.Load(ctx, mid); err != nil {
				t.Errorf("Load: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := cs.snapshotsCalls.Load(); got != 1 {
		t.Errorf("Snapshots called %d times for %d parallel loads; want 1 (coalesced)", got, n)
	}
}

// TestLoaderByMachineID_ReturnsCorrectSnaps asserts results are indexed
// correctly by machineID.
func TestLoaderByMachineID_ReturnsCorrectSnaps(t *testing.T) {
	mid := "machine-A"
	cs := newCountingServiceFromSnaps([]HostServiceSnapshot{
		{MachineID: mid, Name: "alpha", State: StateActive},
		{MachineID: "machine-B", Name: "beta", State: StateInactive},
	})

	loader := NewLoaderByMachineID(cs)
	snaps, err := loader.Load(context.Background(), mid)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("got %d snaps for machine-A, want 1", len(snaps))
	}
	if snaps[0].Name != "alpha" {
		t.Errorf("snap name = %q, want alpha", snaps[0].Name)
	}
}

// TestLoaderByMachineID_EmptyForUnknownMachine asserts unknown machine
// returns empty slice rather than error.
func TestLoaderByMachineID_EmptyForUnknownMachine(t *testing.T) {
	cs := newCountingServiceFromSnaps([]HostServiceSnapshot{
		{MachineID: "m1", Name: "svc", State: StateActive},
	})

	loader := NewLoaderByMachineID(cs)
	snaps, err := loader.Load(context.Background(), "no-such-machine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("got %d snaps, want 0", len(snaps))
	}
}
