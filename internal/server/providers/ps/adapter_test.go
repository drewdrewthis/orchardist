package ps

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestAdapter_FetchAll_RealPs invokes the actual `ps` binary on the host
// and asserts the test process itself (os.Getpid) is in the result.
// Worker-standards §3: provider tests run against real backends.
func TestAdapter_FetchAll_RealPs(t *testing.T) {
	a := NewAdapter("local")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	all, err := a.FetchAll(ctx)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	self := ProcessID{Host: "local", PID: os.Getpid()}
	got, ok := all[self]
	if !ok {
		t.Fatalf("test process pid %d not in ps result (size=%d)", os.Getpid(), len(all))
	}
	if got.Command == "" {
		t.Errorf("test process command basename should be non-empty, got %q", got.Command)
	}
	if got.PPID == 0 {
		t.Errorf("test process should have a non-zero parent pid, got 0")
	}
}

// TestAdapter_Watch_EmitsOnSpawn proves the watcher detects a real
// subprocess appearing in the process table and emits its key.
func TestAdapter_Watch_EmitsOnSpawn(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("ps watcher test is darwin/linux only")
	}
	a := NewAdapter("local").WithPollInterval(150 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := a.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Drain the initial-snapshot burst before spawning so the assertion
	// is unambiguous: we want to see the spawn-pid AFTER the burst ends.
	drainBurst(t, ch, 500*time.Millisecond)

	cmd := exec.CommandContext(ctx, "sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep subprocess: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	want := ProcessID{Host: "local", PID: cmd.Process.Pid}
	deadline := time.After(4 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("watch channel closed before pid %d appeared", want.PID)
			}
			if got == want {
				return // success
			}
		case <-deadline:
			t.Fatalf("did not observe spawn of pid %d within deadline", want.PID)
		}
	}
}

// TestAdapter_FetchCwds_RealLsof asks for the test process's cwd and
// asserts it matches os.Getwd. macOS only — Linux returns empty until
// the /proc fallback lands.
func TestAdapter_FetchCwds_RealLsof(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skipf("lsof cwd test is darwin only (Linux uses /proc which is not yet wired)")
	}
	a := NewAdapter("local")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cwds, err := a.FetchCwds(ctx, []int{os.Getpid()})
	if err != nil {
		t.Fatalf("FetchCwds: %v", err)
	}
	got, ok := cwds[os.Getpid()]
	if !ok {
		t.Fatalf("cwd missing for self pid %d", os.Getpid())
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if got != want {
		t.Errorf("cwd = %q, want %q", got, want)
	}
}

// TestAdapter_FetchArgs_RealPs verifies that argv resolution finds the
// `go test` binary invocation (which always has a -test.* flag).
func TestAdapter_FetchArgs_RealPs(t *testing.T) {
	a := NewAdapter("local")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args, err := a.FetchArgs(ctx, []int{os.Getpid()})
	if err != nil {
		t.Fatalf("FetchArgs: %v", err)
	}
	got, ok := args[os.Getpid()]
	if !ok || len(got) == 0 {
		t.Fatalf("argv missing for self pid %d", os.Getpid())
	}
}

// drainBurst consumes events that arrive within window. Used to swallow
// the initial watcher snapshot so subsequent assertions can target the
// post-snapshot deltas only.
func drainBurst(t *testing.T, ch <-chan ProcessID, window time.Duration) {
	t.Helper()
	deadline := time.After(window)
	for {
		select {
		case <-ch:
			// keep draining
		case <-deadline:
			return
		}
	}
}
