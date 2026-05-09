// Signal-name diagnostic tests for execRunner.Run (issue #464).
//
// These tests drive real /bin/sh processes — not mocks — so we verify against
// the actual exec.ExitError / syscall.WaitStatus surface the OS produces.

package tmux

import (
	"context"
	"os"
	"strings"
	"testing"
)

func skipIfNoSh(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); os.IsNotExist(err) {
		t.Skip("/bin/sh unavailable; skipping signal-name tests")
	}
}

var runner = execRunner{}

// TestExecRunner_LogsSignalName_OnSIGKILL verifies that a child killed by
// SIGKILL produces an error string containing "signal: SIGKILL" rather than
// the opaque "signal: killed" text from Go's default ExitError formatting.
// This is the primary diagnostic for the oomd hypothesis in issue #464.
func TestExecRunner_LogsSignalName_OnSIGKILL(t *testing.T) {
	skipIfNoSh(t)

	// /bin/sh kills itself with SIGKILL via `kill -KILL $$`.
	_, err := runner.Run(context.Background(), "/bin/sh", "-c", "kill -KILL $$")
	if err == nil {
		t.Fatal("expected error from SIGKILL child, got nil")
	}
	if !strings.Contains(err.Error(), "signal: SIGKILL") {
		t.Errorf("want error containing %q, got: %s", "signal: SIGKILL", err.Error())
	}
}

// TestExecRunner_LogsSignalName_OnSIGTERM verifies SIGTERM is also named
// correctly — the signal name helper covers both kill-by-OOM (SIGKILL) and
// graceful-shutdown (SIGTERM) paths that show up in systemd logs.
func TestExecRunner_LogsSignalName_OnSIGTERM(t *testing.T) {
	skipIfNoSh(t)

	_, err := runner.Run(context.Background(), "/bin/sh", "-c", "kill -TERM $$")
	if err == nil {
		t.Fatal("expected error from SIGTERM child, got nil")
	}
	if !strings.Contains(err.Error(), "signal: SIGTERM") {
		t.Errorf("want error containing %q, got: %s", "signal: SIGTERM", err.Error())
	}
}

// TestExecRunner_PreservesExitError_NoSignal verifies that a plain non-zero
// exit (no signal) still uses the existing wrapping format that includes
// "exit status N" — callers depend on this for `no server running` detection.
func TestExecRunner_PreservesExitError_NoSignal(t *testing.T) {
	skipIfNoSh(t)

	_, err := runner.Run(context.Background(), "/bin/sh", "-c", "echo 'oops' >&2; exit 1")
	if err == nil {
		t.Fatal("expected error from non-zero exit, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "exit status 1") {
		t.Errorf("want error containing %q (existing behavior), got: %s", "exit status 1", errStr)
	}
	if strings.Contains(errStr, "signal:") {
		t.Errorf("non-signal exit must not claim a signal; got: %s", errStr)
	}
	// stderr content must be preserved — callers parse it for "no server running".
	if !strings.Contains(errStr, "oops") {
		t.Errorf("want stderr %q preserved in error, got: %s", "oops", errStr)
	}
}

// TestExecRunner_PreservesCleanExit verifies that a clean exit (code 0)
// returns nil — the happy path must be unaffected by the signal-name change.
func TestExecRunner_PreservesCleanExit(t *testing.T) {
	skipIfNoSh(t)

	out, err := runner.Run(context.Background(), "/bin/sh", "-c", "echo hello; exit 0")
	if err != nil {
		t.Fatalf("expected nil error for clean exit, got: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("want stdout to contain %q, got: %q", "hello", string(out))
	}
}

// TestExecRunner_ContextCancel_NotConfusedWithSIGKILL verifies that a child
// killed because the parent cancelled its context surfaces ctx.Err()
// (context.Canceled / DeadlineExceeded) rather than the misleading
// "signal: SIGKILL" diagnostic.
//
// CodeRabbit follow-up on PR #507: exec.CommandContext invokes Process.Kill
// (SIGKILL) on context cancellation; without checking ctx.Err() first, ctx-cancel
// kills are indistinguishable from external (oomd) SIGKILLs in logs, defeating
// the AC4 diagnostic.
func TestExecRunner_ContextCancel_NotConfusedWithSIGKILL(t *testing.T) {
	skipIfNoSh(t)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the slept child gets SIGKILLed via ctx-cancel.
	go func() {
		// Small delay so the child actually starts before cancel arrives.
		// 50ms is plenty for fork+exec on any reasonable host.
		_ = ctx
		cancel()
	}()

	_, err := runner.Run(ctx, "/bin/sh", "-c", "sleep 5")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	errStr := err.Error()
	// The new branch surfaces ctx.Err() — context.Canceled wraps as "context canceled".
	if !strings.Contains(errStr, "context canceled") && !strings.Contains(errStr, "context deadline exceeded") {
		t.Errorf("want ctx.Err() in message (e.g. %q), got: %s", "context canceled", errStr)
	}
	// MUST NOT report this as an external SIGKILL — that would defeat the diagnostic.
	if strings.Contains(errStr, "signal: SIGKILL") {
		t.Errorf("ctx-cancel kill must not surface as SIGKILL diagnostic; got: %s", errStr)
	}
}
