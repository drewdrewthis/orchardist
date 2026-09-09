// daemonlog.go — a launcher-independent daemon.log sink (issue #768).
//
// Before #768 the daemon logger only ever wrote to its inherited stderr.
// launchd's plist happens to redirect that to a file, but a systemd unit or
// a manually backgrounded `orchard daemon daemon start` gets nothing
// durable: there is no file to grep for the bind line after the fact. This
// file makes the daemon open its own <StateDir>/daemon.log, independent of
// how it was launched, and fans every log line out to both sinks.
package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/drewdrewthis/orchardist/internal/orchpaths"
)

// daemonLogFileName is the log file's name inside the state dir.
const daemonLogFileName = "daemon.log"

// openDaemonLog creates stateDir if needed and opens <stateDir>/daemon.log
// for appending. On any failure it returns a nil writer and a non-nil
// error — callers must not treat a nil error as "safe to skip the close".
func openDaemonLog(stateDir string) (io.Writer, func(), error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, daemonLogFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

// daemonLogWriter builds the combined writer the daemon logger is built
// over: stderr plus daemon.log under stateDir. When the file sink cannot be
// opened (unwritable state dir, etc.) it degrades to stderr-only and warns
// exactly once — the daemon must never fail to start over a log sink, so
// this function carries no error return at all.
func daemonLogWriter(stderr io.Writer, stateDir string, warn func(msg string, args ...any)) (io.Writer, func()) {
	w, closeFn, err := openDaemonLog(stateDir)
	if err != nil {
		warn("daemon.log unavailable at "+filepath.Join(stateDir, daemonLogFileName)+", logging to stderr only",
			"err", err)
		return stderr, func() {}
	}
	return io.MultiWriter(stderr, w), closeFn
}

// setupDaemonLogger opens daemon.log alongside stderr so the daemon's log
// trail survives regardless of launcher (launchd redirects stderr to a file
// itself; systemd/manual starts do not) — issue #768, and builds the leveled
// logger runStart installs as slog's default. The returned close func must be
// deferred by the caller to flush/close the file sink.
func setupDaemonLogger(level slog.Level) (*slog.Logger, func(), error) {
	stateDir, err := orchpaths.StateDir()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve state dir: %w", err)
	}
	// warn writes unconditionally to stderr, ignoring level, so the
	// degraded-sink notice reaches an operator watching stderr even at
	// --log-level=error and even though the leveled logger it warns about
	// doesn't exist yet.
	warn := func(msg string, args ...any) {
		fmt.Fprintf(os.Stderr, "orchard: %s\n", msg)
	}
	w, closeFn := daemonLogWriter(os.Stderr, stateDir, warn)
	return newDaemonLogger(w, level), closeFn, nil
}
