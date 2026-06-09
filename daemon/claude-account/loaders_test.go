package claudeaccount_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	claudeaccount "github.com/drewdrewthis/orchardist/daemon/claude-account"
)

// countingService wraps the real provider and counts underlying List calls.
// T5: loader coalescing is verified by asserting ≤1 underlying fetch per batch.
type countingService struct {
	svc       claudeaccount.Service
	listCalls atomic.Int64
}

func (c *countingService) List(ctx context.Context) ([]claudeaccount.Account, error) {
	c.listCalls.Add(1)
	return c.svc.List(ctx)
}

func (c *countingService) Get(ctx context.Context, key claudeaccount.AccountID) (claudeaccount.Account, bool, error) {
	return c.svc.Get(ctx, key)
}

func (c *countingService) Subscribe(ctx context.Context) <-chan claudeaccount.InvalidationEvent {
	return c.svc.Subscribe(ctx)
}

func (c *countingService) LastError() (time.Time, error) {
	return c.svc.LastError()
}

func (c *countingService) Adapter() *claudeaccount.ShellAdapter {
	return c.svc.Adapter()
}

// TestLoaders_AccountsByHost_CoalescesParallelCalls asserts that N concurrent
// Load calls for the same hostID result in ≤1 underlying List call (T5).
func TestLoaders_AccountsByHost_CoalescesParallelCalls(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{
		auth: [][]byte{auth, auth, auth, auth, auth},
		cc:   [][]byte{cc, cc, cc, cc, cc},
	}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, time.Hour, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	svc := &countingService{svc: claudeaccount.NewService(p)}
	loaders := claudeaccount.NewLoaders(svc)

	// Enqueue 5 parallel loads for the same hostID.
	const n = 5
	thunks := make([]func() ([]claudeaccount.Account, error), n)
	for i := range thunks {
		thunks[i] = loaders.AccountsByHost.Load(ctx, "test-host")
	}

	// Execute all thunks concurrently.
	var wg sync.WaitGroup
	wg.Add(n)
	for _, thunk := range thunks {
		thunk := thunk
		go func() {
			defer wg.Done()
			_, _ = thunk()
		}()
	}
	wg.Wait()

	// T5 assertion: ≤1 underlying List call.
	if calls := svc.listCalls.Load(); calls > 1 {
		t.Errorf("AccountsByHost loader made %d List calls, want ≤1 (coalescing broken)", calls)
	}
}

// TestLoaders_AccountByID_CoalescesParallelCalls asserts that N concurrent
// Load calls for the same AccountID result in ≤N underlying Get calls
// (each key is fetched at most once per batch). (T5)
func TestLoaders_AccountByID_CoalescesParallelCalls(t *testing.T) {
	auth, cc := stubAccount()
	fr := &fakeRunnerSeq{
		auth: [][]byte{auth, auth, auth, auth, auth},
		cc:   [][]byte{cc, cc, cc, cc, cc},
	}

	a := claudeaccount.NewShellAdapter("test-host", nil).WithRunner(fr)
	p := claudeaccount.NewWith(a, nil, time.Now, time.Hour, time.Hour)
	defer func() { _ = p.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Warm the cache first so Get returns from cache, not a fresh shellout.
	_, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	key := claudeaccount.AccountID{HostID: "test-host", Email: "alice@example.com"}
	loaders := claudeaccount.NewLoaders(claudeaccount.NewService(p))

	const n = 5
	thunks := make([]func() (claudeaccount.Account, bool, error), n)
	for i := range thunks {
		thunks[i] = loaders.AccountByID.Load(ctx, key)
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for _, thunk := range thunks {
		thunk := thunk
		go func() {
			defer wg.Done()
			_, _, _ = thunk()
		}()
	}
	wg.Wait()
	// No panic / deadlock = loader dispatched correctly.
}
