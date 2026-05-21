package contracts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v07ContractFile writes a minimal v0.7 per-contract JSONL file to the
// contracts directory. If closed is true, a status_change to "cancelled"
// is appended so the fold recognises the contract as closed.
func v07ContractFile(t *testing.T, contractsDir, contractID, sessionID string, closed bool) {
	t.Helper()
	at := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	lines := []string{
		creationLine(t, contractID, "test deliverable", "test-agent", sessionID, at),
	}
	if closed {
		closeAt := at.Add(time.Hour)
		lines = append(lines, statusChangeLine(t, contractID, closeAt, "open", "satisfied", "drew_approve"))
	}
	writeJSONL(t, filepath.Join(contractsDir, contractID+".jsonl"), lines...)
}

// sessionJSONLPath returns the path where a session JSONL would live
// under <home>/.claude/projects/<project>/<sessionID>.jsonl.
func sessionJSONLPath(home, project, sessionID string) string {
	return filepath.Join(home, ".claude", "projects", project, sessionID+".jsonl")
}

// ensureDirFor creates all parent directories for path.
func ensureDirFor(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
}

// sessionJSONLExists reports whether a file exists at path.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sessionHasOpenContract reports whether the session JSONL at path
// contains an open_contract tool_use event for the given contractID.
func sessionHasOpenContract(t *testing.T, sessionPath, contractID string) bool {
	t.Helper()
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session jsonl %s: %v", sessionPath, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 {
			continue
		}
		if containsOpenContract([]byte(line), contractID) {
			return true
		}
	}
	return false
}

// countOpenContractEvents returns how many open_contract events for
// contractID appear in the session JSONL at path.
func countOpenContractEvents(t *testing.T, sessionPath, contractID string) int {
	t.Helper()
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session jsonl %s: %v", sessionPath, err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 {
			continue
		}
		if containsOpenContract([]byte(line), contractID) {
			count++
		}
	}
	return count
}

// TestMigrateV07ToV08_OpenContract verifies the happy path: an open v0.7
// contract is migrated into the owning session's JSONL.
//
// After MigrateV07ToV08:
//   - The session JSONL contains an open_contract event for the contract id.
//   - The v0.7 contract file is removed from contracts/.
//   - The sentinel file .migrated-v0.8 exists.
func TestMigrateV07ToV08_OpenContract(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const contractID = "C-2026-05-21-aabbccdd"
	const sessionID = "S-MIGRATE-001"

	// Create a v0.7 OPEN contract file.
	v07ContractFile(t, contractsDir, contractID, sessionID, false)

	// Create the owning session JSONL (may be empty or have prior records).
	sessionPath := sessionJSONLPath(home, "-test-project", sessionID)
	ensureDirFor(t, sessionPath)
	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create session jsonl: %v", err)
	}

	rpt, err := MigrateV07ToV08(home)
	if err != nil {
		t.Fatalf("MigrateV07ToV08: %v", err)
	}

	// Report: 1 migrated, 0 archived, 0 orphaned.
	if rpt.Migrated != 1 {
		t.Errorf("report.Migrated = %d, want 1", rpt.Migrated)
	}
	if rpt.Archived != 0 {
		t.Errorf("report.Archived = %d, want 0", rpt.Archived)
	}
	if rpt.Orphaned != 0 {
		t.Errorf("report.Orphaned = %d, want 0", rpt.Orphaned)
	}

	// Session JSONL must contain the open_contract event.
	if !sessionHasOpenContract(t, sessionPath, contractID) {
		t.Errorf("session JSONL does not contain open_contract event for %s", contractID)
	}

	// v0.7 file must be gone.
	v07Path := filepath.Join(contractsDir, contractID+".jsonl")
	if fileExists(v07Path) {
		t.Errorf("v0.7 file still exists at %s after migration", v07Path)
	}

	// Sentinel must exist.
	sentinel := filepath.Join(contractsDir, sentinelFile)
	if !fileExists(sentinel) {
		t.Errorf("sentinel %s does not exist", sentinel)
	}
}

// TestMigrateV07ToV08_Idempotent verifies that a second run is a no-op.
//
// After two consecutive MigrateV07ToV08 calls on the same home:
//   - The session JSONL has exactly one open_contract event (not two).
//   - The sentinel still exists.
func TestMigrateV07ToV08_Idempotent(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const contractID = "C-2026-05-21-idem0000"
	const sessionID = "S-IDEM-001"

	v07ContractFile(t, contractsDir, contractID, sessionID, false)

	sessionPath := sessionJSONLPath(home, "-test-project", sessionID)
	ensureDirFor(t, sessionPath)
	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create session jsonl: %v", err)
	}

	// First run.
	if _, err := MigrateV07ToV08(home); err != nil {
		t.Fatalf("first MigrateV07ToV08: %v", err)
	}

	// Verify the event landed once.
	if n := countOpenContractEvents(t, sessionPath, contractID); n != 1 {
		t.Fatalf("after first run: open_contract count = %d, want 1", n)
	}

	// Second run — sentinel is present; should be a no-op.
	if _, err := MigrateV07ToV08(home); err != nil {
		t.Fatalf("second MigrateV07ToV08: %v", err)
	}

	// Still exactly one event — no duplicate.
	if n := countOpenContractEvents(t, sessionPath, contractID); n != 1 {
		t.Errorf("after second run: open_contract count = %d, want 1 (idempotent)", n)
	}
}

// TestMigrateV07ToV08_ClosedContract verifies that closed v0.7 contracts
// are moved to .archive/ without any session JSONL writes.
func TestMigrateV07ToV08_ClosedContract(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const contractID = "C-2026-05-21-clos1111"
	const sessionID = "S-CLOSED-001"

	// Create a CLOSED v0.7 contract file.
	v07ContractFile(t, contractsDir, contractID, sessionID, true)

	// Create the owning session JSONL so we can assert nothing was written.
	sessionPath := sessionJSONLPath(home, "-test-project", sessionID)
	ensureDirFor(t, sessionPath)
	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create session jsonl: %v", err)
	}

	rpt, err := MigrateV07ToV08(home)
	if err != nil {
		t.Fatalf("MigrateV07ToV08: %v", err)
	}

	// Report: 0 migrated, 1 archived.
	if rpt.Migrated != 0 {
		t.Errorf("report.Migrated = %d, want 0", rpt.Migrated)
	}
	if rpt.Archived != 1 {
		t.Errorf("report.Archived = %d, want 1", rpt.Archived)
	}

	// v0.7 file must be in .archive/.
	archivePath := filepath.Join(contractsDir, archiveDir, contractID+".jsonl")
	if !fileExists(archivePath) {
		t.Errorf("closed contract not found in archive at %s", archivePath)
	}

	// v0.7 file must be gone from the root.
	v07Path := filepath.Join(contractsDir, contractID+".jsonl")
	if fileExists(v07Path) {
		t.Errorf("v0.7 file still in contracts root at %s", v07Path)
	}

	// Session JSONL must be unchanged (no open_contract written).
	if sessionHasOpenContract(t, sessionPath, contractID) {
		t.Errorf("closed contract wrote an open_contract event into the session JSONL")
	}

	// Sentinel exists.
	sentinel := filepath.Join(contractsDir, sentinelFile)
	if !fileExists(sentinel) {
		t.Errorf("sentinel %s does not exist", sentinel)
	}
}

// TestMigrateV07ToV08_OrphanContract verifies that an open v0.7 contract
// whose owning session JSONL is not found on disk is moved to .archive/
// and the orphan note is written.
func TestMigrateV07ToV08_OrphanContract(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const contractID = "C-2026-05-21-orph2222"
	const sessionID = "S-MISSING-SESSION"

	// Open v0.7 contract — owning session JSONL does NOT exist.
	v07ContractFile(t, contractsDir, contractID, sessionID, false)
	// Intentionally do NOT create the session JSONL.

	rpt, err := MigrateV07ToV08(home)
	if err != nil {
		t.Fatalf("MigrateV07ToV08: %v", err)
	}

	// Report: 0 migrated, 0 archived, 1 orphaned.
	if rpt.Orphaned != 1 {
		t.Errorf("report.Orphaned = %d, want 1", rpt.Orphaned)
	}
	if rpt.Migrated != 0 {
		t.Errorf("report.Migrated = %d, want 0", rpt.Migrated)
	}

	// v0.7 file must be in .archive/.
	archivePath := filepath.Join(contractsDir, archiveDir, contractID+".jsonl")
	if !fileExists(archivePath) {
		t.Errorf("orphan contract not found in archive at %s", archivePath)
	}

	// v0.7 file must be gone from root.
	v07Path := filepath.Join(contractsDir, contractID+".jsonl")
	if fileExists(v07Path) {
		t.Errorf("orphan v0.7 file still in contracts root at %s", v07Path)
	}

	// Orphan note must exist and mention the contract id.
	notePath := filepath.Join(contractsDir, archiveDir, orphanNote)
	if !fileExists(notePath) {
		t.Fatalf("orphan note %s does not exist", notePath)
	}
	noteData, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("read orphan note: %v", err)
	}
	if !strings.Contains(string(noteData), contractID) {
		t.Errorf("orphan note does not mention contract id %s", contractID)
	}
	if !strings.Contains(string(noteData), sessionID) {
		t.Errorf("orphan note does not mention missing session id %s", sessionID)
	}
}

// TestMigrateV07ToV08_Mixed verifies that a directory with a mix of open,
// closed, and orphan v0.7 contracts is handled correctly in a single pass.
func TestMigrateV07ToV08_Mixed(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const (
		openID1   = "C-2026-05-21-open1111"
		openID2   = "C-2026-05-21-open2222"
		closedID  = "C-2026-05-21-clos3333"
		orphanID  = "C-2026-05-21-orph4444"
		sessionID = "S-MIX-001"
	)

	// Two open contracts with a live session.
	v07ContractFile(t, contractsDir, openID1, sessionID, false)
	v07ContractFile(t, contractsDir, openID2, sessionID, false)

	// One closed contract (session exists but should not receive an event).
	v07ContractFile(t, contractsDir, closedID, sessionID, true)

	// One orphan (no session JSONL on disk).
	const orphanSession = "S-ORPHAN-999"
	v07ContractFile(t, contractsDir, orphanID, orphanSession, false)

	// Create the live session JSONL for the open/closed contracts.
	sessionPath := sessionJSONLPath(home, "-test-project", sessionID)
	ensureDirFor(t, sessionPath)
	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create session jsonl: %v", err)
	}

	rpt, err := MigrateV07ToV08(home)
	if err != nil {
		t.Fatalf("MigrateV07ToV08: %v", err)
	}

	if rpt.Migrated != 2 {
		t.Errorf("report.Migrated = %d, want 2", rpt.Migrated)
	}
	if rpt.Archived != 1 {
		t.Errorf("report.Archived = %d, want 1", rpt.Archived)
	}
	if rpt.Orphaned != 1 {
		t.Errorf("report.Orphaned = %d, want 1", rpt.Orphaned)
	}

	// Both open contracts must appear in the session JSONL.
	if !sessionHasOpenContract(t, sessionPath, openID1) {
		t.Errorf("session JSONL missing open_contract for %s", openID1)
	}
	if !sessionHasOpenContract(t, sessionPath, openID2) {
		t.Errorf("session JSONL missing open_contract for %s", openID2)
	}

	// Closed contract must be in .archive/, not in the session JSONL.
	if sessionHasOpenContract(t, sessionPath, closedID) {
		t.Errorf("session JSONL has open_contract for closed contract %s", closedID)
	}
	closedArchive := filepath.Join(contractsDir, archiveDir, closedID+".jsonl")
	if !fileExists(closedArchive) {
		t.Errorf("closed contract not in archive at %s", closedArchive)
	}

	// Orphan must be in .archive/ and note must exist.
	orphanArchive := filepath.Join(contractsDir, archiveDir, orphanID+".jsonl")
	if !fileExists(orphanArchive) {
		t.Errorf("orphan contract not in archive at %s", orphanArchive)
	}
	notePath := filepath.Join(contractsDir, archiveDir, orphanNote)
	if !fileExists(notePath) {
		t.Errorf("orphan note missing at %s", notePath)
	}

	// All v0.7 source files must be gone from the contracts root.
	for _, id := range []string{openID1, openID2, closedID, orphanID} {
		p := filepath.Join(contractsDir, id+".jsonl")
		if fileExists(p) {
			t.Errorf("v0.7 file still in contracts root: %s", p)
		}
	}

	// Sentinel must exist.
	if !fileExists(filepath.Join(contractsDir, sentinelFile)) {
		t.Errorf("sentinel missing after mixed migration")
	}
}

// TestMigrateV07ToV08_OpenContract_EventShape verifies the exact shape
// of the appended open_contract event so the ContractFold projection
// can parse it correctly.
func TestMigrateV07ToV08_OpenContract_EventShape(t *testing.T) {
	home := t.TempDir()

	contractsDir := filepath.Join(home, ".claude", "contracts")
	if err := os.MkdirAll(contractsDir, 0o755); err != nil {
		t.Fatalf("mkdir contractsDir: %v", err)
	}

	const contractID = "C-2026-05-21-shape000"
	const sessionID = "S-SHAPE-001"

	v07ContractFile(t, contractsDir, contractID, sessionID, false)

	sessionPath := sessionJSONLPath(home, "-shape-project", sessionID)
	ensureDirFor(t, sessionPath)
	if err := os.WriteFile(sessionPath, []byte{}, 0o644); err != nil {
		t.Fatalf("create session jsonl: %v", err)
	}

	if _, err := MigrateV07ToV08(home); err != nil {
		t.Fatalf("MigrateV07ToV08: %v", err)
	}

	// Read back and parse the appended line.
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 line, got %d", len(lines))
	}

	var outer struct {
		Type    string `json:"type"`
		UUID    string `json:"uuid"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					ID          string `json:"id"`
					Deliverable string `json:"deliverable"`
					CreatedAt   string `json:"createdAt"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &outer); err != nil {
		t.Fatalf("unmarshal appended line: %v", err)
	}

	if outer.Type != "assistant" {
		t.Errorf("type = %q, want assistant", outer.Type)
	}
	if outer.UUID != contractID {
		t.Errorf("uuid = %q, want %s", outer.UUID, contractID)
	}
	if outer.Message.Role != "assistant" {
		t.Errorf("message.role = %q, want assistant", outer.Message.Role)
	}
	if len(outer.Message.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(outer.Message.Content))
	}
	c := outer.Message.Content[0]
	if c.Type != "tool_use" {
		t.Errorf("content[0].type = %q, want tool_use", c.Type)
	}
	if c.Name != "open_contract" {
		t.Errorf("content[0].name = %q, want open_contract", c.Name)
	}
	if c.Input.ID != contractID {
		t.Errorf("content[0].input.id = %q, want %s", c.Input.ID, contractID)
	}
	if c.Input.Deliverable != "test deliverable" {
		t.Errorf("content[0].input.deliverable = %q, want 'test deliverable'", c.Input.Deliverable)
	}
	if c.Input.CreatedAt == "" {
		t.Error("content[0].input.createdAt is empty, want RFC3339 timestamp")
	}
}
