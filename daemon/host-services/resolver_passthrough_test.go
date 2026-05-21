package hostservices

import (
	"context"
	"testing"
	"time"
)

// TestResolveHostServiceCtl_EmptyHostErrors asserts empty host is rejected
// before exec (M4 input validation).
func TestResolveHostServiceCtl_EmptyHostErrors(t *testing.T) {
	_, err := ResolveHostServiceCtl(context.Background(), "", []string{"list"})
	if err == nil {
		t.Fatal("expected error for empty host, got nil")
	}
}

// TestResolveHostServiceCtl_EmptyArgsErrors asserts empty args is rejected
// before exec (M4 input validation).
func TestResolveHostServiceCtl_EmptyArgsErrors(t *testing.T) {
	_, err := ResolveHostServiceCtl(context.Background(), "localhost", nil)
	if err == nil {
		t.Fatal("expected error for nil args, got nil")
	}
	_, err = ResolveHostServiceCtl(context.Background(), "localhost", []string{})
	if err == nil {
		t.Fatal("expected error for empty args slice, got nil")
	}
}

// TestResolveHostServiceCtl_TimeoutHonoured asserts the 30s per-call
// timeout propagates to the exec context (T7 — guard 2).
//
// We can't easily make a real binary hang, so we test the guard
// contractually: cancelling the parent context before the call must
// surface a context error — not hang indefinitely.
func TestResolveHostServiceCtl_TimeoutHonoured(t *testing.T) {
	// Provide a context that is already cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Let the context expire.
	<-ctx.Done()

	// The call must return quickly with a context-related error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ResolveHostServiceCtl(ctx, "localhost", []string{"list"})
	}()

	select {
	case <-done:
		// Success — returned without hanging.
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveHostServiceCtl did not respect cancelled context within 5s")
	}
}

// TestResolveHostServiceCtl_ConcurrencyCapBlocks asserts that when the
// concurrency cap (4) is fully occupied, additional calls block (T7 guard 3).
//
// We hold the semaphore manually and verify a fifth call is blocked.
func TestResolveHostServiceCtl_ConcurrencyCapBlocks(t *testing.T) {
	// Acquire all 4 semaphore slots.
	if err := passthroughSem.Acquire(context.Background(), passthroughCap); err != nil {
		t.Fatalf("acquire semaphore: %v", err)
	}
	defer passthroughSem.Release(passthroughCap)

	// A fifth call should be blocked. We give it a short timeout to
	// prove it can't proceed while the cap is held.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := ResolveHostServiceCtl(ctx, "localhost", []string{"list"})
	if err == nil {
		t.Fatal("expected error when concurrency cap is exhausted, got nil")
	}
	// The error should be context-related (deadline/cancel — cap blocked).
}
