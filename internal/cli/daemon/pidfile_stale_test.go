// Tests for the startup pidfile staleness decision — issue #665.
//
// The daemon's startup gate used to refuse to start whenever the pidfile
// named ANY live PID, even after the OS recycled that PID to an unrelated
// process. These tests pin the corrected three-way decision (dead /
// live-non-daemon / live-daemon) via the pure, dependency-injected
// helper `shouldTreatPidfileAsStale`, plus the `/proc`-backed
// `processIsDaemon` identity probe and its conservative fallback.

package daemon

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// alwaysAlive / neverAlive / asDaemon / notDaemon are tiny injectable
// stand-ins so the decision table can be driven without real processes.
func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }
func asDaemon(int) bool    { return true }
func notDaemon(int) bool   { return false }

// TestShouldTreatPidfileAsStale_LiveDaemon is the genuine "already
// running" case: the PID is alive AND is an orchard-daemon, so the
// pidfile is authoritative (NOT stale) and runStart must refuse to start.
func TestShouldTreatPidfileAsStale_LiveDaemon(t *testing.T) {
	if got := shouldTreatPidfileAsStale(1234, alwaysAlive, asDaemon); got {
		t.Errorf("live + is-daemon: shouldTreatPidfileAsStale = true, want false (honour pidfile, refuse start)")
	}
}

// TestShouldTreatPidfileAsStale_LiveNonDaemon is the #665 regression:
// the PID is alive but belongs to an unrelated process (PID reuse). The
// pidfile MUST be treated as stale so the daemon reclaims it and starts.
func TestShouldTreatPidfileAsStale_LiveNonDaemon(t *testing.T) {
	if got := shouldTreatPidfileAsStale(79, alwaysAlive, notDaemon); !got {
		t.Errorf("live + NOT daemon (PID reuse): shouldTreatPidfileAsStale = false, want true (reclaim, proceed) — issue #665 regression")
	}
}

// TestShouldTreatPidfileAsStale_DeadPid is the existing-behaviour
// regression guard: a dead PID means the pidfile is stale and start
// proceeds. The daemon-identity probe must not even be consulted.
func TestShouldTreatPidfileAsStale_DeadPid(t *testing.T) {
	identityConsulted := false
	isDaemon := func(int) bool { identityConsulted = true; return true }

	if got := shouldTreatPidfileAsStale(99999, neverAlive, isDaemon); !got {
		t.Errorf("dead PID: shouldTreatPidfileAsStale = false, want true (proceed to start)")
	}
	if identityConsulted {
		t.Errorf("dead PID: daemon-identity probe was consulted; liveness should short-circuit")
	}
}

// TestProcessIsDaemon_MatchesInjectedName verifies the identity probe
// returns true only when the looked-up process name contains the daemon
// binary basename. The name lookup is injected so the test does not
// depend on a real /proc entry.
func TestProcessIsDaemon_MatchesInjectedName(t *testing.T) {
	orig := procNameReader
	t.Cleanup(func() { procNameReader = orig })

	cases := []struct {
		name string
		read func(int) (string, bool)
		want bool
	}{
		{"bare comm", func(int) (string, bool) { return "orchard-daemon", true }, true},
		{"path-prefixed cmdline", func(int) (string, bool) { return "/usr/local/bin/orchard-daemon daemon start", true }, true},
		{"unrelated process", func(int) (string, bool) { return "boxd-orchardist-start.sh", true }, false},
		{"empty name", func(int) (string, bool) { return "", true }, false},
		{"no introspection falls back to true", func(int) (string, bool) { return "", false }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			procNameReader = tc.read
			if got := processIsDaemon(4321); got != tc.want {
				t.Errorf("processIsDaemon = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProcessIsDaemon_ConservativeFallback isolates the platform we cannot
// introspect: when the name reader reports ok=false, processIsDaemon must
// assume the live PID is the daemon, preserving the pre-#665 double-start
// guard rather than silently reclaiming an unknown live process.
func TestProcessIsDaemon_ConservativeFallback(t *testing.T) {
	orig := procNameReader
	t.Cleanup(func() { procNameReader = orig })
	procNameReader = func(int) (string, bool) { return "irrelevant", false }

	if !processIsDaemon(4321) {
		t.Errorf("ok=false fallback: processIsDaemon = false, want true (conservative, do not double-start)")
	}
}

// TestReadProcName_CurrentProcess exercises the real Linux /proc reader
// against this very test binary (a genuinely-live PID). The reported name
// must be non-empty and must NOT name orchard-daemon — the test binary is
// the live-non-daemon case from #665, end to end against real /proc.
func TestReadProcName_CurrentProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readProcName reads /proc; Linux only")
	}
	name, ok := readProcName(os.Getpid())
	if !ok {
		t.Fatalf("readProcName(self) ok = false; want true on Linux")
	}
	if strings.TrimSpace(name) == "" {
		t.Fatalf("readProcName(self) name empty; want the test binary name")
	}
	if strings.Contains(name, daemonProcessName) {
		t.Fatalf("readProcName(self) = %q unexpectedly contains %q; the go-test binary is not the daemon", name, daemonProcessName)
	}
	// And the composed identity probe must agree this is not the daemon.
	if processIsDaemon(os.Getpid()) {
		t.Errorf("processIsDaemon(self) = true; the test binary is a live non-daemon process")
	}
}

// TestReadProcName_DeadPid confirms the reader reports ok=false for a PID
// with no /proc entry. PID 0x7FFFFFFF is effectively never live, so this
// stays deterministic without spawning and reaping a child.
func TestReadProcName_DeadPid(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readProcName reads /proc; Linux only")
	}
	if _, ok := readProcName(0x7FFFFFFF); ok {
		t.Errorf("readProcName(dead pid) ok = true; want false (no /proc entry)")
	}
}
