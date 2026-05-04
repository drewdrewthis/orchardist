package contracts

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Adapter reads events from the contracts JSONL log on disk. Stateless
// per ADR-011 §3 — any caching, fold state, or watcher bookkeeping
// lives in the [Provider]; this struct is a thin wrapper around file
// I/O so unit tests can fake it cleanly.
//
// The log path is configurable. Callers typically obtain it via
// [DefaultLogPath]; tests pass an explicit t.TempDir()-rooted path.
type Adapter struct {
	path string

	// followMu protects FollowFromOffset's offset state. The fold loop
	// is the only consumer in production; the mutex defends against a
	// future caller that fans the channel out to multiple readers.
	followMu sync.Mutex
}

// NewAdapter constructs an Adapter rooted at the given absolute path
// to a JSONL file. The file does not need to exist at construction time
// — Snapshot returns an empty slice and FollowFromOffset blocks until
// the first byte arrives.
func NewAdapter(path string) *Adapter {
	return &Adapter{path: path}
}

// Path returns the absolute path the adapter reads from.
func (a *Adapter) Path() string {
	return a.path
}

// Snapshot reads the entire JSONL log from start to end and returns
// every event. Used for cold-boot hydration of the Provider's cache.
//
// Missing file → returns ([], 0, nil). The watcher takes responsibility
// for triggering a re-read when the file is created later.
//
// Returns the byte offset of the end of the file along with the events.
// Callers feed the offset back to [FollowFromOffset] to resume from
// where Snapshot left off.
//
// Malformed lines (invalid JSON or events missing a kind field) are
// logged via the returned error. The good events seen so far are still
// returned alongside the error so callers can decide whether to keep
// the cache or bail out.
func (a *Adapter) Snapshot(_ context.Context) ([]Event, int64, error) {
	f, err := os.Open(a.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("open contracts log %q: %w", a.path, err)
	}
	defer func() { _ = f.Close() }()

	events, offset, err := readEvents(f, 0)
	return events, offset, err
}

// FollowFromOffset opens the JSONL file, seeks to fromOffset, and emits
// every newline-terminated event that appears past it. Returns the
// channel and the byte offset reached (callers persist this when the
// channel closes so a restart resumes correctly).
//
// The function is one-shot — it reads the file's current tail in one
// pass and returns. The caller (the [Watcher]) re-invokes it on each
// fsnotify Write event so we never hold the file open across watcher
// ticks.
//
// Errors mid-read are returned alongside any events successfully
// decoded before the error. Callers that want strict "all-or-nothing"
// reads should discard partial output on err != nil.
func (a *Adapter) FollowFromOffset(_ context.Context, fromOffset int64) ([]Event, int64, error) {
	a.followMu.Lock()
	defer a.followMu.Unlock()

	f, err := os.Open(a.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fromOffset, nil
		}
		return nil, fromOffset, fmt.Errorf("open contracts log %q: %w", a.path, err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, fromOffset, fmt.Errorf("stat contracts log %q: %w", a.path, err)
	}
	size := stat.Size()
	if size < fromOffset {
		// File rotated / truncated; restart from zero so we do not
		// miss the rewritten prefix.
		fromOffset = 0
	}
	if size == fromOffset {
		return nil, fromOffset, nil
	}
	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, fromOffset, fmt.Errorf("seek contracts log %q: %w", a.path, err)
	}

	events, advanced, err := readEvents(f, fromOffset)
	return events, advanced, err
}

// Dir returns the parent directory of the configured log path. The
// watcher uses this to attach an fsnotify subscription before the file
// itself exists (fsnotify cannot watch a missing path).
func (a *Adapter) Dir() string {
	return filepath.Dir(a.path)
}

// readEvents decodes newline-delimited JSON events from r, returning
// the decoded events plus the absolute byte offset reached (start
// offset + bytes consumed). Stops cleanly at EOF.
//
// Lines that fail to decode are skipped silently — the JSONL format is
// append-only and a half-written tail is normal during concurrent
// writes. The loop tracks the byte offset of each newline so a partial
// final line stays uncommitted until the next call sees its terminator.
func readEvents(r io.Reader, start int64) ([]Event, int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	offset := start
	var events []Event
	for {
		line, err := br.ReadBytes('\n')
		consumed := int64(len(line))
		if len(line) > 0 && line[len(line)-1] == '\n' {
			// Complete line — advance offset past the terminator.
			offset += consumed
			ev, ok := decodeLine(line)
			if ok {
				events = append(events, ev)
			}
		}
		if errors.Is(err, io.EOF) {
			return events, offset, nil
		}
		if err != nil {
			return events, offset, fmt.Errorf("read contracts log: %w", err)
		}
	}
}

// decodeLine parses one JSONL line into an Event. Empty lines and
// undecodable lines return (Event{}, false) so the caller skips them
// without aborting the whole snapshot.
func decodeLine(line []byte) (Event, bool) {
	// Trim the trailing newline; bufio leaves it on.
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if len(line) == 0 {
		return Event{}, false
	}
	var ev Event
	if err := json.Unmarshal(line, &ev); err != nil {
		return Event{}, false
	}
	if ev.Kind == "" {
		return Event{}, false
	}
	return ev, true
}
