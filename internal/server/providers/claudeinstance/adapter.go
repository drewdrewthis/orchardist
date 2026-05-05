package claudeinstance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HeartbeatReader is the narrow I/O contract for reading heartbeat
// files. The production implementation reads `${ORCHARD_HEARTBEAT_DIR}`
// (default `${TMPDIR}` looking at `orchard-claude-*.json`); tests inject
// fakes that return a fixture slice without touching the filesystem.
//
// Stateless on purpose — caching, debouncing, and watcher state live in
// Provider, not here. ReadAll returns all heartbeats sorted by tmux
// session for deterministic test output.
type HeartbeatReader interface {
	// ReadAll returns every heartbeat the backend currently exposes.
	// Errors from a single malformed file do NOT propagate — the
	// implementation skips and continues so one stale file cannot blank
	// the whole list.
	ReadAll(ctx context.Context) ([]Heartbeat, error)

	// Dir returns the directory the reader is watching, so the watcher
	// can register fsnotify on the same path. Returned as-is so callers
	// see the resolved value, not the configured value.
	Dir() string
}

// FileReader is the production HeartbeatReader. It globs
// `<dir>/orchard-claude-*.json`, JSON-parses each, and collapses the
// disk shape into the package-internal Heartbeat struct.
//
// Inflight files (ending in `.inflight.json`) are mid-write atomic
// staging files; we skip them so a partial read never registers as a
// distinct instance.
type FileReader struct {
	dir string
}

// NewFileReader builds a FileReader rooted at dir. When dir is empty,
// it falls back to ${ORCHARD_HEARTBEAT_DIR} or, failing that, ${TMPDIR}
// — the same env-resolution chain the orchard hook script uses on the
// write side.
func NewFileReader(dir string) *FileReader {
	if dir == "" {
		dir = ResolveDir()
	}
	return &FileReader{dir: dir}
}

// ResolveDir applies the briefing's environment-variable chain:
// ORCHARD_HEARTBEAT_DIR wins; falling back to TMPDIR; falling back to
// /tmp so a misconfigured TMPDIR does not crash the daemon at boot.
func ResolveDir() string {
	if v := os.Getenv("ORCHARD_HEARTBEAT_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("TMPDIR"); v != "" {
		return v
	}
	return "/tmp"
}

// Dir reports the directory this reader globs.
func (r *FileReader) Dir() string {
	return r.dir
}

// ReadAll globs heartbeat files in r.dir and returns the parsed
// Heartbeats. Malformed JSON, unreadable files, and `.inflight.json`
// staging files are silently skipped so one bad file does not erase
// the rest. The result is sorted by TmuxSession for deterministic
// output.
func (r *FileReader) ReadAll(ctx context.Context) ([]Heartbeat, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pattern := filepath.Join(r.dir, "orchard-claude-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}

	out := make([]Heartbeat, 0, len(matches))
	for _, p := range matches {
		// Skip inflight staging files — they are partial JSON during
		// atomic rename windows.
		if strings.HasSuffix(p, ".inflight.json") {
			continue
		}
		hb, ok := parseFile(p)
		if !ok {
			continue
		}
		out = append(out, hb)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].TmuxSession < out[j].TmuxSession
	})
	return out, nil
}

// rawHeartbeat is the on-disk JSON shape. Two field-name conventions
// exist: the legacy snake_case the Rust crate writes today, and the
// camelCase shape ADR-011 §5.1 anticipates. We accept both via the
// dual struct tags below — whichever the writer chose, we read.
type rawHeartbeat struct {
	TmuxSession string `json:"tmux_session"`
	SessionID   string `json:"session_id"`
	State       string `json:"state"`
	Timestamp   string `json:"timestamp"`

	// Optional ADR-011 fields. The hook script will be updated to emit
	// these; when absent, the composer falls back to other matching.
	ClaudePid       int    `json:"claudePid,omitempty"`
	ClaudePidSnake  int    `json:"claude_pid,omitempty"`
	RcURL           string `json:"rcUrl,omitempty"`
	RcURLSnake      string `json:"rc_url,omitempty"`
	RcEnabled       *bool  `json:"rcEnabled,omitempty"`
	RcEnabledSnake  *bool  `json:"rc_enabled,omitempty"`
	LastHeartbeatAt string `json:"lastHeartbeatAt,omitempty"`
}

// parseFile reads and decodes one heartbeat file. Returns (Heartbeat,
// true) on success; (zero, false) on any read or parse failure so the
// caller can skip without aborting the whole sweep.
func parseFile(path string) (Heartbeat, bool) {
	data, err := os.ReadFile(path) // #nosec G304 — path is from a glob we control.
	if err != nil {
		return Heartbeat{}, false
	}
	var raw rawHeartbeat
	if err := json.Unmarshal(data, &raw); err != nil {
		return Heartbeat{}, false
	}

	hb := Heartbeat{
		TmuxSession: raw.TmuxSession,
		SessionID:   raw.SessionID,
		State:       raw.State,
		ClaudePid:   firstNonZero(raw.ClaudePid, raw.ClaudePidSnake),
		RcURL:       firstNonEmpty(raw.RcURL, raw.RcURLSnake),
	}
	if raw.RcEnabled != nil {
		hb.RcEnabled = *raw.RcEnabled
	} else if raw.RcEnabledSnake != nil {
		hb.RcEnabled = *raw.RcEnabledSnake
	}
	if t, err := time.Parse(time.RFC3339, raw.Timestamp); err == nil {
		hb.Timestamp = t
	} else if t, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
		hb.Timestamp = t
	}
	if t, err := time.Parse(time.RFC3339, raw.LastHeartbeatAt); err == nil {
		hb.LastHeartbeatAt = t
	} else if t, err := time.Parse(time.RFC3339Nano, raw.LastHeartbeatAt); err == nil {
		hb.LastHeartbeatAt = t
	}
	if hb.LastHeartbeatAt.IsZero() {
		hb.LastHeartbeatAt = hb.Timestamp
	}

	// A heartbeat with no tmux session is unparseable from the
	// composer's POV — drop it.
	if hb.TmuxSession == "" {
		return Heartbeat{}, false
	}
	return hb, true
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
