package claudeprojects

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// readLatestRecap scans the JSONL at path from END to BEGINNING and
// returns the contents of the most recent `<local-command-stdout>...
// </local-command-stdout>` block whose immediately-preceding user record
// invoked `/recap`. Returns nil when the session has not produced a
// recap.
//
// The shape we're matching:
//
//	{"type":"user","message":{"role":"user","content":"<command-name>/recap</command-name>..."},...}
//	{"type":"system","subtype":"local_command","content":"<local-command-stdout>{RECAP TEXT}</local-command-stdout>",...}
//
// We pair them by walking lines in reverse: when we see a system
// local_command record carrying a `<local-command-stdout>`, we read the
// preceding line and verify it is a user record with `<command-name>/recap`.
// This means we ignore any other slash-command output (e.g. /status, /memory)
// and only surface the recap.
//
// Why walk backwards: a session may invoke `/recap` many times; only the
// latest matters. Reading from the end short-circuits as soon as we find
// one valid (user-/recap → system-local_command) pair.
//
// Tail-window scan: read the last `maxLatestMarkersWindow` bytes (same
// budget the title/agent-name scanner uses); fall back to a head scan if
// the window is exhausted without a match. The recap text itself can be
// large — clip at `maxRecapBytes` to bound memory.
func readLatestRecap(path string, size int64) (*string, error) {
	if size == 0 {
		return nil, nil
	}

	if recap, found, err := scanRecapInTail(path, size); err != nil {
		return nil, err
	} else if found {
		return recap, nil
	}

	// Tail window was exhausted; fall back to a full scan.
	return scanRecapInHead(path)
}

const maxRecapBytes = 32 * 1024

// recapMarker is the wrapper Claude Code emits for slash-command stdout.
const (
	recapOpenTag  = "<local-command-stdout>"
	recapCloseTag = "</local-command-stdout>"
	recapCmdName  = "<command-name>/recap</command-name>"
)

// systemRecord is the partial shape of a `type=system` JSONL record. We
// only care about `subtype` and `content` for recap matching.
type systemRecord struct {
	Type    string `json:"type,omitempty"`
	Subtype string `json:"subtype,omitempty"`
	Content string `json:"content,omitempty"`
}

// userRecord is the partial shape of a `type=user` JSONL record. The
// `message.content` field carries the slash-command invocation text.
type userRecord struct {
	Type    string `json:"type,omitempty"`
	Message struct {
		Content string `json:"content,omitempty"`
	} `json:"message,omitempty"`
}

// scanRecapInTail reads the tail-window of the file, splits on newlines,
// and walks records in reverse looking for a recap pair.
func scanRecapInTail(path string, size int64) (*string, bool, error) {
	window := int64(maxLatestMarkersWindow)
	if window > size {
		window = size
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(size-window, io.SeekStart); err != nil {
		return nil, false, err
	}
	buf := make([]byte, window)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}

	// Drop the first partial line — we may have seeked into the middle
	// of a record. The first newline is our reliable anchor.
	if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
		buf = buf[idx+1:]
	} else {
		// No newline in the entire window — bail; head scan will retry.
		return nil, false, nil
	}

	lines := splitLines(buf)
	if recap := findRecapInReverse(lines); recap != nil {
		return recap, true, nil
	}
	return nil, false, nil
}

// scanRecapInHead walks the whole file line-by-line and tracks the most
// recent recap pair. Last-write-wins — the final pair is returned.
func scanRecapInHead(path string) (*string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, 64*1024)

	var prevUserIsRecap bool
	var latest *string
	for {
		line, err := readBoundedLine(r, maxLineBytes)
		if len(line) > 0 {
			user, isRecap := parseUserRecap(line)
			if user {
				prevUserIsRecap = isRecap
				continue
			}
			// Non-user record: pair with the preceding user record if it
			// was a /recap invocation, then reset the pairing intent
			// (we only pair a system record with the IMMEDIATELY
			// preceding user record).
			if prevUserIsRecap {
				if recap := parseSystemLocalStdout(line); recap != nil {
					latest = recap
				}
			}
			prevUserIsRecap = false
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				// Skip oversize lines but keep scanning.
				continue
			}
			return nil, err
		}
	}
	return latest, nil
}

// findRecapInReverse walks lines from last to first and returns the
// most recent recap pair's text.
func findRecapInReverse(lines [][]byte) *string {
	// Walk backwards. When we find a system local_command with a recap
	// stdout block, check the preceding line is a user /recap record.
	for i := len(lines) - 1; i > 0; i-- {
		recap := parseSystemLocalStdout(lines[i])
		if recap == nil {
			continue
		}
		// Look back one line for the user /recap that triggered it.
		if _, isRecap := parseUserRecap(lines[i-1]); isRecap {
			return recap
		}
	}
	return nil
}

// parseUserRecap returns (isUser, isRecapInvocation). When the line is
// not a user record at all, both are false. When it is a user record
// but not a /recap invocation, isUser=true and isRecapInvocation=false.
func parseUserRecap(line []byte) (isUser bool, isRecapInvocation bool) {
	if len(line) == 0 {
		return false, false
	}
	// Fast reject — every user record contains "type":"user".
	if !bytes.Contains(line, []byte(`"type":"user"`)) {
		return false, false
	}
	var u userRecord
	if err := json.Unmarshal(line, &u); err != nil {
		return false, false
	}
	if u.Type != "user" {
		return false, false
	}
	return true, strings.Contains(u.Message.Content, recapCmdName)
}

// parseSystemLocalStdout returns the recap text extracted from a
// system local_command record's content, or nil when the record is not
// of that shape.
func parseSystemLocalStdout(line []byte) *string {
	if len(line) == 0 || !bytes.Contains(line, []byte(`"type":"system"`)) {
		return nil
	}
	var s systemRecord
	if err := json.Unmarshal(line, &s); err != nil {
		return nil
	}
	if s.Type != "system" || s.Subtype != "local_command" {
		return nil
	}
	openIdx := strings.Index(s.Content, recapOpenTag)
	if openIdx == -1 {
		return nil
	}
	closeIdx := strings.LastIndex(s.Content, recapCloseTag)
	if closeIdx == -1 || closeIdx <= openIdx {
		return nil
	}
	text := s.Content[openIdx+len(recapOpenTag) : closeIdx]
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) > maxRecapBytes {
		text = text[:maxRecapBytes]
	}
	return &text
}

// splitLines splits buf on newlines. A trailing partial line (no
// terminating newline) is dropped.
func splitLines(buf []byte) [][]byte {
	rows := bytes.Split(buf, []byte{'\n'})
	if len(rows) > 0 && len(rows[len(rows)-1]) == 0 {
		rows = rows[:len(rows)-1]
	}
	return rows
}
