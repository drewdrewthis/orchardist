// pane_loaders_test.go — DataLoader batching tests for the ADR-022 pane axes:
// PaneByID, PanesByCwd, PanesByCommand.
//
// These tests focus on:
//  1. Nil-provider safety: loaders return empty/nil results without panicking.
//  2. Batch coalescing: N concurrent Load calls with the same key → 1 batch invocation.
//  3. Deduplication: duplicate keys in one batch fire the provider once.
package loaders_test

import (
	"context"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/server/loaders"
)

// TestPaneByID_NilTmux asserts PaneByID returns nil gracefully when no tmux provider is wired.
func TestPaneByID_NilTmux(t *testing.T) {
	bundle := &loaders.ProvidersBundle{} // Tmux is nil
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	got, err := l.PaneByID.Load(ctx, loaders.PaneKey{HostID: "local", PaneID: "%1"})()
	if err != nil {
		t.Fatalf("PaneByID.Load (nil tmux): unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("PaneByID.Load (nil tmux): want nil, got %v", got)
	}
}

// TestPanesByCwd_NilTmux asserts PanesByCwd returns empty slice gracefully.
func TestPanesByCwd_NilTmux(t *testing.T) {
	bundle := &loaders.ProvidersBundle{}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	got, err := l.PanesByCwd.Load(ctx, loaders.CwdKey{HostID: "local", Cwd: "/some/path"})()
	if err != nil {
		t.Fatalf("PanesByCwd.Load (nil tmux): %v", err)
	}
	if got == nil {
		t.Error("PanesByCwd.Load: must not return nil slice")
	}
	if len(got) != 0 {
		t.Errorf("PanesByCwd.Load (nil tmux): got %d panes, want 0", len(got))
	}
}

// TestPanesByCommand_NilTmux asserts PanesByCommand returns empty slice gracefully.
func TestPanesByCommand_NilTmux(t *testing.T) {
	bundle := &loaders.ProvidersBundle{}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	got, err := l.PanesByCommand.Load(ctx, loaders.CommandKey{HostID: "local", Command: "claude"})()
	if err != nil {
		t.Fatalf("PanesByCommand.Load (nil tmux): %v", err)
	}
	if got == nil {
		t.Error("PanesByCommand.Load: must not return nil slice")
	}
	if len(got) != 0 {
		t.Errorf("PanesByCommand.Load (nil tmux): got %d panes, want 0", len(got))
	}
}

// TestPanesByCommand_BatchCount asserts that N concurrent Load calls with the
// same key collapse into exactly one batch invocation (the core N+1 guarantee).
func TestPanesByCommand_BatchCount(t *testing.T) {
	bundle := &loaders.ProvidersBundle{} // nil Tmux: batch fn still runs once
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 10
	key := loaders.CommandKey{HostID: "local", Command: "claude"}

	// Queue N loads before draining (dataloader batches while thunks are queued).
	thunks := make([]func() (interface{}, error), 0, N)
	for i := 0; i < N; i++ {
		thunk := l.PanesByCommand.Load(ctx, key)
		thunks = append(thunks, func() (interface{}, error) { return thunk() })
	}
	for i, thunk := range thunks {
		if _, err := thunk(); err != nil {
			t.Fatalf("thunk %d: %v", i, err)
		}
	}

	// All N loads with the same key → exactly 1 batch call.
	if got := l.PanesByCommandBatchCount(); got != 1 {
		t.Errorf("PanesByCommandBatchCount = %d, want 1 (%d loads should batch)", got, N)
	}
}

// TestPanesByCwd_BatchCount asserts PanesByCwd batches N concurrent same-key loads.
func TestPanesByCwd_BatchCount(t *testing.T) {
	bundle := &loaders.ProvidersBundle{}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 8
	key := loaders.CwdKey{HostID: "local", Cwd: "/workspace/repo"}

	thunks := make([]func() (interface{}, error), 0, N)
	for i := 0; i < N; i++ {
		thunk := l.PanesByCwd.Load(ctx, key)
		thunks = append(thunks, func() (interface{}, error) { return thunk() })
	}
	for i, thunk := range thunks {
		if _, err := thunk(); err != nil {
			t.Fatalf("thunk %d: %v", i, err)
		}
	}

	if got := l.PanesByCwdBatchCount(); got != 1 {
		t.Errorf("PanesByCwdBatchCount = %d, want 1 (%d loads should batch)", got, N)
	}
}

// TestPaneByID_BatchCount asserts PaneByID batches N concurrent same-key loads.
func TestPaneByID_BatchCount(t *testing.T) {
	bundle := &loaders.ProvidersBundle{}
	l := loaders.NewLoaders(bundle)

	ctx := context.Background()
	const N = 6
	key := loaders.PaneKey{HostID: "local", PaneID: "%5"}

	thunks := make([]func() (interface{}, error), 0, N)
	for i := 0; i < N; i++ {
		thunk := l.PaneByID.Load(ctx, key)
		thunks = append(thunks, func() (interface{}, error) { return thunk() })
	}
	for i, thunk := range thunks {
		if _, err := thunk(); err != nil {
			t.Fatalf("thunk %d: %v", i, err)
		}
	}

	if got := l.PaneByIDBatchCount(); got != 1 {
		t.Errorf("PaneByIDBatchCount = %d, want 1 (%d loads should batch)", got, N)
	}
}
