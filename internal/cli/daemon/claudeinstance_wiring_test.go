package daemon

// claudeinstance_wiring_test.go — unit tests for tmuxInputAdapter.PaneBySession.
//
// These tests exercise the core fix for issue #468: when a tmux session contains
// multiple panes (e.g. [vim, claude]), PaneBySession must iterate ALL panes and
// return only the one owned by a "claude" process — not the first pane with a
// non-zero pid regardless of command.
//
// Tests use inline stubs for tmuxSnapshotter and psGetter so no real tmux or ps
// providers are needed.

import (
	"context"
	"testing"

	tmuxprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// ---------------------------------------------------------------------------
// Stubs for the narrow internal interfaces.
// ---------------------------------------------------------------------------

// staticSnapshotter implements tmuxSnapshotter with a fixed RuntimeSnapshot.
type staticSnapshotter struct {
	snap tmuxprovider.RuntimeSnapshot
}

func (s *staticSnapshotter) Snapshot() tmuxprovider.RuntimeSnapshot {
	return s.snap
}

// staticPsGetter implements psGetter with a fixed pid→command map.
type staticPsGetter struct {
	commands map[int]string // pid → command basename
}

func (g *staticPsGetter) CommandForPid(_ context.Context, _ string, pid int) (string, bool) {
	cmd, ok := g.commands[pid]
	return cmd, ok
}

// ---------------------------------------------------------------------------
// Helper to build a Snapshot with specific panes.
// ---------------------------------------------------------------------------

// makePane builds a tmuxprovider.Pane with the given session, paneID, and
// currentPid. host is always "local".
func makePane(paneID, session string, currentPid int) tmuxprovider.Pane {
	return tmuxprovider.Pane{
		Key:        tmuxprovider.PaneKey{Host: tmuxprovider.HostID("local"), PaneID: paneID},
		WindowKey:  tmuxprovider.WindowKey{Host: tmuxprovider.HostID("local"), Session: session},
		CurrentPid: currentPid,
	}
}

// snapshotWithPanes builds a RuntimeSnapshot containing exactly the given panes.
func snapshotWithPanes(panes ...tmuxprovider.Pane) tmuxprovider.RuntimeSnapshot {
	m := make(map[tmuxprovider.PaneKey]tmuxprovider.Pane, len(panes))
	for _, p := range panes {
		m[p.Key] = p
	}
	return tmuxprovider.RuntimeSnapshot{
		Sessions: map[tmuxprovider.SessionKey]tmuxprovider.Session{},
		Windows:  map[tmuxprovider.WindowKey]tmuxprovider.Window{},
		Panes:    m,
		Clients:  map[tmuxprovider.ClientKey]tmuxprovider.Client{},
	}
}

// ---------------------------------------------------------------------------
// Tests for tmuxInputAdapter.PaneBySession
// ---------------------------------------------------------------------------

// TestTmuxInputAdapter_PaneBySession_ClaudeProcessWins verifies that when a
// session has [vim, claude] panes, PaneBySession returns the claude pane (%11),
// not vim (%10). This is the core regression from issue #468.
func TestTmuxInputAdapter_PaneBySession_ClaudeProcessWins(t *testing.T) {
	snap := snapshotWithPanes(
		makePane("%10", "issue468", 1000), // vim — non-zero pid, but NOT claude
		makePane("%11", "issue468", 2000), // claude — should be returned
	)
	ps := &staticPsGetter{commands: map[int]string{
		1000: "vim",
		2000: "claude",
	}}

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: ps,
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "issue468")
	if !ok {
		t.Fatal("PaneBySession: expected ok=true, got false")
	}
	if pane == nil {
		t.Fatal("PaneBySession: expected non-nil pane, got nil")
	}
	if pane.PaneID != "%11" {
		t.Errorf("PaneBySession: got pane %q, want %%11 (claude pane)", pane.PaneID)
	}
}

// TestTmuxInputAdapter_PaneBySession_NoPanes_ReturnsNil verifies that an
// empty session (no panes at all) returns nil, false.
func TestTmuxInputAdapter_PaneBySession_NoPanes_ReturnsNil(t *testing.T) {
	snap := snapshotWithPanes() // empty
	ps := &staticPsGetter{commands: map[int]string{}}

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: ps,
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "issue468")
	if ok {
		t.Error("PaneBySession (no panes): expected ok=false, got true")
	}
	if pane != nil {
		t.Errorf("PaneBySession (no panes): expected nil pane, got %v", pane)
	}
}

// TestTmuxInputAdapter_PaneBySession_NoClaudeProcess_ReturnsNil verifies that
// when a session has [vim, vim] panes (no claude process), PaneBySession
// returns nil, false.
func TestTmuxInputAdapter_PaneBySession_NoClaudeProcess_ReturnsNil(t *testing.T) {
	snap := snapshotWithPanes(
		makePane("%10", "no-claude", 1000), // vim
		makePane("%12", "no-claude", 1001), // also vim
	)
	ps := &staticPsGetter{commands: map[int]string{
		1000: "vim",
		1001: "vim",
	}}

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: ps,
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "no-claude")
	if ok {
		t.Error("PaneBySession (no claude): expected ok=false, got true")
	}
	if pane != nil {
		t.Errorf("PaneBySession (no claude): expected nil pane, got %v", pane)
	}
}

// TestTmuxInputAdapter_PaneBySession_NilPs_ReturnsFirstNonZeroPid verifies
// that when ps is nil (no cross-check), PaneBySession returns the first pane
// with a non-zero currentPid — v1 behaviour.
func TestTmuxInputAdapter_PaneBySession_NilPs_ReturnsFirstNonZeroPid(t *testing.T) {
	snap := snapshotWithPanes(
		makePane("%10", "alpha", 0),    // zero pid — skip
		makePane("%11", "alpha", 1234), // first non-zero pid
		makePane("%12", "alpha", 5678), // also non-zero, but second
	)

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: nil, // no ps — v1 fallback
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "alpha")
	if !ok {
		t.Fatal("PaneBySession (nil ps): expected ok=true, got false")
	}
	if pane == nil {
		t.Fatal("PaneBySession (nil ps): expected non-nil pane, got nil")
	}
	// When ps is nil we expect the first pane with non-zero pid.
	// Map iteration order is non-deterministic in Go, so we just assert
	// we got one of the non-zero-pid panes (not %10 which has pid 0).
	if pane.PaneID == "%10" {
		t.Errorf("PaneBySession (nil ps): got pane %%10 (pid=0), want a non-zero-pid pane")
	}
	if pane.CurrentPid == nil || *pane.CurrentPid == 0 {
		t.Errorf("PaneBySession (nil ps): got pane with zero/nil CurrentPid, want non-zero")
	}
}
