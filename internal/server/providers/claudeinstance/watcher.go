package claudeinstance

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher drives Provider.Refresh on two complementary triggers:
//
//  1. fsnotify on the heartbeat directory — instant reaction to a hook
//     write (Create / Write / Rename / Remove).
//  2. A periodic 5s poll fallback — covers staleness (state==working
//     but no fresh heartbeat) and platforms where fsnotify either
//     drops events under load or cannot be used at all (e.g. some
//     non-recursive filesystems).
//
// The poll cadence is deliberately matched to the briefing AC: 5s gives
// orchardists "near-instant" reaction without polling so often that
// idle daemons burn cycles. Tests can shrink the interval via
// NewWatcherWith.
//
// fsnotify failures (directory missing, kernel inotify exhausted) are
// non-fatal — the watcher logs a warning and falls back to poll-only.
// This matches ADR-011 §6's "degrade, don't panic" rule and keeps the
// daemon serving stale data rather than going dark.
type Watcher struct {
	provider *Provider
	dir      string
	logger   *slog.Logger
	interval time.Duration

	closeOnce sync.Once
	doneCh    chan struct{}
}

// NewWatcher constructs a Watcher with the standard 5s poll interval.
// Callers (the daemon entry point) build one Watcher per Provider and
// call Run on it inside a goroutine bound to the daemon ctx.
func NewWatcher(p *Provider, logger *slog.Logger) *Watcher {
	return NewWatcherWith(p, logger, PollInterval)
}

// NewWatcherWith is the test-friendly constructor — accepts an
// arbitrary poll interval so tests can verify the fallback path
// converges in milliseconds.
func NewWatcherWith(p *Provider, logger *slog.Logger, interval time.Duration) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = PollInterval
	}
	dir := ""
	if p != nil && p.reader != nil {
		dir = p.reader.Dir()
	}
	return &Watcher{
		provider: p,
		dir:      dir,
		logger:   logger,
		interval: interval,
		doneCh:   make(chan struct{}),
	}
}

// Run drives the watch loop until ctx is cancelled. Returns the first
// fatal error if the loop cannot continue (currently only when ctx
// closes — fsnotify failures degrade to poll-only without exiting).
func (w *Watcher) Run(ctx context.Context) error {
	defer w.closeOnce.Do(func() { close(w.doneCh) })

	// Trigger one refresh up-front so the cache is hydrated before any
	// fsnotify event arrives.
	_ = w.provider.Refresh(ctx, "watcher-bootstrap")

	notify, notifyErr := w.startNotify()
	if notifyErr != nil {
		w.logger.Warn("claudeinstance watcher: fsnotify unavailable, falling back to poll-only",
			"dir", w.dir, "err", notifyErr)
	}
	if notify != nil {
		defer func() { _ = notify.Close() }()
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.provider.Refresh(ctx, "poll-tick"); err != nil {
				w.logger.Warn("claudeinstance watcher: poll refresh failed", "err", err)
			}
		case ev, ok := <-eventsChan(notify):
			if !ok {
				// fsnotify closed unexpectedly — fall back to poll-only
				// for the remainder of this watcher's life.
				notify = nil
				continue
			}
			if !shouldRefreshOn(ev) {
				continue
			}
			if err := w.provider.Refresh(ctx, "fsnotify:"+ev.Op.String()); err != nil {
				w.logger.Warn("claudeinstance watcher: fsnotify refresh failed", "err", err)
			}
		case err, ok := <-errorsChan(notify):
			if !ok {
				notify = nil
				continue
			}
			w.logger.Warn("claudeinstance watcher: fsnotify error", "err", err)
		}
	}
}

// Done is closed when Run returns. Useful in tests so a goroutine can
// wait for graceful shutdown without sleeping.
func (w *Watcher) Done() <-chan struct{} {
	return w.doneCh
}

// startNotify wires fsnotify on the watcher's heartbeat directory. The
// directory must exist; we do not auto-create it — that is the hook
// script's job.
func (w *Watcher) startNotify() (*fsnotify.Watcher, error) {
	if w.dir == "" {
		return nil, errors.New("claudeinstance watcher: empty heartbeat dir")
	}
	notify, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := notify.Add(w.dir); err != nil {
		_ = notify.Close()
		return nil, err
	}
	return notify, nil
}

// shouldRefreshOn filters out fsnotify events that are not interesting
// for our use case. Chmod-only events on staging files would otherwise
// trigger a refresh per write — wasteful when nothing in the heartbeat
// payload actually changed.
func shouldRefreshOn(ev fsnotify.Event) bool {
	const interesting = fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename
	return ev.Op&interesting != 0
}

// eventsChan returns nil when fs is nil so the watcher's select loop
// tolerates the poll-only fallback without a special case.
func eventsChan(fs *fsnotify.Watcher) <-chan fsnotify.Event {
	if fs == nil {
		return nil
	}
	return fs.Events
}

// errorsChan mirrors eventsChan for the error channel.
func errorsChan(fs *fsnotify.Watcher) <-chan error {
	if fs == nil {
		return nil
	}
	return fs.Errors
}
