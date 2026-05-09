// Adapter tests — focused on the deterministic format-string parsers.
// The E2E suite covers behaviour against a real tmux daemon; this file
// pins parsing rules so a tmux output regression fails locally instead
// of in production.

package tmux

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner returns canned bytes per command so parser tests can run
// without a tmux daemon.
type fakeRunner struct {
	out  map[string][]byte
	errs map[string]error
}

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if err, ok := f.errs[key]; ok {
		return f.out[key], err
	}
	if out, ok := f.out[key]; ok {
		return out, nil
	}
	// Match by leading verb when the caller supplies a partial key —
	// keeps test data short.
	for prefix, out := range f.out {
		if strings.HasPrefix(key, prefix) {
			return out, nil
		}
	}
	return nil, nil
}

func TestListPanes_EmptyOnNoServer(t *testing.T) {
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux info": nil,
	}, errs: map[string]error{
		"tmux info": errFake("no server running"),
	}})

	snap, err := a.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if snap.Server.Alive {
		t.Errorf("Server.Alive should be false when daemon is dead")
	}
	if len(snap.Sessions) != 0 || len(snap.Panes) != 0 {
		t.Errorf("expected empty maps, got sessions=%d panes=%d", len(snap.Sessions), len(snap.Panes))
	}
}

// errFake is a tiny error type so tests can stage an "exit 1" response
// without dragging in os/exec.
type errFakeT string

func (e errFakeT) Error() string { return string(e) }

func errFake(msg string) error { return errFakeT(msg) }

func TestParseUnix(t *testing.T) {
	cases := map[string]bool{
		"":           true,  // zero
		"0":          true,  // zero
		"1700000000": false, // valid
		"abc":        true,  // invalid → zero
	}
	for in, wantZero := range cases {
		got := parseUnix(in)
		if got.IsZero() != wantZero {
			t.Errorf("parseUnix(%q): zero=%t (want %t)", in, got.IsZero(), wantZero)
		}
	}
}
