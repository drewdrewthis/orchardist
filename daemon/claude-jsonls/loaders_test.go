package claudejsonls

import (
	"context"
	"sync"
	"testing"
	"time"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// stubService is a minimal Service implementation for loader tests.
// It counts how many times GetMany is called so tests can assert
// coalescing (T5: assert ≤1 underlying fetch per request).
type stubService struct {
	mu            sync.Mutex
	getManyN      int
	conversations map[ConversationID]Conversation
}

var _ Service = (*stubService)(nil)

func newStubService(convs ...Conversation) *stubService {
	s := &stubService{
		conversations: make(map[ConversationID]Conversation, len(convs)),
	}
	for _, c := range convs {
		s.conversations[c.ID] = c
	}
	return s
}

func (s *stubService) Get(ctx context.Context, key ConversationID) (Conversation, error) {
	m, err := s.GetMany(ctx, []ConversationID{key})
	if err != nil {
		return Conversation{}, err
	}
	return m[key], nil
}

func (s *stubService) GetMany(_ context.Context, keys []ConversationID) (map[ConversationID]Conversation, error) {
	s.mu.Lock()
	s.getManyN++
	s.mu.Unlock()
	out := make(map[ConversationID]Conversation, len(keys))
	for _, k := range keys {
		if c, ok := s.conversations[k]; ok {
			out[k] = c
		}
	}
	return out, nil
}

func (s *stubService) Keys(_ context.Context) ([]ConversationID, error) {
	out := make([]ConversationID, 0, len(s.conversations))
	for k := range s.conversations {
		out = append(out, k)
	}
	return out, nil
}

func (s *stubService) List(_ context.Context) ([]Conversation, error) {
	out := make([]Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		out = append(out, c)
	}
	return out, nil
}

func (s *stubService) IsOpen(_ Conversation) bool { return false }

func (s *stubService) ToGraphQL(c Conversation) *gql.Conversation {
	return &gql.Conversation{
		ID:          c.ID.GraphQLID(),
		SessionUUID: c.ID.SessionUUID,
		JsonlPath:   c.Path,
	}
}

func (s *stubService) PathForSessionUUID(_ context.Context, uuid string) (string, bool) {
	for id, c := range s.conversations {
		if id.SessionUUID == uuid {
			return c.Path, true
		}
	}
	return "", false
}

func (s *stubService) GetBySessionUUID(_ context.Context, uuid string) (Conversation, bool) {
	for id, c := range s.conversations {
		if id.SessionUUID == uuid {
			return c, true
		}
	}
	return Conversation{}, false
}

func (s *stubService) Subscribe(_ context.Context) <-chan InvalidationEvent {
	ch := make(chan InvalidationEvent)
	close(ch)
	return ch
}

func (s *stubService) Refresh(_ context.Context) error { return nil }

func (s *stubService) getManyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getManyN
}

// TestLoadByID_Basic verifies that LoadByID returns the right conversation.
func TestLoadByID_Basic(t *testing.T) {
	id := ConversationID{HostID: "h", SessionUUID: "u1"}
	c := Conversation{ID: id, Path: "/tmp/u1.jsonl", MessageCount: 5}

	svc := newStubService(c)
	l := NewLoaders(svc)

	got, err := l.LoadByID(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	if got.Path != c.Path {
		t.Errorf("Path = %q, want %q", got.Path, c.Path)
	}
}

// TestLoadManyByID_Coalescing verifies that LoadManyByID issues at most
// one GetMany call for a batch of keys (T5).
func TestLoadManyByID_Coalescing(t *testing.T) {
	ids := []ConversationID{
		{HostID: "h", SessionUUID: "u1"},
		{HostID: "h", SessionUUID: "u2"},
		{HostID: "h", SessionUUID: "u3"},
	}
	convs := make([]Conversation, len(ids))
	for i, id := range ids {
		convs[i] = Conversation{ID: id, Path: "/tmp/" + id.SessionUUID + ".jsonl"}
	}

	svc := newStubService(convs...)
	l := NewLoaders(svc)

	// Load all three in one call — one GetMany, not three Gets.
	got, err := l.LoadManyByID(context.Background(), ids)
	if err != nil {
		t.Fatalf("LoadManyByID: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d conversations, want 3", len(got))
	}

	// T5: assert the service was called exactly once.
	if n := svc.getManyCount(); n != 1 {
		t.Errorf("GetMany called %d times, want 1 (coalescing)", n)
	}
}

// TestLoadAll_ReturnsList verifies LoadAll delegates to List.
func TestLoadAll_ReturnsList(t *testing.T) {
	ts1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	id1 := ConversationID{HostID: "h", SessionUUID: "u1"}
	id2 := ConversationID{HostID: "h", SessionUUID: "u2"}

	svc := newStubService(
		Conversation{ID: id1, LastSeenAt: &ts1},
		Conversation{ID: id2, LastSeenAt: &ts2},
	)
	l := NewLoaders(svc)

	rows, err := l.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("LoadAll: got %d rows, want 2", len(rows))
	}
}
