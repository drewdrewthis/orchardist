// Tests for the coalesced listAll parser (issue #464 AC1) and the
// IsAlive TTL cache. These are unit tests — no real tmux server required.

package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
)

// buildListAllLine constructs a single list-panes -a output line using the
// listAllFormat field order. Gaps can be filled with empty string.
func buildListAllLine(
	sessName, sessCreated, sessAttached, sessActivity, sessWindows, sessWindowIndex,
	winIndex, winName, winActive, winPanes, winActivePaneID,
	paneID, paneTitle, paneCmd, panePID, paneWidth, paneHeight, paneDead string,
) string {
	const sep = "\x01"
	return strings.Join([]string{
		sessName, sessCreated, sessAttached, sessActivity, sessWindows, sessWindowIndex,
		winIndex, winName, winActive, winPanes, winActivePaneID,
		paneID, paneTitle, paneCmd, panePID, paneWidth, paneHeight, paneDead,
	}, sep)
}

// TestListAll_OnePaneOneWindowOneSession verifies the simplest happy path.
func TestListAll_OnePaneOneWindowOneSession(t *testing.T) {
	line := buildListAllLine(
		"alpha", "1700000000", "1", "1700001000", "1", "0",
		"0", "bash", "1", "1", "%1",
		"%1", "Editor", "vim", "12345", "120", "30", "0",
	)
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux list-panes": []byte(line),
	}})

	sessions, windows, panes, err := a.listAll(context.Background())
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if len(windows) != 1 {
		t.Fatalf("want 1 window, got %d", len(windows))
	}
	if len(panes) != 1 {
		t.Fatalf("want 1 pane, got %d", len(panes))
	}

	sess := sessions[SessionKey{Host: "h", Name: "alpha"}]
	if !sess.Attached {
		t.Errorf("session should be attached")
	}
	if sess.WindowCount != 1 {
		t.Errorf("window count: want 1, got %d", sess.WindowCount)
	}

	win := windows[WindowKey{Host: "h", Session: "alpha", Index: 0}]
	if !win.Active {
		t.Errorf("window should be active")
	}
	if win.Name != "bash" {
		t.Errorf("window name: want bash, got %q", win.Name)
	}

	pane := panes[PaneKey{Host: "h", PaneID: "%1"}]
	if pane.CurrentCommand != "vim" {
		t.Errorf("pane cmd: want vim, got %q", pane.CurrentCommand)
	}
	if pane.Width != 120 || pane.Height != 30 {
		t.Errorf("pane dims: want 120x30, got %dx%d", pane.Width, pane.Height)
	}
}

// TestListAll_MultipleWindowsAndPanes verifies deduplication: 2 windows, 3 panes,
// all in 1 session — the session map must have exactly 1 entry.
func TestListAll_MultipleWindowsAndPanes(t *testing.T) {
	lines := []string{
		// window 0, pane %1
		buildListAllLine("main", "1700000000", "0", "0", "2", "0",
			"0", "editor", "1", "2", "%1",
			"%1", "title1", "vim", "100", "80", "24", "0"),
		// window 0, pane %2
		buildListAllLine("main", "1700000000", "0", "0", "2", "0",
			"0", "editor", "1", "2", "%1",
			"%2", "title2", "zsh", "101", "80", "24", "0"),
		// window 1, pane %3
		buildListAllLine("main", "1700000000", "0", "0", "2", "0",
			"1", "logs", "0", "1", "%3",
			"%3", "title3", "tail", "102", "80", "24", "0"),
	}
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux list-panes": []byte(strings.Join(lines, "\n")),
	}})

	sessions, windows, panes, err := a.listAll(context.Background())
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d (%v)", len(sessions), sessions)
	}
	if len(windows) != 2 {
		t.Fatalf("want 2 windows, got %d (%v)", len(windows), windows)
	}
	if len(panes) != 3 {
		t.Fatalf("want 3 panes, got %d (%v)", len(panes), panes)
	}
}

// TestListAll_AttachedVsDetached verifies session_attached parsing for both
// attached (count > 0) and detached (count == 0) sessions.
func TestListAll_AttachedVsDetached(t *testing.T) {
	lines := []string{
		// attached session — attached count = 2
		buildListAllLine("attached-sess", "1700000000", "2", "1700001000", "1", "0",
			"0", "win", "1", "1", "%1",
			"%1", "", "bash", "200", "80", "24", "0"),
		// detached session — attached count = 0
		buildListAllLine("detached-sess", "1700000000", "0", "0", "1", "0",
			"0", "win", "1", "1", "%2",
			"%2", "", "zsh", "201", "80", "24", "0"),
	}
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux list-panes": []byte(strings.Join(lines, "\n")),
	}})

	sessions, _, _, err := a.listAll(context.Background())
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}

	att := sessions[SessionKey{Host: "h", Name: "attached-sess"}]
	if !att.Attached {
		t.Errorf("attached-sess: Attached should be true")
	}
	if att.AttachedCount != 2 {
		t.Errorf("attached-sess: AttachedCount want 2, got %d", att.AttachedCount)
	}

	det := sessions[SessionKey{Host: "h", Name: "detached-sess"}]
	if det.Attached {
		t.Errorf("detached-sess: Attached should be false")
	}
	if !det.LastActivityAt.IsZero() {
		t.Errorf("detached-sess: LastActivityAt should be zero for raw '0' input")
	}
}

// TestListAll_EmptyOutputIsValid verifies that an empty list-panes output
// (zero sessions on an alive server) returns three empty maps without error.
func TestListAll_EmptyOutputIsValid(t *testing.T) {
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux list-panes": []byte(""),
	}})

	sessions, windows, panes, err := a.listAll(context.Background())
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(sessions) != 0 || len(windows) != 0 || len(panes) != 0 {
		t.Errorf("want empty maps, got s=%d w=%d p=%d", len(sessions), len(windows), len(panes))
	}
}

// TestListAll_ZeroPaneEdgeCase documents the known limitation: a session/window
// with zero panes does not appear in list-panes -a output. The test verifies
// our parser simply produces nothing for that session rather than panicking or
// returning an error. The next poll tick resolves this once a pane exists.
func TestListAll_ZeroPaneEdgeCase(t *testing.T) {
	// Simulate a race: we have one session with one pane (shows up)
	// and another session that appears in list-sessions but not list-panes
	// (it would not appear in our listAll output at all — we can't test
	// for its absence via listAll, only document that the parser doesn't
	// produce phantom rows for it).
	line := buildListAllLine("has-pane", "1700000000", "0", "0", "1", "0",
		"0", "win", "1", "1", "%5",
		"%5", "", "bash", "300", "80", "24", "0")
	a := NewAdapter("h").WithRunner(fakeRunner{out: map[string][]byte{
		"tmux list-panes": []byte(line),
	}})

	sessions, _, _, err := a.listAll(context.Background())
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	// Only the pane-bearing session appears. The zero-pane session (not in
	// our input) is correctly absent — no phantom row.
	if len(sessions) != 1 {
		t.Errorf("want 1 session, got %d", len(sessions))
	}
	if _, ok := sessions[SessionKey{Host: "h", Name: "has-pane"}]; !ok {
		t.Errorf("expected has-pane in sessions")
	}
}

// ----------------------------------------------------------------------
// IsAlive TTL cache tests.
// ----------------------------------------------------------------------

// clockedRunner is a CommandRunner whose Run call count is tracked. It also
// accepts an external clock function so tests can advance time without
// sleeping.
type clockedRunner struct {
	count  int
	result bool // what IsAlive should return (true = no error)
}

func (r *clockedRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	r.count++
	if !r.result {
		return nil, errFake("no server running")
	}
	return []byte("alive"), nil
}

// TestIsAlive_CacheHitWithinTTL asserts that two consecutive IsAlive calls
// within the TTL result in exactly 1 actual exec.
func TestIsAlive_CacheHitWithinTTL(t *testing.T) {
	r := &clockedRunner{result: true}
	// Use a long TTL so a real clock won't expire it during the test.
	a := NewAdapter("h").WithRunner(r).WithAliveTTL(10 * time.Second)

	ctx := context.Background()
	first := a.IsAlive(ctx)
	second := a.IsAlive(ctx)

	if !first || !second {
		t.Errorf("both calls should return true; got first=%v second=%v", first, second)
	}
	if r.count != 1 {
		t.Errorf("want 1 exec within TTL, got %d", r.count)
	}
}

// TestIsAlive_CacheMissAfterTTL asserts that a second call after TTL expiry
// triggers a new exec (total = 2).
func TestIsAlive_CacheMissAfterTTL(t *testing.T) {
	r := &clockedRunner{result: true}
	// Use a tiny TTL so we can expire it without sleeping.
	a := NewAdapter("h").WithRunner(r).WithAliveTTL(1 * time.Nanosecond)

	ctx := context.Background()
	_ = a.IsAlive(ctx) // populates cache

	// Force expiry by writing a past timestamp directly.
	a.alive.mu.Lock()
	a.alive.lastChecked = time.Now().Add(-2 * time.Second) // clearly past the nanosecond TTL
	a.alive.mu.Unlock()

	_ = a.IsAlive(ctx) // should trigger a second exec

	if r.count != 2 {
		t.Errorf("want 2 execs (one per TTL window), got %d", r.count)
	}
}

// TestFetchAll_TotalExecCount asserts FetchAll uses ≤2 execs with the
// coalesced path. This mirrors TestRegression_FetchAllExecCount_Issue464
// but from the unit-test perspective.
func TestFetchAll_TotalExecCount(t *testing.T) {
	// Use a long alive TTL so the IsAlive result from the info call
	// won't re-fire within FetchAll.
	r := &countingRunner{}
	a := NewAdapter("h").WithRunner(r).WithAliveTTL(10 * time.Second)

	_, err := a.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if got := int(r.count.Load()); got > 2 {
		t.Errorf("FetchAll exec count: want ≤2, got %d (calls: %v)", got, r.calls)
	}
}
