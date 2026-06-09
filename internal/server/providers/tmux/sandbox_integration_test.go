//go:build linux

// Regression test for issue #464 (AC5): Linux-only sandbox integration.
//
// This file verifies the structural guardrail introduced by AC1 (≤2 tmux
// execs per poll tick) prevents the SIGKILL storm that the Linux
// systemd-oomd path triggered. The test does NOT attempt to reproduce the
// exact oomd memory-pressure scenario — that requires a constrained cgroup
// environment that neither root nor CI provides. What it does prove is:
//
//   - The provider runs ~20 fast (50ms) poll ticks against a real tmux
//     sandbox without any tmux exec receiving SIGKILL.
//   - The coalesced list-panes parser correctly populates provider state
//     (≥1 session visible after polling).
//
// The faithful oomd reproduction (applying actual memory pressure against a
// constrained systemd user-unit cgroup) belongs to AC6 — the boxd field test
// which requires a Linux host with a systemd-user session slice. AC5 is the
// structural regression guard: if the exec count regresses above the oomd
// trigger threshold AND tmux is killed by the kernel, this test will catch it
// on any well-resourced Linux CI where tmux is installed via apt.
//
// Build tag: linux — the bug is Linux/systemd-user-specific; macOS uses
// launchd, not systemd, so SIGKILL from oomd never occurs there. Excluding
// the file from darwin keeps `go test ./...` green on developer machines
// without tmux needing to handle the cgroup skip path.
//
// Second AC5 scenario ("exec count regression") is covered by
// exec_count_test.go#TestRegression_FetchAllExecCount_Issue464, which runs on
// all platforms using a fake CommandRunner and is not duplicated here.
//
// Third AC5 scenario ("skip with a clear message when cgroup setup is
// unavailable") is implemented by the t.Skip() guard in
// TestRegression_FetchAllSurvivesFastPolls_Issue464_Linux below — the test
// skips if /sys/fs/cgroup is not readable, which is the proxy we use for
// "not running on a Linux host with cgroup visibility".

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/providers/tmux"
)

// sigkillRecord captures one detected SIGKILL event for the assertion
// message. args is the full argument list passed to the tmux exec that was
// killed; signal is the signal name from the error string.
type sigkillRecord struct {
	args   []string
	signal string
}

// sigkillDetectingRunner implements tmux.CommandRunner. It delegates every
// Run call to a real os/exec invocation (identical behaviour to the
// production execRunner) and records any error whose message contains
// "signal: SIGKILL". The slice is protected by a mutex because the provider
// runs the poll loop on a background goroutine.
//
// Purpose: assert that no tmux subprocess is killed during the test window.
// Under normal CI conditions (no memory pressure) this should always be true.
// If the exec-count guard regresses and oomd kicks in under sustained fork
// pressure, this recorder captures the evidence so the test failure message
// is actionable.
type sigkillDetectingRunner struct {
	mu      sync.Mutex
	records []sigkillRecord
}

// Run executes name with args using os/exec and returns combined stdout.
// If the process is killed by SIGKILL the error is recorded in records and
// returned to the caller so the provider's error-handling path is unchanged.
//
// CodeRabbit follow-up on PR #507: exec.CommandContext sends SIGKILL to the
// child when the context is cancelled, so the test's teardown `cancel()`
// would otherwise be recorded as a regression. Skip the record (but still
// return the wrapped error so the provider's error-handling path is
// exercised normally) when ctx.Err() != nil at observation time.
func (r *sigkillDetectingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Reconstruct a descriptive error that matches the production
			// execRunner format introduced in AC4: "signal: <NAME>". We
			// do a simple string match rather than syscall.WaitStatus so
			// the detection logic does not depend on platform syscall
			// types (the build tag already restricts to Linux).
			//
			// On Linux, exec.ExitError.Error() for a SIGKILL'd child
			// returns "signal: killed". The AC4 fix in execRunner wraps
			// this as "signal: SIGKILL". We mimic the same wrap here so
			// the detecting runner stays in sync with what operators see
			// in production logs.
			errStr := exitErr.Error()
			var wrapped error
			if strings.Contains(errStr, "killed") {
				// Normalize to "signal: SIGKILL" (AC4 format).
				wrapped = fmt.Errorf("%s %v: signal: SIGKILL (stderr: %q)", name, args, strings.TrimSpace(stderr.String()))
				// Only record SIGKILLs that happened during the hot path —
				// not the teardown kills exec.CommandContext sends when ctx
				// is cancelled. ctx.Err() != nil means the parent already
				// asked us to stop; the kill is OUR doing, not the bug's.
				if ctx.Err() == nil {
					r.mu.Lock()
					r.records = append(r.records, sigkillRecord{
						args:   append([]string{name}, args...),
						signal: "SIGKILL",
					})
					r.mu.Unlock()
				}
				return stdout.Bytes(), wrapped
			}
			return stdout.Bytes(), fmt.Errorf("%s %v: %w (stderr: %q)", name, args, err, strings.TrimSpace(stderr.String()))
		}
		return stdout.Bytes(), fmt.Errorf("%s %v: %w", name, args, err)
	}
	return stdout.Bytes(), nil
}

// sigkills returns a snapshot of recorded SIGKILL events. Thread-safe.
func (r *sigkillDetectingRunner) sigkills() []sigkillRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sigkillRecord, len(r.records))
	copy(out, r.records)
	return out
}

// TestRegression_FetchAllSurvivesFastPolls_Issue464_Linux is the AC5
// Linux-only regression guard.
//
// It spins up a real sandbox tmux server on a /tmp socket, configures the
// provider to poll every 50ms (matching the frequency at which the oomd
// trigger was observed on systemd-user hosts), runs ~20 ticks, then asserts:
//
//  1. No SIGKILL was recorded on any tmux exec during the polling window.
//  2. The coalesced list-panes parser populated at least one session in the
//     provider snapshot — proving the AC1 structural change did not silently
//     break state propagation.
//
// Skip conditions (test is skipped, not failed, when the environment cannot
// satisfy the test's preconditions):
//
//   - tmux is not on PATH (CI without apt-installed tmux).
//   - /sys/fs/cgroup is not readable (not a Linux host with cgroup
//     visibility — proxy for "cgroup setup unavailable"). Note: this does NOT
//     mean we write or configure a cgroup; we only stat the path. The check
//     confirms we are on a real Linux host where cgroup-mediated pressure
//     would be observable, not a sandboxed environment that emulates it.
func TestRegression_FetchAllSurvivesFastPolls_Issue464_Linux(t *testing.T) {
	// Skip 1: tmux must be on PATH.
	tmuxAvailable(t)

	// Skip 2: /sys/fs/cgroup must be readable as a proxy for "running on a
	// Linux host with cgroup visibility". We do NOT create or modify any
	// cgroup — that requires root or systemd-run which CI doesn't have.
	// The test asserts the structural guardrail (no SIGKILL across N ticks);
	// faithful oomd repro requires the boxd field test (AC6).
	if _, err := os.ReadDir("/sys/fs/cgroup"); err != nil {
		t.Skipf("cgroup setup unavailable: /sys/fs/cgroup not readable (%v); "+
			"this test guards the exec-count structural invariant on Linux hosts "+
			"with cgroup visibility. Faithful oomd repro requires AC6 (boxd field test).", err)
	}

	// Spin up a real sandbox tmux on an isolated /tmp socket.
	// startSandboxTmux is defined in tmux_e2e_test.go (same package).
	socket := startSandboxTmux(t)

	const (
		pollInterval = 50 * time.Millisecond
		tickCount    = 20
		slack        = 250 * time.Millisecond
		totalWait    = tickCount*pollInterval + slack // ~1.25s
	)

	host := tmux.HostID("sandbox-linux-ac5")

	// Build adapter with the detecting runner and a short alive-cache TTL
	// so each tick re-proves the alive check and exercises the full
	// exec path (≤2 execs per tick as guaranteed by AC1).
	detector := &sigkillDetectingRunner{}
	adapter := tmux.NewAdapter(host).
		WithSocket(socket).
		WithRunner(detector).
		WithAliveTTL(pollInterval)

	provider := tmux.New(adapter, nil).WithPollInterval(pollInterval)

	ctx, cancel := context.WithTimeout(context.Background(), totalWait+5*time.Second)
	defer cancel()

	if err := provider.Start(ctx); err != nil {
		t.Fatalf("provider.Start: %v", err)
	}

	// Let the poll loop run for ~20 ticks.
	time.Sleep(totalWait)

	// Cancel the context to stop the poll loop before asserting.
	cancel()

	// --- Assertion 1: no SIGKILL on any tmux exec ---
	kills := detector.sigkills()
	if len(kills) != 0 {
		t.Errorf("issue #464 regression: %d SIGKILL(s) recorded during %d poll ticks at %s interval",
			len(kills), tickCount, pollInterval)
		for i, k := range kills {
			t.Errorf("  kill[%d]: signal=%s args=%v", i, k.signal, k.args)
		}
		t.FailNow()
	}

	// --- Assertion 2: coalesced parser populated ≥1 session ---
	// Prove the AC1 structural change (listAll replaces list-sessions +
	// list-windows + list-panes) did not silently break state propagation.
	// The sandbox tmux was created with a session named "alpha" by
	// startSandboxTmux.
	snap := provider.Snapshot()
	if len(snap.Sessions) == 0 {
		t.Errorf("issue #464 regression: provider snapshot has 0 sessions after %d poll ticks; "+
			"the coalesced list-panes parser may not be populating session state correctly",
			tickCount)
	}
}
