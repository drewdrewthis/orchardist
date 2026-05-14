package claudeinstance

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SidecarJanitor removes stale orchard-claude-*.{json,inflight.json}
// files from heartbeatDir whose tmux session name (encoded in the
// filename as `orchard-claude-<tmux-session>.json`) is not present in
// the current set of live tmux sessions. Run at daemon startup.
//
// The janitor exists for cohabitation: after issue #603 the daemon
// stopped reading these sidecars, but the hook may still be writing
// them until the codex-side cleanup ships (AC #4). The hook deletes
// its own files on SessionEnd, but a Claude session killed via
// SIGKILL or tmux kill-session never fires SessionEnd, so orphans
// accumulate. This is a startup janitor only — it does NOT run
// continuously.
type SidecarJanitor struct {
	heartbeatDir string
	liveSessions func(context.Context) (map[string]bool, error)
	logger       *slog.Logger
}

// NewSidecarJanitor constructs a SidecarJanitor. heartbeatDir should
// be the resolved heartbeat directory (use ResolveDir() when the
// caller doesn't have a custom override). liveSessions must return a
// map of session names that are currently alive on the host.
func NewSidecarJanitor(
	heartbeatDir string,
	liveSessions func(context.Context) (map[string]bool, error),
	logger *slog.Logger,
) *SidecarJanitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &SidecarJanitor{
		heartbeatDir: heartbeatDir,
		liveSessions: liveSessions,
		logger:       logger,
	}
}

// Sweep removes orphan files. Returns the number removed. Errors are
// logged and swallowed — janitor failures must not block daemon
// startup.
func (j *SidecarJanitor) Sweep(ctx context.Context) int {
	live, err := j.liveSessions(ctx)
	if err != nil {
		j.logger.Error("sidecar janitor: failed to enumerate live tmux sessions; skipping sweep",
			"err", err)
		return 0
	}

	pattern := filepath.Join(j.heartbeatDir, "orchard-claude-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		// Non-existent dir or empty dir — nothing to do.
		return 0
	}

	removed := 0
	for _, path := range matches {
		session := extractSession(filepath.Base(path))
		if session == "" {
			continue
		}
		if live[session] {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			j.logger.Warn("sidecar janitor: failed to remove orphan file",
				"file", path, "err", err)
			continue
		}
		j.logger.Info("sidecar janitor: removed orphan sidecar", "file", filepath.Base(path), "session", session)
		removed++
	}

	j.logger.Info("sidecar janitor swept orphan files", "count", removed, "dir", j.heartbeatDir)
	return removed
}

// extractSession strips the `orchard-claude-` prefix and `.json` or
// `.inflight.json` suffix from a filename to recover the tmux session
// name. Returns "" when the filename does not match the expected shape.
func extractSession(name string) string {
	const prefix = "orchard-claude-"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	name = strings.TrimPrefix(name, prefix)
	// Strip longer suffix first to avoid leaving ".inflight" behind.
	if strings.HasSuffix(name, ".inflight.json") {
		return strings.TrimSuffix(name, ".inflight.json")
	}
	if strings.HasSuffix(name, ".json") {
		return strings.TrimSuffix(name, ".json")
	}
	return ""
}
