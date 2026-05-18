package contracts

import (
	"context"
	"sync"
	"time"
)

// ContractByIDLoader batches and deduplicates per-request Contract lookups.
// Implements R3 (DataLoader-shaped reads) and O1 (stable key axis: ByID).
//
// The loader collects all Load calls that arrive before the first caller
// blocks on its result channel, then issues a single [ContractsService.GetMany]
// for the full batch. This is the standard Go DataLoader pattern (T5).
//
// Thread-safety: a single loader instance may be used from multiple goroutines
// concurrently; each batch object is owned by exactly one flush goroutine.
type ContractByIDLoader struct {
	svc ContractsService

	mu    sync.Mutex
	batch *idBatch
}

// idBatch holds one in-flight batch of Load requests.
type idBatch struct {
	mu      sync.Mutex
	keys    []ContractID            // deduplicated, in arrival order
	waiters map[ContractID][]chan loadResult
	// once ensures exactly one flush goroutine reads this batch.
	once    sync.Once
}

type loadResult struct {
	c   *Contract
	err error
}

func newIDBatch() *idBatch {
	return &idBatch{
		waiters: make(map[ContractID][]chan loadResult),
	}
}

// NewContractByIDLoader constructs a ContractByIDLoader backed by svc.
// One loader instance is created per GraphQL request context.
func NewContractByIDLoader(svc ContractsService) *ContractByIDLoader {
	return &ContractByIDLoader{svc: svc}
}

// Load returns a Contract by id. Multiple concurrent calls within one batch
// window coalesce to a single [ContractsService.GetMany] call (T5).
//
// Callers block until the batch flush completes or ctx is cancelled.
func (l *ContractByIDLoader) Load(ctx context.Context, id ContractID) (*Contract, error) {
	ch := make(chan loadResult, 1)

	l.mu.Lock()
	if l.batch == nil {
		l.batch = newIDBatch()
	}
	b := l.batch
	l.mu.Unlock()

	b.mu.Lock()
	// Deduplicate keys.
	found := false
	for _, k := range b.keys {
		if k == id {
			found = true
			break
		}
	}
	if !found {
		b.keys = append(b.keys, id)
	}
	b.waiters[id] = append(b.waiters[id], ch)
	b.mu.Unlock()

	// Trigger flush on the first registered waiter. All subsequent Load calls
	// that land before the flush goroutine executes will coalesce into this
	// batch because they share the same *idBatch pointer.
	b.once.Do(func() {
		go l.flushBatch(ctx, b)
	})

	select {
	case res := <-ch:
		return res.c, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// flushBatch drains the batch, calls GetMany once, and delivers results to
// every waiter. It resets the loader's batch pointer first so new Load calls
// start a fresh batch.
func (l *ContractByIDLoader) flushBatch(ctx context.Context, b *idBatch) {
	// Yield the goroutine once so that other Load goroutines that have
	// already been started (but not yet scheduled) can register their keys
	// before we snapshot the batch. This is the "batch window."
	gosched()

	// Reset the loader's batch pointer so subsequent Load calls begin a new
	// batch. We do this before GetMany so the new batch can accumulate while
	// the current GetMany is in flight.
	l.mu.Lock()
	if l.batch == b {
		l.batch = nil
	}
	l.mu.Unlock()

	b.mu.Lock()
	keys := append([]ContractID(nil), b.keys...)
	b.mu.Unlock()

	results, err := l.svc.GetMany(ctx, keys)

	b.mu.Lock()
	defer b.mu.Unlock()
	for id, waiters := range b.waiters {
		var res loadResult
		if err != nil {
			res.err = err
		} else {
			res.c = results[id]
		}
		for _, ch := range waiters {
			ch <- res
		}
	}
}

// defaultGosched sleeps 1ms — a conservative batch window that lets goroutines
// started in the same sweep register their Load keys before the flush reads.
func defaultGosched() { time.Sleep(1 * time.Millisecond) }

// gosched is the batch-window function. Extracted as a variable so tests can
// inject a synchronisation barrier for deterministic coalescing assertions (T5).
var gosched func() = defaultGosched

// ContractsByOwnerLoader batches per-request Contract lookups by ownerSessionID.
// Implements R3 (DataLoader-shaped reads) and O1 (stable key axis: ByOwner).
type ContractsByOwnerLoader struct {
	svc ContractsService

	mu    sync.Mutex
	batch *ownerBatch
}

type ownerBatch struct {
	mu      sync.Mutex
	keys    []string
	waiters map[string][]chan ownerLoadResult
	once    sync.Once
}

type ownerLoadResult struct {
	contracts []*Contract
	err       error
}

func newOwnerBatch() *ownerBatch {
	return &ownerBatch{
		waiters: make(map[string][]chan ownerLoadResult),
	}
}

// NewContractsByOwnerLoader constructs a ContractsByOwnerLoader backed by svc.
func NewContractsByOwnerLoader(svc ContractsService) *ContractsByOwnerLoader {
	return &ContractsByOwnerLoader{svc: svc}
}

// Load returns all Contracts owned by the given session id. Multiple concurrent
// calls for the same ownerSessionID coalesce to a single service call.
func (l *ContractsByOwnerLoader) Load(ctx context.Context, ownerSessionID string) ([]*Contract, error) {
	ch := make(chan ownerLoadResult, 1)

	l.mu.Lock()
	if l.batch == nil {
		l.batch = newOwnerBatch()
	}
	b := l.batch
	l.mu.Unlock()

	b.mu.Lock()
	found := false
	for _, k := range b.keys {
		if k == ownerSessionID {
			found = true
			break
		}
	}
	if !found {
		b.keys = append(b.keys, ownerSessionID)
	}
	b.waiters[ownerSessionID] = append(b.waiters[ownerSessionID], ch)
	b.mu.Unlock()

	b.once.Do(func() {
		go l.flushBatch(ctx, b)
	})

	select {
	case res := <-ch:
		return res.contracts, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *ContractsByOwnerLoader) flushBatch(ctx context.Context, b *ownerBatch) {
	gosched()

	l.mu.Lock()
	if l.batch == b {
		l.batch = nil
	}
	l.mu.Unlock()

	b.mu.Lock()
	keys := append([]string(nil), b.keys...)
	b.mu.Unlock()

	for _, ownerSessionID := range keys {
		sid := ownerSessionID
		filter := &ContractFilter{OwnerSessionID: &sid}
		contracts, err := l.svc.List(ctx, filter)
		res := ownerLoadResult{contracts: contracts, err: err}
		b.mu.Lock()
		for _, ch := range b.waiters[ownerSessionID] {
			ch <- res
		}
		b.mu.Unlock()
	}
}
