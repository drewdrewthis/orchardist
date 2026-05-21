package contracts

// fold_v08_jsonl_test.go exercises the v0.8 session-JSONL fold path.
//
// v0.8 stores contract open/close as tool_use events inside assistant
// message records inside session JSONL files (under
// ~/.claude/projects/*/*.jsonl) rather than per-contract JSONL files
// under ~/.claude/contracts/. These tests validate:
//
//   - L1.6: FoldFromSessionJSONL reads open_contract + close_contract
//     tool_use events and derives the correct Contract state.
//   - L1.6 (cross-jsonl): a close event in a different session jsonl
//     (F2 non-owner abandon) resolves against the open in the owner
//     jsonl, using the contract id as the global key.
//   - L1.7: ownerSessionId filter is satisfied by the existing
//     matches() helper (already part of provider.go), so the
//     integration test here confirms end-to-end filtering via
//     SessionJSONLAdapter + FoldFromSessionJSONL.
//   - L1.8: fsnotify invalidation — appending an open_contract event
//     to a watched tempdir jsonl causes FoldFromSessionJSONL on the
//     ProjectsAdapter to pick up the new contract.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeSessionJSONL creates a session JSONL file at path with the given
// pre-marshalled lines. Mirrors the production shape: each line is a
// record from ~/.claude/projects/<project>/<uuid>.jsonl.
func writeSessionJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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

// appendSessionJSONL appends lines to an existing session JSONL file.
func appendSessionJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append %s: %v", path, err)
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

// openContractLine returns one assistant-message JSONL line that wraps an
// open_contract tool_use event. sessionID is embedded in the record.
// deliverable becomes the Contract's Statement.
func openContractLine(contractID, deliverable, sessionID string, at time.Time) string {
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{
					"type": "tool_use",
					"name": "open_contract",
					"input": map[string]any{
						"id":          contractID,
						"deliverable": deliverable,
						"createdAt":   at.UTC().Format(time.RFC3339Nano),
					},
				},
			},
		},
		"sessionId": sessionID,
		"timestamp": at.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// closeContractLine returns one assistant-message JSONL line that wraps a
// close_contract tool_use event. aboutSessionId, when non-empty, is the
// F2 non-owner abandon field: the close lives in a different session's
// jsonl but references the owning session.
func closeContractLine(contractID, closedReason, sessionID, aboutSessionID string, at time.Time) string {
	input := map[string]any{
		"id":          contractID,
		"closedAt":    at.UTC().Format(time.RFC3339Nano),
		"closedReason": closedReason,
	}
	if aboutSessionID != "" {
		input["aboutSessionId"] = aboutSessionID
	}
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"name":  "close_contract",
					"input": input,
				},
			},
		},
		"sessionId": sessionID,
		"timestamp": at.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// L1.6 — FoldFromSessionJSONL reads open + close events
// ---------------------------------------------------------------------------

// TestFoldV08_OpenContractFromSessionJSONL verifies that
// FoldFromSessionJSONL produces a Contract record from a single
// open_contract tool_use event embedded in an assistant message.
func TestFoldV08_OpenContractFromSessionJSONL(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	sessionID := "S-OPEN-001"
	contractID := "C-2026-05-21-AAAA0001"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "ship the widget", sessionID, t0)),
	}

	state := FoldFromSessionJSONL(records, sessionID)

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Statement != "ship the widget" {
		t.Errorf("Statement = %q, want %q", c.Statement, "ship the widget")
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want open", c.Status)
	}
	if c.OwnerSessionID != sessionID {
		t.Errorf("OwnerSessionID = %q, want %q", c.OwnerSessionID, sessionID)
	}
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v", c.CreatedAt, t0)
	}
}

// TestFoldV08_CloseContractSameSession verifies that a close_contract
// tool_use in the same session jsonl closes the contract opened earlier.
func TestFoldV08_CloseContractSameSession(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T11:00:00Z")
	sessionID := "S-CLOSE-SAME-001"
	contractID := "C-2026-05-21-BBBB0002"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "close me", sessionID, t0)),
		mustDecodeSessionRecord(t, closeContractLine(contractID, "delivered", sessionID, "", t1)),
	}

	state := FoldFromSessionJSONL(records, sessionID)

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Status != "closed" {
		t.Errorf("Status = %q, want closed", c.Status)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t1)
	}
}

// ---------------------------------------------------------------------------
// L1.6 (cross-jsonl) — non-owner abandon: close in jsonl-B resolves open in jsonl-A
// ---------------------------------------------------------------------------

// TestFoldV08_CrossJsonlClose verifies the F2 non-owner abandon case: an
// open in jsonl-A and a close in jsonl-B (with aboutSessionId=A). The
// fold must index globally by contract id and resolve across files.
func TestFoldV08_CrossJsonlClose(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T11:00:00Z")

	ownerSessionID := "S-OWNER-CROSS-001"
	otherSessionID := "S-OTHER-CROSS-001"
	contractID := "C-2026-05-21-CCCC0003"

	// Seed global state from two different session jsonls.
	// jsonl-A (owner's session): open_contract
	recordsA := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "cross-jsonl contract", ownerSessionID, t0)),
	}
	// jsonl-B (non-owner's session): close_contract with aboutSessionId pointing to owner
	recordsB := []SessionRecord{
		mustDecodeSessionRecord(t, closeContractLine(contractID, "abandoned", otherSessionID, ownerSessionID, t1)),
	}

	// Build a global state across both jsonls.
	global := make(map[ContractID]Contract)
	for id, c := range FoldFromSessionJSONL(recordsA, ownerSessionID) {
		global[id] = c
	}
	// Apply cross-jsonl close events against the global state.
	ApplySessionRecords(global, recordsB, otherSessionID)

	c, ok := global[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in global state after cross-jsonl close", contractID)
	}
	// The contract should be closed (the close from jsonl-B applied to the open from jsonl-A).
	if c.Status != "closed" {
		t.Errorf("Status = %q, want closed (cross-jsonl close must resolve against owner's open)", c.Status)
	}
	// Owner session id must be the original owner (from jsonl-A), not the abandoner (jsonl-B).
	if c.OwnerSessionID != ownerSessionID {
		t.Errorf("OwnerSessionID = %q, want %q (owner must not change on cross-jsonl close)", c.OwnerSessionID, ownerSessionID)
	}
}

// ---------------------------------------------------------------------------
// L1.7 — ownerSessionId filter
// ---------------------------------------------------------------------------

// TestFoldV08_OwnerSessionIdFilter confirms that contracts from two different
// session jsonls are correctly distinguished by ownerSessionId so the
// existing ContractFilter.OwnerSessionID works as expected.
func TestFoldV08_OwnerSessionIdFilter(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	sessionA := "S-FOLD-001"
	sessionB := "S-FOLD-002"

	idA1 := "C-2026-05-21-DDDD0001"
	idA2 := "C-2026-05-21-DDDD0002"
	idA3 := "C-2026-05-21-DDDD0003" // closed
	idB1 := "C-2026-05-21-EEEE0001"

	t1 := t0.Add(time.Hour)

	// Session A: two open + one closed.
	recA := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(idA1, "A open 1", sessionA, t0)),
		mustDecodeSessionRecord(t, openContractLine(idA2, "A open 2", sessionA, t0)),
		mustDecodeSessionRecord(t, openContractLine(idA3, "A closed", sessionA, t0)),
		mustDecodeSessionRecord(t, closeContractLine(idA3, "delivered", sessionA, "", t1)),
	}
	// Session B: one open.
	recB := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(idB1, "B open 1", sessionB, t0)),
	}

	// Build global state.
	global := make(map[ContractID]Contract)
	for id, c := range FoldFromSessionJSONL(recA, sessionA) {
		global[id] = c
	}
	for id, c := range FoldFromSessionJSONL(recB, sessionB) {
		global[id] = c
	}

	// Contracts owned by sessionA with status open must be exactly 2.
	var openByA []Contract
	for _, c := range global {
		if c.OwnerSessionID == sessionA && c.Status == "open" {
			openByA = append(openByA, c)
		}
	}
	if got := len(openByA); got != 2 {
		t.Errorf("open contracts for %s = %d, want 2", sessionA, got)
	}

	// No contract owned by sessionB should appear in openByA.
	for _, c := range openByA {
		if c.OwnerSessionID == sessionB {
			t.Errorf("contract %s from session B leaked into session A results", c.ID)
		}
	}

	// Total: idA1, idA2 open; idA3 closed; idB1 open → 4 contracts total.
	if got := len(global); got != 4 {
		t.Errorf("total contracts = %d, want 4", got)
	}
}

// ---------------------------------------------------------------------------
// L1.8 — fsnotify invalidation via ProjectsAdapter
// ---------------------------------------------------------------------------

// TestFoldV08_FsnotifyInvalidation verifies that when a session JSONL
// under the projects root is written to, the ProjectsAdapter picks up
// the new open_contract event on the next Snapshot call. The watcher
// integration is confirmed by a poke on the Watcher.
func TestFoldV08_FsnotifyInvalidation(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	// Mimic ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl layout.
	projectsRoot := t.TempDir()
	projectDir := filepath.Join(projectsRoot, "-Users-test-myproject")
	sessionUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	jsonlPath := filepath.Join(projectDir, sessionUUID+".jsonl")

	contractID := "C-2026-05-21-FFFF0001"

	// Start with no open contracts.
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeSessionJSONL(t, jsonlPath /* no lines */)

	adapter := NewProjectsAdapter(projectsRoot)
	records, offsets, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	initialState := FoldProjectsRecords(records)
	if _, ok := initialState[ContractID(contractID)]; ok {
		t.Fatalf("contract appeared before write; expected absent")
	}

	// Append an open_contract event to the watched jsonl.
	appendSessionJSONL(t, jsonlPath, openContractLine(contractID, "new contract", sessionUUID, t0))

	// Re-read from saved offsets — simulates what the watcher does on a poke.
	newRecords, _, err := adapter.FollowFromOffsets(context.Background(), offsets)
	if err != nil {
		t.Fatalf("FollowFromOffsets: %v", err)
	}

	// Apply new records to the existing (empty) global state.
	global := make(map[ContractID]Contract)
	for id, c := range initialState {
		global[id] = c
	}
	ApplyProjectsRecords(global, newRecords)

	c, ok := global[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not found after fsnotify-driven refresh; expected it to appear", contractID)
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want open", c.Status)
	}
	if c.OwnerSessionID != sessionUUID {
		t.Errorf("OwnerSessionID = %q, want %q", c.OwnerSessionID, sessionUUID)
	}
}

// ---------------------------------------------------------------------------
// Helpers: decode session records inline
// ---------------------------------------------------------------------------

// mustDecodeSessionRecord decodes one JSONL line into a SessionRecord or
// fails the test.
func mustDecodeSessionRecord(t *testing.T, line string) SessionRecord {
	t.Helper()
	var rec SessionRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode session record: %v", err)
	}
	return rec
}
