package claudeinstance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// readRecordsFromPath reads and decodes all non-sidechain records from a
// jsonl file at ~/.claude/projects/<encodedCwd>/<sessionUUID>.jsonl.
//
// Sidechain records (isSidechain==true) are filtered out here so
// ClassifyState never sees sub-agent records from the parent turn.
//
// Tolerances:
//   - Missing file: returns (nil, nil) — caller treats as "no data".
//   - Partial trailing line (mid-write): scanner skips it automatically.
//   - Lines > 1 MB: skipped silently; content snapshots can be large.
//   - Malformed JSON: line skipped, scan continues.
func readRecordsFromPath(projectsDir, cwd, sessionUUID string) ([]Record, error) {
	path := filepath.Join(projectsDir, encodeCwd(cwd), sessionUUID+".jsonl")
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	const maxLine = 1024 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLine)

	var records []Record
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		r, ok := decodeLine(line)
		if !ok || r.IsSidechain {
			continue
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return records, err // partial result is still usable
	}
	return records, nil
}

// decodeLine parses one jsonl line into a Record. Returns (zero, false)
// when the line is empty or contains invalid JSON.
func decodeLine(line []byte) (Record, bool) {
	var raw struct {
		Timestamp   string      `json:"timestamp"`
		Type        string      `json:"type"`
		IsSidechain bool        `json:"isSidechain"`
		Message     *Message    `json:"message"`
		Attachment  *Attachment `json:"attachment"`
		System      *SystemInfo `json:"system"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Record{}, false
	}

	var ts time.Time
	if raw.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
			ts = t
		} else if t, err := time.Parse(time.RFC3339, raw.Timestamp); err == nil {
			ts = t
		}
	}

	return Record{
		Timestamp:   ts,
		Type:        raw.Type,
		IsSidechain: raw.IsSidechain,
		Message:     raw.Message,
		Attachment:  raw.Attachment,
		System:      raw.System,
	}, true
}
