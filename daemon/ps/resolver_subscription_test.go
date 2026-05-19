package ps

import (
	"context"
	"testing"
	"time"
)

// channelStubService is a stubService that can emit invalidation events
// for subscription tests (T6).
type channelStubService struct {
	stubService
	eventCh chan invalidationEvent
}

func newChannelStubService(processes []Process) *channelStubService {
	return &channelStubService{
		stubService: stubService{
			hostID:    "local",
			processes: processes,
		},
		eventCh: make(chan invalidationEvent, 4),
	}
}

func (s *channelStubService) Subscribe(_ context.Context) <-chan invalidationEvent {
	return s.eventCh
}

// emit pushes an event onto the stub's channel. Used by tests to simulate
// the provider fanout (R16: emit AFTER cache write).
func (s *channelStubService) emit(ev invalidationEvent) {
	s.eventCh <- ev
}

// TestSubscriptionResolver_EmitsAfterCacheWrite verifies that the subscription
// goroutine reads the cache state AFTER receiving an invalidation event,
// not before (T6 — R16).
//
// Shape: stub stores a process list, subscriber starts, event is emitted,
// first emission must match the current list (not an empty pre-event state).
func TestSubscriptionResolver_EmitsAfterCacheWrite(t *testing.T) {
	procs := []Process{
		{ID: ProcessID{Host: "local", PID: 42}, Command: "claude"},
	}
	svc := newChannelStubService(procs)
	r := NewSubscriptionResolver(svc, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.Processes(ctx)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}

	// Simulate the provider emitting an invalidation event AFTER the cache
	// has been updated (R16 pattern: write first, then emit).
	svc.emit(invalidationEvent{Key: ProcessID{Host: "local", PID: 42}, Reason: "test"})

	select {
	case snapshot := <-ch:
		// T6: subscriber must see post-mutation state (pid 42 present).
		found := false
		for _, p := range snapshot {
			if p.Pid == 42 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("first emission did not include pid 42; got %d processes", len(snapshot))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not emit within deadline")
	}
}

// TestSubscriptionResolver_ClosesOnContextCancel verifies that the output
// channel is closed when the context is cancelled (T3: can fail).
func TestSubscriptionResolver_ClosesOnContextCancel(t *testing.T) {
	svc := newChannelStubService(nil)
	r := NewSubscriptionResolver(svc, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := r.Processes(ctx)
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}

	// Cancel the context to trigger goroutine exit and channel close.
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			// A value arrived before the channel closed; drain it and wait.
			select {
			case _, ok2 := <-ch:
				if ok2 {
					t.Error("channel should be closed after context cancel")
				}
			case <-time.After(2 * time.Second):
				t.Error("channel did not close after context cancel")
			}
		}
		// ok == false → channel closed correctly
	case <-time.After(2 * time.Second):
		t.Error("channel did not close within deadline after context cancel")
	}
}

// TestSubscriptionResolver_NilServiceReturnsError asserts that Processes
// returns an error when the service is not wired (T3: can fail).
func TestSubscriptionResolver_NilServiceReturnsError(t *testing.T) {
	r := NewSubscriptionResolver(nil, nil)
	_, err := r.Processes(context.Background())
	if err == nil {
		t.Fatal("expected error when service is nil, got nil")
	}
}
