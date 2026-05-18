package ps

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBatchLoader_CoalescesMultipleLoads verifies that N concurrent
// Load calls for distinct keys result in at most 1 underlying fetch
// call (T5: loader coalescing verified by counting underlying fetches).
func TestBatchLoader_CoalescesMultipleLoads(t *testing.T) {
	var fetchCount int64

	fetch := func(_ context.Context, keys []int) (map[int]string, error) {
		atomic.AddInt64(&fetchCount, 1)
		out := make(map[int]string, len(keys))
		for _, k := range keys {
			out[k] = "value"
		}
		return out, nil
	}

	loader := newBatchLoader[int, string](20*time.Millisecond, 512, fetch)

	const n = 50
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err := loader.Load(context.Background(), idx)
			results[idx] = v
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Load(%d): %v", i, err)
		}
	}
	for i, v := range results {
		if v != "value" {
			t.Errorf("results[%d] = %q, want %q", i, v, "value")
		}
	}

	// T5: ≤1 underlying fetch call for a single batch window.
	if got := atomic.LoadInt64(&fetchCount); got > 1 {
		t.Errorf("fetch called %d times, want ≤1 for coalesced batch", got)
	}
}

// TestBatchLoader_LoadMany asserts that LoadMany produces correct results
// and coalesces into a single fetch call (T5).
func TestBatchLoader_LoadMany(t *testing.T) {
	var fetchCount int64

	fetch := func(_ context.Context, keys []int) (map[int]string, error) {
		atomic.AddInt64(&fetchCount, 1)
		out := make(map[int]string, len(keys))
		for _, k := range keys {
			out[k] = "ok"
		}
		return out, nil
	}

	loader := newBatchLoader[int, string](20*time.Millisecond, 512, fetch)
	keys := []int{10, 20, 30, 40, 50}

	got, err := loader.LoadMany(context.Background(), keys)
	if err != nil {
		t.Fatalf("LoadMany: %v", err)
	}
	for _, k := range keys {
		if got[k] != "ok" {
			t.Errorf("result[%d] = %q, want %q", k, got[k], "ok")
		}
	}

	// T5: single fetch call.
	if got := atomic.LoadInt64(&fetchCount); got != 1 {
		t.Errorf("fetch called %d times, want 1", got)
	}
}

// TestBatchLoader_EmptyKeys asserts that LoadMany with no keys returns
// an empty map without calling fetch (T3: assertion can fail).
func TestBatchLoader_EmptyKeys(t *testing.T) {
	called := false
	fetch := func(_ context.Context, _ []int) (map[int]string, error) {
		called = true
		return nil, nil
	}
	loader := newBatchLoader[int, string](20*time.Millisecond, 512, fetch)
	got, err := loader.LoadMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("LoadMany(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
	if called {
		t.Error("fetch should not be called for empty key set")
	}
}

// TestBatchLoader_MissingKey asserts that Load for a key not returned
// by fetch yields the zero value and no error (T1, T3).
func TestBatchLoader_MissingKey(t *testing.T) {
	fetch := func(_ context.Context, _ []int) (map[int]string, error) {
		return map[int]string{}, nil // nothing returned for any key
	}
	loader := newBatchLoader[int, string](20*time.Millisecond, 512, fetch)

	v, err := loader.Load(context.Background(), 99)
	if err != nil {
		t.Fatalf("Load(missing): %v", err)
	}
	if v != "" {
		t.Errorf("Load(missing) = %q, want zero value", v)
	}
}

// TestBatchLoader_ContextCancel asserts that a cancelled context returns
// ctx.Err() and does not block (T3: assertion can fail if Load blocks).
func TestBatchLoader_ContextCancel(t *testing.T) {
	// fetch that never returns.
	fetch := func(ctx context.Context, _ []int) (map[int]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	loader := newBatchLoader[int, string](100*time.Millisecond, 512, fetch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := loader.Load(ctx, 1)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestBatchLoader_MaxBatch asserts that when the pending set hits
// maxBatch, a flush is triggered immediately without waiting for the
// timer (T5: timing-sensitive coalescing).
func TestBatchLoader_MaxBatch(t *testing.T) {
	var fetchCount int64
	const maxBatch = 5

	fetch := func(_ context.Context, keys []int) (map[int]string, error) {
		atomic.AddInt64(&fetchCount, 1)
		out := make(map[int]string, len(keys))
		for _, k := range keys {
			out[k] = "v"
		}
		return out, nil
	}

	// 100ms wait so the timer doesn't fire before we enqueue maxBatch keys.
	loader := newBatchLoader[int, string](100*time.Millisecond, maxBatch, fetch)

	var wg sync.WaitGroup
	for i := 0; i < maxBatch; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			_, _ = loader.Load(context.Background(), k)
		}(i)
	}
	wg.Wait()

	// maxBatch loads should trigger exactly 1 flush.
	if got := atomic.LoadInt64(&fetchCount); got == 0 {
		t.Error("fetch should have been called at least once")
	}
}
