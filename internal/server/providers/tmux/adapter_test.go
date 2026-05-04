// Adapter tests — focused on the deterministic format-string parsers.
// The E2E suite covers behaviour against a real tmux daemon; this file
// pins parsing rules so a tmux output regression fails locally instead
// of in production.

package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestListSessions_Parses(t *testing.T) {
	const sep = "\x01"
	out := strings.Join([]string{
		strings.Join([]string{"alpha", "1700000000", "1", "1700001000", "2", "0"}, sep),
		strings.Join([]string{"beta", "1700000100", "0", "0", "1", "0"}, sep),
	}, "\n")

	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux info":          []byte("alive"),
		"tmux list-sessions": []byte(out),
	}})

	sessions, err := a.listSessions(context.Background())
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d (%v)", len(sessions), sessions)
	}

	alpha := sessions[SessionKey{Host: "h", Name: "alpha"}]
	if !alpha.Attached {
		t.Errorf("alpha should be attached")
	}
	if alpha.AttachedCount != 1 {
		t.Errorf("alpha attached count: want 1, got %d", alpha.AttachedCount)
	}
	if alpha.WindowCount != 2 {
		t.Errorf("alpha window count: want 2, got %d", alpha.WindowCount)
	}
	if alpha.CreatedAt.Equal(time.Time{}) {
		t.Errorf("alpha createdAt: want non-zero")
	}

	beta := sessions[SessionKey{Host: "h", Name: "beta"}]
	if beta.Attached {
		t.Errorf("beta should not be attached")
	}
	if !beta.LastActivityAt.IsZero() {
		t.Errorf("beta lastActivity: want zero (raw '0' input), got %v", beta.LastActivityAt)
	}
}

func TestListPanes_Parses(t *testing.T) {
	const sep = "\x01"
	out := strings.Join([]string{
		strings.Join([]string{"alpha", "0", "%26", "Editor", "vim", "12345", "120", "30", "0"}, sep),
		strings.Join([]string{"alpha", "0", "%27", "Shell", "zsh", "12346", "120", "30", "0"}, sep),
	}, "\n")

	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux info":       []byte("alive"),
		"tmux list-panes": []byte(out),
	}})

	panes, err := a.listPanes(context.Background())
	if err != nil {
		t.Fatalf("listPanes: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("want 2 panes, got %d", len(panes))
	}

	p := panes[PaneKey{Host: "h", PaneID: "%26"}]
	if p.CurrentCommand != "vim" {
		t.Errorf("currentCommand: want vim, got %q", p.CurrentCommand)
	}
	if p.WindowKey.Index != 0 {
		t.Errorf("windowIndex: want 0, got %d", p.WindowKey.Index)
	}
	if p.Width != 120 || p.Height != 30 {
		t.Errorf("dims: want 120x30, got %dx%d", p.Width, p.Height)
	}
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
