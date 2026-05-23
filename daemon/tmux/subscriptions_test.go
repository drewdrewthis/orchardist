// subscriptions_test.go verifies R16: subscription emits after cache write (T6).
//
// T6: test fires a mutation (cache update), observes the subscription channel,
// asserts the subscriber sees the post-mutation state on first emission.
package tmux

import (
	"context"
	"sync"
	"testing"
	"time"
)

// manualService is a TmuxService stub with manual change control.
type manualService struct {
	stubService
	mu sync.Mutex
	ch chan SessionChangeEvent
}

func newManualService(sessions ...Session) *manualService {
	m := make(map[SessionKey]Session, len(sessions))
	for _, s := range sessions {
		m[s.Key] = s
	}
	return &manualService{
		stubService: stubService{sessions: m},
		ch:          make(chan SessionChangeEvent, 8),
	}
}

func (m *manualService) Subscribe(_ context.Context) <-chan SessionChangeEvent {
	return m.ch
}

func (m *manualService) AllSessions() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// AddSession simulates writing a new session to the cache before emitting.
func (m *manualService) AddSession(s Session) {
	m.mu.Lock()
	m.stubService.sessions[s.Key] = s
	m.mu.Unlock()
}

// emit simulates what Provider.refresh() does: write the cache, then emit.
// R16 contract: AllSessions() called after receipt returns the new session.
func (m *manualService) emit(ev SessionChangeEvent) {
	m.ch <- ev
}

// TestSubscription_EmitAfterCacheWrite verifies T6 (R16).
func TestSubscription_EmitAfterCacheWrite(t *testing.T) {
	svc := newManualService()
	r := &SubscriptionResolvers{Svc: svc}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := r.TmuxSessionsChanged(ctx)
	if err != nil {
		t.Fatalf("TmuxSessionsChanged: %v", err)
	}

	// Write cache THEN emit (R16 order).
	newSession := Session{
		Key:      SessionKey{Host: "local", Name: "work"},
		Attached: true,
	}
	svc.AddSession(newSession)                            // cache write first
	svc.emit(SessionChangeEvent{Reason: "test", At: time.Now()}) // emit after

	select {
	case nodes, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		// T6: subscriber must see post-write state.
		if len(nodes) == 0 {
			t.Fatal("received empty session list, want 1 session (post-write state)")
		}
		found := false
		for _, n := range nodes {
			if n.Name == "work" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("session 'work' not in emitted list; got %v", nodeNames(nodes))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscription emission")
	}
}

// TestSubscription_ContextCancel verifies the channel closes when ctx is done.
func TestSubscription_ContextCancel(t *testing.T) {
	svc := newManualService()
	r := &SubscriptionResolvers{Svc: svc}

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := r.TmuxSessionsChanged(ctx)
	if err != nil {
		t.Fatalf("TmuxSessionsChanged: %v", err)
	}

	cancel() // cancel context; channel should close

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel not closed after context cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel close after ctx cancel")
	}
}

// TestSubscription_NilService verifies an error is returned, not a panic.
func TestSubscription_NilService(t *testing.T) {
	r := &SubscriptionResolvers{Svc: nil}
	_, err := r.TmuxSessionsChanged(context.Background())
	if err == nil {
		t.Error("expected error for nil service, got nil")
	}
}

func nodeNames(nodes []*TmuxSessionNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}
