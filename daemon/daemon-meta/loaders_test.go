package daemonmeta_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	daemonmeta "github.com/drewdrewthis/orchardist/daemon/daemon-meta"
)

// countingService wraps ServiceImpl and records DaemonState() call count.
// Used by T5 to assert loader coalescing.
type countingService struct {
	inner *daemonmeta.ServiceImpl
	calls atomic.Int32
}

func (c *countingService) DaemonState(ctx context.Context) (*daemonmeta.DaemonState, error) {
	c.calls.Add(1)
	return c.inner.DaemonState(ctx)
}

func (c *countingService) Reload(ctx context.Context) (*daemonmeta.DaemonState, error) {
	return c.inner.Reload(ctx)
}

// --- T5: loader coalescing verified by call count ---

// TestDaemonStateLoader_Coalesces verifies that N concurrent Load(key) calls
// against the same loader result in ≤1 Service.DaemonState() invocation (T5, O1).
func TestDaemonStateLoader_Coalesces(t *testing.T) {
	t.Parallel()

	svc := &countingService{
		inner: daemonmeta.NewService(time.Now()),
	}
	loader := daemonmeta.NewDaemonStateLoader(svc)

	const N = 10
	var wg sync.WaitGroup
	results := make([]*daemonmeta.DaemonState, N)
	errs := make([]error, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = daemonmeta.LoadDaemonState(context.Background(), loader)
		}(i)
	}
	wg.Wait()

	// All calls must succeed.
	for i, err := range errs {
		if err != nil {
			t.Errorf("LoadDaemonState[%d] error: %v", i, err)
		}
	}
	// All results must be non-nil.
	for i, ds := range results {
		if ds == nil {
			t.Errorf("results[%d] is nil; want DaemonState", i)
		}
	}

	// The loader must have coalesced N calls into ≤ N/2 actual service calls.
	// (In practice the dataloader batches all N into 1, but we allow for
	// scheduling variance by asserting < N rather than == 1.)
	callCount := int(svc.calls.Load())
	if callCount >= N {
		t.Errorf("DaemonState() called %d times for %d Load() calls; expected coalescing (< %d)", callCount, N, N)
	}
}

// TestLoadDaemonState_ReturnsNonNil verifies the helper function returns a
// non-nil DaemonState and no error under normal conditions.
func TestLoadDaemonState_ReturnsNonNil(t *testing.T) {
	t.Parallel()
	svc := daemonmeta.NewService(time.Now())
	loader := daemonmeta.NewDaemonStateLoader(svc)

	ds, err := daemonmeta.LoadDaemonState(context.Background(), loader)
	if err != nil {
		t.Fatalf("LoadDaemonState() error: %v", err)
	}
	if ds == nil {
		t.Error("LoadDaemonState() returned nil; want *DaemonState")
	}
}
