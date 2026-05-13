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

// snapshotWithPanes builds a RuntimeSnapshot containing the given panes plus
// synthesized Sessions/Windows entries keyed off each pane's WindowKey/Session.
// Tests that assert pane.Window/Session edges in the projected GraphQL value
// require Sessions to be populated (projectPaneWithSession reads from snap.Sessions).
func snapshotWithPanes(panes ...tmuxprovider.Pane) tmuxprovider.RuntimeSnapshot {
	paneMap := make(map[tmuxprovider.PaneKey]tmuxprovider.Pane, len(panes))
	sessMap := make(map[tmuxprovider.SessionKey]tmuxprovider.Session)
	winMap := make(map[tmuxprovider.WindowKey]tmuxprovider.Window)
	for _, p := range panes {
		paneMap[p.Key] = p
		sk := tmuxprovider.SessionKey{Host: p.Key.Host, Name: p.WindowKey.Session}
		if _, ok := sessMap[sk]; !ok {
			sessMap[sk] = tmuxprovider.Session{Key: sk}
		}
		if _, ok := winMap[p.WindowKey]; !ok {
			winMap[p.WindowKey] = tmuxprovider.Window{Key: p.WindowKey}
		}
	}
	return tmuxprovider.RuntimeSnapshot{
		Sessions: sessMap,
		Windows:  winMap,
		Panes:    paneMap,
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

// TestTmuxInputAdapter_PaneBySession_NoClaudeMatch_ReturnsFallback verifies the
// issue #580 fallback: when ps cross-check finds no claude-named pane, the
// first pane with a non-zero pid in the session is returned with CurrentPid
// stripped. The composer needs the pane.window.session edge for self-
// suppression even when ps can't confirm a claude basename.
func TestTmuxInputAdapter_PaneBySession_NoClaudeMatch_ReturnsFallback(t *testing.T) {
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
	if !ok {
		t.Fatal("PaneBySession (no claude match): expected ok=true (fallback), got false")
	}
	if pane == nil {
		t.Fatal("PaneBySession (no claude match): expected non-nil fallback pane, got nil")
	}
	if pane.CurrentPid != nil {
		t.Errorf("PaneBySession (no claude match): expected stripped CurrentPid (nil), got %v", *pane.CurrentPid)
	}
	if pane.Window == nil || pane.Window.Session == nil || pane.Window.Session.Name != "no-claude" {
		t.Errorf("PaneBySession (no claude match): expected pane.Window.Session.Name = %q, got %+v", "no-claude", pane.Window)
	}
}

// TestTmuxInputAdapter_PaneBySession_PsCacheMiss_ReturnsFallback covers the
// real-world issue #580 trigger: ps cross-check returns ok=false (pid not in
// ps cache, ps provider stale, or ps unavailable). The fallback pane must be
// returned with CurrentPid stripped, but pane.window.session edges must be
// populated so attend.sh self-suppression can match.
func TestTmuxInputAdapter_PaneBySession_PsCacheMiss_ReturnsFallback(t *testing.T) {
	snap := snapshotWithPanes(
		makePane("%21", "orchardist", 95507),
	)
	ps := &staticPsGetter{commands: map[int]string{}} // pid 95507 absent — cache miss

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: ps,
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "orchardist")
	if !ok {
		t.Fatal("PaneBySession (ps cache miss): expected ok=true (fallback), got false")
	}
	if pane == nil {
		t.Fatal("PaneBySession (ps cache miss): expected non-nil fallback pane, got nil")
	}
	if pane.PaneID != "%21" {
		t.Errorf("PaneBySession (ps cache miss): got pane %q, want %%21", pane.PaneID)
	}
	if pane.CurrentPid != nil {
		t.Errorf("PaneBySession (ps cache miss): expected stripped CurrentPid (nil), got %v", *pane.CurrentPid)
	}
	if pane.Window == nil || pane.Window.Session == nil || pane.Window.Session.Name != "orchardist" {
		t.Errorf("PaneBySession (ps cache miss): expected pane.Window.Session.Name = %q, got %+v", "orchardist", pane.Window)
	}
}

// TestTmuxInputAdapter_PaneBySession_DeterministicOrder verifies that when a
// session has multiple eligible panes, PaneBySession picks the same one every
// call. snap.Panes is a Go map (randomized iteration) so the implementation
// must sort candidates explicitly. Running the call many times in a tight
// loop catches map-order regressions even if a single run got lucky.
func TestTmuxInputAdapter_PaneBySession_DeterministicOrder(t *testing.T) {
	// Five non-claude vim panes in the same session. With map iteration the
	// chosen fallback would drift; with sort it must lock onto %10 (lowest
	// pane id) every time.
	snap := snapshotWithPanes(
		makePane("%14", "deterministic", 1004),
		makePane("%10", "deterministic", 1000),
		makePane("%13", "deterministic", 1003),
		makePane("%11", "deterministic", 1001),
		makePane("%12", "deterministic", 1002),
	)
	ps := &staticPsGetter{commands: map[int]string{
		1000: "vim", 1001: "vim", 1002: "vim", 1003: "vim", 1004: "vim",
	}}
	a := &tmuxInputAdapter{p: &staticSnapshotter{snap: snap}, ps: ps}

	for i := 0; i < 100; i++ {
		pane, ok := a.PaneBySession(context.Background(), "local", "deterministic")
		if !ok || pane == nil {
			t.Fatalf("iteration %d: expected ok=true and non-nil pane", i)
		}
		if pane.PaneID != "%10" {
			t.Fatalf("iteration %d: got pane %q, want %%10 (lowest pane id)", i, pane.PaneID)
		}
	}
}

// TestTmuxInputAdapter_PaneBySession_NoSessionPanes_ReturnsNil verifies that
// when the named session genuinely has no panes (heartbeat references a
// session that doesn't exist in tmux), PaneBySession returns nil, false.
func TestTmuxInputAdapter_PaneBySession_NoSessionPanes_ReturnsNil(t *testing.T) {
	snap := snapshotWithPanes(
		makePane("%30", "other-session", 1234),
	)
	ps := &staticPsGetter{commands: map[int]string{1234: "claude"}}

	a := &tmuxInputAdapter{
		p:  &staticSnapshotter{snap: snap},
		ps: ps,
	}

	pane, ok := a.PaneBySession(context.Background(), "local", "missing-session")
	if ok {
		t.Error("PaneBySession (no session panes): expected ok=false, got true")
	}
	if pane != nil {
		t.Errorf("PaneBySession (no session panes): expected nil pane, got %v", pane)
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
	// Candidates are sorted by (window index, pane id); %11 has the lowest
	// pane id among non-zero-pid panes, so it is the deterministic winner.
	if pane.PaneID != "%11" {
		t.Errorf("PaneBySession (nil ps): got pane %q, want %%11 (lowest pane id, non-zero pid)", pane.PaneID)
	}
	if pane.CurrentPid == nil || *pane.CurrentPid == 0 {
		t.Errorf("PaneBySession (nil ps): got pane with zero/nil CurrentPid, want non-zero")
	}
}
