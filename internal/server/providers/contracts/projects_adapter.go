package contracts

// projects_adapter.go provides the ProjectsAdapter that scans the
// ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl tree for session records.
//
// The adapter is the IO layer for the v0.8 ContractFold projection. It reads
// session JSONL files and returns ProjectsRecord slices that the fold
// functions (fold_v08.go) can consume without caring about file paths.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ProjectsAdapter scans a projects root directory (typically ~/.claude/projects/)
// and reads session JSONL files under it. The directory layout is:
//
//	<projectsRoot>/<encoded-cwd>/<session-uuid>.jsonl
//
// Session UUIDs are extracted from the filename (without the .jsonl suffix)
// and become each ProjectsRecord's SessionID.
//
// Stateless — all offset bookkeeping lives in the caller (the Provider). The
// adapter is a thin IO wrapper so unit tests can substitute a temp directory.
type ProjectsAdapter struct {
	root string

	followMu sync.Mutex
}

// NewProjectsAdapter returns a ProjectsAdapter rooted at projectsRoot.
func NewProjectsAdapter(projectsRoot string) *ProjectsAdapter {
	return &ProjectsAdapter{root: projectsRoot}
}

// Root returns the configured projects root directory.
func (a *ProjectsAdapter) Root() string { return a.root }

// Snapshot reads every session JSONL under the projects root and returns
// all ProjectsRecords plus per-file byte offsets (keyed by absolute path).
//
// A missing root returns ([], {}, nil) — treated the same as an empty tree.
// Per-file read errors are accumulated and the first is returned alongside
// whatever records were successfully decoded.
func (a *ProjectsAdapter) Snapshot(_ context.Context) ([]ProjectsRecord, map[string]int64, error) {
	offsets := make(map[string]int64)
	files, err := a.listFiles()
	if err != nil {
		return nil, offsets, err
	}

	var records []ProjectsRecord
	var firstErr error
	for _, f := range files {
		recs, advanced, err := a.readSessionFile(f, 0)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		records = append(records, recs...)
		offsets[f] = advanced
	}
	return records, offsets, firstErr
}

// FollowFromOffsets re-scans the projects root and returns any new records
// that appeared past the saved per-file offsets. Files not present in from
// (newly created since the last call) are read from the start.
//
// Returns the new records, the updated offsets map, and the first per-file
// error encountered (if any).
func (a *ProjectsAdapter) FollowFromOffsets(_ context.Context, from map[string]int64) ([]ProjectsRecord, map[string]int64, error) {
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

	var records []ProjectsRecord
	var firstErr error
	for _, f := range files {
		offset := updated[f]
		recs, advanced, err := a.readSessionFile(f, offset)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		records = append(records, recs...)
		updated[f] = advanced
	}
	return records, updated, firstErr
}

// listFiles returns all session JSONL paths under the projects root,
// sorted lexicographically. Only files two levels deep
// (<root>/<project>/<uuid>.jsonl) are included.
func (a *ProjectsAdapter) listFiles() ([]string, error) {
	entries, err := os.ReadDir(a.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		projectDir := filepath.Join(a.root, ent.Name())
		subEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue // unreadable project dir — skip
		}
		for _, sub := range subEntries {
			if sub.IsDir() {
				continue
			}
			name := sub.Name()
			if filepath.Ext(name) != ".jsonl" {
				continue
			}
			files = append(files, filepath.Join(projectDir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

// readSessionFile reads one session JSONL from fromOffset, decodes
// ProjectsRecords, and returns them plus the byte offset reached.
//
// The session UUID is derived from the filename (<uuid>.jsonl). If the record
// also carries a sessionId field, it takes precedence.
func (a *ProjectsAdapter) readSessionFile(path string, fromOffset int64) ([]ProjectsRecord, int64, error) {
	sessionID := sessionIDFromPath(path)

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fromOffset, nil
		}
		return nil, fromOffset, err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return nil, fromOffset, err
	}
	size := stat.Size()
	if size < fromOffset {
		fromOffset = 0 // file truncated — restart
	}
	if size == fromOffset {
		return nil, fromOffset, nil
	}
	if fromOffset > 0 {
		if _, err := f.Seek(fromOffset, io.SeekStart); err != nil {
			return nil, fromOffset, err
		}
	}

	var records []ProjectsRecord
	offset := fromOffset
	br := bufio.NewReaderSize(f, 128*1024)
	for {
		line, err := br.ReadBytes('\n')
		consumed := int64(len(line))
		if len(line) > 0 && line[len(line)-1] == '\n' {
			offset += consumed
			if trimmed := strings.TrimSpace(string(line)); len(trimmed) > 0 {
				var rec SessionRecord
				if jsonErr := json.Unmarshal([]byte(trimmed), &rec); jsonErr == nil {
					srcID := rec.SessionID
					if srcID == "" {
						srcID = sessionID
					}
					records = append(records, ProjectsRecord{
						SessionID: srcID,
						Record:    rec,
					})
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return records, offset, err
		}
	}
	return records, offset, nil
}

// sessionIDFromPath extracts the session UUID from a JSONL path.
// The filename is expected to be <session-uuid>.jsonl.
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".jsonl")
}
