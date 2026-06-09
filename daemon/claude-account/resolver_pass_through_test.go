package claudeaccount_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	claudeaccount "github.com/drewdrewthis/orchardist/daemon/claude-account"
)

// stallingRunner blocks until unblocked or ctx expires.
// Used to test pass-through timeout and concurrency cap.
type stallingRunner struct {
	mu       sync.Mutex
	unblock  chan struct{}
	callCount atomic.Int64
}

func newStallingRunner() *stallingRunner {
	return &stallingRunner{unblock: make(chan struct{})}
}

func (s *stallingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	s.callCount.Add(1)
	select {
	case <-s.unblock:
		return []byte(`{"email":"a@b.com"}`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestPassThrough_Timeout_HonorsDeadline asserts the pass-through returns
// within the deadline when the underlying shellout stalls.
//
// The pass-through encodes errors into the result JSON envelope rather than
// returning a Go-level error, so we assert: (a) Invoke returns within 2×timeout
// and (b) the result's "error" field is non-empty.
//
// T7 guard: honors the per-call timeout.
func TestPassThrough_Timeout_HonorsDeadline(t *testing.T) {
	staller := newStallingRunner()
	adapter := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(staller)
	guards := claudeaccount.NewPassThroughGuardsForTest(adapter, 50*time.Millisecond, 4)

	ctx := context.Background()
	start := time.Now()
	result, err := guards.Invoke(ctx, "claude", []string{"auth", "status"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Invoke returned unexpected Go error: %v", err)
	}
	// Should have timed out near the 50ms mark, not hung indefinitely.
	if elapsed > 2*time.Second {
		t.Errorf("Invoke took %v, want ≤2s (timeout not honored)", elapsed)
	}
	// Result must encode the timeout as an error in the JSON envelope.
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map[string]interface{}", result)
	}
	errField, _ := m["error"].(string)
	if errField == "" {
		t.Error("result.error is empty; want timeout error message in JSON envelope")
	}
}

// TestPassThrough_ConcurrencyCap_RejectsExcessCalls asserts that concurrent
// invocations beyond the cap return an error immediately without blocking.
// T7 guard: honors the concurrency cap.
func TestPassThrough_ConcurrencyCap_RejectsExcessCalls(t *testing.T) {
	staller := newStallingRunner()
	adapter := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(staller)
	// Cap of 2 with a long timeout so we can pile up callers.
	guards := claudeaccount.NewPassThroughGuardsForTest(adapter, 5*time.Second, 2)

	ctx := context.Background()

	// Launch 2 calls that will stall (occupying all slots).
	var once sync.Once
	started := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			once.Do(func() { close(started) })
			_, _ = guards.Invoke(ctx, "claude", []string{"--version"})
		}()
	}
	// Wait for at least one goroutine to start.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stalling goroutines didn't start")
	}
	// Sleep briefly to let both goroutines advance past the inFlight increment.
	time.Sleep(30 * time.Millisecond)

	// 3rd call should be rejected immediately.
	_, err := guards.Invoke(ctx, "claude", []string{"--version"})
	if err == nil {
		t.Error("3rd concurrent call succeeded; want concurrency-cap error")
	}

	// Unblock the stalled goroutines.
	close(staller.unblock)
	wg.Wait()
}

// TestPassThrough_UnknownTool_RejectsCall asserts the enum guard fires
// before the shellout.
func TestPassThrough_UnknownTool_RejectsCall(t *testing.T) {
	fr := newFakeRunner()
	adapter := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	guards := claudeaccount.NewPassThroughGuardsForTest(adapter, time.Second, 4)

	_, err := guards.Invoke(context.Background(), "rm", []string{"-rf", "/"})
	if err == nil {
		t.Fatal("unknown tool was not rejected")
	}
	if fr.Calls() > 0 {
		t.Error("shellout was invoked for unknown tool; want guard to fire first")
	}
}
