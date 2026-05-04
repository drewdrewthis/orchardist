package claudeprojects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadJSONLMeta_Empty asserts an empty file degrades cleanly to
// zero records and nil timestamps — important so a freshly-created
// transcript does not panic the provider.
func TestReadJSONLMeta_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.MessageCount != 0 {
		t.Errorf("messageCount = %d, want 0", meta.MessageCount)
	}
	if meta.FirstSeenAt != nil || meta.LastSeenAt != nil {
		t.Errorf("expected nil timestamps for empty file, got first=%v last=%v",
			meta.FirstSeenAt, meta.LastSeenAt)
	}
}

// TestReadJSONLMeta_Single ensures a one-record file reports both
// firstSeenAt and lastSeenAt as the same value.
func TestReadJSONLMeta_Single(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.jsonl")
	ts := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	cwd := "/tmp/fixture"
	body := `{"timestamp":"` + ts.Format(time.RFC3339Nano) + `","cwd":"` + cwd + `","type":"user"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.MessageCount != 1 {
		t.Errorf("messageCount = %d, want 1", meta.MessageCount)
	}
	if meta.FirstSeenAt == nil || !meta.FirstSeenAt.Equal(ts) {
		t.Errorf("firstSeenAt = %v, want %v", meta.FirstSeenAt, ts)
	}
	if meta.LastSeenAt == nil || !meta.LastSeenAt.Equal(ts) {
		t.Errorf("lastSeenAt = %v, want %v", meta.LastSeenAt, ts)
	}
	if meta.Cwd == nil || *meta.Cwd != cwd {
		t.Errorf("cwd = %v, want %q", meta.Cwd, cwd)
	}
}

// TestReadJSONLMeta_Many reads a 5-line transcript and confirms we
// pick the last record's timestamp, not the file's mtime.
func TestReadJSONLMeta_Many(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.jsonl")
	t0 := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		ts := t0.Add(time.Duration(i) * time.Second)
		b.WriteString(`{"timestamp":"` + ts.Format(time.RFC3339Nano) + `","type":"user"}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.MessageCount != 5 {
		t.Errorf("messageCount = %d, want 5", meta.MessageCount)
	}
	if !meta.FirstSeenAt.Equal(t0) {
		t.Errorf("firstSeenAt = %v, want %v", meta.FirstSeenAt, t0)
	}
	wantLast := t0.Add(4 * time.Second)
	if !meta.LastSeenAt.Equal(wantLast) {
		t.Errorf("lastSeenAt = %v, want %v", meta.LastSeenAt, wantLast)
	}
}

// TestReadJSONLMeta_PartialTrailingLine asserts a file that ends mid-
// write (no terminating newline on the final line) does not crash and
// counts only fully-terminated records — same as wc -l.
func TestReadJSONLMeta_PartialTrailingLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.jsonl")
	t0 := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	body := `{"timestamp":"` + t0.Format(time.RFC3339Nano) + `","type":"user"}` + "\n" +
		`{"timestamp":"` + t0.Add(time.Second).Format(time.RFC3339Nano) + `","type":"assistant"}` + "\n" +
		`{"timestamp":"`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.MessageCount != 2 {
		t.Errorf("messageCount = %d, want 2 (partial trailing line ignored)", meta.MessageCount)
	}
}

// TestReadJSONLMeta_NoTimestamp tolerates records that omit timestamps —
// firstSeenAt/lastSeenAt are nil but messageCount still reflects the
// line count.
func TestReadJSONLMeta_NoTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nots.jsonl")
	body := `{"type":"summary"}` + "\n" + `{"type":"summary"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.MessageCount != 2 {
		t.Errorf("messageCount = %d, want 2", meta.MessageCount)
	}
	if meta.FirstSeenAt != nil || meta.LastSeenAt != nil {
		t.Errorf("expected nil timestamps, got first=%v last=%v",
			meta.FirstSeenAt, meta.LastSeenAt)
	}
}

// TestReadJSONLMeta_LongLineSkipped asserts a single line longer than
// maxLineBytes does not crash the parser; we surface (nil, nil) for
// firstSeenAt/lastSeenAt rather than failing the whole walk.
func TestReadJSONLMeta_LongLineSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.jsonl")
	huge := strings.Repeat("x", maxLineBytes+10)
	body := `{"timestamp":"2026-05-04T12:00:00Z","blob":"` + huge + `"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	// One newline → one record counted, but the firstRecord parser
	// hits the bound and returns nil.
	if meta.MessageCount != 1 {
		t.Errorf("messageCount = %d, want 1", meta.MessageCount)
	}
	if meta.FirstSeenAt != nil {
		t.Errorf("firstSeenAt should be nil after a too-long record, got %v", meta.FirstSeenAt)
	}
}

// TestSessionUUIDFromPath asserts the filename → uuid extraction
// peels only the .jsonl suffix and respects directory components.
func TestSessionUUIDFromPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/a/b/c/abcd.jsonl", "abcd"},
		{"abcd.jsonl", "abcd"},
		{"/x/abcd.jsonl.bak", "abcd.jsonl.bak"}, // not a .jsonl, returns base unchanged
	}
	for _, c := range cases {
		if got := sessionUUIDFromPath(c.in); got != c.want {
			t.Errorf("sessionUUIDFromPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestConversationID_GraphQLID confirms the orchard id format
// matches the schema docs: "Conversation:<sessionUuid>".
func TestConversationID_GraphQLID(t *testing.T) {
	id := ConversationID{HostID: "test-host", SessionUUID: "abcd"}
	if got, want := id.GraphQLID(), "Conversation:abcd"; got != want {
		t.Errorf("GraphQLID = %q, want %q", got, want)
	}
}
