package claudeprojects

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Watch starts a recursive fsnotify watcher rooted at the adapter's
// projects directory. fsnotify itself is non-recursive on macOS and
// recursive only on Windows, so we do the bookkeeping by hand:
//
//   - Add a watcher on the root.
//   - Walk the root once and add a watcher on every existing project
//     subdirectory. Adapter.FetchAll already enumerates the same set,
//     but we re-walk here so this method has no implicit dependency
//     on the call order.
//   - When a CREATE event fires for a directory under the root, add
//     a watcher on it.
//   - When a CREATE / WRITE / RENAME event fires for a `.jsonl` file
//     under any watched directory, emit the corresponding
//     ConversationID on the output channel.
//
// Returns the read-only channel and an error from initial setup. The
// channel is closed when ctx is cancelled or Close is called.
//
// Sends are non-blocking — a slow consumer drops events. The provider
// is the only consumer in v1 and reloads from the cache, so a dropped
// event surfaces as "next refresh sees the change", which is fine.
func (a *FSAdapter) Watch(ctx context.Context) (<-chan ConversationID, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// The root may not exist yet (fresh install); ensure it so the
	// watcher attaches and we see the first project subdir CREATE.
	if err := os.MkdirAll(a.root, 0o755); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Add(a.root); err != nil {
		_ = w.Close()
		return nil, err
	}

	// Hydrate watchers for every existing project subdir.
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
//
// Closing the underlying fsnotify watcher closes its Events and
// Errors channels, which causes runWatch to exit and the deferred
// cleanup (driven from the goroutine itself) to close watcherDone.
// We wait for that close to complete so the caller can rely on no
// further channel sends after Close returns.
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

// addExistingSubdirs walks the projects root once and attaches a
// watcher on each direct subdirectory. We do not recurse deeper —
// Claude Code keeps transcripts one level under the root.
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
			a.logger.Warn("claudeprojects: failed to watch project dir",
				"dir", dir, "err", err)
			continue
		}
	}
	return nil
}

// runWatch is the long-running goroutine that translates fsnotify
// events into ConversationID emissions. Exits on ctx cancellation or
// when the underlying fsnotify channels close.
//
// On exit, runWatch closes a.watcherDone so Close can wait on a clean
// shutdown. We keep the channel reference on the struct (rather than
// nilling it from Close) so the close happens here, in the goroutine
// that owns the lifecycle.
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
			a.logger.Warn("claudeprojects: fsnotify error", "err", err)
		}
	}
}

// handleEvent classifies one fsnotify event:
//
//   - A new directory under the root → add a watcher; do not emit a
//     ConversationID (no transcripts yet).
//   - A `.jsonl` file under any watched directory → emit the
//     corresponding ConversationID.
//   - Anything else → ignore.
func (a *FSAdapter) handleEvent(ev fsnotify.Event, w *fsnotify.Watcher, ctx context.Context, out chan<- ConversationID) {
	if isDirCreate(ev) {
		// Best-effort: directory creation under the root means a new
		// project. Add a watcher; if the path is already watched
		// fsnotify ignores the duplicate.
		if filepath.Dir(ev.Name) == a.root {
			if err := w.Add(ev.Name); err != nil {
				a.logger.Warn("claudeprojects: failed to watch new project dir",
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
		// Drop on a full buffer rather than block the watcher loop.
		// Subscribers that fall behind miss this notification but
		// stay alive; the next event for the same key catches us up.
		a.logger.Warn("claudeprojects: subscriber lagging, dropping event",
			"session_uuid", id.SessionUUID)
	}
}

// isDirCreate is true when ev is a CREATE for a directory. fsnotify
// reports the path; we have to stat to decide.
//
// stat() failures are not fatal: a CREATE followed by an immediate
// REMOVE leaves no entry to stat, and the right behaviour is "not a
// dir, ignore" — which falls out of returning false on error.
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

