package git

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

// watcher is a long-lived fsnotify watcher attached to a single project.
// We watch three locations relevant to worktree state:
//
//  1. `.git/HEAD`               — main checkout's branch / detached state.
//  2. `.git/refs/heads/`        — main checkout's head SHA per branch.
//  3. `.git/worktrees/`         — linked worktree creation / removal.
//  4. `.git/worktrees/*/HEAD`   — per-linked-worktree branch / detached state.
//
// fsnotify on macOS / Linux is non-recursive, so when `.git/worktrees/`
// gains a new entry we have to add a watch for the new directory; and
// when the directory itself disappears we have to drop watches.
//
// One watcher per project, reused across re-fetches (worker-standards
// §2: "fsnotify watchers reuse a single watcher per provider, not one
// per key"). The provider holds at most one watcher per project at a
// time; re-registering a project replaces the watcher.
type watcher struct {
	project Project
	fsw     *fsnotify.Watcher
	logger  *slog.Logger

	// invalidations is the channel the provider listens on. Closes when
	// the watcher is stopped.
	invalidations chan WorktreeID

	mu      sync.Mutex
	watched map[string]struct{} // dirs currently registered with fsw
	stopped bool
}

func newWatcher(project Project, logger *slog.Logger) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	w := &watcher{
		project:       project,
		fsw:           fsw,
		logger:        logger,
		invalidations: make(chan WorktreeID, 32),
		watched:       map[string]struct{}{},
	}
	return w, nil
}

// run is the watcher event loop. It blocks until ctx is cancelled or
// the underlying fsnotify watcher errors out terminally.
func (w *watcher) run(ctx context.Context) {
	defer w.close()

	if err := w.seedWatches(); err != nil {
		// A project the user registered before its `.git` exists is a
		// legitimate state (`orchard config add-repo` before `git init`)
		// — log and bail out of seeding; we re-attempt on the next call
		// to RefreshWatches once the directory shows up.
		w.logger.Warn("git watcher seed failed", "project", w.project.ID, "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// fsnotify errors during a directory's lifecycle (e.g. ENOENT
			// after a remove that we haven't yet reconciled) are routine
			// — log at debug, keep looping.
			w.logger.Debug("git watcher fsnotify err", "project", w.project.ID, "err", err)
		}
	}
}

// seedWatches adds the canonical directories under `.git`. Missing
// directories are tolerated (fresh repo, project not yet `git
// worktree`-d) and re-attempted on subsequent events.
func (w *watcher) seedWatches() error {
	gitDir, err := resolveGitDir(w.project.Dir)
	if err != nil {
		return fmt.Errorf("resolve gitdir: %w", err)
	}
	// addDir is best-effort: it logs at debug for non-existent dirs and
	// fsnotify add errors. The watcher recovers via subsequent events,
	// so seed-time errors are not propagated.
	_ = w.addDir(gitDir) // catches HEAD changes (HEAD lives at .git/HEAD)

	_ = w.addDir(filepath.Join(gitDir, "refs", "heads"))

	worktreesRoot := filepath.Join(gitDir, "worktrees")
	if err := w.addDir(worktreesRoot); err == nil {
		// Add a watch for each existing linked worktree so per-worktree
		// HEAD changes raise events.
		entries, err := os.ReadDir(worktreesRoot)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					_ = w.addDir(filepath.Join(worktreesRoot, e.Name()))
				}
			}
		}
	}
	return nil
}

// handleEvent translates a raw fsnotify event into one or more
// invalidation notifications and adjusts the watch set.
func (w *watcher) handleEvent(ev fsnotify.Event) {
	gitDir, err := resolveGitDir(w.project.Dir)
	if err != nil {
		// Repo went away mid-flight; signal a main-checkout invalidation
		// so the cache flushes and stop seeding new watches.
		w.emit(NewWorktreeID(w.project.ID, MainWorktreeName))
		return
	}

	rel, ok := relUnder(gitDir, ev.Name)
	if !ok {
		return
	}

	switch {
	case rel == "HEAD" || strings.HasPrefix(rel, "refs/heads/"):
		// Main checkout's branch or head-SHA changed.
		w.emit(NewWorktreeID(w.project.ID, MainWorktreeName))

	case rel == "worktrees" || rel == "worktrees"+string(filepath.Separator):
		// The worktrees container itself changed (created / removed).
		w.refreshWorktreesContainer(gitDir)

	case strings.HasPrefix(rel, "worktrees"+string(filepath.Separator)):
		w.handleWorktreesEvent(ev, gitDir, rel)
	}
}

// handleWorktreesEvent handles changes inside `.git/worktrees/`. Two
// shapes:
//   - `worktrees/<name>` (no further segments) — a directory was created
//     or removed; add or drop the watch and emit an invalidation for
//     the implicated worktree id.
//   - `worktrees/<name>/HEAD` (or anything below) — emit invalidation
//     for that specific worktree id.
func (w *watcher) handleWorktreesEvent(ev fsnotify.Event, gitDir, rel string) {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[0] != "worktrees" {
		return
	}
	name := parts[1]
	full := filepath.Join(gitDir, "worktrees", name)

	if len(parts) == 2 {
		// `worktrees/<name>` itself
		switch {
		case ev.Has(fsnotify.Create):
			_ = w.addDir(full)
		case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
			w.dropDir(full)
		}
	}

	w.emit(NewWorktreeID(w.project.ID, name))
}

// refreshWorktreesContainer is invoked when `.git/worktrees/` itself
// changed. It re-seeds child watches for any directory that exists
// but isn't yet watched, and drops watches for entries that are gone.
// We always emit a main-checkout invalidation in case the container
// removal corresponds to all-worktrees-gone.
func (w *watcher) refreshWorktreesContainer(gitDir string) {
	worktreesRoot := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(worktreesRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Container deleted — drop any per-worktree watches that
			// are now under it.
			w.mu.Lock()
			for d := range w.watched {
				if strings.HasPrefix(d, worktreesRoot+string(filepath.Separator)) {
					_ = w.fsw.Remove(d)
					delete(w.watched, d)
				}
			}
			w.mu.Unlock()
			return
		}
		w.logger.Debug("read worktrees dir", "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			full := filepath.Join(worktreesRoot, e.Name())
			_ = w.addDir(full)
			// And surface the (re)appearance.
			w.emit(NewWorktreeID(w.project.ID, e.Name()))
		}
	}
}

// addDir registers d with fsnotify if not already watched. Missing dirs
// return nil — the caller may retry later.
func (w *watcher) addDir(d string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[d]; ok {
		return nil
	}
	if err := w.fsw.Add(d); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		w.logger.Debug("git watcher add", "dir", d, "err", err)
		return err
	}
	w.watched[d] = struct{}{}
	return nil
}

// dropDir removes a previously-registered watch. Idempotent.
func (w *watcher) dropDir(d string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[d]; !ok {
		return
	}
	_ = w.fsw.Remove(d)
	delete(w.watched, d)
}

// emit pushes an invalidation, dropping under back-pressure rather than
// blocking the watcher loop. The store's eventual-consistency guarantee
// relies on the next FetchAll catching up, so a missed event here is
// recoverable.
func (w *watcher) emit(id WorktreeID) {
	select {
	case w.invalidations <- id:
	default:
		w.logger.Warn("git watcher invalidation dropped (channel full)",
			"project", w.project.ID, "id", id)
	}
}

// close tears down the underlying fsnotify watcher and closes the
// invalidation channel. Idempotent.
func (w *watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	_ = w.fsw.Close()
	close(w.invalidations)
}

// relUnder returns ev relative to root if ev is under root.
func relUnder(root, ev string) (string, bool) {
	rel, err := filepath.Rel(root, ev)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}
