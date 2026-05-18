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
// We watch:
//  1. `.git/HEAD`               — main checkout's branch / detached state.
//  2. `.git/refs/heads/`        — main checkout's head SHA per branch.
//  3. `.git/worktrees/`         — linked worktree creation / removal.
//  4. `.git/worktrees/*/HEAD`   — per-linked-worktree branch / detached state.
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

// run is the watcher event loop. Blocks until ctx is cancelled.
func (w *watcher) run(ctx context.Context) {
	defer w.close()

	if err := w.seedWatches(); err != nil {
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
			w.logger.Debug("git watcher fsnotify err", "project", w.project.ID, "err", err)
		}
	}
}

// seedWatches adds the canonical directories under `.git`.
func (w *watcher) seedWatches() error {
	gitDir, err := resolveGitDir(w.project.Dir)
	if err != nil {
		return fmt.Errorf("resolve gitdir: %w", err)
	}
	_ = w.addDir(gitDir)
	_ = w.addDir(filepath.Join(gitDir, "refs", "heads"))

	worktreesRoot := filepath.Join(gitDir, "worktrees")
	if err := w.addDir(worktreesRoot); err == nil {
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

// handleEvent translates a raw fsnotify event into invalidation notifications.
func (w *watcher) handleEvent(ev fsnotify.Event) {
	gitDir, err := resolveGitDir(w.project.Dir)
	if err != nil {
		w.emit(NewWorktreeID(w.project.ID, MainWorktreeName))
		return
	}

	rel, ok := relUnder(gitDir, ev.Name)
	if !ok {
		return
	}

	switch {
	case rel == "HEAD" || strings.HasPrefix(rel, "refs/heads/"):
		w.emit(NewWorktreeID(w.project.ID, MainWorktreeName))
	case rel == "worktrees" || rel == "worktrees"+string(filepath.Separator):
		w.refreshWorktreesContainer(gitDir)
	case strings.HasPrefix(rel, "worktrees"+string(filepath.Separator)):
		w.handleWorktreesEvent(ev, gitDir, rel)
	}
}

// handleWorktreesEvent handles changes inside `.git/worktrees/`.
func (w *watcher) handleWorktreesEvent(ev fsnotify.Event, gitDir, rel string) {
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[0] != "worktrees" {
		return
	}
	name := parts[1]
	full := filepath.Join(gitDir, "worktrees", name)

	if len(parts) == 2 {
		switch {
		case ev.Has(fsnotify.Create):
			_ = w.addDir(full)
		case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
			w.dropDir(full)
		}
	}

	w.emit(NewWorktreeID(w.project.ID, name))
}

// refreshWorktreesContainer is invoked when `.git/worktrees/` itself changed.
func (w *watcher) refreshWorktreesContainer(gitDir string) {
	worktreesRoot := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(worktreesRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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
			w.emit(NewWorktreeID(w.project.ID, e.Name()))
		}
	}
}

// addDir registers d with fsnotify if not already watched.
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

// dropDir removes a previously-registered watch.
func (w *watcher) dropDir(d string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[d]; !ok {
		return
	}
	_ = w.fsw.Remove(d)
	delete(w.watched, d)
}

// emit pushes an invalidation, dropping under back-pressure.
func (w *watcher) emit(id WorktreeID) {
	select {
	case w.invalidations <- id:
	default:
		w.logger.Warn("git watcher invalidation dropped (channel full)",
			"project", w.project.ID, "id", id)
	}
}

// close tears down fsnotify and closes the invalidation channel. Idempotent.
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
