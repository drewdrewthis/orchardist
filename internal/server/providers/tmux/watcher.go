// Watcher is a heartbeat-fsnotify hybrid per ADR-011 §12 line 413.
// tmux exposes no native event stream, so the canonical signal is a 1s
// poll over `list-*`. To shorten "I just attached / detached / opened a
// window" latency below the poll interval, the watcher also subscribes
// to fsnotify events on the tmux socket directory and pokes the poll
// loop early when the daemon writes to its socket.
//
// Failures of either side degrade gracefully — fsnotify failures fall
// back to poll-only; poll failures get logged and re-tried next tick.

package tmux

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// SocketDir returns the directory holding the tmux socket the adapter
// is bound to. For the default socket on macOS / Linux this is
// `$TMPDIR/tmux-<uid>/`; for a custom -S path it's the directory
// containing that file.
//
// Returns "" with no error when the socket dir cannot be determined —
// the watcher falls back to poll-only.
func (a *Adapter) SocketDir() (string, error) {
	if a.socket != "" {
		dir := filepath.Dir(a.socket)
		if dir == "" {
			return "", nil
		}
		if _, err := os.Stat(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", fmt.Errorf("stat socket dir: %w", err)
		}
		return dir, nil
	}
	// Default socket dir is `$TMPDIR/tmux-<uid>/`. We don't need it for
	// the polling fallback, so absence is a no-op rather than an error.
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = "/tmp"
	}
	candidates, err := filepath.Glob(filepath.Join(tmp, "tmux-*"))
	if err != nil || len(candidates) == 0 {
		return "", nil
	}
	return candidates[0], nil
}

// StartWatcher installs the fsnotify watcher on the tmux socket dir and
// pokes the provider's poll loop on each event. It is non-fatal: when
// fsnotify is unavailable the function logs and returns nil so the
// provider continues poll-only.
func StartWatcher(ctx context.Context, p *Provider, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	dir, err := p.adapter.SocketDir()
	if err != nil {
		logger.Warn("tmux watcher: socket dir lookup failed; falling back to poll-only", "err", err)
		return nil
	}
	if dir == "" {
		// No socket dir to watch yet (no tmux server has run). Poll
		// loop will pick the dir up when it appears.
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("tmux watcher: fsnotify unavailable; falling back to poll-only", "err", err)
		return nil
	}
	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		logger.Warn("tmux watcher: cannot watch socket dir; falling back to poll-only", "dir", dir, "err", err)
		return nil
	}

	expectedBasename := p.adapter.SocketBasename()

	go func() {
		defer func() { _ = watcher.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only socket-relevant events should poke the poll loop.
				// Filter out the noise from temporary files in the same dir.
				if !relevantSocketEvent(ev, expectedBasename) {
					continue
				}
				p.PokeRefresh()
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn("tmux watcher: fsnotify error", "err", err)
			}
		}
	}()
	return nil
}

// relevantSocketEvent decides whether an fsnotify event should trigger
// a refresh. Tmux writes its socket as a unix-domain file inside the
// socket dir; only create/remove/write events on the configured socket
// basename matter. Everything else (temp files, lock files, other sockets)
// is ignored to prevent feedback loops where the watcher pokes a refresh
// that causes tmux to write to its socket, firing another event.
//
// expectedBasename is the filename component of the adapter's socket path
// (or "default" for the tmux-default socket).
func relevantSocketEvent(ev fsnotify.Event, expectedBasename string) bool {
	if ev.Op == 0 {
		return false
	}
	base := filepath.Base(ev.Name)
	if base == "" || strings.HasPrefix(base, ".") {
		return false
	}
	if base != expectedBasename {
		return false
	}
	return true
}
