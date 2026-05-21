package contracts

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// ProjectsWatcher emits a notification whenever any session JSONL file
// under <root>/<subdir>/*.jsonl may have new bytes appended. The layout
// is the two-level ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl tree.
//
// The watcher tracks three layers:
//
//   - The root directory: fsnotify Create on a new <encoded-cwd> subdir
//     signals a new project; the watcher subscribes to the subdir.
//   - Each project subdir: fsnotify Create on a <uuid>.jsonl signals a
//     new session; the watcher subscribes to the file.
//   - Each .jsonl file: fsnotify Write drives incremental tail reads.
//
// A nudge channel decouples bursts of events (the writer fires several
// Writes within milliseconds of each other) — duplicate notifications
// collapse to one fold pass at the consumer side. Same design as Watcher.
type ProjectsWatcher struct {
	root    string
	logger  *slog.Logger
	notify  chan struct{}
	stopped chan struct{}

	mu     sync.Mutex
	fsw    *fsnotify.Watcher
	closed bool
}

// NewProjectsWatcher constructs a ProjectsWatcher rooted at the given
// projects directory. Run must be called exactly once after construction.
func NewProjectsWatcher(root string, logger *slog.Logger) *ProjectsWatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProjectsWatcher{
		root:    root,
		logger:  logger,
		notify:  make(chan struct{}, 1),
		stopped: make(chan struct{}),
	}
}

// Notifications returns the channel callers select on. A receive signals
// "some session jsonl in the projects tree may have new data."
func (w *ProjectsWatcher) Notifications() <-chan struct{} {
	return w.notify
}

// Run starts the fsnotify loop and blocks until ctx is cancelled or an
// unrecoverable error occurs.
func (w *ProjectsWatcher) Run(ctx context.Context) error {
	defer close(w.stopped)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.fsw = fsw
	w.closed = false
	w.mu.Unlock()
	defer w.closeWatcher()

	// Watch the root. Missing root is non-fatal — we wait.
	if err := fsw.Add(w.root); err != nil {
		w.logger.Warn("projects watcher: root not watchable",
			"root", w.root, "err", err)
	}

	// Subscribe to all existing project subdirs and their jsonl files.
	if err := w.attachExistingTree(); err != nil {
		w.logger.Warn("projects watcher: failed to enumerate tree",
			"root", w.root, "err", err)
	}

	// Initial poke — ensures the provider runs a snapshot at boot even
	// when no fsnotify event has fired yet.
	w.poke()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ev)
		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			w.logger.Warn("projects watcher: fsnotify error", "err", err)
		}
	}
}

// Done returns a channel that closes when Run has fully exited.
func (w *ProjectsWatcher) Done() <-chan struct{} {
	return w.stopped
}

// Stop tears down the underlying fsnotify watcher. Safe from any goroutine;
// idempotent.
func (w *ProjectsWatcher) Stop() {
	w.closeWatcher()
}

// Poke is a public version of poke for tests.
func (w *ProjectsWatcher) Poke() {
	w.poke()
}

// handleEvent decides whether an fsnotify event warrants a refresh.
// Any Write on a .jsonl file, or a Create of a new .jsonl or subdir,
// triggers a notification. New subdirs and files are subscribed.
func (w *ProjectsWatcher) handleEvent(ev fsnotify.Event) {
	name := ev.Name

	if ev.Op&fsnotify.Create != 0 {
		info, statErr := os.Stat(name)
		if statErr == nil && info.IsDir() {
			// New project subdir — subscribe to it so future .jsonl creates fire.
			if err := w.attachDir(name); err != nil && !errors.Is(err, errWatcherClosed) {
				w.logger.Warn("projects watcher: failed to attach new subdir",
					"path", name, "err", err)
			}
			w.poke()
			return
		}
		if strings.HasSuffix(name, ".jsonl") {
			// New session jsonl — subscribe and poke.
			if err := w.attachPath(name); err != nil && !errors.Is(err, errWatcherClosed) {
				w.logger.Warn("projects watcher: failed to attach new jsonl",
					"path", name, "err", err)
			}
			w.poke()
			return
		}
	}

	if ev.Op&(fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0 {
		if strings.HasSuffix(name, ".jsonl") {
			w.poke()
		}
	}
}

// attachExistingTree subscribes to all project subdirs and their jsonl
// files that exist at the time of Run. A missing root is not fatal.
func (w *ProjectsWatcher) attachExistingTree() error {
	subdirs, err := os.ReadDir(w.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, sub := range subdirs {
		if !sub.IsDir() {
			continue
		}
		subpath := filepath.Join(w.root, sub.Name())
		if err := w.attachDir(subpath); err != nil {
			if errors.Is(err, errWatcherClosed) {
				return nil
			}
			w.logger.Warn("projects watcher: subdir not watchable",
				"path", subpath, "err", err)
			continue
		}
		// Also attach individual jsonl files so Write events fire on the
		// file path, not just on the parent dir.
		if err := w.attachJsonlFiles(subpath); err != nil && !errors.Is(err, errWatcherClosed) {
			w.logger.Warn("projects watcher: failed to attach jsonl files",
				"subdir", subpath, "err", err)
		}
	}
	return nil
}

// attachJsonlFiles subscribes to every existing .jsonl file inside subdir.
func (w *ProjectsWatcher) attachJsonlFiles(subdir string) error {
	entries, err := os.ReadDir(subdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if filepath.Ext(ent.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(subdir, ent.Name())
		if err := w.attachPath(path); err != nil {
			if errors.Is(err, errWatcherClosed) {
				return nil
			}
			w.logger.Warn("projects watcher: file not watchable",
				"path", path, "err", err)
		}
	}
	return nil
}

// errWatcherClosed is returned by attachDir/attachPath when Stop has already
// torn down the underlying fsnotify watcher. Callers can short-circuit
// remaining work rather than logging once per file.
var errWatcherClosed = errors.New("watcher closed")

// attachDir adds a directory to the fsnotify watcher. Idempotent.
func (w *ProjectsWatcher) attachDir(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fsw == nil || w.closed {
		return errWatcherClosed
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return w.fsw.Add(path)
}

// attachPath adds a file path to the fsnotify watcher. Idempotent.
func (w *ProjectsWatcher) attachPath(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fsw == nil || w.closed {
		return errWatcherClosed
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return w.fsw.Add(path)
}

// poke pushes one signal onto the notification channel, dropping it
// silently if the channel is already full.
func (w *ProjectsWatcher) poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// closeWatcher tears down the underlying fsnotify watcher. Idempotent.
func (w *ProjectsWatcher) closeWatcher() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.fsw == nil {
		return
	}
	_ = w.fsw.Close()
	w.closed = true
}
