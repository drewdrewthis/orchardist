package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// JSONFileAdapter reads ~/.config/orchard/config.json and watches it
// for changes. It implements adapter.Adapter[ProjectID, Project].
//
// fsnotify subtleties this adapter handles:
//
//   - Watching the file directly is fragile because editors (vim, VS
//     Code) replace the file via rename, which removes the watch. We
//     watch the *parent directory* and filter events to our basename.
//   - The parent directory may not exist when the daemon boots; we
//     create it with 0o755 to make watching reliable in fresh setups.
//   - WRITE, CREATE and RENAME all may indicate a config edit; the
//     adapter coalesces them into a single "the file changed" notice
//     by emitting the same sentinel key on the Watch channel.
type JSONFileAdapter struct {
	path     string
	logger   *slog.Logger
	watcher  *fsnotify.Watcher
	closedCh chan struct{}
}

// allKey is the sentinel emitted on Watch — config.json is a single
// document, so per-key invalidation isn't meaningful. The provider
// listens for this key and reloads the entire cache.
const allKey ProjectID = "*"

// NewJSONFileAdapter constructs an adapter rooted at path. The parent
// directory is created if missing (best effort; permission errors fall
// through to the caller). The adapter is safe to call before the file
// exists — Fetch / FetchAll return empty results, and Watch fires once
// the file appears.
func NewJSONFileAdapter(path string, logger *slog.Logger) *JSONFileAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &JSONFileAdapter{
		path:     path,
		logger:   logger,
		closedCh: make(chan struct{}),
	}
}

// Path returns the configured config-file path. Useful for diagnostics
// and tests.
func (a *JSONFileAdapter) Path() string { return a.path }

// Fetch returns one project by ID. The full file is loaded each call —
// this is fine for the config provider because the cache layer above
// caches the result; Fetch is only called on cache miss for that ID.
func (a *JSONFileAdapter) Fetch(ctx context.Context, id ProjectID) (Project, error) {
	all, err := a.FetchAll(ctx)
	if err != nil {
		return Project{}, err
	}
	p, ok := all[id]
	if !ok {
		return Project{}, fmt.Errorf("project %q not found in %s", id, a.path)
	}
	return p, nil
}

// FetchAll loads and normalises every project in the config file. A
// missing file returns an empty map (not an error) so the daemon can
// boot before `orchard config init` has run.
func (a *JSONFileAdapter) FetchAll(ctx context.Context) (map[ProjectID]Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[ProjectID]Project{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", a.path, err)
	}
	if len(data) == 0 {
		return map[ProjectID]Project{}, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", a.path, err)
	}
	out := make(map[ProjectID]Project, len(f.Projects))
	for _, row := range f.Projects {
		p := row.ToProject()
		if p.Directory == "" {
			a.logger.Warn("config: skipping project with empty directory", "id", p.ID)
			continue
		}
		out[p.ID] = p
	}
	return out, nil
}

// Watch starts an fsnotify watcher on the parent directory of a.path
// and emits allKey whenever an event matching the config file is
// observed. The channel is closed when ctx is cancelled or Close is
// called.
func (a *JSONFileAdapter) Watch(ctx context.Context) (<-chan ProjectID, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify new: %w", err)
	}
	a.watcher = w

	dir := filepath.Dir(a.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("ensure config dir %s: %w", dir, err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("watch %s: %w", dir, err)
	}

	out := make(chan ProjectID, 8)
	go a.run(ctx, out)
	return out, nil
}

// run drains fsnotify events, filters them to our basename, and emits
// allKey on the output channel. It exits when ctx is done or the
// watcher is closed.
func (a *JSONFileAdapter) run(ctx context.Context, out chan<- ProjectID) {
	defer close(out)
	defer close(a.closedCh)

	base := filepath.Base(a.path)
	for {
		select {
		case <-ctx.Done():
			_ = a.watcher.Close()
			return
		case ev, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != base {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			a.logger.Debug("config: fsnotify event", "op", ev.Op.String(), "name", ev.Name)
			select {
			case out <- allKey:
			case <-ctx.Done():
				_ = a.watcher.Close()
				return
			}
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			a.logger.Warn("config: fsnotify error", "err", err)
		}
	}
}

// Close shuts down the watcher and waits briefly for the run loop to
// exit so callers can rely on no further channel sends after Close
// returns. Idempotent.
func (a *JSONFileAdapter) Close() error {
	if a.watcher == nil {
		return nil
	}
	err := a.watcher.Close()
	<-a.closedCh
	a.watcher = nil
	if err != nil {
		return fmt.Errorf("close watcher: %w", err)
	}
	return nil
}
