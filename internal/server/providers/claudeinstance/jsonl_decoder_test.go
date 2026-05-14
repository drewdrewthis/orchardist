package claudeinstance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadRecordsFromPath_OversizedLineDoesNotHaltDecoding regresses PR #606
// review comment r3243103650. The original implementation used bufio.Scanner
// with a 1 MB max-line cap, which causes Scanner.Scan() to halt permanently
// (returning false with ErrTooLong) on any line over the cap. Every record
// after the oversized one was silently dropped — a real correctness bug for
// busy sessions where a single tool snapshot can exceed 1 MB.
//
// The fix uses bufio.Reader.ReadBytes('\n') so an oversized line is dropped
// in isolation and the loop continues. This test seeds a fixture with:
//
//	1. small valid record
//	2. a >1 MB record (a 2 MB user message)
//	3. small valid record
//
// and asserts both surrounding records are returned.
func TestReadRecordsFromPath_OversizedLineDoesNotHaltDecoding(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/oversized"
	const sessionID = "uuid-oversized"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Line 1: small valid assistant record with end_turn.
	before := `{"type":"assistant","timestamp":"2026-05-14T10:00:00Z","message":{"stop_reason":"end_turn","content":[{"type":"text"}]}}`

	// Line 2: a record whose payload pushes the line past the 1 MB cap.
	huge := `{"type":"user","timestamp":"2026-05-14T10:00:01Z","message":{"content":"` +
		strings.Repeat("x", 2*1024*1024) + `"}}`

	// Line 3: small valid assistant record with tool_use.
	after := `{"type":"assistant","timestamp":"2026-05-14T10:00:02Z","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_after","name":"Bash"}]}}`

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
		t.Fatalf("got %d records, want 2 (the oversized line should be dropped, surrounding records kept); records=%+v", len(records), records)
	}
	if records[0].Type != "assistant" || records[0].Message == nil || records[0].Message.StopReason != "end_turn" {
		t.Errorf("records[0] = %+v, want assistant/end_turn (the pre-oversized record)", records[0])
	}
	if records[1].Type != "assistant" || records[1].Message == nil || records[1].Message.StopReason != "tool_use" {
		t.Errorf("records[1] = %+v, want assistant/tool_use (the post-oversized record)", records[1])
	}
}

// TestReadRecordsFromPath_MissingFile returns (nil, nil) so the caller can
// treat the path as "no data" without distinguishing missing-file from
// empty-file.
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

// TestReadRecordsFromPath_FiltersSidechain confirms the decoder strips
// isSidechain records (sub-agent transcripts) before returning. The
// classifier never sees those records, so this filter is load-bearing for
// AC #5 (sub-agent records must not affect parent state).
func TestReadRecordsFromPath_FiltersSidechain(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/sidechain"
	const sessionID = "uuid-sidechain"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	parent := `{"type":"assistant","timestamp":"2026-05-14T10:00:00Z","isSidechain":false,"message":{"stop_reason":"end_turn"}}`
	sub := `{"type":"assistant","timestamp":"2026-05-14T10:00:01Z","isSidechain":true,"message":{"stop_reason":"tool_use"}}`

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

// TestReadRecordsFromPath_NoTrailingNewlineStillRead covers the EOF case where
// the file's last line lacks a terminating '\n'. ReadBytes returns the
// fragment at EOF; we must still parse it.
func TestReadRecordsFromPath_NoTrailingNewlineStillRead(t *testing.T) {
	dir := t.TempDir()
	const cwd = "/workspace/no-newline"
	const sessionID = "uuid-no-newline"

	projectDir := filepath.Join(dir, encodeCwd(cwd))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Two records; the second lacks the trailing newline.
	line1 := `{"type":"assistant","timestamp":"2026-05-14T10:00:00Z","message":{"stop_reason":"end_turn"}}`
	line2 := `{"type":"assistant","timestamp":"2026-05-14T10:00:01Z","message":{"stop_reason":"tool_use"}}`

	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(line1+"\n"+line2), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	records, err := readRecordsFromPath(dir, cwd, sessionID)
	if err != nil {
		t.Fatalf("readRecordsFromPath: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (the no-trailing-newline record must still be returned)", len(records))
	}
}
