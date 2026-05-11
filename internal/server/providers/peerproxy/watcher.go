package peerproxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// ConfigWatcher watches the federation config file for changes and calls
// Provider.ApplyPeers whenever the file is written, created, or renamed.
//
// The watcher monitors the parent directory (not the file directly) to
// survive atomic-rename edits that editors and `orchard config` use. Only
// events whose basename matches the configured file are forwarded.
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
}

// NewConfigWatcher constructs a ConfigWatcher that reloads the federation
// config at path and applies changes to provider whenever the file is
// modified.
func NewConfigWatcher(path string, provider *Provider, logger *slog.Logger) *ConfigWatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConfigWatcher{
		path:     path,
		provider: provider,
		logger:   logger,
		closedCh: make(chan struct{}),
	}
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
// LoadFederationConfig + ApplyPeers on each matching event. It exits when
// ctx is cancelled or the fsnotify channel closes.
func (cw *ConfigWatcher) run(ctx context.Context) {
	defer close(cw.closedCh)

	base := filepath.Base(cw.path)
	for {
		select {
		case <-ctx.Done():
			_ = cw.watcher.Close()
			return
		case ev, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			cw.logger.Debug("peerproxy: fsnotify event; reloading config",
				"op", ev.Op.String(), "name", ev.Name)

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
		case err, ok := <-cw.watcher.Errors:
			if !ok {
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
