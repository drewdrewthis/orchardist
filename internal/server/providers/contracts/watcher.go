package contracts

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher emits a notification whenever the contracts JSONL file may
// have new bytes appended. The Provider drives a poll-from-offset on
// every notification so the actual fold runs in the Provider's
// goroutine, not the watcher's.
//
// The watcher tracks two paths:
//
//   - The log file itself, when it exists. fsnotify Write / Create /
//     Rename events all signal "re-read."
//   - The parent directory, always. fsnotify Create on the log file
//     name promotes the missing-file case to the file-watch path
//     without any polling.
//
// A nudge channel decouples bursts of events (the writer often fires
// several Writes within milliseconds of each other) — duplicate
// notifications collapse to one fold pass at the consumer side.
type Watcher struct {
	path    string
	logger  *slog.Logger
	notify  chan struct{}
	stopped chan struct{}

	mu     sync.Mutex
	fsw    *fsnotify.Watcher
	closed bool
}

// NewWatcher constructs a Watcher rooted at the given log path.
// Run must be called exactly once after construction.
func NewWatcher(path string, logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		path:    path,
		logger:  logger,
		notify:  make(chan struct{}, 1),
		stopped: make(chan struct{}),
	}
}

// Notifications returns the channel callers select on. A receive
// signals "the log file may have new data" — semantics intentionally
// fuzzy because fsnotify's CREATE/WRITE/RENAME events can arrive in
// any combination during a single append.
//
// Sends are coalesced: if a notification is pending and the consumer
// has not yet drained it, additional events are dropped.
func (w *Watcher) Notifications() <-chan struct{} {
	return w.notify
}

// Run starts the fsnotify loop and blocks until ctx is cancelled or an
// unrecoverable error occurs. It is safe to call from a goroutine; the
// returned error reflects setup failures only — runtime errors are
// logged and the loop continues.
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

	dir := filepath.Dir(w.path)
	if err := fsw.Add(dir); err != nil {
		// Parent directory missing → emit one notification so the
		// Provider attempts a snapshot (which will return empty), then
		// keep retrying every tick. In practice the parent directory
		// is created by the contracts plugin's first write.
		w.logger.Warn("contracts watcher: parent dir not watchable",
			"dir", dir, "err", err)
	}
	if err := w.attachFile(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.logger.Warn("contracts watcher: file not watchable",
			"path", w.path, "err", err)
	}

	// Best-effort initial poke — ensures the Provider runs a snapshot
	// at boot even when no fsnotify event has fired yet.
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

// Done returns a channel that closes when Run has fully exited. Useful
// for tests that want to wait on shutdown without polling.
func (w *Watcher) Done() <-chan struct{} {
	return w.stopped
}

// Stop tears down the underlying fsnotify watcher. Safe to call from
// any goroutine; idempotent. After Stop returns, [Run] is on its way
// to exiting (it observes the closed event channels and unwinds).
func (w *Watcher) Stop() {
	w.closeWatcher()
}

// Poke is a public version of poke for tests — fires a notification
// without waiting for fsnotify.
func (w *Watcher) Poke() {
	w.poke()
}

// handleEvent decides whether an fsnotify event warrants a refresh.
// Any event touching the log file (or its parent dir, when CREATE)
// triggers a notification.
func (w *Watcher) handleEvent(ev fsnotify.Event) {
	cleanPath := filepath.Clean(w.path)
	cleanEv := filepath.Clean(ev.Name)
	if cleanEv != cleanPath {
		// Parent-dir activity that is not our file. Two cases worth
		// reacting to: CREATE on a path that resolves to our file
		// (handled below) and RENAME of our file (handled by the
		// fsnotify Op flags).
		if ev.Op&fsnotify.Create != 0 && cleanEv == cleanPath {
			_ = w.attachFile()
		}
		return
	}

	if ev.Op&fsnotify.Create != 0 {
		// Promote the parent-dir watch to a real file watch so future
		// Write events fire on the file path directly.
		_ = w.attachFile()
	}
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
		w.poke()
	}
}

// attachFile adds the log file to the watcher. Returns fs.ErrNotExist
// when the file does not exist yet — caller decides how to react.
func (w *Watcher) attachFile() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fsw == nil || w.closed {
		return errors.New("watcher closed")
	}
	if _, err := os.Stat(w.path); err != nil {
		return err
	}
	return w.fsw.Add(w.path)
}

// poke pushes one signal onto the notification channel, dropping it
// silently if the channel is already full. Coalescing keeps the
// downstream fold loop from running in lockstep with every fsnotify
// event during a burst write.
func (w *Watcher) poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// closeWatcher tears down the underlying fsnotify watcher. Idempotent.
func (w *Watcher) closeWatcher() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.fsw == nil {
		return
	}
	_ = w.fsw.Close()
	w.closed = true
}
