package claudejsonls

import (
	"context"
	"testing"
	"time"
)

// TestToGraphQL_JsonlPath asserts that ToGraphQL maps the in-memory
// Conversation.Path field onto the wire-level JsonlPath (T1).
func TestToGraphQL_JsonlPath(t *testing.T) {
	const wantPath = "/Users/alice/.claude/projects/foo/bar.jsonl"
	const sessionUUID = "00000000-aaaa-bbbb-cccc-dddddddddddd"

	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	c := Conversation{
		ID:           ConversationID{HostID: "test-host", SessionUUID: sessionUUID},
		Path:         wantPath,
		MessageCount: 1,
	}

	got := p.ToGraphQL(c)
	if got == nil {
		t.Fatal("ToGraphQL returned nil")
	}
	if got.JsonlPath != wantPath {
		t.Errorf("JsonlPath = %q, want %q", got.JsonlPath, wantPath)
	}
	if got.SessionUUID != sessionUUID {
		t.Errorf("SessionUUID = %q, want %q", got.SessionUUID, sessionUUID)
	}
}

// TestToGraphQL_JsonlPath_Empty asserts that an empty Path produces an
// empty JsonlPath — not a panic or nil pointer.
func TestToGraphQL_JsonlPath_Empty(t *testing.T) {
	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	c := Conversation{
		ID:   ConversationID{HostID: "test-host", SessionUUID: "empty-path-uuid"},
		Path: "",
	}

	got := p.ToGraphQL(c)
	if got == nil {
		t.Fatal("ToGraphQL returned nil")
	}
	if got.JsonlPath != "" {
		t.Errorf("JsonlPath = %q, want empty string for empty Path", got.JsonlPath)
	}
}

// TestPathForSessionUUID_Hit asserts that PathForSessionUUID finds a
// conversation by sessionUUID in the in-memory cache (T1).
func TestPathForSessionUUID_Hit(t *testing.T) {
	const uuid = "test-uuid-001"
	const path = "/tmp/test.jsonl"

	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	id := ConversationID{HostID: "test-host", SessionUUID: uuid}
	p.cachePut(id, Conversation{ID: id, Path: path})

	got, ok := p.PathForSessionUUID(context.Background(), uuid)
	if !ok {
		t.Fatalf("PathForSessionUUID returned ok=false, want true")
	}
	if got != path {
		t.Errorf("PathForSessionUUID = %q, want %q", got, path)
	}
}

// TestPathForSessionUUID_Miss asserts that PathForSessionUUID returns
// ("", false) when the sessionUUID is not in cache.
func TestPathForSessionUUID_Miss(t *testing.T) {
	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	got, ok := p.PathForSessionUUID(context.Background(), "nonexistent-uuid")
	if ok {
		t.Fatalf("PathForSessionUUID returned ok=true for unknown uuid, path=%q", got)
	}
	if got != "" {
		t.Errorf("PathForSessionUUID = %q, want empty string on miss", got)
	}
}

// TestIsOpen asserts the heartbeat logic: open when LastSeenAt is
// within the threshold; closed when nil or outside.
func TestIsOpen(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	p := NewWith(nil, nil, clock, 60*time.Second)

	// Within threshold → open.
	fresh := now.Add(-30 * time.Second)
	if !p.IsOpen(Conversation{LastSeenAt: &fresh}) {
		t.Error("IsOpen = false for LastSeenAt 30s ago, want true")
	}

	// Beyond threshold → closed.
	stale := now.Add(-90 * time.Second)
	if p.IsOpen(Conversation{LastSeenAt: &stale}) {
		t.Error("IsOpen = true for LastSeenAt 90s ago, want false")
	}

	// Nil LastSeenAt → closed.
	if p.IsOpen(Conversation{}) {
		t.Error("IsOpen = true for nil LastSeenAt, want false")
	}
}

// TestRecapAlwaysNil asserts that ToGraphQL never populates Recap.
func TestRecapAlwaysNil(t *testing.T) {
	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	c := Conversation{
		ID: ConversationID{HostID: "h", SessionUUID: "u"},
	}
	got := p.ToGraphQL(c)
	if got.Recap != nil {
		t.Errorf("Recap = %v, want nil (v1 contract)", got.Recap)
	}
}

// TestGetBySessionUUID_HitAndMiss covers both cache-hit and cache-miss
// for GetBySessionUUID.
func TestGetBySessionUUID_HitAndMiss(t *testing.T) {
	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	id := ConversationID{HostID: "h", SessionUUID: "known-uuid"}
	c := Conversation{ID: id, Path: "/tmp/known.jsonl"}
	p.cachePut(id, c)

	got, ok := p.GetBySessionUUID(context.Background(), "known-uuid")
	if !ok {
		t.Fatal("GetBySessionUUID: ok=false, want true")
	}
	if got.Path != "/tmp/known.jsonl" {
		t.Errorf("GetBySessionUUID: path=%q, want %q", got.Path, "/tmp/known.jsonl")
	}

	_, ok = p.GetBySessionUUID(context.Background(), "unknown-uuid")
	if ok {
		t.Error("GetBySessionUUID: ok=true for unknown uuid, want false")
	}
}

// TestList_SortedDescending verifies that List returns conversations
// in descending LastSeenAt order.
func TestList_SortedDescending(t *testing.T) {
	p := NewWith(nil, nil, time.Now, HeartbeatThreshold)

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		uuid string
		ts   time.Time
	}{
		{"uuid-1", t1},
		{"uuid-2", t2},
		{"uuid-3", t3},
	} {
		id := ConversationID{HostID: "h", SessionUUID: tc.uuid}
		ts := tc.ts
		p.cachePut(id, Conversation{ID: id, LastSeenAt: &ts})
	}

	rows, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List: got %d rows, want 3", len(rows))
	}
	// Most recent first.
	if rows[0].ID.SessionUUID != "uuid-2" {
		t.Errorf("rows[0] = %q, want uuid-2 (latest)", rows[0].ID.SessionUUID)
	}
	if rows[1].ID.SessionUUID != "uuid-3" {
		t.Errorf("rows[1] = %q, want uuid-3", rows[1].ID.SessionUUID)
	}
	if rows[2].ID.SessionUUID != "uuid-1" {
		t.Errorf("rows[2] = %q, want uuid-1 (oldest)", rows[2].ID.SessionUUID)
	}
}
