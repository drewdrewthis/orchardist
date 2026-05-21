package contracts

// MigrateV07ToV08 is the one-shot migration runner that upgrades per-id
// v0.7 contract JSONL files to the v0.8 model, where contract lifecycle
// events live inside the owning session's JSONL.
//
// Call order in the daemon:
//
//  1. Before the contracts provider starts watching.
//  2. Guarded by a sentinel file (.migrated-v0.8) so subsequent boots
//     are no-ops.
//
// Per-file handling:
//
//   - OPEN contract whose owning session JSONL exists: append an
//     open_contract tool_use record to that session JSONL, then remove
//     the v0.7 file.
//   - CLOSED contract: move to .archive/ untouched.
//   - OPEN contract whose owning session JSONL is missing (orphan): move
//     to .archive/ and append a note to .archive/migration-orphan.md.
//
// Idempotency beyond the sentinel: before appending an open_contract
// event, the migration scans the target session JSONL for an existing
// open_contract whose input.id matches the v0.7 contract id. A match
// means the event was already written (partial run, manual edit) and the
// append is skipped.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report holds the per-category counts from a migration run, used for
// daemon startup logging.
type Report struct {
	// Migrated is the number of open v0.7 contracts whose open_contract
	// event was successfully appended to the owning session JSONL.
	Migrated int
	// Archived is the number of closed v0.7 contracts moved to .archive/.
	Archived int
	// Orphaned is the number of open v0.7 contracts whose owning session
	// JSONL was not found; moved to .archive/ with a note.
	Orphaned int
}

const (
	// sentinelFile is created after a successful migration run.
	sentinelFile = ".migrated-v0.8"
	// archiveDir is the sub-directory that receives closed and orphan files.
	archiveDir = ".archive"
	// orphanNote is the filename for the orphan annotation inside archiveDir.
	orphanNote = "migration-orphan.md"
)

// MigrateV07ToV08 runs the one-shot v0.7→v0.8 migration.
//
// home is the user's home directory (os.UserHomeDir() in production, a
// t.TempDir()-rooted path in tests).
//
// Returns a Report with per-category counts and any error that aborted
// the run. Partial failures (e.g. a single file that couldn't be moved)
// are accumulated in the report but do not abort processing of remaining
// files.
func MigrateV07ToV08(home string) (Report, error) {
	contractsDir := filepath.Join(home, ".claude", "contracts")
	sentinel := filepath.Join(contractsDir, sentinelFile)
	projectsRoot := filepath.Join(home, ".claude", "projects")

	// Fast path: sentinel already written — skip everything.
	if _, err := os.Stat(sentinel); err == nil {
		return Report{}, nil
	}

	var rpt Report

	// Collect v0.7 contract files (*.jsonl at the top level of contractsDir,
	// skipping the .archive sub-directory and hidden files like .migrated-v0.8).
	files, err := listV07Files(contractsDir)
	if err != nil {
		return rpt, fmt.Errorf("migration: list v0.7 files: %w", err)
	}

	archive := filepath.Join(contractsDir, archiveDir)

	for _, path := range files {
		if err := processOne(path, contractsDir, archive, projectsRoot, &rpt); err != nil {
			// Log and continue — one bad file should not abort the rest.
			// The caller (daemon startup) will log the error.
			_ = err
		}
	}

	// Write sentinel with a brief summary so operators can inspect it.
	if err := writeSentinel(sentinel, rpt); err != nil {
		return rpt, fmt.Errorf("migration: write sentinel: %w", err)
	}

	return rpt, nil
}

// listV07Files returns the sorted *.jsonl paths in dir that are
// eligible for migration: not hidden, not inside a sub-directory.
func listV07Files(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil // Nothing to migrate.
		}
		return nil, err
	}

	var files []string
	for _, ent := range entries {
		if ent.IsDir() {
			continue // skip .archive and any other sub-dirs
		}
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip .migrated-v0.8, .DS_Store, etc.
		}
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}

// processOne handles one v0.7 contract file.
func processOne(path, contractsDir, archive, projectsRoot string, rpt *Report) error {
	events, err := readAllEvents(path)
	if err != nil {
		// Unreadable file — archive it as a safety measure.
		_ = ensureDir(archive)
		dest := filepath.Join(archive, filepath.Base(path))
		_ = os.Rename(path, dest)
		return fmt.Errorf("read %q: %w", path, err)
	}

	if len(events) == 0 {
		// Empty file — nothing to do; remove it so it doesn't confuse future reads.
		_ = os.Remove(path)
		return nil
	}

	// Fold to determine open vs closed.
	state := Fold(events)

	// The contract id is the first event's ID (creation record).
	contractID := events[0].ID
	if contractID == "" {
		// Malformed file — archive it.
		if err := ensureDir(archive); err != nil {
			return err
		}
		return os.Rename(path, filepath.Join(archive, filepath.Base(path)))
	}

	contract, ok := state[ContractID(contractID)]
	if !ok {
		// Fold produced no record — archive.
		if err := ensureDir(archive); err != nil {
			return err
		}
		return os.Rename(path, filepath.Join(archive, filepath.Base(path)))
	}

	if isClosedStatus(contract.Status) {
		// Closed contract → archive untouched.
		if err := ensureDir(archive); err != nil {
			return err
		}
		if err := os.Rename(path, filepath.Join(archive, filepath.Base(path))); err != nil {
			return err
		}
		rpt.Archived++
		return nil
	}

	// Open contract — locate the owning session JSONL.
	ownerSessionID := contract.OwnerSessionID
	sessionPath := findSessionJSONL(projectsRoot, ownerSessionID)

	if sessionPath == "" {
		// Orphan — archive with a note.
		if err := ensureDir(archive); err != nil {
			return err
		}
		if err := os.Rename(path, filepath.Join(archive, filepath.Base(path))); err != nil {
			return err
		}
		_ = appendOrphanNote(filepath.Join(archive, orphanNote), contractID, ownerSessionID)
		rpt.Orphaned++
		return nil
	}

	// Idempotency check: does the session JSONL already contain an
	// open_contract event for this contract id?
	if alreadyMigrated(sessionPath, contractID) {
		// Event already written — remove the v0.7 file and count as migrated.
		_ = os.Remove(path)
		rpt.Migrated++
		return nil
	}

	// Append the v0.8 open_contract tool_use event.
	createdAt := contract.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if err := appendOpenContractEvent(sessionPath, contractID, contract.Statement, createdAt); err != nil {
		return fmt.Errorf("append open_contract for %q: %w", contractID, err)
	}

	// Remove the v0.7 source file — it has been migrated.
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove migrated file %q: %w", path, err)
	}

	rpt.Migrated++
	return nil
}

// isClosedStatus returns true for any v0.7 status that is not "open".
// The full v0.7 status set is: open, delivered_pending_validation,
// delivered_pending_parent_validation, pending_drew_approval,
// awaiting_cancel_ack, waiting_external, satisfied, cancelled,
// judge_rejected_terminal. Only "open" is considered live.
func isClosedStatus(status string) bool {
	return status != "open"
}

// findSessionJSONL returns the absolute path to <projectsRoot>/*/<sessionID>.jsonl
// or "" if no such file exists.
func findSessionJSONL(projectsRoot, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	// Walk one level of project directories under projectsRoot.
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return ""
	}
	target := sessionID + ".jsonl"
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsRoot, ent.Name(), target)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// alreadyMigrated scans the session JSONL at path for an open_contract
// tool_use whose input.id matches contractID. Returns true if found.
func alreadyMigrated(sessionPath, contractID string) bool {
	f, err := os.Open(sessionPath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 128*1024), 128*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if !containsOpenContract(line, contractID) {
			continue
		}
		return true
	}
	return false
}

// containsOpenContract does a fast structural check on a JSONL line:
// is it an open_contract tool_use carrying the given contract id?
func containsOpenContract(line []byte, contractID string) bool {
	// Decode just enough to check type, role, and the first content block.
	var outer struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Input struct {
					ID string `json:"id"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &outer); err != nil {
		return false
	}
	if outer.Type != "assistant" {
		return false
	}
	for _, c := range outer.Message.Content {
		if c.Type == "tool_use" && c.Name == "open_contract" && c.Input.ID == contractID {
			return true
		}
	}
	return false
}

// appendOpenContractEvent appends a v0.8 open_contract tool_use record
// to the session JSONL at sessionPath. The shape mirrors what the MCP
// handleOpenContract function writes, so the ContractFold projection
// treats them identically.
func appendOpenContractEvent(sessionPath, contractID, deliverable string, createdAt time.Time) error {
	createdAtStr := createdAt.UTC().Format(time.RFC3339)

	// v0.8 open_contract tool_use payload (same shape as MCP main.go).
	type openContractInput struct {
		ID          string `json:"id"`
		Deliverable string `json:"deliverable"`
		CreatedAt   string `json:"createdAt"`
	}
	type messageContent struct {
		Type  string            `json:"type"`
		ID    string            `json:"id"`
		Name  string            `json:"name"`
		Input openContractInput `json:"input"`
	}
	type sessionMessage struct {
		Role    string           `json:"role"`
		Content []messageContent `json:"content"`
	}
	type sessionRecord struct {
		Type      string        `json:"type"`
		Message   sessionMessage `json:"message"`
		UUID      string        `json:"uuid"`
		Timestamp string        `json:"timestamp"`
	}

	record := sessionRecord{
		Type:      "assistant",
		Timestamp: createdAtStr,
		UUID:      contractID,
		Message: sessionMessage{
			Role: "assistant",
			Content: []messageContent{
				{
					Type: "tool_use",
					ID:   contractID,
					Name: "open_contract",
					Input: openContractInput{
						ID:          contractID,
						Deliverable: deliverable,
						CreatedAt:   createdAtStr,
					},
				},
			},
		},
	}

	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal open_contract record: %w", err)
	}

	if err := ensureDir(filepath.Dir(sessionPath)); err != nil {
		return err
	}

	f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open session jsonl %q: %w", sessionPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return fmt.Errorf("write to session jsonl %q: %w", sessionPath, err)
	}
	return nil
}

// appendOrphanNote appends a Markdown record to the orphan note file.
func appendOrphanNote(notePath, contractID, missingSessionID string) error {
	if err := ensureDir(filepath.Dir(notePath)); err != nil {
		return err
	}
	f, err := os.OpenFile(notePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	note := fmt.Sprintf(
		"## Orphan: %s\n\nContract `%s` was open at migration time but its owning session JSONL `%s.jsonl` was not found under `~/.claude/projects/`. The contract file has been archived.\n\n",
		contractID, contractID, missingSessionID,
	)
	_, err = io.WriteString(f, note)
	return err
}

// writeSentinel creates the sentinel file with a brief JSON summary.
func writeSentinel(path string, rpt Report) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	content, err := json.Marshal(map[string]any{
		"migratedAt": time.Now().UTC().Format(time.RFC3339),
		"migrated":   rpt.Migrated,
		"archived":   rpt.Archived,
		"orphaned":   rpt.Orphaned,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

// readAllEvents reads every JSONL event from a v0.7 per-contract file.
func readAllEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	events, _, err := readEvents(f, 0)
	return events, err
}

// ensureDir creates dir and all parents with mode 0755 if not present.
func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// isNotExist is a thin wrapper so callers can skip the fs import.
func isNotExist(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, fs.ErrNotExist)
}
