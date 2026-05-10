package claudeprojects

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// jsonlMeta is the cheap-to-compute summary of a JSONL transcript —
// enough to populate every Conversation field except recap.
//
// All three "interesting" timestamps (and Cwd, when available) are
// derived from the first and last record only. We never keep the
// middle of the file in memory; that is the entire point of this
// adapter.
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
// pointers so absence on any specific line falls through cleanly — we
// only need a handful, and Claude Code's JSONL has many shapes (user
// turn, assistant turn, summary record, sidechain marker, …).
//
// Timestamp uses RFC 3339Nano via *time.Time so the standard library
// parses it for us. CWD is the only string we need from the body, and
// only on the latest record we have it on (newer records carry it).
type jsonlRecord struct {
	Timestamp   *time.Time `json:"timestamp,omitempty"`
	Cwd         *string    `json:"cwd,omitempty"`
	Type        string     `json:"type,omitempty"`
	CustomTitle *string    `json:"customTitle,omitempty"`
	AgentName   *string    `json:"agentName,omitempty"`
}

// readJSONLMeta returns the metadata summary of the JSONL at path
// without reading the entire file into memory. Approach:
//
//   - stat() once to get size + modtime.
//   - count newlines by streaming the file through a fixed-size
//     buffer; we never allocate per-line.
//   - parse the first record by reading from offset 0 until the first
//     newline.
//   - parse the last record by seeking near EOF and walking backwards
//     for the last newline that precedes another (or BOF), then
//     parsing the slice between them.
//
// The function is robust to files in mid-write: a partial trailing
// line (no terminating newline) is ignored — better to under-count
// than to surface a parse error to the GraphQL caller.
//
// All three reads are independent; we do not hold a lock on the file.
// fsnotify will fire again if a write lands between passes, and the
// provider's cache will refresh.
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
		// Prefer cwd from the latest record — older transcripts may
		// only carry it on later turns.
		if last.Cwd != nil && *last.Cwd != "" {
			meta.Cwd = last.Cwd
		}
	}

	customTitle, agentName, err := readHeadMarkers(path)
	if err != nil {
		return jsonlMeta{}, fmt.Errorf("read head markers of %s: %w", path, err)
	}
	meta.CustomTitle = customTitle
	meta.AgentName = agentName

	return meta, nil
}

// countNewlines streams the file and returns the number of '\n' bytes.
//
// We deliberately count bytes rather than lines — bufio.Scanner.Scan()
// can panic on a single line longer than its max-token size, and
// Claude Code's JSONL records sometimes embed large base64 attachments
// that would overflow the default 64KB limit. Counting raw bytes is
// O(file size) but with a fixed-size 64KB buffer regardless of how
// long any single line happens to be.
//
// A trailing partial line (no '\n' at the very end of the file) is
// not counted — same as `wc -l`.
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

// readFirstRecord parses the first newline-terminated JSON record in
// path. Returns (nil, nil) when the file is empty or contains no
// terminated record.
//
// Implementation: a buffered reader with bufio.ReadBytes('\n'). We
// cap the line length at 1 MB to defend against runaway records — a
// single 1 MB log line is already absurd; refusing to parse beyond
// that is a feature, not a regression.
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

// readHeadMarkers scans the first N records of path for the JSONL marker types `custom-title` and `agent-name`. Both are written by Claude Code at session start (typically lines 2-3) and stay stable for the life of the session, so a small bounded scan is sufficient — we never load the whole file.
//
// Returns nil pointers when the markers are absent, malformed, or carry empty strings. Bounded by `maxHeadRecords`; once both markers are seen the scan returns early.
func readHeadMarkers(path string) (customTitle, agentName *string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, 64*1024)
	for i := 0; i < maxHeadRecords; i++ {
		line, lineErr := readBoundedLine(r, maxLineBytes)
		if lineErr != nil {
			if errors.Is(lineErr, io.EOF) {
				// Partial trailing line is fine — let the loop exit; it's
				// not a head marker we expect to be unterminated anyway.
				break
			}
			if errors.Is(lineErr, errLineTooLong) {
				continue
			}
			return customTitle, agentName, lineErr
		}
		rec, decErr := decodeRecord(line)
		if decErr != nil || rec == nil {
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
			break
		}
	}
	return customTitle, agentName, nil
}

// readLastRecord parses the last newline-terminated JSON record in
// path. Returns (nil, nil) when the file is empty or contains no
// terminated record.
//
// Implementation: seek to a small tail window (default 64 KB), search
// backwards for the last '\n' (which terminates the last record) and
// the second-to-last '\n' (which terminates the record before it). The
// slice between them is the last full record. If the window contains
// no newline-pair, double the window and retry, up to a hard cap so
// we never read more than a few MB to find the tail.
//
// Edge case: a single-record file. There is exactly one '\n' at the
// end (or no terminator at all). We treat the prefix (offset 0 → last
// '\n') as the record body in that case.
func readLastRecord(path string, size int64) (*jsonlRecord, error) {
	if size == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// Walk the tail in expanding chunks until we find a complete
	// trailing record or hit the hard cap.
	chunk := int64(initialTailWindow)
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

		// We want the last *complete* (newline-terminated) record. If
		// the file ends with a trailing newline we drop it so the
		// search for the prior newline points at the terminator of
		// the *last* record body. If it does not — i.e. the file ends
		// mid-write — we drop the partial trailing fragment by walking
		// back to the most recent newline and treating that as the
		// effective EOF.
		end := len(buf)
		if end > 0 && buf[end-1] == '\n' {
			end--
		} else {
			// Partial trailing line: prune it. After this, end points
			// at the position immediately after the last terminating
			// '\n', i.e. exclusive of the partial fragment.
			lastNL := bytes.LastIndexByte(buf[:end], '\n')
			if lastNL == -1 {
				// No newline at all in this window. If we have read
				// the whole file there is no complete record; if not,
				// expand the window.
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

		// The body of the last record runs from (lastInternalNewline+1)
		// up to `end`. lastInternalNewline is the most-recent '\n' we
		// can find before `end`; it terminates the record *before* the
		// last one.
		lastInternal := bytes.LastIndexByte(buf[:end], '\n')
		if lastInternal == -1 {
			// No internal newline yet — either we have only one record
			// in the entire file, or the chunk is too small. If we
			// have already read the whole file, the body runs from 0
			// to `end`.
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

const (
	// initialTailWindow is the first chunk size we read from EOF when
	// searching for the last record. 64 KB is large enough for most
	// records (Claude Code lines are typically <8 KB) and small
	// enough to be cheap.
	initialTailWindow int64 = 64 * 1024

	// maxTailWindow is the hard cap on how much of the file we will
	// re-read while hunting for the second-to-last newline. A 4 MB
	// last record is implausible enough that returning "no last
	// record" is the right answer.
	maxTailWindow int64 = 4 * 1024 * 1024

	// maxLineBytes guards readBoundedLine. Records longer than this
	// are skipped — we surface (nil, nil) to the caller, which
	// degrades to "unknown firstSeenAt/lastSeenAt" on the node.
	maxLineBytes = 1024 * 1024

	// maxHeadRecords bounds readHeadMarkers. Claude Code writes the
	// custom-title and agent-name markers at the very top of the JSONL
	// (typically lines 2-3); 16 records is generous and never reads
	// past the prologue.
	maxHeadRecords = 16
)

// errLineTooLong is the sentinel error returned by readBoundedLine
// when a single line exceeds maxLineBytes. Callers convert it to a
// soft "no record" outcome.
var errLineTooLong = errors.New("jsonl line exceeds bound")

// readBoundedLine reads up to limit bytes or until '\n', whichever
// comes first. Returns the line *without* the trailing newline.
func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	out := make([]byte, 0, 4096)
	for {
		if len(out) >= limit {
			// Drain to next newline so the reader is left in a sane
			// state, but report the bound-busting error to the caller.
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

// decodeRecord parses one JSONL line into a jsonlRecord. We tolerate
// records that don't have a `timestamp` (returns the record with a
// nil pointer) but a parse error on the JSON itself surfaces — those
// records are corrupt and the caller should know.
//
// An empty body (zero bytes after newline trimming) returns
// (nil, nil) — there is no record to decode.
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
