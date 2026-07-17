package claudeinstance

// Tests for DeriveInstanceState — the pane-first state-derivation entrypoint
// (ADR-022 Phase 4). These pin down the causal core of orchardist#710: a live,
// actively-working claude is reported "idle" whenever its cwd (or the session
// it resolves to) is empty. On Linux the ps adapter's cwd resolver was stubbed
// to return "" for every pid (fixed in this PR by reading /proc/<pid>/cwd), so
// `Cwd` arrived empty here and EVERY instance fell through to the idle branch
// below — regardless of what the process was actually doing.
//
// TestDeriveInstanceState_EmptyCwdPinsWorkingClaudeToIdle is the linchpin: it
// feeds a snapshot that unambiguously classifies as Working, yet asserts idle,
// because Cwd is empty. That is exactly the symptom #710 reported on the live
// box. The companion "resolved cwd -> working" test is the fix's payoff.

import (
	"context"
	"testing"
	"time"

	"github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// stubSnapshotReader returns a fixed set of records, letting a test drive
// DeriveInstanceState without touching the filesystem.
type stubSnapshotReader struct {
	records []Record
	ok      bool
}

func (s stubSnapshotReader) ReadSnapshot(_ context.Context, _, _ string) ([]Record, bool) {
	return s.records, s.ok
}

// stubLiveness reports a fixed liveness verdict for any pid.
type stubLiveness struct{ alive bool }

func (s stubLiveness) IsAlive(int) bool { return s.alive }

// workingSnapshot classifies as InstanceStateWorking: an assistant turn ended
// on tool_use with one open tool and no matching result.
func workingSnapshot() stubSnapshotReader {
	return stubSnapshotReader{ok: true, records: []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "tool_use", []ContentItem{toolUseContent("toolu_X", "Bash")}),
	}}
}

// idleSnapshot classifies as InstanceStateIdle: the turn ended (end_turn) and a
// turn_duration system record followed.
func idleSnapshot() stubSnapshotReader {
	return stubSnapshotReader{ok: true, records: []Record{
		userRecord(ts(0), nil),
		assistantRecord(ts(1), "end_turn", nil),
		systemRecord(ts(2), "turn_duration"),
	}}
}

func deriveWith(cwd, sessionUUID string, pid int, alive bool, snap SnapshotReader) graphql.InstanceState {
	state, _ := DeriveInstanceState(context.Background(), DeriveState{
		Cwd:         cwd,
		SessionUUID: sessionUUID,
		Pid:         pid,
		Snapshot:    snap,
		Liveness:    stubLiveness{alive: alive},
		Clock:       func() time.Time { return testNow },
	})
	return state
}

// TestDeriveInstanceState_EmptyCwdPinsWorkingClaudeToIdle is the #710 core:
// with an empty cwd, an actively-working claude is misreported as idle because
// derivation short-circuits before it ever reads the (working) snapshot. This
// is what the Linux cwd stub produced for every instance.
func TestDeriveInstanceState_EmptyCwdPinsWorkingClaudeToIdle(t *testing.T) {
	got := deriveWith("", "sess-uuid", 4242, true /*alive*/, workingSnapshot())
	if got != graphql.InstanceStateIdle {
		t.Fatalf("state = %s, want idle (empty cwd must short-circuit to idle — the #710 symptom)", got)
	}
}

// TestDeriveInstanceState_EmptySessionUUIDYieldsIdle covers the other half of
// the short-circuit: a resolvable cwd that matches no conversation (sessionUUID
// empty) also falls back to idle.
func TestDeriveInstanceState_EmptySessionUUIDYieldsIdle(t *testing.T) {
	got := deriveWith("/home/ubuntu/.claude", "", 4242, true, workingSnapshot())
	if got != graphql.InstanceStateIdle {
		t.Fatalf("state = %s, want idle (empty sessionUUID must short-circuit to idle)", got)
	}
}

// TestDeriveInstanceState_ResolvedCwdYieldsWorking is the fix's payoff: once cwd
// AND sessionUUID resolve (which they now do on Linux), a working snapshot
// derives InstanceStateWorking for a live claude — matching what session-truth
// reports for an actively-working process.
func TestDeriveInstanceState_ResolvedCwdYieldsWorking(t *testing.T) {
	got := deriveWith("/home/ubuntu/.claude", "sess-uuid", 4242, true, workingSnapshot())
	if got != graphql.InstanceStateWorking {
		t.Fatalf("state = %s, want working (resolved cwd+sessionUUID + working snapshot)", got)
	}
}

// TestDeriveInstanceState_ResolvedCwdIdleSnapshotYieldsIdle proves derivation
// distinguishes a genuinely idle-at-prompt claude from a working one — the
// "distinct state for a stalled/idle one" half of the DoD.
func TestDeriveInstanceState_ResolvedCwdIdleSnapshotYieldsIdle(t *testing.T) {
	got := deriveWith("/home/ubuntu/.claude", "sess-uuid", 4242, true, idleSnapshot())
	if got != graphql.InstanceStateIdle {
		t.Fatalf("state = %s, want idle (resolved cwd + idle snapshot)", got)
	}
}

// TestDeriveInstanceState_DeadPidYieldsNoClaude verifies a dead pid is reported
// no_claude before any snapshot read, independent of cwd resolution.
func TestDeriveInstanceState_DeadPidYieldsNoClaude(t *testing.T) {
	got := deriveWith("/home/ubuntu/.claude", "sess-uuid", 4242, false /*dead*/, workingSnapshot())
	if got != graphql.InstanceStateNoClaude {
		t.Fatalf("state = %s, want no_claude (dead pid)", got)
	}
}
