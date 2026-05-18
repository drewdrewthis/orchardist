package claudejsonls

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// FSAdapter walks the Claude Code projects root and reads metadata from
// every JSONL it finds. It is the I/O boundary for this domain; the
// Provider owns caching, the adapter owns disk access.
//
// Layout assumed:
//
//	<root>/
//	  <project-slug>/
//	    <session-uuid>.jsonl
//	    …
//
// Subdirectories two-deep are not enumerated. The watcher handles new
// project subdirs appearing at runtime.
type FSAdapter struct {
	root   string
	hostID string
	logger *slog.Logger

	// Watcher state — owned by Watch / Close. Held on the struct so
	// Close (called during teardown) can shut the goroutine down.
	watcherMu   sync.Mutex
	watcher     *fsnotify.Watcher
	watcherDone chan struct{}
}

// NewFSAdapter constructs an adapter rooted at root. The root is not
// required to exist at construction time — Fetch / FetchAll degrade to
// empty results when it is missing, so a daemon that boots before the
// user has installed Claude Code does not fail.
func NewFSAdapter(root, hostID string, logger *slog.Logger) *FSAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &FSAdapter{
		root:   root,
		hostID: hostID,
		logger: logger,
	}
}

// Root returns the configured projects root. Useful for diagnostics.
func (a *FSAdapter) Root() string { return a.root }

// HostID returns the host id stamped onto every ConversationID.
func (a *FSAdapter) HostID() string { return a.hostID }

// Fetch returns one Conversation by id. O(N) scan over all .jsonl
// filenames — FetchAll is the dominant code path; Fetch is the
// cache-miss fallback.
func (a *FSAdapter) Fetch(ctx context.Context, id ConversationID) (Conversation, error) {
	if err := ctx.Err(); err != nil {
		return Conversation{}, err
	}
	if id.HostID != a.hostID {
		return Conversation{}, fmt.Errorf("unknown host id %q (this adapter serves %q)", id.HostID, a.hostID)
	}
	all, err := a.FetchAll(ctx)
	if err != nil {
		return Conversation{}, err
	}
	c, ok := all[id]
	if !ok {
		return Conversation{}, fmt.Errorf("conversation %q: %w", id.SessionUUID, fs.ErrNotExist)
	}
	return c, nil
}

// FetchAll enumerates every JSONL under the projects root. A missing
// root returns an empty map (not an error) so the daemon can boot before
// Claude Code has been used on this host. Errors reading individual
// files are logged but do not fail the whole walk.
func (a *FSAdapter) FetchAll(ctx context.Context) (map[ConversationID]Conversation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[ConversationID]Conversation)

	root, err := os.Stat(a.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("stat projects root %s: %w", a.root, err)
	}
	if !root.IsDir() {
		return nil, fmt.Errorf("projects root is not a directory: %s", a.root)
	}

	projectDirs, err := os.ReadDir(a.root)
	if err != nil {
		return nil, fmt.Errorf("read projects root %s: %w", a.root, err)
	}

	for _, dir := range projectDirs {
		if !dir.IsDir() {
			continue
		}
		projectPath := filepath.Join(a.root, dir.Name())
		entries, err := os.ReadDir(projectPath)
		if err != nil {
			a.logger.Warn("claude-jsonls: skipping unreadable project dir",
				"dir", projectPath, "err", err)
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			if !strings.HasSuffix(ent.Name(), ".jsonl") {
				continue
			}
			full := filepath.Join(projectPath, ent.Name())
			c, err := a.fromFile(full)
			if err != nil {
				a.logger.Warn("claude-jsonls: skipping unparseable transcript",
					"file", full, "err", err)
				continue
			}
			out[c.ID] = c
		}
	}
	return out, nil
}

// FetchOne reads a single transcript file and returns its summary.
// Used by the watcher hot-path: a fsnotify Write event for one file
// re-reads only that file rather than walking the whole tree.
func (a *FSAdapter) FetchOne(ctx context.Context, path string) (Conversation, error) {
	if err := ctx.Err(); err != nil {
		return Conversation{}, err
	}
	return a.fromFile(path)
}

// fromFile builds a Conversation from one transcript path.
func (a *FSAdapter) fromFile(path string) (Conversation, error) {
	meta, err := readJSONLMeta(path)
	if err != nil {
		return Conversation{}, err
	}
	id := ConversationID{
		HostID:      a.hostID,
		SessionUUID: sessionUUIDFromPath(path),
	}
	return Conversation{
		ID:           id,
		Path:         path,
		Cwd:          meta.Cwd,
		FirstSeenAt:  meta.FirstSeenAt,
		LastSeenAt:   meta.LastSeenAt,
		MessageCount: meta.MessageCount,
		CustomTitle:  meta.CustomTitle,
		AgentName:    meta.AgentName,
	}, nil
}

// Watch starts a recursive fsnotify watcher rooted at the adapter's
// projects directory. fsnotify itself is non-recursive on macOS, so we
// maintain per-subdir watchers manually.
//
// Returns a receive-only channel (RULES.md R12) that emits
// ConversationIDs when JSONL files change. The channel closes when ctx
// is cancelled or Close is called.
func (a *FSAdapter) Watch(ctx context.Context) (<-chan ConversationID, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(a.root, 0o755); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Add(a.root); err != nil {
		_ = w.Close()
		return nil, err
	}

	if err := a.addExistingSubdirs(w); err != nil {
		_ = w.Close()
		return nil, err
	}

	out := make(chan ConversationID, 16)
	a.watcherMu.Lock()
	a.watcher = w
	a.watcherDone = make(chan struct{})
	a.watcherMu.Unlock()

	go a.runWatch(ctx, w, out)
	return out, nil
}

// Close releases watcher resources. Safe to call when no watcher is
// active. Idempotent.
func (a *FSAdapter) Close() error {
	a.watcherMu.Lock()
	w := a.watcher
	done := a.watcherDone
	a.watcher = nil
	a.watcherMu.Unlock()

	if w == nil {
		return nil
	}
	err := w.Close()
	if done != nil {
		<-done
	}
	return err
}

// addExistingSubdirs attaches a watcher on each direct subdirectory of
// the projects root.
func (a *FSAdapter) addExistingSubdirs(w *fsnotify.Watcher) error {
	entries, err := os.ReadDir(a.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(a.root, ent.Name())
		if err := w.Add(dir); err != nil {
			a.logger.Warn("claude-jsonls: failed to watch project dir",
				"dir", dir, "err", err)
		}
	}
	return nil
}

// runWatch is the long-running goroutine that translates fsnotify events
// into ConversationID emissions. Exits on ctx cancellation or when the
// underlying fsnotify channels close. Per R17 it wraps its loop in a
// panic-recover handler.
func (a *FSAdapter) runWatch(ctx context.Context, w *fsnotify.Watcher, out chan<- ConversationID) {
	defer close(out)
	defer func() {
		a.watcherMu.Lock()
		done := a.watcherDone
		a.watcherDone = nil
		a.watcherMu.Unlock()
		if done != nil {
			close(done)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			a.logger.Error("claude-jsonls: watcher goroutine panicked",
				"recover", r)
		}
	}()

	const interesting = fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&interesting == 0 {
				continue
			}
			a.handleEvent(ev, w, ctx, out)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			a.logger.Warn("claude-jsonls: fsnotify error", "err", err)
		}
	}
}

// handleEvent classifies one fsnotify event:
//   - A new directory under the root → add a watcher on it.
//   - A .jsonl file under any watched directory → emit ConversationID.
//   - Anything else → ignore.
func (a *FSAdapter) handleEvent(ev fsnotify.Event, w *fsnotify.Watcher, ctx context.Context, out chan<- ConversationID) {
	if isDirCreate(ev) {
		if filepath.Dir(ev.Name) == a.root {
			if err := w.Add(ev.Name); err != nil {
				a.logger.Warn("claude-jsonls: failed to watch new project dir",
					"dir", ev.Name, "err", err)
			}
		}
		return
	}

	if !strings.HasSuffix(ev.Name, ".jsonl") {
		return
	}
	id := ConversationID{
		HostID:      a.hostID,
		SessionUUID: sessionUUIDFromPath(ev.Name),
	}
	select {
	case out <- id:
	case <-ctx.Done():
		return
	default:
		a.logger.Warn("claude-jsonls: subscriber lagging, dropping watcher event",
			"session_uuid", id.SessionUUID)
	}
}

// isDirCreate is true when ev is a CREATE for a directory.
func isDirCreate(ev fsnotify.Event) bool {
	if ev.Op&fsnotify.Create == 0 {
		return false
	}
	info, err := os.Stat(ev.Name)
	if err != nil {
		return false
	}
	return info.IsDir()
}
