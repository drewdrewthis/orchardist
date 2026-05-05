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

// Watcher emits a notification whenever any per-contract JSONL file
// under the configured directory may have new bytes appended. The
// Provider drives a poll-from-offsets on every notification so the
// actual fold runs in the Provider's goroutine, not the watcher's.
//
// The watcher tracks two layers:
//
//   - The directory itself, always. fsnotify Create on a `.jsonl`
//     file name signals a fresh contract; the watcher attaches a
//     subscription to the new file in addition to firing the notify.
//   - Each `.jsonl` file under the directory. fsnotify Write events
//     drive incremental tail reads.
//
// A nudge channel decouples bursts of events (the writer often fires
// several Writes within milliseconds of each other) — duplicate
// notifications collapse to one fold pass at the consumer side.
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
// Run must be called exactly once after construction.
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

// Notifications returns the channel callers select on. A receive
// signals "some jsonl in the dir may have new data" — semantics
// intentionally fuzzy because fsnotify's CREATE/WRITE/RENAME events
// can arrive in any combination during a single append.
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

	if err := fsw.Add(w.dir); err != nil {
		// Directory missing → emit one notification so the Provider
		// attempts a snapshot (which will return empty), then keep
		// waiting on the dir's parent. In practice the directory is
		// created by the contracts plugin's first write.
		w.logger.Warn("contracts watcher: dir not watchable",
			"dir", w.dir, "err", err)
	}
	if err := w.attachExistingFiles(); err != nil {
		w.logger.Warn("contracts watcher: failed to enumerate dir",
			"dir", w.dir, "err", err)
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
// Any event touching a `.jsonl` file under the dir, or a CREATE on a
// new `.jsonl` file, triggers a notification.
func (w *Watcher) handleEvent(ev fsnotify.Event) {
	if !strings.HasSuffix(ev.Name, ".jsonl") {
		return
	}
	if ev.Op&fsnotify.Create != 0 {
		// New jsonl file — attach a watcher so future Write events fire
		// on the file path directly, not just on the parent dir.
		_ = w.attachFile(ev.Name)
	}
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0 {
		w.poke()
	}
}

// attachExistingFiles adds an fsnotify subscription for every existing
// `*.jsonl` file under the dir, so Write events on a contract that
// existed at boot fire on the file path directly. Missing dir is not
// fatal — the parent-dir watch (also missing) takes care of CREATE.
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
				// Concurrent Stop() — bail out of the loop instead of
				// logging once per remaining file.
				return nil
			}
			w.logger.Warn("contracts watcher: file not watchable",
				"path", path, "err", err)
		}
	}
	return nil
}

// errWatcherClosed is returned by attachFile when Stop has already
// torn down the underlying fsnotify watcher. Callers can short-circuit
// remaining work rather than logging once per file.
var errWatcherClosed = errors.New("watcher closed")

// attachFile adds one jsonl file to the watcher. fsnotify ignores
// duplicate Add calls so repeated invocations are safe.
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
