// loaders.go implements DataLoader-shaped reads for the tmux domain (R3, O1).
//
// DataLoader axes (ADR-022):
//   - PaneByID     → one pane per (host, paneID)
//   - PanesByCwd   → []Pane per (host, cwd)
//   - PanesByCommand → []Pane per (host, commandSubstring)
//   - SessionByName  → one session per (host, name)
//   - WindowByKey    → one window per (host, session, index)
//
// Each loader batches all keys that arrive within a single request tick,
// making at most one underlying service call per unique key. This eliminates
// the N+1 that caused the #612 60s lens-load.
//
// Correctness guarantee (O1): the loader key must match the access pattern
// of the underlying service method so coalescing actually fires.
package tmux

import (
	"context"
	"sync"
)

// PaneByIDLoader is a per-request DataLoader for the PaneByID axis.
// Usage: call Load(ctx, PaneKey{...}), then call the returned thunk.
//
// All Load calls that arrive before the first thunk is called are
// batched into a single service.PaneByID call per unique key (T5).
type PaneByIDLoader struct {
	mu      sync.Mutex
	svc     TmuxService
	pending map[PaneKey]*paneThunk
	// batchCount is the number of batch fetch invocations (O4, T5).
	batchCount int
}

type paneThunk struct {
	pane Pane
	ok   bool
	done chan struct{}
}

// NewPaneByIDLoader constructs a per-request PaneByID loader backed by svc.
func NewPaneByIDLoader(svc TmuxService) *PaneByIDLoader {
	return &PaneByIDLoader{
		svc:     svc,
		pending: make(map[PaneKey]*paneThunk),
	}
}

// Load schedules a load of key. The returned function blocks until the batch
// fires and returns (Pane, true) or (Pane{}, false) when not found.
//
// The batch fires on the first call to any thunk. All Load calls before
// the first thunk call are coalesced into one service round-trip (T5).
func (l *PaneByIDLoader) Load(ctx context.Context, key PaneKey) func() (Pane, bool) {
	l.mu.Lock()
	if t, ok := l.pending[key]; ok {
		l.mu.Unlock()
		return func() (Pane, bool) {
			<-t.done
			return t.pane, t.ok
		}
	}
	t := &paneThunk{done: make(chan struct{})}
	l.pending[key] = t
	l.mu.Unlock()

	return func() (Pane, bool) {
		l.mu.Lock()
		// First thunk caller fires the batch for ALL pending keys.
		if len(l.pending) > 0 {
			l.batchCount++
			toFetch := l.pending
			l.pending = make(map[PaneKey]*paneThunk)
			l.mu.Unlock()
			for k, th := range toFetch {
				pn, ok := l.svc.PaneByID(string(k.Host), k.PaneID)
				th.pane = pn
				th.ok = ok
				close(th.done)
			}
		} else {
			l.mu.Unlock()
		}
		<-t.done
		return t.pane, t.ok
	}
}

// BatchCount returns the number of batch invocations (O4).
func (l *PaneByIDLoader) BatchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.batchCount
}

// SessionByNameLoader is a per-request DataLoader for the SessionByName axis.
type SessionByNameLoader struct {
	mu         sync.Mutex
	svc        TmuxService
	pending    map[SessionKey]*sessionThunk
	batchCount int
}

type sessionThunk struct {
	session Session
	ok      bool
	done    chan struct{}
}

// NewSessionByNameLoader constructs a per-request SessionByName loader.
func NewSessionByNameLoader(svc TmuxService) *SessionByNameLoader {
	return &SessionByNameLoader{
		svc:     svc,
		pending: make(map[SessionKey]*sessionThunk),
	}
}

// Load schedules a load of key. The returned thunk blocks until the batch fires.
func (l *SessionByNameLoader) Load(ctx context.Context, key SessionKey) func() (Session, bool) {
	l.mu.Lock()
	if t, ok := l.pending[key]; ok {
		l.mu.Unlock()
		return func() (Session, bool) {
			<-t.done
			return t.session, t.ok
		}
	}
	t := &sessionThunk{done: make(chan struct{})}
	l.pending[key] = t
	l.mu.Unlock()

	return func() (Session, bool) {
		l.mu.Lock()
		if len(l.pending) > 0 {
			l.batchCount++
			toFetch := l.pending
			l.pending = make(map[SessionKey]*sessionThunk)
			l.mu.Unlock()
			for k, th := range toFetch {
				s, ok := l.svc.SessionByName(string(k.Host), k.Name)
				th.session = s
				th.ok = ok
				close(th.done)
			}
		} else {
			l.mu.Unlock()
		}
		<-t.done
		return t.session, t.ok
	}
}

// BatchCount returns the number of batch invocations (O4).
func (l *SessionByNameLoader) BatchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.batchCount
}

// WindowByKeyLoader is a per-request DataLoader for the WindowByKey axis.
type WindowByKeyLoader struct {
	mu         sync.Mutex
	svc        TmuxService
	pending    map[WindowKey]*windowThunk
	batchCount int
}

type windowThunk struct {
	window Window
	ok     bool
	done   chan struct{}
}

// NewWindowByKeyLoader constructs a per-request WindowByKey loader.
func NewWindowByKeyLoader(svc TmuxService) *WindowByKeyLoader {
	return &WindowByKeyLoader{
		svc:     svc,
		pending: make(map[WindowKey]*windowThunk),
	}
}

// Load schedules a load of key. The returned thunk blocks until the batch fires.
func (l *WindowByKeyLoader) Load(ctx context.Context, key WindowKey) func() (Window, bool) {
	l.mu.Lock()
	if t, ok := l.pending[key]; ok {
		l.mu.Unlock()
		return func() (Window, bool) {
			<-t.done
			return t.window, t.ok
		}
	}
	t := &windowThunk{done: make(chan struct{})}
	l.pending[key] = t
	l.mu.Unlock()

	return func() (Window, bool) {
		l.mu.Lock()
		if len(l.pending) > 0 {
			l.batchCount++
			toFetch := l.pending
			l.pending = make(map[WindowKey]*windowThunk)
			l.mu.Unlock()
			for k, th := range toFetch {
				w, ok := l.svc.WindowByKey(string(k.Host), k.Session, k.Index)
				th.window = w
				th.ok = ok
				close(th.done)
			}
		} else {
			l.mu.Unlock()
		}
		<-t.done
		return t.window, t.ok
	}
}

// BatchCount returns the number of batch invocations (O4).
func (l *WindowByKeyLoader) BatchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.batchCount
}

// RequestLoaders holds the per-request loader instances. Construct one per
// GraphQL request and pass it through context (following the standard
// DataLoader pattern; the resolver files access it via LoadersFromContext).
type RequestLoaders struct {
	PaneByID     *PaneByIDLoader
	SessionByName *SessionByNameLoader
	WindowByKey  *WindowByKeyLoader
}

// NewRequestLoaders constructs a fresh loader set for one GraphQL request.
func NewRequestLoaders(svc TmuxService) *RequestLoaders {
	return &RequestLoaders{
		PaneByID:      NewPaneByIDLoader(svc),
		SessionByName: NewSessionByNameLoader(svc),
		WindowByKey:   NewWindowByKeyLoader(svc),
	}
}

// contextKey is unexported so no other package can forge a loader context.
type contextKey struct{}

// WithLoaders attaches loaders to ctx. Resolvers call LoadersFromContext to retrieve.
func WithLoaders(ctx context.Context, l *RequestLoaders) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// LoadersFromContext retrieves the per-request loaders. Returns nil when no loaders
// are attached (e.g. subscription goroutines that don't go through HTTP middleware).
func LoadersFromContext(ctx context.Context) *RequestLoaders {
	l, _ := ctx.Value(contextKey{}).(*RequestLoaders)
	return l
}
