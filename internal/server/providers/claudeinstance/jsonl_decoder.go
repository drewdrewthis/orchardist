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
//   - Partial trailing line (mid-write): silently dropped (no terminating
//     newline) — the next read picks it up.
//   - Lines > 1 MB: that single line is discarded; the loop continues with
//     the next line. We use bufio.Reader rather than bufio.Scanner because
//     Scanner.Scan() halts permanently on a token over the buffer cap,
//     dropping every record after the oversized one (verified against
//     pkg.go.dev/bufio docs).
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
	reader := bufio.NewReader(f)

	var records []Record
	for {
		line, err := reader.ReadBytes('\n')
		// ReadBytes returns the line up through '\n' (or whatever was buffered
		// at EOF). Process what we got before checking the error so a missing
		// trailing newline at EOF still gets seen.
		if len(line) > 0 && len(line) <= maxLine {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				if r, ok := decodeLine(trimmed); ok && !r.IsSidechain {
					records = append(records, r)
				}
			}
		}
		// len(line) > maxLine: discard that one line and keep reading.

		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return records, err // partial result is still usable
		}
	}
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
