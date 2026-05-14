package claudeinstance

// jsonl.go — read the last `timestamp` from a Claude session's transcript
// jsonl. Used as the authoritative source for ClaudeInstance.lastActivityAt
// when the hook script's `last_activity` field is absent (today's hook does
// not write it).
//
// Why the jsonl: every line claude writes carries an RFC3339 `timestamp`
// field, and unlike the hook (which fires on lifecycle events only), the
// jsonl is appended to on every assistant/user/system step. This keeps
// lastActivityAt precise even for long-lived sessions sitting in `input` or
// `idle` state where the hook would otherwise go stale.
//
// Layout: ~/.claude/projects/<encoded-cwd>/<session_uuid>.jsonl, where
// encoded-cwd swaps every '/' AND every '.' for '-' (so worktree paths
// like /repo/.worktrees/foo become -repo--worktrees-foo). encodeCwd here
// mirrors that exact transform; nothing else canonicalises the path so
// the function stays a pure string operation.
//
// I/O profile: one os.Open + one bounded reverse-scan per call. We read the
// last 64 KiB at most so a multi-GB transcript stays cheap; the most recent
// line is invariably within that tail. No locking is needed — claude opens,
// appends, closes per write, so any read sees a consistent line boundary.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JsonlReader reads the most recent activity timestamp from a Claude
// session's transcript jsonl. The narrow interface lets the composer fall
// back gracefully when the reader is nil (tests, missing claude home) and
// keeps test stubs trivial.
type JsonlReader interface {
	// LastActivityAt returns the timestamp of the last line in the session's
	// transcript jsonl. (zero, false) when the file is missing, empty,
	// unreadable, or the last line has no parseable timestamp. Errors are
	// silently swallowed — this is a fallback path, not a critical one.
	LastActivityAt(ctx context.Context, cwd, sessionUUID string) (time.Time, bool)
}

// SnapshotReader reads all non-sidechain records from a session jsonl and
// returns them for ClassifyState. Separate from JsonlReader so the composer
// can read full records for state derivation without duplicating the
// decode logic. Tests inject a stub; production uses FsSnapshotReader.
type SnapshotReader interface {
	// ReadSnapshot returns decoded non-sidechain records for the session
	// identified by cwd + sessionUUID. Returns (nil, false) when the file
	// does not exist; returns (records, true) even for an empty file.
	// Errors in individual lines are silently skipped per readRecordsFromPath.
	ReadSnapshot(ctx context.Context, cwd, sessionUUID string) ([]Record, bool)
}

// FsSnapshotReader is the production SnapshotReader. Resolves files under
// projectsDir (default ~/.claude/projects) on demand.
type FsSnapshotReader struct {
	projectsDir string
}

// NewFsSnapshotReader constructs a reader rooted at projectsDir. When
// empty, it resolves to ~/.claude/projects. Returns nil when the home
// directory is unresolvable.
func NewFsSnapshotReader(projectsDir string) *FsSnapshotReader {
	if projectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		projectsDir = filepath.Join(home, ".claude", "projects")
	}
	return &FsSnapshotReader{projectsDir: projectsDir}
}

// ReadSnapshot reads and decodes all non-sidechain records for the given
// cwd+sessionUUID. Returns (nil, false) when the file does not exist.
func (r *FsSnapshotReader) ReadSnapshot(_ context.Context, cwd, sessionUUID string) ([]Record, bool) {
	if r == nil || cwd == "" || sessionUUID == "" {
		return nil, false
	}
	records, err := readRecordsFromPath(r.projectsDir, cwd, sessionUUID)
	if err != nil || records == nil {
		return nil, false
	}
	return records, true
}

// FsJsonlReader is the production JsonlReader. Resolves the project root
// once at construction time (~/.claude/projects by default) and reads tail
// bytes from each transcript on demand. No caching — the composer caches
// at the ClaudeInstance level, and a stale tail timestamp is no worse than
// a stale heartbeat.
type FsJsonlReader struct {
	projectsDir string
}

// NewFsJsonlReader constructs a reader rooted at projectsDir. When empty,
// it resolves to ~/.claude/projects via os.UserHomeDir. Returns nil when
// the home directory is unresolvable so the composer's "if reader == nil"
// guards remain reliable.
func NewFsJsonlReader(projectsDir string) *FsJsonlReader {
	if projectsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		projectsDir = filepath.Join(home, ".claude", "projects")
	}
	return &FsJsonlReader{projectsDir: projectsDir}
}

// LastActivityAt resolves the transcript path from cwd+sessionUUID and
// returns its last line's timestamp. (zero, false) on any failure.
func (r *FsJsonlReader) LastActivityAt(ctx context.Context, cwd, sessionUUID string) (time.Time, bool) {
	if r == nil || cwd == "" || sessionUUID == "" {
		return time.Time{}, false
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, false
	}
	path := filepath.Join(r.projectsDir, encodeCwd(cwd), sessionUUID+".jsonl")
	return readLastTimestamp(path)
}

// encodeCwd applies claude's project-directory naming convention: every
// '/' AND every '.' in the absolute cwd becomes '-'. Verified empirically
// against ~/.claude/projects on a live host: paths containing `/.` (e.g.
// `/repo/.worktrees/foo` → `-repo--worktrees-foo`) produce a double-dash
// at the boundary because both characters map to '-'. Pure string
// operation; we do NOT resolve symlinks or normalise the path because
// claude itself does not.
func encodeCwd(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// tailWindow is the maximum number of trailing bytes we read from the
// jsonl. Claude lines are dominated by tool snapshots that can run into
// the hundreds of KiB; 64 KiB comfortably covers the most recent line on
// any sane transcript while keeping per-call I/O O(1).
const tailWindow = 64 * 1024

// readLastTimestamp opens path, reads up to tailWindow bytes from the end,
// finds the last newline-delimited line, parses it as JSON, and extracts
// the `timestamp` field. Returns (zero, false) on any failure path.
func readLastTimestamp(path string) (time.Time, bool) {
	f, err := os.Open(path) // #nosec G304 — path is composed from inputs we already trust.
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return time.Time{}, false
	}
	size := info.Size()
	if size == 0 {
		return time.Time{}, false
	}

	readFrom := int64(0)
	if size > tailWindow {
		readFrom = size - tailWindow
	}
	if _, err := f.Seek(readFrom, io.SeekStart); err != nil {
		return time.Time{}, false
	}

	buf, err := io.ReadAll(f)
	if err != nil && !errors.Is(err, fs.ErrClosed) {
		return time.Time{}, false
	}

	// Trim a trailing newline if present so LastIndexByte finds the
	// separator BEFORE the final line, not the one after it.
	buf = bytes.TrimRight(buf, "\n")
	if len(buf) == 0 {
		return time.Time{}, false
	}

	if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
		buf = buf[idx+1:]
	}

	var line struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(buf, &line); err != nil {
		return time.Time{}, false
	}
	if line.Timestamp == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, line.Timestamp); err == nil {
		return t, true
	}
	return time.Time{}, false
}
