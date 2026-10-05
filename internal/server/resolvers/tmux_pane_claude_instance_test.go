package resolvers

// Tests for tmuxPaneResolver.ClaudeInstance (ADR-022 Phase 5).
//
// Scenarios covered:
//  1. Pane with CurrentCommand=="claude" → resolver returns a non-nil instance.
//  2. Pane with CurrentCommand!="claude" → resolver returns (nil, nil).
//  3. r.Tmux is nil → resolver returns (nil, nil).

import (
	"context"
	"testing"

	graphql1 "github.com/drewdrewthis/orchardist/internal/server/graphql"
	tmuxprovider "github.com/drewdrewthis/orchardist/internal/server/providers/tmux"
)

// TestTmuxPaneClaudeInstance_ClaudePaneReturnsInstance verifies that a pane
// whose CurrentCommand is "claude" produces a non-nil ClaudeInstance.
func TestTmuxPaneClaudeInstance_ClaudePaneReturnsInstance(t *testing.T) {
	prov := tmuxprovider.New(tmuxprovider.NewAdapter(tmuxprovider.HostID("local")), nil)
	r := &tmuxPaneResolver{&Resolver{Tmux: prov}}
	obj := &graphql1.TmuxPane{
		ID:             "TmuxPane:local:%59",
		PaneID:         "%59",
		CurrentCommand: "claude",
	}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance() returned nil, want non-nil *graphql1.ClaudeInstance for claude pane")
	}
}

// TestTmuxPaneClaudeInstance_NonClaudePaneReturnsNil verifies that a pane
// whose CurrentCommand is not "claude" returns (nil, nil).
func TestTmuxPaneClaudeInstance_NonClaudePaneReturnsNil(t *testing.T) {
	prov := tmuxprovider.New(tmuxprovider.NewAdapter(tmuxprovider.HostID("local")), nil)
	r := &tmuxPaneResolver{&Resolver{Tmux: prov}}
	obj := &graphql1.TmuxPane{
		ID:             "TmuxPane:local:%99",
		PaneID:         "%99",
		CurrentCommand: "vim",
	}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (non-claude pane)", got)
	}
}

// TestTmuxPaneClaudeInstance_NilTmuxReturnsNil verifies that when r.Tmux
// is nil the resolver returns (nil, nil) without panicking.
func TestTmuxPaneClaudeInstance_NilTmuxReturnsNil(t *testing.T) {
	r := &tmuxPaneResolver{&Resolver{Tmux: nil}}
	obj := &graphql1.TmuxPane{ID: "TmuxPane:local:%42", CurrentCommand: "claude"}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (nil tmux provider)", got)
	}
}
