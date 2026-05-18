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
// per-contract jsonl file in the directory and returns the union of their
// events.
func TestAdapter_Snapshot_DirectoryScan(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Hour)

	writeJSONL(t, filepath.Join(dir, "C-test-001.jsonl"),
		creationLine(t, "C-test-001", "deliver thing one", "alice", "session-alice", t0),
		statusChangeLine(t, "C-test-001", t1, "open", "delivered_pending_validation", "owner_judge_pass"),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-002.jsonl"),
		creationLine(t, "C-test-002", "thing two stays open", "bob", "session-bob", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-003.jsonl"),
		creationLine(t, "C-test-003", "abandoned thing", "carol", "session-carol", t0),
		statusChangeLine(t, "C-test-003", t1, "open", "cancelled", "drew_cancel"),
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
		status ContractStatus
	}{
		{"C-test-001", StatusDeliveredPendingValidation},
		{"C-test-002", StatusOpen},
		{"C-test-003", StatusCancelled},
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

	for _, name := range []string{"C-test-001.jsonl", "C-test-002.jsonl", "C-test-003.jsonl"} {
		if offsets[name] == 0 {
			t.Errorf("offsets[%q] = 0, want > 0", name)
		}
	}
}

// TestAdapter_Snapshot_EmptyDir asserts the empty-directory case returns no
// events and no error.
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

// TestAdapter_Snapshot_MissingDir asserts a non-existent directory returns no
// events and no error.
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

// TestAdapter_FollowFromOffsets_ResumesPerFile asserts that appending events
// to one file yields only the new events on a follow-up call.
func TestAdapter_FollowFromOffsets_ResumesPerFile(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	writeJSONL(t, filepath.Join(dir, "C-test-aaa.jsonl"),
		creationLine(t, "C-test-aaa", "alpha", "alice", "session-alice", t0),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-bbb.jsonl"),
		creationLine(t, "C-test-bbb", "beta", "bob", "session-bob", t0),
	)

	adapter := NewAdapter(dir)
	events, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got, want := len(events), 2; got != want {
		t.Fatalf("initial events = %d, want %d", got, want)
	}

	appendJSONL(t, filepath.Join(dir, "C-test-aaa.jsonl"),
		statusChangeLine(t, "C-test-aaa", t1, "open", "delivered_pending_validation", "owner_judge_pass"),
	)
	writeJSONL(t, filepath.Join(dir, "C-test-ccc.jsonl"),
		creationLine(t, "C-test-ccc", "gamma", "carol", "session-carol", t2),
	)

	tail, advanced, err := adapter.FollowFromOffsets(context.Background(), offsets)
	if err != nil {
		t.Fatalf("FollowFromOffsets: %v", err)
	}
	if got, want := len(tail), 2; got != want {
		t.Fatalf("tail events = %d, want %d (status_change on aaa + creation of ccc)", got, want)
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

// TestAdapter_Snapshot_IgnoresNonJSONLFiles guards against .md mirrors the
// contracts plugin writes alongside each jsonl.
func TestAdapter_Snapshot_IgnoresNonJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := mustTime(t, "2026-05-04T12:00:00Z")
	writeJSONL(t, filepath.Join(dir, "C-test-001.jsonl"),
		creationLine(t, "C-test-001", "alpha", "alice", "session-alice", t0),
	)
	if err := os.WriteFile(filepath.Join(dir, "C-test-001.md"), []byte("# mirror\n"), 0o644); err != nil {
		t.Fatalf("write .md sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a contract\n"), 0o644); err != nil {
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

// Helpers

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

func creationLine(t *testing.T, id, statement, agentName, sessionID string, at time.Time) string {
	t.Helper()
	row := map[string]any{
		"kind":      "contract",
		"id":        id,
		"statement": statement,
		"owner": map[string]any{
			"session_id": sessionID,
			"agent_name": agentName,
			"vm_address": "test-host",
		},
		"reports_to": map[string]any{
			"kind":       "drew",
			"agent_name": nil,
			"vm_address": nil,
		},
		"parent_contract_id": nil,
		"created_on":         at.UTC().Format(time.RFC3339Nano),
		"updated_on":         at.UTC().Format(time.RFC3339Nano),
		"status":             "open",
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal creation: %v", err)
	}
	return string(b)
}

func statusChangeLine(t *testing.T, id string, at time.Time, from, to, trigger string) string {
	t.Helper()
	row := map[string]any{
		"kind":      "status_change",
		"id":        id,
		"timestamp": at.UTC().Format(time.RFC3339Nano),
		"by":        "system",
		"from":      from,
		"to":        to,
		"trigger":   trigger,
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal status_change: %v", err)
	}
	return string(b)
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}
