package claudejsonls

import (
	"context"
	"testing"
	"time"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// TestConversationsResolver_ProjectsAll verifies that the Conversations
// resolver returns all items from the loader (T1: field resolver tests
// against a stubbed service).
func TestConversationsResolver_ProjectsAll(t *testing.T) {
	id1 := ConversationID{HostID: "h", SessionUUID: "u1"}
	id2 := ConversationID{HostID: "h", SessionUUID: "u2"}

	ts := time.Now().UTC()
	svc := newStubService(
		Conversation{ID: id1, Path: "/tmp/u1.jsonl", LastSeenAt: &ts},
		Conversation{ID: id2, Path: "/tmp/u2.jsonl", LastSeenAt: &ts},
	)
	loaders := NewLoaders(svc)
	r := NewConversationResolver(svc, loaders, nil)

	convs, err := r.Conversations(context.Background())
	if err != nil {
		t.Fatalf("Conversations: %v", err)
	}
	if len(convs) != 2 {
		t.Errorf("got %d conversations, want 2", len(convs))
	}
}

// TestConversationResolver_LookupByID verifies that the Conversation
// resolver returns the right node for a known ID and nil for unknown
// (T1).
func TestConversationResolver_LookupByID(t *testing.T) {
	id := ConversationID{HostID: "h", SessionUUID: "known-uuid"}
	ts := time.Now().UTC()
	svc := newStubService(Conversation{ID: id, Path: "/tmp/known.jsonl", LastSeenAt: &ts})
	loaders := NewLoaders(svc)
	r := NewConversationResolver(svc, loaders, nil)

	// Known ID → returns conversation.
	got, err := r.Conversation(context.Background(), "Conversation:known-uuid")
	if err != nil {
		t.Fatalf("Conversation: %v", err)
	}
	if got == nil {
		t.Fatal("Conversation(known-uuid) returned nil")
	}
	if got.SessionUUID != "known-uuid" {
		t.Errorf("SessionUUID = %q, want %q", got.SessionUUID, "known-uuid")
	}

	// Unknown ID → returns nil (not an error).
	got2, err := r.Conversation(context.Background(), "Conversation:does-not-exist")
	if err != nil {
		t.Fatalf("Conversation(unknown): unexpected error: %v", err)
	}
	if got2 != nil {
		t.Errorf("Conversation(unknown) = %+v, want nil", got2)
	}

	// Wrong prefix → returns nil (not an error).
	got3, err := r.Conversation(context.Background(), "Host:something")
	if err != nil {
		t.Fatalf("Conversation(wrong-prefix): unexpected error: %v", err)
	}
	if got3 != nil {
		t.Errorf("Conversation(wrong-prefix) = %+v, want nil", got3)
	}
}

// TestConversationResolver_LiveInstances_NilReader verifies that
// LiveInstances returns an empty slice (not nil, not error) when no
// ClaudeInstanceReader is wired (T1 — tests the cross-domain back-edge
// with a stub returning nil).
func TestConversationResolver_LiveInstances_NilReader(t *testing.T) {
	id := ConversationID{HostID: "h", SessionUUID: "u1"}
	svc := newStubService(Conversation{ID: id})
	loaders := NewLoaders(svc)
	r := NewConversationResolver(svc, loaders, nil) // nil instance reader

	conv := svc.ToGraphQL(Conversation{ID: id})
	instances, err := r.LiveInstances(context.Background(), conv)
	if err != nil {
		t.Fatalf("LiveInstances: %v", err)
	}
	if instances == nil {
		t.Error("LiveInstances returned nil, want empty slice")
	}
	if len(instances) != 0 {
		t.Errorf("LiveInstances = %d items, want 0 (no reader)", len(instances))
	}
}

// TestConversationResolver_LiveInstances_WithReader verifies that
// LiveInstances delegates to the ClaudeInstanceReader interface (T1 +
// S15b cross-domain back-edge).
func TestConversationResolver_LiveInstances_WithReader(t *testing.T) {
	id := ConversationID{HostID: "h", SessionUUID: "u1"}
	svc := newStubService(Conversation{ID: id})
	loaders := NewLoaders(svc)

	reader := &stubInstanceReader{count: 2}
	r := NewConversationResolver(svc, loaders, reader)

	conv := svc.ToGraphQL(Conversation{ID: id})
	instances, err := r.LiveInstances(context.Background(), conv)
	if err != nil {
		t.Fatalf("LiveInstances: %v", err)
	}
	if len(instances) != 2 {
		t.Errorf("LiveInstances returned %d instances, want 2", len(instances))
	}
	if reader.called != "u1" {
		t.Errorf("reader called with %q, want %q", reader.called, "u1")
	}
}

// TestConversationResolver_Recap_AlwaysNil asserts that the wire type
// produced by ToGraphQL always has Recap=nil in v1 (T1 + v1 contract).
func TestConversationResolver_Recap_AlwaysNil(t *testing.T) {
	id := ConversationID{HostID: "h", SessionUUID: "u1"}
	svc := newStubService(Conversation{ID: id})
	loaders := NewLoaders(svc)
	r := NewConversationResolver(svc, loaders, nil)

	convs, err := r.Conversations(context.Background())
	if err != nil {
		t.Fatalf("Conversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("got %d conversations, want 1", len(convs))
	}
	if convs[0].Recap != nil {
		t.Errorf("Recap = %v, want nil (v1 contract)", convs[0].Recap)
	}
}

// TestConversationResolver_JsonlPath asserts that ToGraphQL maps Path
// onto JsonlPath correctly (T1, AC1).
func TestConversationResolver_JsonlPath(t *testing.T) {
	const wantPath = "/Users/alice/.claude/projects/foo/bar.jsonl"
	id := ConversationID{HostID: "h", SessionUUID: "u1"}
	svc := newStubService(Conversation{ID: id, Path: wantPath})
	loaders := NewLoaders(svc)
	r := NewConversationResolver(svc, loaders, nil)

	convs, err := r.Conversations(context.Background())
	if err != nil {
		t.Fatalf("Conversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("got %d conversations, want 1", len(convs))
	}
	if convs[0].JsonlPath != wantPath {
		t.Errorf("JsonlPath = %q, want %q", convs[0].JsonlPath, wantPath)
	}
}

// --- stubs ---

// stubInstanceReader implements ClaudeInstanceReader and records which
// sessionUUID it was called with.
type stubInstanceReader struct {
	count  int
	called string
}

func (s *stubInstanceReader) LiveInstancesByConversationUUID(
	_ context.Context,
	sessionUUID string,
) ([]*gql.ClaudeInstance, error) {
	s.called = sessionUUID
	out := make([]*gql.ClaudeInstance, s.count)
	for i := range out {
		out[i] = &gql.ClaudeInstance{ID: "ClaudeInstance:h:" + sessionUUID}
	}
	return out, nil
}
