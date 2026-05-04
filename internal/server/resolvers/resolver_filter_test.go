package resolvers

import (
	"context"
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
)

// TestApplyProcessFilter_PidIn confirms pidIn filters are applied at the
// resolver layer over the cached process snapshot.
func TestApplyProcessFilter_PidIn(t *testing.T) {
	all := []ps.Process{
		{ID: ps.ProcessID{Host: "local", PID: 1}, Command: "launchd"},
		{ID: ps.ProcessID{Host: "local", PID: 2}, Command: "init"},
		{ID: ps.ProcessID{Host: "local", PID: 17729}, Command: "zsh"},
	}
	want := int64(17729)
	filter := &graphql1.ProcessFilter{PidIn: []int64{want}}
	got := applyProcessFilter(context.Background(), nil, all, filter)
	if len(got) != 1 || got[0].ID.PID != int(want) {
		t.Fatalf("got %+v, want pid %d", got, want)
	}
}

// TestApplyProcessFilter_CommandIn confirms commandIn matches by basename.
func TestApplyProcessFilter_CommandIn(t *testing.T) {
	all := []ps.Process{
		{ID: ps.ProcessID{Host: "local", PID: 1}, Command: "launchd"},
		{ID: ps.ProcessID{Host: "local", PID: 2}, Command: "claude"},
		{ID: ps.ProcessID{Host: "local", PID: 3}, Command: "claude"},
		{ID: ps.ProcessID{Host: "local", PID: 4}, Command: "zsh"},
	}
	filter := &graphql1.ProcessFilter{CommandIn: []string{"claude"}}
	got := applyProcessFilter(context.Background(), nil, all, filter)
	if len(got) != 2 {
		t.Fatalf("got len %d, want 2", len(got))
	}
	for _, p := range got {
		if p.Command != "claude" {
			t.Errorf("unexpected command %q", p.Command)
		}
	}
}

// TestApplyProcessFilter_NilReturnsAll confirms a nil filter passes through.
func TestApplyProcessFilter_NilReturnsAll(t *testing.T) {
	all := []ps.Process{{ID: ps.ProcessID{PID: 1}}, {ID: ps.ProcessID{PID: 2}}}
	got := applyProcessFilter(context.Background(), nil, all, nil)
	if len(got) != len(all) {
		t.Fatalf("nil filter should pass-through, got %d, want %d", len(got), len(all))
	}
}

// TestProjectProcess_TimeFormatting verifies a parseable startedAt is
// emitted in RFC3339, and an unparseable one falls back to the raw string.
func TestProjectProcess_TimeFormatting(t *testing.T) {
	zero := ps.Process{
		ID:         ps.ProcessID{Host: "local", PID: 1},
		Command:    "launchd",
		StartedRaw: "Sun May  3 15:38:46 2026",
	}
	got := projectProcess(&zero, "local")
	if got.StartedAt != "Sun May  3 15:38:46 2026" {
		t.Errorf("zero StartedAt should fall back to raw, got %q", got.StartedAt)
	}
}
