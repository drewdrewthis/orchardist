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

// Adapter reads events from a directory of per-contract JSONL files on disk.
// Stateless per L9 — any caching, fold state, or watcher bookkeeping lives in
// the [Provider]; this struct is a thin wrapper around file I/O so unit tests
// can fake it cleanly.
//
// The directory is configurable. Callers typically obtain it via
// [DefaultLogDir]; tests pass an explicit t.TempDir()-rooted path.
//
// File layout (written by the claude-contracts plugin):
//
//	<dir>/<contract-id>.jsonl
//
// Each file is the append-only event log for a single contract. The first
// event is always `kind: "contract"` (creation); subsequent events are
// status_change, criterion_added, question_*, etc.
type Adapter struct {
	dir string

	// followMu protects FollowFromOffsets's per-file offset state.
	followMu sync.Mutex
}

// NewAdapter constructs an Adapter rooted at the given absolute path to a
// directory of per-contract jsonl files. The directory does not need to exist
// at construction time.
func NewAdapter(dir string) *Adapter {
	return &Adapter{dir: dir}
}

// Dir returns the absolute path the adapter scans.
func (a *Adapter) Dir() string {
	return a.dir
}

// Snapshot reads every `*.jsonl` file under the directory and returns the
// union of all events plus a map of per-file byte offsets keyed by file
// basename. Used for cold-boot hydration of the Provider's cache.
//
// Missing dir returns ([], {}, nil). Malformed lines are skipped silently.
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

// FollowFromOffsets re-scans the directory, opens each `*.jsonl` file, seeks
// to the per-file offset stored in `from`, and emits every newline-terminated
// event that appears past it. Files not present in `from` are read from zero.
//
// Returns the events seen, the updated offsets map, and the first per-file
// error encountered (if any). Callers persist the returned offsets so the next
// call resumes correctly.
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

// listFiles returns the sorted set of `*.jsonl` files directly inside the
// configured directory. A missing directory yields ([], nil).
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

// readFile opens one jsonl file, seeks to fromOffset, decodes events past it,
// and returns the events plus the byte offset reached. A missing file returns
// ([], fromOffset, nil). File rotation/truncation is detected and the read
// restarts from zero.
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
		fromOffset = 0
	}
	if size == fromOffset {
		return nil, fromOffset, nil
	}
	if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
		return nil, fromOffset, fmt.Errorf("seek %q: %w", path, err)
	}

	return readEvents(f, fromOffset)
}

// readEvents decodes newline-delimited JSON events from r, returning the
// decoded events plus the absolute byte offset reached.
func readEvents(r io.Reader, start int64) ([]Event, int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	offset := start
	var events []Event
	for {
		line, err := br.ReadBytes('\n')
		consumed := int64(len(line))
		if len(line) > 0 && line[len(line)-1] == '\n' {
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

// decodeLine parses one JSONL line into an Event. Empty or undecodable lines
// return (Event{}, false).
func decodeLine(line []byte) (Event, bool) {
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
