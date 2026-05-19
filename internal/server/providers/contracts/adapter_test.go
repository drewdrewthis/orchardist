package contracts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAdapter_Snapshot_DirectoryScan asserts the adapter reads every
// per-contract jsonl file in the directory and returns the union of
// their events. Three files with three distinct end-states: delivered,
// open (started), and open-then-delivered-in-sequence.
func TestAdapter_Snapshot_DirectoryScan(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Hour)

	writeJSONL(t, filepath.Join(dir, "C-test-001.jsonl"),
		v7CreationLine(t, "C-test-001", "deliver thing one", "orchard:claude:session-alice", t0),
		v7UpdateLine(t, "C-test-001", "delivered", t1),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-002.jsonl"),
		v7CreationLine(t, "C-test-002", "thing two stays open", "orchard:claude:session-bob", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-003.jsonl"),
		v7CreationLine(t, "C-test-003", "abandoned thing", "orchard:claude:session-carol", t0),
		v7UpdateLine(t, "C-test-003", "delivered", t1),
	)

	adapter := NewAdapter(dir)
	events, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	state := Fold(events)
	if got, want := len(state), 3; got != want {
		t.Fatalf("contract count = %d, want %d", got, want)
	}

	cases := []struct {
		id     ContractID
		status string
	}{
		{"C-test-001", "delivered"},
		{"C-test-002", "started"},
		{"C-test-003", "delivered"},
	}
	for _, tc := range cases {
		c, ok := state[tc.id]
		if !ok {
			t.Fatalf("contract %s missing from fold", tc.id)
		}
		if c.Status != tc.status {
			t.Errorf("%s status = %q, want %q", tc.id, c.Status, tc.status)
		}
	}

	// Each file must have a non-zero offset recorded so a follow-up
	// FollowFromOffsets call resumes after these events.
	for _, name := range []string{"C-test-001.jsonl", "C-test-002.jsonl", "C-test-003.jsonl"} {
		if offsets[name] == 0 {
			t.Errorf("offsets[%q] = 0, want > 0", name)
		}
	}
}

// TestAdapter_Snapshot_EmptyDir asserts the empty-directory case
// returns no events and no error — the safe shape for a daemon boot
// before any contract has been filed.
func TestAdapter_Snapshot_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	adapter := NewAdapter(dir)
	events, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot empty dir: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want 0", len(events))
	}
	if len(offsets) != 0 {
		t.Errorf("offsets = %d, want 0", len(offsets))
	}
}

// TestAdapter_Snapshot_MissingDir asserts a non-existent directory
// returns no events and no error — same shape as the missing-aggregate
// case the old code handled.
func TestAdapter_Snapshot_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	adapter := NewAdapter(dir)
	events, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot missing dir: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want 0", len(events))
	}
	if len(offsets) != 0 {
		t.Errorf("offsets = %d, want 0", len(offsets))
	}
}

// TestAdapter_FollowFromOffsets_ResumesPerFile asserts that appending
// events to one file yields only the new events on a follow-up call,
// while files unchanged stay quiet.
func TestAdapter_FollowFromOffsets_ResumesPerFile(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	writeJSONL(t, filepath.Join(dir, "C-test-aaa.jsonl"),
		v7CreationLine(t, "C-test-aaa", "alpha", "orchard:claude:session-alice", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-bbb.jsonl"),
		v7CreationLine(t, "C-test-bbb", "beta", "orchard:claude:session-bob", t0),
	)

	adapter := NewAdapter(dir)
	events, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got, want := len(events), 2; got != want {
		t.Fatalf("initial events = %d, want %d", got, want)
	}

	// Append a new event to one file only.
	appendJSONL(t, filepath.Join(dir, "C-test-aaa.jsonl"),
		v7UpdateLine(t, "C-test-aaa", "delivered", t1),
	)

	// And add a third file entirely.
	writeJSONL(t, filepath.Join(dir, "C-test-ccc.jsonl"),
		v7CreationLine(t, "C-test-ccc", "gamma", "orchard:claude:session-carol", t2),
	)

	tail, advanced, err := adapter.FollowFromOffsets(context.Background(), offsets)
	if err != nil {
		t.Fatalf("FollowFromOffsets: %v", err)
	}
	if got, want := len(tail), 2; got != want {
		t.Fatalf("tail events = %d, want %d (update on aaa + creation of ccc)", got, want)
	}
	if advanced["C-test-bbb.jsonl"] != offsets["C-test-bbb.jsonl"] {
		t.Errorf("bbb offset moved despite no new bytes: from %d to %d",
			offsets["C-test-bbb.jsonl"], advanced["C-test-bbb.jsonl"])
	}
	if advanced["C-test-aaa.jsonl"] <= offsets["C-test-aaa.jsonl"] {
		t.Errorf("aaa offset did not advance: from %d to %d",
			offsets["C-test-aaa.jsonl"], advanced["C-test-aaa.jsonl"])
	}
	if advanced["C-test-ccc.jsonl"] == 0 {
		t.Errorf("ccc offset = 0, want > 0 after first read")
	}
}

// TestAdapter_Snapshot_IgnoresNonJSONLFiles guards against the .md
// mirrors that the contracts plugin writes alongside each jsonl. The
// adapter must read only the .jsonl files.
func TestAdapter_Snapshot_IgnoresNonJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	writeJSONL(t, filepath.Join(dir, "C-test-001.jsonl"),
		v7CreationLine(t, "C-test-001", "alpha", "orchard:claude:session-alice", t0),
	)
	if err := os.WriteFile(filepath.Join(dir, "C-test-001.md"),
		[]byte("# mirror file\n"), 0o644); err != nil {
		t.Fatalf("write .md sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"),
		[]byte("not a contract\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	adapter := NewAdapter(dir)
	events, _, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	state := Fold(events)
	if got, want := len(state), 1; got != want {
		t.Fatalf("contract count = %d, want %d (non-jsonl files must be skipped)", got, want)
	}
}

// writeJSONL writes a fresh per-contract jsonl file with the given
// pre-marshalled lines.
func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync %s: %v", path, err)
	}
}

// appendJSONL appends pre-marshalled lines to an existing per-contract
// jsonl file.
func appendJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("append %s: %v", path, err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync %s: %v", path, err)
	}
}

// v7CreationLine returns a flat v0.7 creation JSONL line.
func v7CreationLine(t *testing.T, contractID, summary, owner string, at time.Time) string {
	t.Helper()
	row := map[string]any{
		"timestamp":   at.UTC().Format(time.RFC3339Nano),
		"contract_id": contractID,
		"status":      "started",
		"summary":     summary,
		"reasoning":   "contract filed",
		"owner":       owner,
		"created_by":  "test-agent",
		"source":      nil,
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal v7 creation: %v", err)
	}
	return string(b)
}

// v7UpdateLine returns a flat v0.7 update JSONL line (no summary, no
// owner — inherits from prior state).
func v7UpdateLine(t *testing.T, contractID, status string, at time.Time) string {
	t.Helper()
	row := map[string]any{
		"timestamp":   at.UTC().Format(time.RFC3339Nano),
		"contract_id": contractID,
		"status":      status,
		"summary":     nil,
		"reasoning":   "status updated",
		"owner":       nil,
		"created_by":  "test-agent",
		"source":      nil,
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal v7 update: %v", err)
	}
	return string(b)
}

// mustTime parses an RFC3339 timestamp or fails the test.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}
