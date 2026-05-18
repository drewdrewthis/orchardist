package ps

import (
	"context"
	"sync"
	"time"
)

// batchLoader coalesces concurrent single-key Loads into a single
// multi-key fetch. Mirrors the DataLoader pattern (R3, O10).
//
// Two flush triggers:
//  1. The pending set reaches maxBatch.
//  2. wait elapses since the first Load in the current batch.
//
// T5: loader coalescing is verified by counting underlying fetches in
// loaders_test.go.
type batchLoader[K comparable, V any] struct {
	wait     time.Duration
	maxBatch int
	fetch    func(ctx context.Context, keys []K) (map[K]V, error)

	mu      sync.Mutex
	pending map[K][]chan loadResult[V]
	timer   *time.Timer
}

type loadResult[V any] struct {
	v   V
	err error
}

func newBatchLoader[K comparable, V any](
	wait time.Duration,
	maxBatch int,
	fetch func(ctx context.Context, keys []K) (map[K]V, error),
) *batchLoader[K, V] {
	return &batchLoader[K, V]{
		wait:     wait,
		maxBatch: maxBatch,
		fetch:    fetch,
		pending:  make(map[K][]chan loadResult[V]),
	}
}

// Load enqueues one key and returns its value once the next batch
// completes. Context cancellation returns ctx.Err().
func (l *batchLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	ch := make(chan loadResult[V], 1)
	l.enqueue(ctx, key, ch)
	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case r := <-ch:
		return r.v, r.err
	}
}

// LoadMany enqueues every key and waits for all results.
func (l *batchLoader[K, V]) LoadMany(ctx context.Context, keys []K) (map[K]V, error) {
	if len(keys) == 0 {
		return map[K]V{}, nil
	}
	results := make(map[K]V, len(keys))
	chans := make(map[K]chan loadResult[V], len(keys))
	for _, k := range keys {
		ch := make(chan loadResult[V], 1)
		chans[k] = ch
		l.enqueue(ctx, k, ch)
	}
	for k, ch := range chans {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case r := <-ch:
			if r.err != nil {
				return results, r.err
			}
			results[k] = r.v
		}
	}
	return results, nil
}

// enqueue adds a (key, waiter) pair and arms the flush timer.
func (l *batchLoader[K, V]) enqueue(ctx context.Context, key K, ch chan loadResult[V]) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending[key] = append(l.pending[key], ch)
	if len(l.pending) >= l.maxBatch {
		l.flushLocked(ctx)
		return
	}
	if l.timer == nil {
		l.timer = time.AfterFunc(l.wait, func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.flushLocked(ctx)
		})
	}
}

// flushLocked runs one batch under l.mu. Releases the lock before the
// fetcher runs so concurrent Loads can start the next batch.
func (l *batchLoader[K, V]) flushLocked(ctx context.Context) {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if len(l.pending) == 0 {
		return
	}
	batch := l.pending
	l.pending = make(map[K][]chan loadResult[V])

	keys := make([]K, 0, len(batch))
	for k := range batch {
		keys = append(keys, k)
	}

	go func() {
		results, err := l.fetch(ctx, keys)
		for k, waiters := range batch {
			for _, w := range waiters {
				if err != nil {
					w <- loadResult[V]{err: err}
					continue
				}
				v, ok := results[k]
				if !ok {
					var zero V
					w <- loadResult[V]{v: zero}
					continue
				}
				w <- loadResult[V]{v: v}
			}
		}
	}()
}
