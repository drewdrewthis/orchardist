package chat

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
	"strings"
	"sync"
)

// Adapter reads chat events from a directory of per-room JSONL files
// on disk. Stateless per ADR-011 §3 — caching, fold state, and watcher
// bookkeeping live in [Provider]; this struct is a thin file-I/O
// wrapper so unit tests can fake it cleanly.
//
// File layout (written by chat-core / orchard-chat CLI):
//
//	<dir>/<room>.jsonl
//
// One file per room. Each line is one [Event] discriminated by `type`.
// The first event in a typical room is `member.joined`; chat-core does
// NOT auto-join, so a freshly-created room file may begin with a
// `message` line if the sender pre-joined.
type Adapter struct {
	dir      string
	followMu sync.Mutex
}

// NewAdapter constructs an Adapter rooted at the given absolute path.
// The directory does not need to exist at construction; Snapshot
// returns empty results until chat-core creates the first file.
func NewAdapter(dir string) *Adapter { return &Adapter{dir: dir} }

// Dir returns the absolute path the adapter scans.
func (a *Adapter) Dir() string { return a.dir }

// Snapshot reads every `*.jsonl` file under the directory and returns
// the union of all events partitioned by RoomID, plus a map of
// per-file byte offsets keyed by file basename. Used for cold-boot
// hydration of the Provider's cache.
//
// Missing dir → returns empty maps + nil. The watcher is responsible
// for triggering a re-read when the dir is created later.
//
// Malformed lines or unknown `type` values are skipped silently.
// Per-file read failures are returned in the error but do NOT abort
// the snapshot — events from other files are still returned.
func (a *Adapter) Snapshot(_ context.Context) (map[RoomID][]Event, map[string]int64, error) {
	offsets := make(map[string]int64)
	files, err := a.listFiles()
	if err != nil {
		return nil, offsets, err
	}
	out := make(map[RoomID][]Event)
	var firstErr error
	for _, file := range files {
		room := roomFromPath(file)
		evs, advanced, err := a.readFile(file, 0)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(evs) > 0 {
			out[room] = append(out[room], evs...)
		}
		offsets[filepath.Base(file)] = advanced
	}
	return out, offsets, firstErr
}

// FollowFromOffsets re-scans the directory, opens each `*.jsonl` file,
// seeks to the per-file offset stored in `from`, and emits every
// newline-terminated event past it. Files not present in `from`
// (newly created since the last call) are read from offset 0.
//
// Returns the per-room event slices and the updated offsets map.
// Callers persist the returned offsets for the next call.
func (a *Adapter) FollowFromOffsets(_ context.Context, from map[string]int64) (map[RoomID][]Event, map[string]int64, error) {
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

	out := make(map[RoomID][]Event)
	var firstErr error
	for _, file := range files {
		base := filepath.Base(file)
		room := roomFromPath(file)
		offset := updated[base]
		evs, advanced, err := a.readFile(file, offset)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(evs) > 0 {
			out[room] = append(out[room], evs...)
		}
		updated[base] = advanced
	}
	return out, updated, firstErr
}

// roomFromPath extracts the RoomID (basename without `.jsonl`).
// Preserves leading `@` so direct rooms round-trip cleanly.
func roomFromPath(path string) RoomID {
	base := filepath.Base(path)
	return RoomID(strings.TrimSuffix(base, ".jsonl"))
}

// listFiles returns the sorted set of `*.jsonl` files directly under
// the configured directory. Missing directory → ([], nil).
func (a *Adapter) listFiles() ([]string, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read chat dir %q: %w", a.dir, err)
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

// readFile opens one jsonl file, seeks to fromOffset, decodes events,
// returns the events plus the byte offset reached. Missing file →
// ([], fromOffset, nil). File rotation (size < fromOffset) → restart
// from zero.
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

// readEvents decodes newline-delimited JSON events from r, returning
// the decoded events plus the absolute byte offset reached. Stops
// cleanly at EOF; partial trailing lines stay uncommitted.
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
	if ev.Type == "" {
		return Event{}, false
	}
	return ev, true
}
