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

// Watcher emits a notification whenever any per-contract JSONL file under the
// configured directory may have new bytes appended. The [Provider] drives a
// poll-from-offsets on every notification so the actual fold runs in the
// Provider's goroutine, not the watcher's.
//
// The watcher tracks two layers:
//   - The directory itself, always. fsnotify Create on a `.jsonl` file name
//     signals a fresh contract; the watcher attaches a subscription to the
//     new file in addition to firing the notify.
//   - Each `.jsonl` file under the directory. Write events drive incremental
//     tail reads.
//
// A nudge channel decouples bursts of events — duplicate notifications
// collapse to one fold pass at the consumer side.
type Watcher struct {
	dir     string
	logger  *slog.Logger
	notify  chan struct{}
	stopped chan struct{}

	mu     sync.Mutex
	fsw    *fsnotify.Watcher
	closed bool
}

// NewWatcher constructs a Watcher rooted at the given log directory.
// [Watcher.Run] must be called exactly once after construction.
func NewWatcher(dir string, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		dir:     dir,
		logger:  logger,
		notify:  make(chan struct{}, 1),
		stopped: make(chan struct{}),
	}
}

// Notifications returns the channel callers select on. A receive signals "some
// jsonl in the dir may have new data" — semantics intentionally fuzzy because
// fsnotify's CREATE/WRITE/RENAME events can arrive in any combination.
//
// Sends are coalesced: if a notification is pending and the consumer has not
// yet drained it, additional events are dropped.
func (w *Watcher) Notifications() <-chan struct{} {
	return w.notify
}

// Run starts the fsnotify loop and blocks until ctx is cancelled or an
// unrecoverable error occurs. Safe to call from a goroutine.
func (w *Watcher) Run(ctx context.Context) error {
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

	if err := fsw.Add(w.dir); err != nil {
		w.logger.Warn("contracts watcher: dir not watchable",
			"dir", w.dir, "err", err)
	}
	if err := w.attachExistingFiles(); err != nil {
		w.logger.Warn("contracts watcher: failed to enumerate dir",
			"dir", w.dir, "err", err)
	}

	// Best-effort initial poke — ensures the Provider runs a snapshot at boot
	// even when no fsnotify event has fired yet.
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
			w.logger.Warn("contracts watcher: fsnotify error", "err", err)
		}
	}
}

// Done returns a channel that closes when [Run] has fully exited.
func (w *Watcher) Done() <-chan struct{} {
	return w.stopped
}

// Stop tears down the underlying fsnotify watcher. Safe to call from any
// goroutine; idempotent.
func (w *Watcher) Stop() {
	w.closeWatcher()
}

// Poke is a public version of poke for tests — fires a notification without
// waiting for fsnotify.
func (w *Watcher) Poke() {
	w.poke()
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	if !strings.HasSuffix(ev.Name, ".jsonl") {
		return
	}
	if ev.Op&fsnotify.Create != 0 {
		_ = w.attachFile(ev.Name)
	}
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
		w.poke()
	}
}

func (w *Watcher) attachExistingFiles() error {
	entries, err := os.ReadDir(w.dir)
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
		name := ent.Name()
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		path := filepath.Join(w.dir, name)
		if err := w.attachFile(path); err != nil {
			if errors.Is(err, errWatcherClosed) {
				return nil
			}
			w.logger.Warn("contracts watcher: file not watchable",
				"path", path, "err", err)
		}
	}
	return nil
}

var errWatcherClosed = errors.New("watcher closed")

func (w *Watcher) attachFile(path string) error {
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

func (w *Watcher) poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *Watcher) closeWatcher() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.fsw == nil {
		return
	}
	_ = w.fsw.Close()
	w.closed = true
}
