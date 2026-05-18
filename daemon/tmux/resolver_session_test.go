// resolver_session_test.go verifies TmuxSession field resolvers (T1).
package tmux

import (
	"context"
	"testing"
	"time"
)

func testSession() Session {
	return Session{
		Key:            SessionKey{Host: "local", Name: "work"},
		CreatedAt:      time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Attached:       true,
		AttachedCount:  1,
		LastActivityAt: time.Date(2026, 5, 18, 9, 0, 0, 0, time.UTC),
		WindowCount:    2,
		CurrentWindow:  0,
	}
}

func testSessionNode(s Session) *TmuxSessionNode {
	return projectSessionNode(s)
}

func newSessionResolver(sessions ...Session) (*TmuxSessionResolvers, *stubService) {
	m := make(map[SessionKey]Session, len(sessions))
	for _, s := range sessions {
		m[s.Key] = s
	}
	svc := &stubService{sessions: m}
	r := &TmuxSessionResolvers{Svc: svc}
	return r, svc
}

func TestSessionResolver_CreatedAt(t *testing.T) {
	s := testSession()
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.CreatedAt(context.Background(), obj)
	if err != nil {
		t.Fatalf("CreatedAt: %v", err)
	}
	want := "2026-01-01T12:00:00Z"
	if got != want {
		t.Errorf("CreatedAt = %q, want %q", got, want)
	}
}

func TestSessionResolver_Attached(t *testing.T) {
	s := testSession()
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.Attached(context.Background(), obj)
	if err != nil {
		t.Fatalf("Attached: %v", err)
	}
	if !got {
		t.Error("Attached = false, want true")
	}
}

func TestSessionResolver_ActiveAttached(t *testing.T) {
	s := testSession()
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.ActiveAttached(context.Background(), obj)
	if err != nil {
		t.Fatalf("ActiveAttached: %v", err)
	}
	if !got {
		t.Error("ActiveAttached = false, want true")
	}
}

func TestSessionResolver_ActiveAttached_NotAttached(t *testing.T) {
	s := testSession()
	s.Attached = false
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.ActiveAttached(context.Background(), obj)
	if err != nil {
		t.Fatalf("ActiveAttached: %v", err)
	}
	if got {
		t.Error("ActiveAttached = true, want false for non-attached session")
	}
}

func TestSessionResolver_LastActivityAt(t *testing.T) {
	s := testSession()
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.LastActivityAt(context.Background(), obj)
	if err != nil {
		t.Fatalf("LastActivityAt: %v", err)
	}
	if got == nil {
		t.Fatal("LastActivityAt = nil, want non-nil")
	}
	want := "2026-05-18T09:00:00Z"
	if *got != want {
		t.Errorf("LastActivityAt = %q, want %q", *got, want)
	}
}

func TestSessionResolver_LastActivityAt_Zero(t *testing.T) {
	s := testSession()
	s.LastActivityAt = time.Time{}
	r, _ := newSessionResolver(s)
	obj := testSessionNode(s)

	got, err := r.LastActivityAt(context.Background(), obj)
	if err != nil {
		t.Fatalf("LastActivityAt: %v", err)
	}
	if got != nil {
		t.Errorf("LastActivityAt = %v, want nil for zero time", got)
	}
}

func TestSessionResolver_UsesLoader(t *testing.T) {
	s := testSession()
	svc := &stubService{sessions: map[SessionKey]Session{s.Key: s}}
	r := &TmuxSessionResolvers{Svc: svc}
	obj := testSessionNode(s)

	// With loader on context — should use the loader, not direct call.
	loaders := NewRequestLoaders(svc)
	ctx := WithLoaders(context.Background(), loaders)

	got, err := r.CreatedAt(ctx, obj)
	if err != nil {
		t.Fatalf("CreatedAt with loader: %v", err)
	}
	if got == "" {
		t.Error("CreatedAt with loader = empty, want RFC3339")
	}

	// Session was loaded via loader — session call count should be 1 (batched).
	if n := svc.sessionCallCount.Load(); n != 1 {
		t.Errorf("sessionCallCount = %d, want 1 (loader batched)", n)
	}
}

func TestSessionResolver_Windows_UsesAllWindows(t *testing.T) {
	s := testSession()
	w1 := Window{Key: WindowKey{Host: "local", Session: "work", Index: 0}, Name: "main"}
	w2 := Window{Key: WindowKey{Host: "local", Session: "work", Index: 1}, Name: "aux"}
	w3 := Window{Key: WindowKey{Host: "local", Session: "other", Index: 0}, Name: "other"}

	svc := &stubService{
		sessions: map[SessionKey]Session{s.Key: s},
		windows: map[WindowKey]Window{
			w1.Key: w1,
			w2.Key: w2,
			w3.Key: w3,
		},
	}
	r := &TmuxSessionResolvers{Svc: svc}
	obj := testSessionNode(s)

	got, err := r.Windows(context.Background(), obj)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	// Only w1 and w2 belong to session "work".
	if len(got) != 2 {
		t.Errorf("Windows = %d, want 2", len(got))
	}
}

// sortSessions correctness.
func TestSortSessions_LastActivity(t *testing.T) {
	early := Session{
		Key:            SessionKey{Host: "local", Name: "early"},
		LastActivityAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	late := Session{
		Key:            SessionKey{Host: "local", Name: "late"},
		LastActivityAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	sorted := sortSessions([]Session{early, late}, nil)
	if sorted[0].Key.Name != "late" {
		t.Errorf("first = %q, want late (most recent first)", sorted[0].Key.Name)
	}
}

func TestSortSessions_Name(t *testing.T) {
	b := Session{Key: SessionKey{Host: "local", Name: "b"}}
	a := Session{Key: SessionKey{Host: "local", Name: "a"}}
	key := TmuxSessionSortName
	sorted := sortSessions([]Session{b, a}, &key)
	if sorted[0].Key.Name != "a" {
		t.Errorf("first = %q, want a (lex order)", sorted[0].Key.Name)
	}
}
