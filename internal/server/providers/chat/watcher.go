package chat

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

// Watcher emits a notification whenever any per-room JSONL file under
// the configured directory may have new bytes appended. The Provider
// drives a poll-from-offsets on every notification so the actual fold
// runs in the Provider's goroutine, not the watcher's.
//
// Mirrors the contracts watcher pattern. A nudge channel coalesces
// bursts (chat-core often appends member.joined + first message within
// milliseconds, fsnotify Write events arrive in clusters).
type Watcher struct {
	dir     string
	logger  *slog.Logger
	notify  chan struct{}
	stopped chan struct{}

	mu     sync.Mutex
	fsw    *fsnotify.Watcher
	closed bool
}

// NewWatcher constructs a Watcher rooted at the given chat directory.
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
// signals "some jsonl in the dir may have new data".
func (w *Watcher) Notifications() <-chan struct{} { return w.notify }

// Run starts the fsnotify loop and blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	defer close(w.stopped)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.fsw = fsw
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		_ = fsw.Close()
	}()

	// If the directory exists, watch it AND every existing JSONL.
	if err := w.attachIfExists(); err != nil {
		w.logger.Warn("chat watcher: initial attach failed", "err", err)
	}

	w.fire() // hydrate trigger

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
			w.logger.Warn("chat watcher: fsnotify error", "err", err)
		}
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	if !strings.HasSuffix(ev.Name, ".jsonl") {
		return
	}
	if ev.Op&fsnotify.Create != 0 {
		// New room file — start watching it directly so we get Writes.
		w.mu.Lock()
		if !w.closed && w.fsw != nil {
			_ = w.fsw.Add(ev.Name)
		}
		w.mu.Unlock()
	}
	w.fire()
}

func (w *Watcher) attachIfExists() error {
	if _, err := os.Stat(w.dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := w.fsw.Add(w.dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if filepath.Ext(ent.Name()) != ".jsonl" {
			continue
		}
		_ = w.fsw.Add(filepath.Join(w.dir, ent.Name()))
	}
	return nil
}

// fire writes a notification, coalescing bursts.
func (w *Watcher) fire() {
	select {
	case w.notify <- struct{}{}:
	default:
		// already pending; consumer will see the bundled change set
	}
}

// Stopped returns a channel that closes when Run returns.
func (w *Watcher) Stopped() <-chan struct{} { return w.stopped }
