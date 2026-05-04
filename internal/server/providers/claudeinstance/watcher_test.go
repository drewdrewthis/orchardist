package claudeinstance

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestWatcher_FSNotify_TriggersRefresh writes a heartbeat into a
// tempdir, kicks Run on a Watcher with a generous fsnotify path, and
// asserts the provider's cache observes the new instance within the
// poll window. Covers both the fsnotify push path and the poll
// fallback — whichever fires first wins.
func TestWatcher_FSNotify_TriggersRefresh(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	clock := func() time.Time { return now }

	reader := NewFileReader(dir)
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{alive: map[int]bool{42100: true}}, clock, HeartbeatStaleAfter)
	p := NewWith("local", reader, c, clock)

	w := NewWatcherWith(p, silentLogger(), 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = w.Run(ctx)
	}()

	// Wait for bootstrap refresh to settle.
	time.Sleep(100 * time.Millisecond)

	// Drop a heartbeat into the dir.
	hb := map[string]any{
		"tmux_session":    "alpha",
		"session_id":      "uuid-alpha",
		"state":           "working",
		"timestamp":       now.Format(time.RFC3339),
		"claudePid":       42100,
		"lastHeartbeatAt": now.Format(time.RFC3339),
	}
	b, err := json.Marshal(hb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orchard-claude-alpha.json"), b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait up to 1s for the refresh path to populate the cache.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		got, err := p.List(ctx)
		if err == nil && len(got) == 1 {
			cancel()
			wg.Wait()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher did not refresh after writing heartbeat within 1s")
}

// silentLogger returns a slog.Logger that discards everything — keeps
// test output clean. We do not want test logs to spam stderr just
// because a fixture exercises a fsnotify warning path.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
