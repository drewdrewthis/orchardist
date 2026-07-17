package resolvers

// Tests for issue #711 — ClaudeInstance identity must be the real claude pid,
// not the pane's shell-wrapper pid.
//
// Every worker on the box launches `bash -> claude` (via `tmux send-keys`), so
// tmux's pane_pid is the BASH wrapper. schema.graphql defines a ClaudeInstance's
// identity as "the foreground claude pid scoped to a host", yet the projection
// keyed id / process / cwd off the wrapper pid. These tests address the pane by
// the exact shape production hands the resolver (a pane row selected by
// pane_current_command) and assert identity resolves through the descendant tree
// to claude — the falsifying case for #711.

import (
	"context"
	"testing"

	graphql1 "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// TestClaudeInstances_IdentityResolvesToClaudePid_NotWrapper is the #711
// regression. A pane rooted at a bash wrapper (pane_pid) with a claude child
// must yield a ClaudeInstance whose id and process.pid are the CHILD claude pid.
// Pids are the real ones from the live box (#706/#711): 806301 bash, 806825 claude.
func TestClaudeInstances_IdentityResolvesToClaudePid_NotWrapper(t *testing.T) {
	const wrapperPid = 806301 // bash — the pane's root process (tmux pane_pid)
	const claudePid = 806825  // claude — a child of the wrapper
	const paneID = "%21"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("orchard_issue711", paneID, wrapperPid, "claude"),
	})
	psProv := buildPsProvider(t, []psRow{
		{pid: wrapperPid, ppid: 1, cmd: "-bash"},
		{pid: claudePid, ppid: wrapperPid, cmd: "claude"},
	})

	r := &queryResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	insts, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("ClaudeInstances() error: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("want exactly 1 instance, got %d: %+v", len(insts), insts)
	}
	got := insts[0]

	const wantID = "ClaudeInstance:local:806825"
	if got.ID != wantID {
		t.Errorf("id = %q, want %q — identity must key on the claude pid (%d), not the bash wrapper (%d)",
			got.ID, wantID, claudePid, wrapperPid)
	}
	if got.Process == nil {
		t.Fatalf("process = nil, want the claude process (pid %d)", claudePid)
	}
	if got.Process.Pid != claudePid {
		t.Errorf("process.pid = %d, want %d (the claude pid, not the wrapper %d)",
			got.Process.Pid, claudePid, wrapperPid)
	}
	if got.Process.Command != "claude" {
		t.Errorf("process.command = %q, want %q", got.Process.Command, "claude")
	}
}

// TestClaudeInstances_ExecdClaudeKeepsOwnPid covers the other launch shape:
// claude exec'd directly, so the pane_pid IS claude. Identity must be that pid
// unchanged — the descendant walk must not wander off to some grandchild.
func TestClaudeInstances_ExecdClaudeKeepsOwnPid(t *testing.T) {
	const claudePid = 1648 // real exec'd claude pid from the live box
	const paneID = "%0"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("assistant", paneID, claudePid, "claude"),
	})
	psProv := buildPsProvider(t, []psRow{
		{pid: claudePid, ppid: 1, cmd: "claude"},
		// a bun child, as claude actually has on the box — must be ignored
		{pid: 2902, ppid: claudePid, cmd: "bun"},
	})

	r := &queryResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	insts, err := r.ClaudeInstances(context.Background())
	if err != nil {
		t.Fatalf("ClaudeInstances() error: %v", err)
	}
	if len(insts) != 1 {
		t.Fatalf("want exactly 1 instance, got %d: %+v", len(insts), insts)
	}
	got := insts[0]
	const wantID = "ClaudeInstance:local:1648"
	if got.ID != wantID {
		t.Errorf("id = %q, want %q (exec'd claude keeps its own pid)", got.ID, wantID)
	}
	if got.Process == nil || got.Process.Pid != claudePid {
		t.Errorf("process.pid = %v, want %d", got.Process, claudePid)
	}
}

// TestTmuxPaneClaudeInstance_IdentityResolvesToClaudePid covers the SUBSCRIPTION
// surface: attend.js reads pane.claudeInstance.state via the single-pane
// tmuxPane.claudeInstance resolver (not Query.claudeInstances). That resolver
// shares buildClaudeInstanceFromPane, so identity must resolve to the claude pid
// here too — otherwise the fleet-attention feed keys on the wrong node id (#711).
func TestTmuxPaneClaudeInstance_IdentityResolvesToClaudePid(t *testing.T) {
	const wrapperPid = 806301
	const claudePid = 806825
	const paneID = "%21"

	tmuxProv := buildTmuxProvider(t, []string{
		paneRowWithCommand("orchard_issue711", paneID, wrapperPid, "claude"),
	})
	psProv := buildPsProvider(t, []psRow{
		{pid: wrapperPid, ppid: 1, cmd: "-bash"},
		{pid: claudePid, ppid: wrapperPid, cmd: "claude"},
	})

	r := &tmuxPaneResolver{&Resolver{Tmux: tmuxProv, PS: psProv}}
	obj := &graphql1.TmuxPane{ID: paneGQLID(testHost, paneID)}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() error: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance() = nil for a shell-wrapped claude pane")
	}
	const wantID = "ClaudeInstance:local:806825"
	if got.ID != wantID {
		t.Errorf("id = %q, want %q (subscription surface must key on the claude pid, not the wrapper %d)",
			got.ID, wantID, wrapperPid)
	}
	if got.Process == nil || got.Process.Pid != claudePid {
		t.Errorf("process.pid = %v, want %d", got.Process, claudePid)
	}
}
