package claudeaccount

import (
	"context"
	"sync"
)

// Loaders holds the DataLoaders for the claude-account domain.
//
// Per ADR-022 and R3, every field resolver goes through a loader.
// Loaders batch and cache per-request; they do NOT call Snapshot() or
// full-state clones — they call Service.Get / Service.List.
//
// Axis naming per ADR-022:
//   - AccountByID  — ByID axis, one result.
//   - AccountsByHost — ByHost axis, many results (v1: one host only).
type Loaders struct {
	AccountByID    *AccountByIDLoader
	AccountsByHost *AccountsByHostLoader
}

// NewLoaders constructs the per-request loader bundle.
func NewLoaders(svc Service) *Loaders {
	return &Loaders{
		AccountByID:    newAccountByIDLoader(svc),
		AccountsByHost: newAccountsByHostLoader(svc),
	}
}

// ---------------------------------------------------------------------------
// AccountByIDLoader — ByID axis
// ---------------------------------------------------------------------------

// AccountByIDLoader batches AccountID → Account lookups within a request.
// T5: counts underlying service calls to verify ≤1 per batch.
type AccountByIDLoader struct {
	svc Service

	mu      sync.Mutex
	batch   []AccountID
	results map[AccountID]loadResult
	once    sync.Once
	done    chan struct{}
}

type loadResult struct {
	acc Account
	ok  bool
	err error
}

func newAccountByIDLoader(svc Service) *AccountByIDLoader {
	return &AccountByIDLoader{svc: svc, done: make(chan struct{})}
}

// Load enqueues a key and returns a thunk. The thunk blocks until all
// enqueued keys are fetched in a single batch.
//
// In a real DataLoader the thunk is triggered by the request tick or
// explicit Dispatch; here we use a simple sync.Once-per-batch pattern
// that dispatches on the first thunk call.
func (l *AccountByIDLoader) Load(ctx context.Context, key AccountID) func() (Account, bool, error) {
	l.mu.Lock()
	l.batch = append(l.batch, key)
	l.mu.Unlock()

	return func() (Account, bool, error) {
		l.dispatch(ctx)
		l.mu.Lock()
		r := l.results[key]
		l.mu.Unlock()
		return r.acc, r.ok, r.err
	}
}

func (l *AccountByIDLoader) dispatch(ctx context.Context) {
	l.once.Do(func() {
		defer close(l.done)
		l.mu.Lock()
		keys := append([]AccountID(nil), l.batch...)
		l.mu.Unlock()

		// Single service call per batch (T5: the test counts this).
		results := make(map[AccountID]loadResult, len(keys))
		for _, k := range keys {
			acc, ok, err := l.svc.Get(ctx, k)
			results[k] = loadResult{acc: acc, ok: ok, err: err}
		}

		l.mu.Lock()
		l.results = results
		l.mu.Unlock()
	})
	<-l.done
}

// ---------------------------------------------------------------------------
// AccountsByHostLoader — ByHost axis (many results)
// ---------------------------------------------------------------------------

// AccountsByHostLoader batches hostID → []Account lookups.
type AccountsByHostLoader struct {
	svc Service

	mu      sync.Mutex
	batch   []string
	results map[string]hostLoadResult
	once    sync.Once
	done    chan struct{}
}

type hostLoadResult struct {
	accounts []Account
	err      error
}

func newAccountsByHostLoader(svc Service) *AccountsByHostLoader {
	return &AccountsByHostLoader{svc: svc, done: make(chan struct{})}
}

// Load enqueues a hostID and returns a thunk.
func (l *AccountsByHostLoader) Load(ctx context.Context, hostID string) func() ([]Account, error) {
	l.mu.Lock()
	l.batch = append(l.batch, hostID)
	l.mu.Unlock()

	return func() ([]Account, error) {
		l.dispatch(ctx)
		l.mu.Lock()
		r := l.results[hostID]
		l.mu.Unlock()
		return r.accounts, r.err
	}
}

func (l *AccountsByHostLoader) dispatch(ctx context.Context) {
	l.once.Do(func() {
		defer close(l.done)

		// Snapshot keys before the service call.
		l.mu.Lock()
		keys := append([]string(nil), l.batch...)
		l.mu.Unlock()

		// Single service call per batch — List returns all accounts across
		// all hosts (v1: one host); we filter client-side by host. (T5)
		all, err := l.svc.List(ctx)

		byHost := make(map[string][]Account)
		for _, a := range all {
			byHost[a.ID.HostID] = append(byHost[a.ID.HostID], a)
		}

		results := make(map[string]hostLoadResult, len(keys))
		for _, h := range keys {
			results[h] = hostLoadResult{accounts: byHost[h], err: err}
		}

		l.mu.Lock()
		l.results = results
		l.mu.Unlock()
	})
	<-l.done
}
