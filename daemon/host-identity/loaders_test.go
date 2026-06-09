package hostidentity_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	graphql "github.com/drewdrewthis/orchardist/internal/server/graphql"

	hostidentity "github.com/drewdrewthis/orchardist/daemon/host-identity"
)

// countingService wraps a stubService and records how many times Host() is called.
// Used by T5 to assert the loader coalesces N parallel calls into ≤1 provider fetch.
type countingService struct {
	inner hostidentity.Service
	calls atomic.Int64
}

func (c *countingService) LocalID() hostidentity.HostID { return c.inner.LocalID() }

func (c *countingService) Host(ctx context.Context, key hostidentity.HostID) (*graphql.Host, error) {
	c.calls.Add(1)
	return c.inner.Host(ctx, key)
}

func (c *countingService) Hosts(ctx context.Context) ([]*graphql.Host, error) {
	return c.inner.Hosts(ctx)
}

// TestLoader_HostByID_Coalesces verifies T5: N parallel Load(key) calls against
// the HostByID loader result in ≤1 underlying service.Host() call per request.
//
// The DataLoader batches all keys presented within its 1ms wait window into a
// single batch invocation. A batch counter tracks how many times our batchFn
// fires — one batch per loader request.
func TestLoader_HostByID_Coalesces(t *testing.T) {
	const N = 10

	stub := newStubService("ABCD-1234", "test-host.local", "darwin")
	counted := &countingService{inner: stub}
	loaders := hostidentity.NewLoaders(counted)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Issue N parallel Load calls for the same key within one wait window.
	thunks := make([]func() (*graphql.Host, error), N)
	for i := 0; i < N; i++ {
		thunks[i] = loaders.HostByID.Load(ctx, string(stub.LocalID()))
	}

	var wg sync.WaitGroup
	for _, thunk := range thunks {
		wg.Add(1)
		go func(fn func() (*graphql.Host, error)) {
			defer wg.Done()
			h, err := fn()
			if err != nil {
				t.Errorf("Load(): %v", err)
				return
			}
			if h == nil {
				t.Error("Load() returned nil host")
			}
		}(thunk)
	}
	wg.Wait()

	// The batch function should have fired exactly once for N coalesced keys.
	// Per T5: assert ≤1 batch call per request for duplicate keys.
	batches := loaders.BatchCount()
	if batches != 1 {
		t.Errorf("batch function fired %d times for %d parallel loads of the same key, want 1", batches, N)
	}
}

// TestLoader_HostByID_Returns asserts each thunk resolves to a non-nil Host
// with the correct ID field.
func TestLoader_HostByID_Returns(t *testing.T) {
	stub := newStubService("ABCD-1234", "test-host.local", "darwin")
	loaders := hostidentity.NewLoaders(stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thunk := loaders.HostByID.Load(ctx, "ABCD-1234")
	h, err := thunk()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if h == nil {
		t.Fatal("Load() returned nil host")
	}
	if h.ID != "Host:ABCD-1234" {
		t.Errorf("Host.id = %q, want Host:ABCD-1234", h.ID)
	}
}

// TestLoader_HostByID_UnknownKey asserts that an unknown key returns a stub
// Host (not nil) so downstream field resolvers don't nil-panic.
func TestLoader_HostByID_UnknownKey(t *testing.T) {
	stub := newStubService("ABCD-1234", "test-host.local", "darwin")
	loaders := hostidentity.NewLoaders(stub)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thunk := loaders.HostByID.Load(ctx, "UNKNOWN-KEY")
	h, err := thunk()
	if err != nil {
		t.Fatalf("Load() returned error for unknown key: %v", err)
	}
	// Unknown key should return a stub (not nil) with the key as ID.
	if h == nil {
		t.Fatal("Load() returned nil for unknown key, want stub Host")
	}
	if h.ID != "UNKNOWN-KEY" {
		t.Errorf("stub Host.id = %q, want UNKNOWN-KEY", h.ID)
	}
}
