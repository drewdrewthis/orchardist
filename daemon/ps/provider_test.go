package ps

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestProvider_StartHydratesCache spins up the provider against the real
// `ps` binary and confirms the cache is non-empty after Start.
func TestProvider_StartHydratesCache(t *testing.T) {
	a := NewAdapter("local").WithPollInterval(200 * time.Millisecond)
	p := NewProvider(a, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	procs := p.List()
	if len(procs) == 0 {
		t.Fatal("cache empty after Start; expected at least the test process")
	}

	self := ProcessID{Host: "local", PID: os.Getpid()}
	v, ok := p.Get(self)
	if !ok {
		t.Fatalf("Get(self pid %d) not found", os.Getpid())
	}
	if v.ID != self {
		t.Errorf("Get(self).ID = %v, want %v", v.ID, self)
	}
}

// TestProvider_SubscribeReceivesInvalidations proves the subscription
// fanout fires after a Refresh that introduces new entries.
func TestProvider_SubscribeReceivesInvalidations(t *testing.T) {
	a := NewAdapter("local").WithPollInterval(time.Hour) // no background ticks
	p := NewProvider(a, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	ch := p.Subscribe(subCtx)

	// Clear the cache so Refresh sees everything as new.
	p.replaceAll(map[ProcessID]Process{})

	if err := p.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				t.Fatal("subscription channel closed unexpectedly")
			}
			return // received at least one invalidation — success
		case <-deadline:
			t.Fatal("subscriber received no invalidation events after Refresh")
		}
	}
}

// TestProvider_HostID returns the adapter's host id.
func TestProvider_HostID(t *testing.T) {
	a := NewAdapter("myhost")
	p := NewProvider(a, nil)
	if got := p.HostID(); got != "myhost" {
		t.Errorf("HostID = %q, want %q", got, "myhost")
	}
}
