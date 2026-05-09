// Regression test for issue #464: FetchAll issues too many tmux execs
// (6 total: info, list-sessions, list-windows, list-panes, list-clients,
// display-message). The fork-storm trips systemd-oomd / cgroup pressure
// on Linux/systemd-user and the kernel SIGKILLs list-windows. This test
// uses a fake CommandRunner so the structural guardrail runs on every
// platform; the Linux-only sandbox-tmux integration repro lives in a
// separate file.
//
// AC being exercised:
//   AC1 — A single FetchAll cycle issues at most 3 tmux subprocess calls.

package tmux

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// countingRunner implements CommandRunner. It atomically counts every Run
// call, records the first meaningful arg (the tmux subcommand), and returns
// realistic empty-but-valid output so FetchAll can complete successfully.
type countingRunner struct {
	count atomic.Int64
	calls []string // subcommand names, appended on each Run
}

// Run satisfies CommandRunner. It increments the counter, records the
// subcommand, and returns canned output that allows the adapter to treat
// the daemon as alive with zero sessions/windows/panes/clients.
func (r *countingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.count.Add(1)

	// Extract the tmux subcommand from args. tmuxArgs may prepend "-S
	// <socket>" global flags; the subcommand is the first non-flag arg.
	sub := subcommandOf(args)
	r.calls = append(r.calls, sub)

	switch sub {
	case "display-message":
		// serverInfo reads the pid from display-message output.
		return []byte("12345"), nil
	default:
		// info, list-sessions, list-windows, list-panes, list-clients:
		// empty output = alive server with no sessions (valid empty state).
		return []byte(""), nil
	}
}

// subcommandOf returns the first non-flag element from a tmux arg list.
// Skips "-S <path>" and "-L <name>" global flag pairs that tmuxArgs may
// prepend. Falls back to the whole arg-slice joined as a string so the
// test assertion is still readable if the adapter changes its flag order.
func subcommandOf(args []string) string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-S", "-L":
			i++ // skip the value that follows
		default:
			if !strings.HasPrefix(args[i], "-") {
				return args[i]
			}
		}
	}
	return strings.Join(args, " ")
}

// TestRegression_FetchAllExecCount_Issue464 asserts that a single FetchAll
// cycle calls the tmux binary at most 3 times. The original unpatched code
// called it 6 times (info + list-sessions + list-windows + list-panes +
// list-clients + display-message), which is the bug described in #464.
//
// The patched path uses 3 execs: info (IsAlive), list-panes -a (listAll),
// and list-clients (listClients). list-clients was briefly dropped to hit a
// ≤2 target but silently broke five GraphQL fields (clients, attachedClients,
// activeAttached, watchingClients, subscribeTmuxClientChanged). Restoring it
// yields 3 execs/tick — a 50 % reduction from 6, still well below the
// oomd-trip threshold.
//
// Scope: this test bounds FetchAll only. CapturePane / CapturePaneTail are
// on-demand (not poll-driven) and intentionally excluded — calls to those
// are user-initiated and do not contribute to the per-tick fork-storm
// pressure that triggered #464.
//
// The test FAILS on unpatched code (count == 6) and PASSES after the fix.
func TestRegression_FetchAllExecCount_Issue464(t *testing.T) {
	runner := &countingRunner{}
	adapter := NewAdapter("test").WithRunner(runner)

	ctx := context.Background()
	_, err := adapter.FetchAll(ctx)
	if err != nil {
		t.Fatalf("FetchAll returned unexpected error: %v", err)
	}

	got := int(runner.count.Load())
	const maxExecs = 3
	if got > maxExecs {
		t.Errorf(
			"issue #464 regression: FetchAll issued %d tmux execs, want ≤ %d\n"+
				"  subcommands called: %v\n"+
				"  Patching note: FetchAll must use ≤3 execs (info + list-panes + list-clients).",
			got, maxExecs, runner.calls,
		)
	}

	// Sub-test: lenient check on the exact subcommands invoked. The
	// implementer is free to choose any coalescing strategy (e.g. a single
	// list-panes -a -F ... that implies sessions/windows, or a combined
	// format string). We only require:
	//   1. Some alive-equivalent probe is present (e.g. "info").
	//   2. Pane data is fetched (list-panes covers sessions+windows hierarchy).
	//
	// This sub-test is advisory — it documents intent without over-specifying
	// the implementation. The hard assertion above is what must pass.
	t.Run("invocation_set", func(t *testing.T) {
		callSet := make(map[string]bool, len(runner.calls))
		for _, c := range runner.calls {
			callSet[c] = true
		}

		// After the fix the alive probe should still be present.
		hasAliveProbe := callSet["info"] || callSet["display-message"] || callSet["server-info"]
		if !hasAliveProbe {
			t.Logf("advisory: expected an alive-probe subcommand (info/display-message/server-info); got %v", runner.calls)
		}

		// After the fix pane data should still be fetched — it is the richest
		// source (panes imply windows which imply sessions in tmux's hierarchy).
		if !callSet["list-panes"] {
			t.Logf("advisory: expected list-panes in subcommands after fix; got %v", runner.calls)
		}
	})
}
