package peerproxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// defaultDebounce is the coalescing window for bursty fsnotify events.
// Editor save bursts (typically 3-10 events within ~200ms) collapse into
// a single LoadFederationConfig + ApplyPeers call.
const defaultDebounce = 1 * time.Second

// ConfigWatcherOption configures a ConfigWatcher at construction time.
// Mirror the ProviderOption pattern so callers use the same idiom for both.
type ConfigWatcherOption func(*ConfigWatcher)

// WithDebounce overrides the debounce window. The primary use case is
// tests: passing a short duration (e.g. 100ms) avoids sleeping for the
// full 1-second default.
func WithDebounce(d time.Duration) ConfigWatcherOption {
	return func(cw *ConfigWatcher) { cw.debounce = d }
}

// ConfigWatcher watches the federation config file for changes and calls
// Provider.ApplyPeers whenever the file is written, created, or renamed.
//
// The watcher monitors the parent directory (not the file directly) to
// survive atomic-rename edits that editors and `orchard config` use. Only
// events whose basename matches the configured file are forwarded.
//
// Bursty editor saves (multiple fsnotify events within the debounce
// window) are coalesced into a single LoadFederationConfig + ApplyPeers
// call. The default debounce window is 1 second; use WithDebounce to
// override in tests.
//
// Lifetime: Start spawns a goroutine that runs until ctx is cancelled.
// Close shuts the underlying fsnotify watcher and waits for the goroutine
// to exit. Callers should tie the ctx to the server lifetime and call
// Close in a defer or cleanup hook.
type ConfigWatcher struct {
	path     string
	provider *Provider
	logger   *slog.Logger
	watcher  *fsnotify.Watcher
	closedCh chan struct{}
	debounce time.Duration

	// reloadCount is incremented each time LoadFederationConfig + ApplyPeers
	// fires. Exposed via ReloadCount() as a test seam.
	reloadCount atomic.Int64
}

// NewConfigWatcher constructs a ConfigWatcher that reloads the federation
// config at path and applies changes to provider whenever the file is
// modified. Pass WithDebounce to shorten the coalescing window in tests.
func NewConfigWatcher(path string, provider *Provider, logger *slog.Logger, opts ...ConfigWatcherOption) *ConfigWatcher {
	if logger == nil {
		logger = slog.Default()
	}
	cw := &ConfigWatcher{
		path:     path,
		provider: provider,
		logger:   logger,
		closedCh: make(chan struct{}),
		debounce: defaultDebounce,
	}
	for _, opt := range opts {
		opt(cw)
	}
	return cw
}

// ReloadCount returns the number of times LoadFederationConfig + ApplyPeers
// has been invoked by the debounce timer. Intended for tests that assert
// burst coalescing — a count of 1 after N rapid events means the debouncer
// worked correctly.
func (cw *ConfigWatcher) ReloadCount() int {
	return int(cw.reloadCount.Load())
}

// Start creates an fsnotify watcher on the parent directory of the config
// file, ensures the directory exists, and spawns a goroutine that reloads
// and applies the federation config on every matching filesystem event.
//
// Start is not idempotent — call it once per ConfigWatcher.
func (cw *ConfigWatcher) Start(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("peerproxy watcher: fsnotify new: %w", err)
	}
	cw.watcher = w

	dir := filepath.Dir(cw.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = w.Close()
		return fmt.Errorf("peerproxy watcher: ensure config dir %s: %w", dir, err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return fmt.Errorf("peerproxy watcher: watch %s: %w", dir, err)
	}

	go cw.run(ctx)
	return nil
}

// run drains fsnotify events, filters to the config basename, and calls
// LoadFederationConfig + ApplyPeers once per debounce window. Bursty
// saves (multiple events within debounce) collapse into a single reload.
//
// Debounce algorithm:
//   - On a matching event: stop any running timer and start a fresh one
//     (d from now). This resets the window on every new event.
//   - When the timer fires it sends on reloadCh. The select picks it up
//     and performs the actual reload.
//   - On ctx.Done(): stop any pending timer and exit. The pending reload
//     is dropped — consistent with the "context = lifetime" contract.
//
// Using time.AfterFunc + a dedicated channel avoids the time.Timer.Reset
// footgun (draining the channel before Reset). The goroutine never drains
// a timer channel; it only reads from reloadCh.
func (cw *ConfigWatcher) run(ctx context.Context) {
	defer close(cw.closedCh)

	base := filepath.Base(cw.path)

	// reloadCh is written by the AfterFunc goroutine and read by the
	// select below. Buffered with 1 so the AfterFunc never blocks even
	// if a previous signal hasn't been consumed yet.
	reloadCh := make(chan struct{}, 1)

	var pendingTimer *time.Timer
	stopTimer := func() {
		if pendingTimer != nil {
			pendingTimer.Stop()
			pendingTimer = nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			stopTimer()
			_ = cw.watcher.Close()
			return

		case ev, ok := <-cw.watcher.Events:
			if !ok {
				stopTimer()
				return
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			cw.logger.Debug("peerproxy: fsnotify event; debouncing config reload",
				"op", ev.Op.String(), "name", ev.Name)

			// Reset the debounce window: stop any running timer, start a
			// fresh one. When it fires it will send on reloadCh.
			stopTimer()
			pendingTimer = time.AfterFunc(cw.debounce, func() {
				select {
				case reloadCh <- struct{}{}:
				default:
					// A previous signal is still pending; skip — one
					// reload will handle all accumulated events.
				}
			})

		case <-reloadCh:
			pendingTimer = nil // timer already fired; nothing to stop
			cw.logger.Debug("peerproxy: debounce window elapsed; reloading config")

			cfg, err := LoadFederationConfig(cw.path)
			if err != nil {
				cw.logger.Warn("peerproxy: config reload failed; keeping existing peers",
					"path", cw.path, "err", err)
				continue
			}
			if err := cw.provider.ApplyPeers(cfg); err != nil {
				cw.logger.Warn("peerproxy: ApplyPeers error after config reload",
					"err", err)
			}
			cw.reloadCount.Add(1)

		case err, ok := <-cw.watcher.Errors:
			if !ok {
				stopTimer()
				return
			}
			cw.logger.Warn("peerproxy: fsnotify error", "err", err)
		}
	}
}

// Close shuts down the fsnotify watcher and waits for the run goroutine
// to exit. Idempotent: a second call is a no-op.
func (cw *ConfigWatcher) Close() error {
	if cw.watcher == nil {
		return nil
	}
	err := cw.watcher.Close()
	<-cw.closedCh
	cw.watcher = nil
	if err != nil {
		return fmt.Errorf("peerproxy watcher: close: %w", err)
	}
	return nil
}
