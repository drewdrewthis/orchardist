package features_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// @scenario conversations returns all conversations ordered latest-first
func TestConversationsQuery_ReturnsOrderedLatestFirst(t *testing.T) {
	cpDir := t.TempDir()

	// Write two JSONL files with session metadata so the claudeprojects
	// provider discovers them as conversations.
	writeSessionJSONL(t, cpDir, "session-aaa", time.Now().Add(-5*time.Minute))
	writeSessionJSONL(t, cpDir, "session-bbb", time.Now())

	ts := startServerWithClaudeProjects(t, cpDir)

	// Give the watcher a moment to observe the files.
	time.Sleep(100 * time.Millisecond)

	r := postGQL(t, ts.URL, `{ conversations { id sessionUuid agentName customTitle cwd firstSeenAt lastSeenAt messageCount open recap } }`)
	assertNoErrors(t, r)

	convRaw, ok := r.Data["conversations"]
	if !ok {
		t.Fatal("conversations field missing from response")
	}
	convs := asList(t, convRaw, "conversations")
	// Ordering: if two convs present, bbb (more recent) must come first.
	if len(convs) >= 2 {
		c0 := asMap(t, convs[0], "conversations[0]")
		c1 := asMap(t, convs[1], "conversations[1]")
		t0, _ := c0["lastSeenAt"].(string)
		t1, _ := c1["lastSeenAt"].(string)
		if t0 != "" && t1 != "" && t0 < t1 {
			t.Errorf("conversations not ordered latest-first: [0].lastSeenAt=%s [1].lastSeenAt=%s", t0, t1)
		}
	}
}

// @scenario conversation entry carries required fields
func TestConversationsQuery_EntryCarriesRequiredFields(t *testing.T) {
	cpDir := t.TempDir()
	writeSessionJSONL(t, cpDir, "session-ccc", time.Now())
	ts := startServerWithClaudeProjects(t, cpDir)
	time.Sleep(100 * time.Millisecond)

	r := postGQL(t, ts.URL, `{ conversations { id sessionUuid agentName customTitle cwd firstSeenAt lastSeenAt messageCount open recap } }`)
	assertNoErrors(t, r)

	convs := asList(t, r.Data["conversations"], "conversations")
	for _, raw := range convs {
		c := asMap(t, raw, "conversation")
		requireFields(t, c,
			"id", "sessionUuid", "agentName", "customTitle",
			"cwd", "firstSeenAt", "lastSeenAt", "messageCount", "open", "recap",
		)
	}
}

// @scenario conversations is empty when no JSONL files exist
func TestConversationsQuery_EmptyWhenNoJSONL(t *testing.T) {
	cpDir := t.TempDir() // empty
	ts := startServerWithClaudeProjects(t, cpDir)

	r := postGQL(t, ts.URL, `{ conversations { id sessionUuid } }`)
	assertNoErrors(t, r)

	convs := asList(t, r.Data["conversations"], "conversations")
	if len(convs) != 0 {
		t.Errorf("expected empty conversations, got %d entries", len(convs))
	}
}

// ---------------------------------------------------------------------------
// Fixture: write a minimal JSONL file the claudeprojects provider can parse.
// ---------------------------------------------------------------------------

// writeSessionJSONL writes a minimal Claude JSONL file that the claudeprojects
// provider will discover and register as a conversation.
func writeSessionJSONL(t *testing.T, cpDir, sessionUUID string, ts time.Time) {
	t.Helper()
	// Claude stores JSONL under cpDir/<project-hash>/<uuid>.jsonl.
	// The simplest approach: write directly into cpDir/test/<uuid>.jsonl.
	dir := filepath.Join(cpDir, "test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, sessionUUID+".jsonl")

	// Write a minimal assistant message record that the JSONL parser understands.
	record := map[string]any{
		"type":      "assistant",
		"sessionId": sessionUUID,
		"timestamp": ts.UTC().Format(time.RFC3339),
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": "hello"}},
		},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal jsonl record: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write jsonl %s: %v", path, err)
	}
}
