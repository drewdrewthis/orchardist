package claudeaccount

import (
	"context"
	"time"
)

// Watcher is the poll-based change detector for the ClaudeAccount domain.
//
// There is no observable file under the user's control — `claude` and
// `ccusage` keep their state in opaque stores under ~/.claude/ and
// ~/.ccusage/ that are not safe to fsnotify against — so the provider polls
// every PollInterval (60s by default).
//
// Exposed so future workstreams can drive a different cadence (e.g. faster on
// user-initiated /tick) without forking the provider.
type Watcher struct {
	provider *Provider
	interval time.Duration
}

// NewWatcher constructs a Watcher rooted at the given Provider.
// If interval is <= 0 the Provider's pollInterval is used.
func NewWatcher(p *Provider, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = p.pollInterval
	}
	return &Watcher{provider: p, interval: interval}
}

// Run blocks until ctx is cancelled, calling Provider.refresh on every tick.
// Returns nil on clean shutdown. Errors are recorded on the provider;
// the watcher silently retries on the next tick.
//
// R17: the caller is responsible for wrapping Run in a goroutine with a
// recover handler if they want the outer goroutine to survive a panic.
func (w *Watcher) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = w.provider.refresh(ctx, "external-watcher")
		}
	}
}
