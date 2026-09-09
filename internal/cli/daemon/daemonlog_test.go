// Tests for the daemon's file log sink (issue #768, AC1-AC5).
//
// Before #768 the daemon logger was built as newDaemonLogger(os.Stderr,
// level) (daemon.go:145) — only inherited stderr. launchd's plist redirects
// stderr to a file, but a systemd unit or a manual `orchard daemon daemon
// start` gets nothing durable: no file exists to grep for the bind line.
// These tests pin the contract for the fix: the daemon opens
// <StateDir>/daemon.log itself, appends to it alongside stderr through one
// leveled handler, and never fails startup when the state dir can't be
// created or the file can't be opened.
package daemon

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenDaemonLog_CreatesDirAndFile asserts openDaemonLog creates a state
// dir that does not yet exist (MkdirAll) and opens daemon.log inside it
// (AC1: the file must exist regardless of launcher).
func TestOpenDaemonLog_CreatesDirAndFile(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	stateDir := filepath.Join(base, "nested", "state", "dir")

	w, closeFn, err := openDaemonLog(stateDir)
	if err != nil {
		t.Fatalf("openDaemonLog(%q) returned error: %v", stateDir, err)
	}
	if w == nil {
		t.Fatal("openDaemonLog returned a nil writer with no error")
	}
	defer closeFn()

	if _, err := io.WriteString(w, "hello daemon.log\n"); err != nil {
		t.Fatalf("write to opened log: %v", err)
	}

	logPath := filepath.Join(stateDir, "daemon.log")
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read back %s: %v", logPath, err)
	}
	if !strings.Contains(string(got), "hello daemon.log") {
		t.Errorf("daemon.log content = %q, want it to contain the written line", got)
	}
}

// TestOpenDaemonLog_AppendsAcrossOpens asserts a second open against the
// same state dir does not truncate what a prior open wrote (AC3: a daemon
// restart must not erase the earlier investigation trail).
func TestOpenDaemonLog_AppendsAcrossOpens(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()

	w1, close1, err := openDaemonLog(stateDir)
	if err != nil {
		t.Fatalf("first openDaemonLog: %v", err)
	}
	if _, err := io.WriteString(w1, "run-one\n"); err != nil {
		t.Fatalf("write during first open: %v", err)
	}
	close1()

	w2, close2, err := openDaemonLog(stateDir)
	if err != nil {
		t.Fatalf("second openDaemonLog: %v", err)
	}
	if _, err := io.WriteString(w2, "run-two\n"); err != nil {
		t.Fatalf("write during second open: %v", err)
	}
	close2()

	got, err := os.ReadFile(filepath.Join(stateDir, "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	content := string(got)
	if !strings.Contains(content, "run-one") {
		t.Errorf("daemon.log lost the first open's content: %q", content)
	}
	if !strings.Contains(content, "run-two") {
		t.Errorf("daemon.log missing the second open's content: %q", content)
	}
}

// TestOpenDaemonLog_UnwritableStateDirErrors asserts a state dir that cannot
// be created (its parent is a regular file, so MkdirAll fails with ENOTDIR)
// returns a nil writer and a non-nil error, rather than panicking or
// silently succeeding.
func TestOpenDaemonLog_UnwritableStateDirErrors(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	stateDir := filepath.Join(blocker, "orchard")

	w, closeFn, err := openDaemonLog(stateDir)
	if err == nil {
		if closeFn != nil {
			closeFn()
		}
		t.Fatalf("openDaemonLog(%q) = (writer, nil error), want an error since the parent is a regular file", stateDir)
	}
	if w != nil {
		t.Errorf("openDaemonLog returned a non-nil writer alongside an error: %v", w)
	}
}

// TestDaemonLogWriter_WritesBothStderrAndFile asserts the combined writer
// fans out to both sinks (AC1 file exists + AC2 stderr regression: the file
// is added alongside stderr, not in place of it).
func TestDaemonLogWriter_WritesBothStderrAndFile(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	var stderrBuf bytes.Buffer
	warnCalls := 0
	warn := func(msg string, args ...any) { warnCalls++ }

	w, closeFn := daemonLogWriter(&stderrBuf, stateDir, warn)
	defer closeFn()

	const marker = "orchard daemon listening"
	if _, err := io.WriteString(w, marker+"\n"); err != nil {
		t.Fatalf("write to daemonLogWriter result: %v", err)
	}

	if !strings.Contains(stderrBuf.String(), marker) {
		t.Errorf("stderr buffer = %q, want it to contain %q", stderrBuf.String(), marker)
	}

	fileContent, err := os.ReadFile(filepath.Join(stateDir, "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if !strings.Contains(string(fileContent), marker) {
		t.Errorf("daemon.log content = %q, want it to contain %q", fileContent, marker)
	}
	if warnCalls != 0 {
		t.Errorf("warn called %d times on a writable state dir, want 0", warnCalls)
	}
}

// TestDaemonLogWriter_LogLevelHonouredByFileSink asserts one leveled slog
// handler built over daemonLogWriter's result filters the file sink exactly
// like stderr — a second, unleveled handler over the file would leak Debug
// into daemon.log regardless of --log-level (AC4).
func TestDaemonLogWriter_LogLevelHonouredByFileSink(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		level     slog.Level
		wantDebug bool
	}{
		{name: "info drops debug from the file", level: slog.LevelInfo, wantDebug: false},
		{name: "debug emits debug to the file", level: slog.LevelDebug, wantDebug: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stateDir := t.TempDir()
			var stderrBuf bytes.Buffer
			w, closeFn := daemonLogWriter(&stderrBuf, stateDir, func(string, ...any) {})
			defer closeFn()

			logger := newDaemonLogger(w, tc.level)
			logger.Debug("marker-debug")
			logger.Info("marker-info")

			fileContent, err := os.ReadFile(filepath.Join(stateDir, "daemon.log"))
			if err != nil {
				t.Fatalf("read daemon.log: %v", err)
			}
			out := string(fileContent)

			if got := strings.Contains(out, "marker-debug"); got != tc.wantDebug {
				t.Errorf("debug record present in daemon.log = %v, want %v (content: %q)", got, tc.wantDebug, out)
			}
			if !strings.Contains(out, "marker-info") {
				t.Errorf("info record missing from daemon.log at level %v (content: %q)", tc.level, out)
			}
		})
	}
}

// TestDaemonLogWriter_UnwritableStateDirFallsBackToStderr asserts the
// failure-mode contract (AC5): when the state dir cannot be created, the
// daemon must still start. daemonLogWriter degrades to stderr-only, warns
// exactly once naming the intended daemon.log path, and returns no error
// (its signature carries none) so the caller can never propagate one into a
// failed startup.
func TestDaemonLogWriter_UnwritableStateDirFallsBackToStderr(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	stateDir := filepath.Join(blocker, "orchard")
	intendedLogPath := filepath.Join(stateDir, "daemon.log")

	var stderrBuf bytes.Buffer
	var warnMsgs []string
	warn := func(msg string, args ...any) {
		warnMsgs = append(warnMsgs, msg)
	}

	w, closeFn := daemonLogWriter(&stderrBuf, stateDir, warn)
	defer closeFn()

	const marker = "orchard daemon listening"
	if _, err := io.WriteString(w, marker+"\n"); err != nil {
		t.Fatalf("write to degraded daemonLogWriter result: %v", err)
	}
	if !strings.Contains(stderrBuf.String(), marker) {
		t.Errorf("stderr buffer = %q, want it to still receive output when the file sink is unavailable", stderrBuf.String())
	}

	if len(warnMsgs) != 1 {
		t.Fatalf("warn called %d times, want exactly 1 (got: %v)", len(warnMsgs), warnMsgs)
	}
	if !strings.Contains(warnMsgs[0], intendedLogPath) {
		t.Errorf("warn message %q does not name the intended daemon.log path %q", warnMsgs[0], intendedLogPath)
	}
}
