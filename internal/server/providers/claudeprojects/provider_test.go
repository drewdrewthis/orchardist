package claudeprojects

import (
	"testing"
	"time"
)

// TestToGraphQL_JsonlPath asserts that ToGraphQL maps the in-memory
// Conversation.Path field onto the wire-level JsonlPath. This is the
// AC1 unit assertion: "jsonlPath resolver maps an in-memory Conversation
// to its on-disk path".
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
	// Sanity-check other fields are still populated.
	if got.SessionUUID != sessionUUID {
		t.Errorf("SessionUUID = %q, want %q", got.SessionUUID, sessionUUID)
	}
}

// TestToGraphQL_JsonlPath_Empty asserts that an empty Path produces an
// empty JsonlPath (not a panic or nil pointer).
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
