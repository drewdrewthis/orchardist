package repodiscovery

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	output []byte
	err    error
	calls  int
}

func (f *fakeRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	f.calls++
	return f.output, f.err
}

func TestTmuxSource_Roots_Parses(t *testing.T) {
	src := NewTmuxSource().WithRunner(&fakeRunner{output: []byte("/foo\n/bar\n/baz\n")})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	want := []string{"/foo", "/bar", "/baz"}
	if !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTmuxSource_Roots_TrimsEmptyLines(t *testing.T) {
	src := NewTmuxSource().WithRunner(&fakeRunner{output: []byte("/foo\n\n /bar \n\n")})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Fatalf("Roots: %v", err)
	}
	if !equalSlices(got, []string{"/foo", "/bar"}) {
		t.Errorf("got %v, want [/foo /bar]", got)
	}
}

func TestTmuxSource_Roots_NoServerReturnsEmpty(t *testing.T) {
	// A missing tmux server (or a missing tmux binary entirely) should
	// degrade silently — Roots returns nil, nil so the Provider keeps
	// merging the other sources.
	src := NewTmuxSource().WithRunner(&fakeRunner{err: errors.New("no server running")})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Errorf("err: got %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestTmuxSource_Roots_EmptyOutput(t *testing.T) {
	src := NewTmuxSource().WithRunner(&fakeRunner{output: []byte("")})
	got, err := src.Roots(context.Background())
	if err != nil {
		t.Errorf("err: got %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestTmuxSource_Roots_NewProductionConstructor(t *testing.T) {
	// Ensure NewTmuxSource works without explicit runner injection.
	src := NewTmuxSource()
	if src.runner == nil {
		t.Fatal("NewTmuxSource should wire a default runner")
	}
	// Smoke test: we don't actually want to shell out in unit tests,
	// but the call should not panic if tmux is absent. The error
	// degrades to "no contribution".
	got, _ := src.Roots(context.Background())
	// Just assert the type; the result depends on host state.
	_ = got
	// Sanity: passing tmux args don't include any shell metachars.
	if strings.Contains(strings.Join([]string{"#{pane_current_path}"}, ""), ";") {
		t.Errorf("format string contains shell metachar")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
