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
	"sort"
	"sync"
)

// Adapter reads events from a directory of per-contract JSONL files on
// disk. Stateless per ADR-011 §3 — any caching, fold state, or watcher
// bookkeeping lives in the [Provider]; this struct is a thin wrapper
// around file I/O so unit tests can fake it cleanly.
//
// The directory is configurable. Callers typically obtain it via
// [DefaultLogDir]; tests pass an explicit t.TempDir()-rooted path.
//
// File layout (written by the claude-contracts plugin):
//
//	<dir>/<contract-id>.jsonl
//
// Each file is the append-only event log for a single contract. The
// first event is always `kind: "contract"` (creation); subsequent
// events are status_change, criterion_added, question_*, etc.
type Adapter struct {
	dir string

	// followMu protects FollowFromOffsets's per-file offset state. The
	// fold loop is the only consumer in production; the mutex defends
	// against a future caller that fans the channel out to multiple
	// readers.
	followMu sync.Mutex
}

// NewAdapter constructs an Adapter rooted at the given absolute path
// to a directory of per-contract jsonl files. The directory does not
// need to exist at construction time — Snapshot returns empty results
// and FollowFromOffsets blocks until the first file appears.
func NewAdapter(dir string) *Adapter {
	return &Adapter{dir: dir}
}

// Dir returns the absolute path the adapter scans.
func (a *Adapter) Dir() string {
	return a.dir
}

// Path is retained as an alias for [Dir] so existing diagnostics that
// asked the adapter for its location continue to compile. Returns the
// directory, not a file.
func (a *Adapter) Path() string {
	return a.dir
}

// Snapshot reads every `*.jsonl` file under the directory and returns
// the union of all events plus a map of per-file byte offsets keyed
// by file basename (e.g. `C-2026-04-27-0398e48e.jsonl` → 12345).
// Used for cold-boot hydration of the Provider's cache.
//
// Missing dir → returns ([], {}, nil). The watcher takes
// responsibility for triggering a re-read when the dir is created
// later.
//
// Returns the byte offset of the end of each file. Callers feed the
// offsets back to [FollowFromOffsets] to resume from where Snapshot
// left off.
//
// Malformed lines (invalid JSON or events missing a kind field) are
// skipped silently per the JSONL append-only convention. Per-file
// read failures are logged via the returned error but do not abort
// the snapshot — events from other files are still returned.
func (a *Adapter) Snapshot(_ context.Context) ([]Event, map[string]int64, error) {
	offsets := make(map[string]int64)
	files, err := a.listFiles()
	if err != nil {
		return nil, offsets, err
	}

	var events []Event
	var firstErr error
	for _, file := range files {
		fileEvents, advanced, err := a.readFile(file, 0)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		events = append(events, fileEvents...)
		offsets[filepath.Base(file)] = advanced
	}
	return events, offsets, firstErr
}

// FollowFromOffsets re-scans the directory, opens each `*.jsonl` file,
// seeks to the per-file offset stored in `from`, and emits every
// newline-terminated event that appears past it. Files not present in
// `from` (newly created since the last call) are read from the start.
//
// Returns the events seen, the updated offsets map, and the first
// per-file error encountered (if any). Callers persist the returned
// offsets so the next call resumes correctly.
//
// The function is one-shot — it reads the directory's current state
// in one pass and returns. The caller (the [Watcher]) re-invokes it
// on each fsnotify event so we never hold the file descriptors open
// across watcher ticks.
func (a *Adapter) FollowFromOffsets(_ context.Context, from map[string]int64) ([]Event, map[string]int64, error) {
	a.followMu.Lock()
	defer a.followMu.Unlock()

	updated := make(map[string]int64, len(from))
	for k, v := range from {
		updated[k] = v
	}

	files, err := a.listFiles()
	if err != nil {
		return nil, updated, err
	}

	var events []Event
	var firstErr error
	for _, file := range files {
		base := filepath.Base(file)
		offset := updated[base]
		fileEvents, advanced, err := a.readFile(file, offset)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		events = append(events, fileEvents...)
		updated[base] = advanced
	}
	return events, updated, firstErr
}

// listFiles returns the sorted set of `*.jsonl` files directly inside
// the configured directory. A missing directory yields ([], nil) so
// the caller treats absent and empty identically.
func (a *Adapter) listFiles() ([]string, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read contracts dir %q: %w", a.dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		files = append(files, filepath.Join(a.dir, name))
	}
	sort.Strings(files)
	return files, nil
}

// readFile opens one jsonl file, seeks to fromOffset, decodes events
// past it, and returns the events plus the byte offset reached. A
// missing file returns ([], fromOffset, nil) so a deletion between
// listFiles and readFile does not surface as an error.
//
// File rotation / truncation (size < fromOffset) is detected and the
// read restarts from zero; the new offset reflects the rewritten
// contents.
func (a *Adapter) readFile(path string, fromOffset int64) ([]Event, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fromOffset, nil
		}
		return nil, fromOffset, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, fromOffset, fmt.Errorf("stat %q: %w", path, err)
	}
	size := stat.Size()
	if size < fromOffset {
		// File rotated / truncated; restart from zero so we do not miss
		// the rewritten prefix.
		fromOffset = 0
	}
	if size == fromOffset {
		return nil, fromOffset, nil
	}
	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, fromOffset, fmt.Errorf("seek %q: %w", path, err)
	}

	events, advanced, err := readEvents(f, fromOffset)
	return events, advanced, err
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
			return events, offset, fmt.Errorf("read jsonl: %w", err)
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
