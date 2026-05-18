// loaders_test.go verifies DataLoader coalescing (T5).
//
// T5: A loader test runs N parallel Load(key) calls against a service whose
// fetch method records call count; assert ≤1 call per unique key per batch.
package tmux

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubService is a TmuxService stub that counts PaneByID and SessionByName calls.
type stubService struct {
	panes    map[PaneKey]Pane
	sessions map[SessionKey]Session
	windows  map[WindowKey]Window
	clients  map[ClientKey]Client

	paneCallCount    atomic.Int64
	sessionCallCount atomic.Int64
}

func (s *stubService) Host() HostID                   { return "local" }
func (s *stubService) Server() ServerInfo             { return ServerInfo{Alive: true, SocketPath: "default"} }
func (s *stubService) AllSessions() []Session {
	out := make([]Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	return out
}
func (s *stubService) AllPanes() []Pane {
	out := make([]Pane, 0, len(s.panes))
	for _, v := range s.panes {
		out = append(out, v)
	}
	return out
}
func (s *stubService) AllClients() []Client {
	out := make([]Client, 0, len(s.clients))
	for _, v := range s.clients {
		out = append(out, v)
	}
	return out
}
func (s *stubService) AllWindows() []Window {
	out := make([]Window, 0, len(s.windows))
	for _, v := range s.windows {
		out = append(out, v)
	}
	return out
}
func (s *stubService) PanesByCwd(_, _ string, _ PanePsGetter) []Pane    { return nil }
func (s *stubService) PanesByCommand(_, _ string, _ PanePsGetter) []Pane { return nil }
func (s *stubService) PanesBySession(_, _ string) []Pane                 { return nil }
func (s *stubService) CapturePane(_ context.Context, _ PaneKey, _, _ int, _, _ bool) (string, error) {
	return "", nil
}
func (s *stubService) CapturePaneTail(_ context.Context, _ PaneKey, _ int, _ bool) (string, error) {
	return "", nil
}
func (s *stubService) Subscribe(_ context.Context) <-chan SessionChangeEvent {
	ch := make(chan SessionChangeEvent)
	close(ch)
	return ch
}
func (s *stubService) PokeRefresh() {}
func (s *stubService) Start(_ context.Context) error { return nil }

func (s *stubService) PaneByID(host, paneID string) (Pane, bool) {
	s.paneCallCount.Add(1)
	pn, ok := s.panes[PaneKey{Host: HostID(host), PaneID: paneID}]
	return pn, ok
}

func (s *stubService) SessionByName(host, name string) (Session, bool) {
	s.sessionCallCount.Add(1)
	sess, ok := s.sessions[SessionKey{Host: HostID(host), Name: name}]
	return sess, ok
}

func (s *stubService) WindowByKey(host, session string, index int) (Window, bool) {
	w, ok := s.windows[WindowKey{Host: HostID(host), Session: session, Index: index}]
	return w, ok
}

func (s *stubService) ClientByName(host, clientName string) (Client, bool) {
	c, ok := s.clients[ClientKey{Host: HostID(host), ClientName: clientName}]
	return c, ok
}

// TestPaneByIDLoader_Coalescing verifies T5: N parallel Load calls for the
// same key result in ≤1 underlying PaneByID call.
func TestPaneByIDLoader_Coalescing(t *testing.T) {
	svc := &stubService{
		panes: map[PaneKey]Pane{
			{Host: "local", PaneID: "%26"}: {
				Key:            PaneKey{Host: "local", PaneID: "%26"},
				CurrentCommand: "zsh",
			},
		},
	}

	loader := NewPaneByIDLoader(svc)
	key := PaneKey{Host: "local", PaneID: "%26"}

	const n = 10
	thunks := make([]func() (Pane, bool), n)
	for i := 0; i < n; i++ {
		thunks[i] = loader.Load(context.Background(), key)
	}

	// Call all thunks concurrently — the first one fires the batch.
	var wg sync.WaitGroup
	results := make([]Pane, n)
	for i, th := range thunks {
		wg.Add(1)
		i, th := i, th
		go func() {
			defer wg.Done()
			p, _ := th()
			results[i] = p
		}()
	}
	wg.Wait()

	// T5: ≤1 batch call for N parallel loads of the same key.
	// The loader design fires one batch per loader lifetime (first thunk wins).
	if got := svc.paneCallCount.Load(); got != 1 {
		t.Errorf("PaneByID called %d times, want 1 (coalescing broken)", got)
	}

	// Results must be correct.
	for i, p := range results {
		if p.Key.PaneID != "%26" {
			t.Errorf("result[%d].PaneID = %q, want %%26", i, p.Key.PaneID)
		}
	}

	// Batch count must be 1.
	if bc := loader.BatchCount(); bc != 1 {
		t.Errorf("BatchCount = %d, want 1", bc)
	}
}

// TestPaneByIDLoader_DifferentKeys verifies that two different keys each get
// their own lookup (not falsely coalesced).
func TestPaneByIDLoader_DifferentKeys(t *testing.T) {
	svc := &stubService{
		panes: map[PaneKey]Pane{
			{Host: "local", PaneID: "%1"}: {Key: PaneKey{Host: "local", PaneID: "%1"}},
			{Host: "local", PaneID: "%2"}: {Key: PaneKey{Host: "local", PaneID: "%2"}},
		},
	}

	loader := NewPaneByIDLoader(svc)
	ctx := context.Background()

	th1 := loader.Load(ctx, PaneKey{Host: "local", PaneID: "%1"})
	th2 := loader.Load(ctx, PaneKey{Host: "local", PaneID: "%2"})

	p1, ok1 := th1()
	p2, ok2 := th2()

	if !ok1 || p1.Key.PaneID != "%1" {
		t.Errorf("th1 = (%v, %v), want (%v, true)", p1, ok1, "%1")
	}
	if !ok2 || p2.Key.PaneID != "%2" {
		t.Errorf("th2 = (%v, %v), want (%v, true)", p2, ok2, "%2")
	}
}

// TestSessionByNameLoader_Coalescing verifies T5 for the SessionByName loader.
func TestSessionByNameLoader_Coalescing(t *testing.T) {
	svc := &stubService{
		sessions: map[SessionKey]Session{
			{Host: "local", Name: "work"}: {
				Key:       SessionKey{Host: "local", Name: "work"},
				Attached:  true,
				CreatedAt: time.Now(),
			},
		},
	}

	loader := NewSessionByNameLoader(svc)
	key := SessionKey{Host: "local", Name: "work"}

	const n = 5
	thunks := make([]func() (Session, bool), n)
	for i := 0; i < n; i++ {
		thunks[i] = loader.Load(context.Background(), key)
	}

	var wg sync.WaitGroup
	results := make([]Session, n)
	for i, th := range thunks {
		wg.Add(1)
		i, th := i, th
		go func() {
			defer wg.Done()
			s, _ := th()
			results[i] = s
		}()
	}
	wg.Wait()

	if got := svc.sessionCallCount.Load(); got != 1 {
		t.Errorf("SessionByName called %d times, want 1", got)
	}
	for i, s := range results {
		if s.Key.Name != "work" {
			t.Errorf("result[%d].Name = %q, want work", i, s.Key.Name)
		}
	}
}

// TestPaneByIDLoader_MissingKey verifies ok=false for a key not in the cache.
func TestPaneByIDLoader_MissingKey(t *testing.T) {
	svc := &stubService{panes: map[PaneKey]Pane{}}
	loader := NewPaneByIDLoader(svc)

	thunk := loader.Load(context.Background(), PaneKey{Host: "local", PaneID: "%99"})
	p, ok := thunk()
	if ok {
		t.Errorf("expected ok=false for missing key, got pane %+v", p)
	}
}
