// adapter_test.go — tests for the filesystem adapter (FsSnapshotReader + helpers).
package claudeinstance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncodeCwd_SlashesAndDots: '/' and '.' both map to '-'.
func TestEncodeCwd_SlashesAndDots(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/repo/worktree", "-repo-worktree"},
		{"/repo/.worktrees/foo", "-repo--worktrees-foo"},
		{"/home/user/project.go", "-home-user-project-go"},
	}
	for _, tc := range cases {
		got := encodeCwd(tc.in)
		if got != tc.want {
			t.Errorf("encodeCwd(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestReadRecordsFromPath_MissingFile returns (nil, nil).
func TestReadRecordsFromPath_MissingFile(t *testing.T) {
	dir := t.TempDir()
	records, err := readRecordsFromPath(dir, "/nowhere", "no-uuid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if records != nil {
		t.Errorf("got %d records, want nil", len(records))
	}
}

// TestReadRecordsFromPath_FiltersSidechain: isSidechain records are stripped.
func TestReadRecordsFromPath_FiltersSidechain(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/sidechain"
	const sessionID = "uuid-sidechain"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	parent := `{"type":"assistant","timestamp":"2026-05-18T10:00:00Z","isSidechain":false,"message":{"stop_reason":"end_turn"}}`
	sub := `{"type":"assistant","timestamp":"2026-05-18T10:00:01Z","isSidechain":true,"message":{"stop_reason":"tool_use"}}`

	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(parent+"\n"+sub+"\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := readRecordsFromPath(dir, cwd, sessionID)
	if err != nil {
		t.Fatalf("readRecordsFromPath: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1 (sidechain must be filtered)", len(records))
	}
	if records[0].IsSidechain {
		t.Error("returned a sidechain record; filter is broken")
	}
}

// TestReadRecordsFromPath_OversizedLineDoesNotHaltDecoding: an oversized line
// is dropped in isolation; surrounding records are still returned.
// Regression for PR #606 review comment r3243103650.
func TestReadRecordsFromPath_OversizedLineDoesNotHaltDecoding(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/oversized"
	const sessionID = "uuid-oversized"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before := `{"type":"assistant","timestamp":"2026-05-18T10:00:00Z","message":{"stop_reason":"end_turn","content":[{"type":"text"}]}}`
	huge := `{"type":"user","timestamp":"2026-05-18T10:00:01Z","message":{"content":"` +
		strings.Repeat("x", 2*1024*1024) + `"}}`
	after := `{"type":"assistant","timestamp":"2026-05-18T10:00:02Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_after","name":"Bash"}]}}`

	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	body := before + "\n" + huge + "\n" + after + "\n"
	if err := os.WriteFile(jsonlPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := readRecordsFromPath(dir, cwd, sessionID)
	if err != nil {
		t.Fatalf("readRecordsFromPath: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (oversized line dropped, surrounding records kept)", len(records))
	}
	if records[0].Type != "assistant" || records[0].Message == nil || records[0].Message.StopReason != "end_turn" {
		t.Errorf("records[0] = %+v, want assistant/end_turn", records[0])
	}
	if records[1].Type != "assistant" || records[1].Message == nil || records[1].Message.StopReason != "tool_use" {
		t.Errorf("records[1] = %+v, want assistant/tool_use", records[1])
	}
}

// TestReadRecordsFromPath_NoTrailingNewlineStillRead: no trailing newline is fine.
func TestReadRecordsFromPath_NoTrailingNewlineStillRead(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/no-newline"
	const sessionID = "uuid-no-newline"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	line1 := `{"type":"assistant","timestamp":"2026-05-18T10:00:00Z","message":{"stop_reason":"end_turn"}}`
	line2 := `{"type":"assistant","timestamp":"2026-05-18T10:00:01Z","message":{"stop_reason":"tool_use"}}`

	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(line1+"\n"+line2), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := readRecordsFromPath(dir, cwd, sessionID)
	if err != nil {
		t.Fatalf("readRecordsFromPath: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (no-trailing-newline record must be returned)", len(records))
	}
}

// TestFsSnapshotReader_NilReader: nil reader returns (nil, false) without panic.
func TestFsSnapshotReader_NilReader(t *testing.T) {
	var r *FsSnapshotReader
	got, ok := r.ReadSnapshot(context.Background(), "/work", "uuid")
	if ok {
		t.Error("ok = true for nil reader, want false")
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

// TestFsSnapshotReader_MissingProjectsDir: missing projects dir returns (nil, false).
func TestFsSnapshotReader_MissingProjectsDir(t *testing.T) {
	dir := t.TempDir()
	r := NewFsSnapshotReader(filepath.Join(dir, "nonexistent"))
	got, ok := r.ReadSnapshot(context.Background(), "/work", "uuid-missing")
	if ok {
		t.Error("ok = true for missing projects dir, want false")
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}
