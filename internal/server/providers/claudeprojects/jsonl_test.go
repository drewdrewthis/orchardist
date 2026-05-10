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

// TestReadJSONLMeta_HeadMarkers parses a transcript whose first three
// records are the canonical Claude Code prologue: last-prompt,
// custom-title, agent-name. We expect the latter two to be surfaced.
func TestReadJSONLMeta_HeadMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "markers.jsonl")
	body := `{"type":"last-prompt","leafUuid":"u1","sessionId":"s1"}` + "\n" +
		`{"type":"custom-title","customTitle":"local:my-session","sessionId":"s1"}` + "\n" +
		`{"type":"agent-name","agentName":"my-agent","sessionId":"s1"}` + "\n" +
		`{"timestamp":"2026-05-04T12:00:00Z","type":"user"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.CustomTitle == nil || *meta.CustomTitle != "local:my-session" {
		t.Errorf("customTitle = %v, want %q", meta.CustomTitle, "local:my-session")
	}
	if meta.AgentName == nil || *meta.AgentName != "my-agent" {
		t.Errorf("agentName = %v, want %q", meta.AgentName, "my-agent")
	}
}

// TestReadJSONLMeta_HeadMarkers_Missing degrades cleanly when neither
// marker is present — both fields should be nil, not the empty string.
func TestReadJSONLMeta_HeadMarkers_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nomarkers.jsonl")
	body := `{"timestamp":"2026-05-04T12:00:00Z","type":"user"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.CustomTitle != nil {
		t.Errorf("customTitle = %v, want nil", *meta.CustomTitle)
	}
	if meta.AgentName != nil {
		t.Errorf("agentName = %v, want nil", *meta.AgentName)
	}
}

// TestReadJSONLMeta_HeadMarkers_EmptyStringDropped treats an empty
// customTitle as absent — no point surfacing "" to clients.
func TestReadJSONLMeta_HeadMarkers_EmptyStringDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emptymarkers.jsonl")
	body := `{"type":"custom-title","customTitle":"","sessionId":"s1"}` + "\n" +
		`{"type":"agent-name","agentName":"","sessionId":"s1"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.CustomTitle != nil {
		t.Errorf("customTitle = %v, want nil (empty string dropped)", *meta.CustomTitle)
	}
	if meta.AgentName != nil {
		t.Errorf("agentName = %v, want nil (empty string dropped)", *meta.AgentName)
	}
}

// TestReadJSONLMeta_LatestMarkersWins covers the case Drew hit:
// title/agentName are rewritten MID-SESSION (e.g. /title, /agent-name,
// /btw RULE: ...) — the LATEST values must win, not the first ones.
// The head-only scan we previously used missed every rewrite.
func TestReadJSONLMeta_LatestMarkersWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail-markers.jsonl")
	// Head sets one pair; many turns later the user re-titles. The tail
	// values must win.
	body := `{"type":"custom-title","customTitle":"original-title","sessionId":"s1"}` + "\n" +
		`{"type":"agent-name","agentName":"original-agent","sessionId":"s1"}` + "\n"
	for i := 0; i < 50; i++ {
		body += `{"timestamp":"2026-05-04T12:00:00Z","type":"user","uuid":"u` + string(rune('a'+i%26)) + `"}` + "\n"
	}
	body += `{"type":"custom-title","customTitle":"renamed-title","sessionId":"s1"}` + "\n" +
		`{"type":"agent-name","agentName":"renamed-agent","sessionId":"s1"}` + "\n" +
		`{"timestamp":"2026-05-04T12:30:00Z","type":"user","uuid":"final"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta, err := readJSONLMeta(path)
	if err != nil {
		t.Fatalf("readJSONLMeta: %v", err)
	}
	if meta.CustomTitle == nil || *meta.CustomTitle != "renamed-title" {
		t.Errorf("customTitle = %v, want %q (latest record must win)", meta.CustomTitle, "renamed-title")
	}
	if meta.AgentName == nil || *meta.AgentName != "renamed-agent" {
		t.Errorf("agentName = %v, want %q (latest record must win)", meta.AgentName, "renamed-agent")
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
