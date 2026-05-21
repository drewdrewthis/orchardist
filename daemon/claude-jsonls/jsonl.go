package claudejsonls

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jsonlMeta is the cheap-to-compute summary of a JSONL transcript —
// enough to populate every Conversation field except recap.
//
// All timestamps and Cwd are derived from the first and last record
// only (plus a tail-window walk when those miss cwd). We never keep
// the middle of the file in memory; that is the entire point.
type jsonlMeta struct {
	FirstSeenAt  *time.Time
	LastSeenAt   *time.Time
	Cwd          *string
	MessageCount int64
	ModTime      time.Time
	CustomTitle  *string
	AgentName    *string
}

// jsonlRecord is the parsed shape of a single JSONL line. Fields are
// pointers so absence on any specific line is clean — we only need a
// handful, and Claude Code's JSONL has many shapes.
type jsonlRecord struct {
	Timestamp   *time.Time `json:"timestamp,omitempty"`
	Cwd         *string    `json:"cwd,omitempty"`
	Type        string     `json:"type,omitempty"`
	CustomTitle *string    `json:"customTitle,omitempty"`
	AgentName   *string    `json:"agentName,omitempty"`
}

const (
	// initialTailWindow is the first chunk size we read from EOF when
	// searching for the last record.
	initialTailWindow int64 = 64 * 1024

	// maxTailWindow caps the tail search for the last record.
	maxTailWindow int64 = 4 * 1024 * 1024

	// maxLineBytes guards readBoundedLine. Records longer than this
	// are skipped — we surface (nil, nil) to the caller.
	maxLineBytes = 1024 * 1024

	// maxLatestMarkersWindow caps how far back we read when searching
	// for the most recent custom-title/agent-name records.
	maxLatestMarkersWindow int64 = 4 * 1024 * 1024
)

// errLineTooLong is the sentinel returned by readBoundedLine when a
// single line exceeds maxLineBytes.
var errLineTooLong = errors.New("jsonl line exceeds bound")

// readJSONLMeta returns the metadata summary of the JSONL at path
// without reading the entire file into memory. It:
//
//   - stats the file for size + modtime
//   - counts newlines via a fixed-size streaming buffer (O8: no alloc per line)
//   - parses the first record from offset 0
//   - parses the last record via a tail-window walk
//   - scans the tail backwards for the latest custom-title / agent-name markers
//   - falls back to a broader tail scan for cwd when head/tail both miss it
//
// A partial trailing line (no terminating newline) is ignored — better
// to under-count than to surface a parse error to GraphQL callers.
func readJSONLMeta(path string) (jsonlMeta, error) {
	info, err := os.Stat(path)
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return jsonlMeta{}, fmt.Errorf("not a file: %s", path)
	}

	meta := jsonlMeta{ModTime: info.ModTime()}
	if info.Size() == 0 {
		return meta, nil
	}

	count, err := countNewlines(path)
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("count records in %s: %w", path, err)
	}
	meta.MessageCount = count

	first, err := readFirstRecord(path)
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("read first record of %s: %w", path, err)
	}
	if first != nil {
		meta.FirstSeenAt = first.Timestamp
		if first.Cwd != nil && *first.Cwd != "" {
			meta.Cwd = first.Cwd
		}
	}

	last, err := readLastRecord(path, info.Size())
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("read last record of %s: %w", path, err)
	}
	if last != nil {
		meta.LastSeenAt = last.Timestamp
		if last.Cwd != nil && *last.Cwd != "" {
			meta.Cwd = last.Cwd
		}
	}

	customTitle, agentName, err := readLatestMarkers(path, info.Size())
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("read latest markers of %s: %w", path, err)
	}
	meta.CustomTitle = customTitle
	meta.AgentName = agentName

	// If neither endpoint carried cwd, walk the tail to find the most
	// recent record that does. Many sessions only set cwd on substantive
	// turns (user/assistant/tool records), not on prologue/epilogue records.
	if meta.Cwd == nil {
		cwd, cwdErr := readLatestCwd(path, info.Size())
		if cwdErr != nil {
			return jsonlMeta{}, fmt.Errorf("read latest cwd of %s: %w", path, cwdErr)
		}
		meta.Cwd = cwd
	}

	return meta, nil
}

// countNewlines streams the file counting '\n' bytes. We count bytes
// rather than lines — bufio.Scanner panics on lines longer than its
// max-token size (64KB), and Claude Code's JSONL records can embed
// large base64 attachments. A trailing partial line is not counted.
func countNewlines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	const bufSize = 64 * 1024
	buf := make([]byte, bufSize)
	var count int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			count += int64(bytes.Count(buf[:n], []byte{'\n'}))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return count, nil
}

// readFirstRecord parses the first newline-terminated JSON record.
// Returns (nil, nil) when the file is empty or the first record
// exceeds maxLineBytes.
func readFirstRecord(path string) (*jsonlRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, 64*1024)
	line, err := readBoundedLine(r, maxLineBytes)
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, nil
		}
		if errors.Is(err, errLineTooLong) {
			return nil, nil
		}
		return nil, err
	}
	return decodeRecord(line)
}

// readLastRecord parses the last newline-terminated JSON record via a
// tail-window walk. Returns (nil, nil) for empty or unterminated files.
func readLastRecord(path string, size int64) (*jsonlRecord, error) {
	if size == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	chunk := initialTailWindow
	for {
		if chunk > size {
			chunk = size
		}
		off := size - chunk
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, err
		}
		buf := make([]byte, chunk)
		if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}

		end := len(buf)
		if end > 0 && buf[end-1] == '\n' {
			end--
		} else {
			lastNL := bytes.LastIndexByte(buf[:end], '\n')
			if lastNL == -1 {
				if off == 0 {
					return nil, nil
				}
				if chunk >= maxTailWindow {
					return nil, nil
				}
				chunk *= 2
				continue
			}
			end = lastNL
		}

		lastInternal := bytes.LastIndexByte(buf[:end], '\n')
		if lastInternal == -1 {
			if off == 0 {
				return decodeRecord(buf[:end])
			}
			if chunk >= maxTailWindow {
				return nil, nil
			}
			chunk *= 2
			continue
		}
		body := buf[lastInternal+1 : end]
		return decodeRecord(body)
	}
}

// readLatestMarkers scans path from the END backwards for the most
// recent custom-title and agent-name records. Claude Code may rewrite
// these mid-session; the LATEST value wins.
func readLatestMarkers(path string, size int64) (customTitle, agentName *string, err error) {
	if size == 0 {
		return nil, nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	chunk := initialTailWindow
	var sawHead bool
	for {
		if chunk > size {
			chunk = size
			sawHead = true
		}
		off := size - chunk
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, nil, err
		}
		buf := make([]byte, chunk)
		if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil, err
		}

		start := 0
		if off > 0 {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				start = i + 1
			} else {
				if chunk >= maxLatestMarkersWindow || sawHead {
					break
				}
				chunk *= 2
				continue
			}
		}

		end := len(buf)
		for end > start {
			if buf[end-1] == '\n' {
				end--
			}
			lineStart := bytes.LastIndexByte(buf[:end], '\n')
			if lineStart < start-1 {
				lineStart = start - 1
			}
			line := buf[lineStart+1 : end]
			end = lineStart + 1
			if len(line) == 0 {
				if end <= start {
					break
				}
				continue
			}
			rec, decErr := decodeRecord(line)
			if decErr != nil || rec == nil {
				if end <= start {
					break
				}
				continue
			}
			switch rec.Type {
			case "custom-title":
				if customTitle == nil && rec.CustomTitle != nil && *rec.CustomTitle != "" {
					customTitle = rec.CustomTitle
				}
			case "agent-name":
				if agentName == nil && rec.AgentName != nil && *rec.AgentName != "" {
					agentName = rec.AgentName
				}
			}
			if customTitle != nil && agentName != nil {
				return customTitle, agentName, nil
			}
			if end <= start {
				break
			}
		}

		if sawHead || chunk >= maxLatestMarkersWindow {
			break
		}
		chunk *= 2
	}
	return customTitle, agentName, nil
}

// readLatestCwd scans path from the END backwards for the most recent
// record with a non-empty cwd. Returns nil when none found.
func readLatestCwd(path string, size int64) (cwd *string, err error) {
	if size == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	chunk := initialTailWindow
	var sawHead bool
	for {
		if chunk > size {
			chunk = size
			sawHead = true
		}
		off := size - chunk
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, err
		}
		buf := make([]byte, chunk)
		if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}

		start := 0
		if off > 0 {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				start = i + 1
			} else {
				if chunk >= maxLatestMarkersWindow || sawHead {
					break
				}
				chunk *= 2
				continue
			}
		}

		end := len(buf)
		for end > start {
			if buf[end-1] == '\n' {
				end--
			}
			lineStart := bytes.LastIndexByte(buf[:end], '\n')
			if lineStart < start-1 {
				lineStart = start - 1
			}
			line := buf[lineStart+1 : end]
			end = lineStart + 1
			if len(line) == 0 {
				if end <= start {
					break
				}
				continue
			}
			rec, decErr := decodeRecord(line)
			if decErr != nil || rec == nil {
				if end <= start {
					break
				}
				continue
			}
			if rec.Cwd != nil && *rec.Cwd != "" {
				return rec.Cwd, nil
			}
			if end <= start {
				break
			}
		}

		if sawHead || chunk >= maxLatestMarkersWindow {
			break
		}
		chunk *= 2
	}
	return nil, nil
}

// readBoundedLine reads up to limit bytes or until '\n', whichever
// comes first. Returns the line without the trailing newline.
func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	out := make([]byte, 0, 4096)
	for {
		if len(out) >= limit {
			for {
				b, err := r.ReadByte()
				if err != nil {
					return out, errLineTooLong
				}
				if b == '\n' {
					return out, errLineTooLong
				}
			}
		}
		b, err := r.ReadByte()
		if err != nil {
			return out, err
		}
		if b == '\n' {
			return out, nil
		}
		out = append(out, b)
	}
}

// decodeRecord parses one JSONL line into a jsonlRecord. An empty body
// returns (nil, nil). A parse error on the JSON itself is surfaced.
func decodeRecord(line []byte) (*jsonlRecord, error) {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		return nil, nil
	}
	var rec jsonlRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, fmt.Errorf("decode jsonl record: %w", err)
	}
	return &rec, nil
}

// sessionUUIDFromPath returns the JSONL filename minus its `.jsonl`
// suffix — which is the canonical session UUID.
func sessionUUIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".jsonl")
}
