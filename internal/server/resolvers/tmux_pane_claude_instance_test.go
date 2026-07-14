package resolvers

// Tests for tmuxPaneResolver.ClaudeInstance (ADR-022 Phase 5).
//
// These tests address the pane by node id ONLY — `&graphql1.TmuxPane{ID: ...}`
// — because that is the exact object production hands the resolver:
// tmuxWindowResolver.Panes projects via projectPane(), which sets {ID, PaneID}
// and nothing else.
//
// An earlier version of this file constructed obj with CurrentCommand
// pre-populated. No production path produces that shape, so the suite passed
// while `TmuxPane.claudeInstance` returned null for every pane on the live box
// — attend.js reads that field and treats null as healthy, leaving the
// attention feed blind to every worker (#706). Seed the provider and address
// panes by id; never hand the resolver a field production would not set.

import (
	"context"
	"testing"

	graphql1 "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// TestTmuxPaneClaudeInstance_ShellWrappedClaudeByNodeID is the #706 regression.
// A worker launched as `bash -> claude` has pane_pid = the bash pid, so ps
// answers "-bash" while tmux's pane_current_command answers "claude". The pane
// is addressed by node id, exactly as Window.panes does.
func TestTmuxPaneClaudeInstance_ShellWrappedClaudeByNodeID(t *testing.T) {
	const pid = 806301 // real bash wrapper pid from the live box (#706)
	const paneID = "%21"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("orchard_issue706", paneID, pid, "claude"),
	})
	psProv := buildPsProvider(t, []psRow{{pid: pid, cmd: "-bash"}})

	r := &tmuxPaneResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, paneID)}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance() = nil for a live claude pane addressed by node id; " +
			"attend.js reads this field and treats null as healthy, so every worker is invisible (#706)")
	}
}

// TestTmuxPaneClaudeInstance_ExecdClaudeByNodeID covers the other launch shape:
// claude exec'd directly, so the pane's root process IS claude and ps agrees.
func TestTmuxPaneClaudeInstance_ExecdClaudeByNodeID(t *testing.T) {
	const pid = 1648 // real exec'd claude pid from the live box (#706)
	const paneID = "%0"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("assistant", paneID, pid, "claude"),
	})
	psProv := buildPsProvider(t, []psRow{{pid: pid, cmd: "claude"}})

	r := &tmuxPaneResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, paneID)}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance() = nil for an exec'd claude pane addressed by node id")
	}
}

// TestTmuxPaneClaudeInstance_NonClaudePaneReturnsNil guards against
// over-matching: a plain shell pane with no claude must still resolve to nil.
func TestTmuxPaneClaudeInstance_NonClaudePaneReturnsNil(t *testing.T) {
	const pid = 13036
	const paneID = "%4"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("agents-view", paneID, pid, "bash"),
	})
	psProv := buildPsProvider(t, []psRow{{pid: pid, cmd: "-bash"}})

	r := &tmuxPaneResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, paneID)}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (plain shell pane)", got)
	}
}

// TestTmuxPaneClaudeInstance_UnknownPaneReturnsNil covers a node id the
// provider has no pane for (e.g. the pane died between resolution steps).
func TestTmuxPaneClaudeInstance_UnknownPaneReturnsNil(t *testing.T) {
	tmuxProv := buildTmuxProvider(t, nil)
	r := &tmuxPaneResolver{&Resolver{Tmux: tmuxProv}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, "%404")}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (unknown pane)", got)
	}
}

// TestTmuxPaneClaudeInstance_NilTmuxReturnsNil verifies that when r.Tmux
// is nil the resolver returns (nil, nil) without panicking.
func TestTmuxPaneClaudeInstance_NilTmuxReturnsNil(t *testing.T) {
	r := &tmuxPaneResolver{&Resolver{Tmux: nil}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, "%42")}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (nil tmux provider)", got)
	}
}
