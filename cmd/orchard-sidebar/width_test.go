package main

import (
	"os"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// widthSpy captures every effect the width contract has on the world — what was
// published to the OUTER server, resized, and written to disk — stubbed together
// because a change that does only some of them is exactly the bug.
type widthSpy struct {
	published []int
	resized   []int
	saved     []sidebarState
	// winWidth is what readWindowWidth reports: move it for a mechanical resize,
	// leave it fixed for a drag. reads counts the stubbed window reads.
	winWidth int
	reads    int
}

func newWidthSpy(t *testing.T) *widthSpy {
	t.Helper()
	s := &widthSpy{}
	ow, or, osv := setWidthOption, resizePane, saveSidebarState
	orw := readWindowWidth
	setWidthOption = func(w int) { s.published = append(s.published, w) }
	resizePane = func(w int) { s.resized = append(s.resized, w) }
	saveSidebarState = func(st sidebarState) error { s.saved = append(s.saved, st); return nil }
	readWindowWidth = func() int { s.reads++; return s.winWidth }
	t.Cleanup(func() {
		setWidthOption, resizePane, saveSidebarState = ow, or, osv
		readWindowWidth = orw
	})
	return s
}

// The OUTER server owns the width: the sidebar publishes what the user dragged
// to, and outer.conf's hooks re-pin the pane to that. This pins the round trip —
// drag, publish, get re-pinned to the same width, and DON'T read the re-pin as a
// fresh drag (the two-owners bug that republished the hook's default over it).
func TestWidthRoundTripsThroughTheOuterServer(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 200 // a real, fixed outer window: every drag below stays in it
	m := &model{}

	m.Update(tea.WindowSizeMsg{Width: 40, Height: 50}) // the wrapper's own split
	if len(spy.published) != 0 || len(spy.saved) != 0 {
		t.Fatalf("the startup size was published: %v / %v", spy.published, spy.saved)
	}
	if m.desiredWidth != 40 {
		t.Fatalf("desiredWidth = %d after the first size, want 40", m.desiredWidth)
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 50}) // the user drags the border
	m.Update(widthSettledMsg{seq: m.widthSeq})         // the drag stays; the settle fires
	if len(spy.published) != 1 || spy.published[0] != 60 {
		t.Fatalf("drag published %v, want [60]", spy.published)
	}
	if len(spy.saved) != 1 || spy.saved[0].Width != 60 {
		t.Fatalf("drag persisted %+v, want width 60", spy.saved)
	}

	// the terminal is resized: the hook re-pins the pane, tmux reports it back
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if len(spy.published) != 1 {
		t.Errorf("the hook's re-pin was read as a drag: %v", spy.published)
	}
	if len(spy.saved) != 1 {
		t.Errorf("the hook's re-pin was persisted again: %v", spy.saved)
	}
	if m.width != 60 || m.height != 30 {
		t.Errorf("pane = %dx%d, want 60x30", m.width, m.height)
	}
}

// A drag below the readable floor publishes the floor and pushes the pane out.
func TestDragBelowTheFloorPublishesTheFloor(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 200
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 200}

	m.Update(tea.WindowSizeMsg{Width: 20, Height: 50})
	m.Update(widthSettledMsg{seq: m.widthSeq}) // the drag stays; the floor is published

	if last := spy.published[len(spy.published)-1]; last != minWidth {
		t.Errorf("published %d, want the floor %d", last, minWidth)
	}
	if len(spy.resized) != 1 || spy.resized[0] != minWidth {
		t.Errorf("resized %v, want [%d] — the floor has to be pushed back onto the pane", spy.resized, minWidth)
	}
	if got := spy.saved[len(spy.saved)-1].Width; got != minWidth {
		t.Errorf("persisted %d, want the floor %d", got, minWidth)
	}
}

// Collapsing remembers both facts, and expanding goes back to the width the
// user dragged to rather than the wrapper's default.
func TestCollapseIsRememberedAndReopensAtTheDraggedWidth(t *testing.T) {
	spy := newWidthSpy(t)
	var collapses []sidebarState
	prev := setCollapsed
	setCollapsed = func(c bool, w int) { collapses = append(collapses, sidebarState{Width: w, Collapsed: c}) }
	t.Cleanup(func() { setCollapsed = prev })

	m := &model{desiredWidth: 52, width: 52, sized: true}
	m.toggleCollapse()
	if len(collapses) != 1 || !reflect.DeepEqual(collapses[0], sidebarState{Width: collapsedWidth, Collapsed: true}) {
		t.Fatalf("collapse drove tmux with %+v", collapses)
	}
	if last := spy.saved[len(spy.saved)-1]; !reflect.DeepEqual(last, sidebarState{Width: 52, Collapsed: true}) {
		t.Fatalf("persisted %+v, want the collapsed flag beside the remembered width", last)
	}

	m.toggleCollapse()
	if last := collapses[len(collapses)-1]; !reflect.DeepEqual(last, sidebarState{Width: 52, Collapsed: false}) {
		t.Fatalf("expand drove tmux with %+v, want the dragged 52", last)
	}
	if last := spy.saved[len(spy.saved)-1]; last.Collapsed {
		t.Errorf("the expand was persisted as collapsed: %+v", last)
	}
}

// The layout survives a restart of everything: it is written on the gesture
// and read back at startup. A corrupt or truncated file means "nothing
// remembered", never a refusal to start.
func TestSidebarStateFileRoundTrip(t *testing.T) {
	stateHome(t)

	if got := loadSidebarState(); !reflect.DeepEqual(got, sidebarState{}) {
		t.Errorf("a missing file loaded as %+v, want the zero layout", got)
	}
	if err := saveSidebarState(sidebarState{Width: 52, Collapsed: true}); err != nil {
		t.Fatal(err)
	}
	if got := loadSidebarState(); !reflect.DeepEqual(got, sidebarState{Width: 52, Collapsed: true}) {
		t.Errorf("round trip = %+v", got)
	}

	writeStateFile(t, "sidebar-state.json", `{"width": 52, "collapsed":`)
	if got := loadSidebarState(); !reflect.DeepEqual(got, sidebarState{}) {
		t.Errorf("a truncated file loaded as %+v, want the zero layout", got)
	}
	writeStateFile(t, "sidebar-state.json", `not json at all`)
	if got := loadSidebarState(); !reflect.DeepEqual(got, sidebarState{}) {
		t.Errorf("garbage loaded as %+v, want the zero layout", got)
	}
	// a width below the readable floor is dropped rather than restored: the
	// pane it would reopen at is unusable
	writeStateFile(t, "sidebar-state.json", `{"width": 9}`)
	if got := loadSidebarState(); got.Width != 0 {
		t.Errorf("a sub-floor width was restored: %+v", got)
	}
}

// What a remembered layout tells tmux, before bubbletea ever reads the pane
// size. Nothing remembered writes nothing: an unset main-pane-width is what
// makes outer.conf fall back to its own default.
func TestRestorePane(t *testing.T) {
	cases := []struct {
		name      string
		st        sidebarState
		width     int
		collapsed bool
		ok        bool
	}{
		{"nothing remembered", sidebarState{}, 0, false, false},
		{"a dragged width", sidebarState{Width: 52}, 52, false, true},
		{"collapsed", sidebarState{Width: 52, Collapsed: true}, collapsedWidth, true, true},
		{"collapsed with no width", sidebarState{Collapsed: true}, collapsedWidth, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, collapsed, ok := restorePane(c.st)
			if w != c.width || collapsed != c.collapsed || ok != c.ok {
				t.Errorf("restorePane(%+v) = (%d, %v, %v), want (%d, %v, %v)",
					c.st, w, collapsed, ok, c.width, c.collapsed, c.ok)
			}
		})
	}
}

// A restored width must not be read back as a drag, nor overwritten by a stale
// pre-restore size that arrives first.
func TestRestoredWidthIsNotADrag(t *testing.T) {
	spy := newWidthSpy(t)
	m := &model{desiredWidth: 52, collapsed: false} // seeded from the state file

	m.Update(tea.WindowSizeMsg{Width: 52, Height: 50})
	if len(spy.published) != 0 || len(spy.saved) != 0 {
		t.Fatalf("the restored width was republished: %v / %v", spy.published, spy.saved)
	}
	if m.desiredWidth != 52 {
		t.Errorf("desiredWidth = %d, want the restored 52", m.desiredWidth)
	}

	// a pre-restore size that lost the race must not become the new preference
	m2 := &model{desiredWidth: 52}
	m2.Update(tea.WindowSizeMsg{Width: 40, Height: 50})
	if m2.desiredWidth != 52 {
		t.Errorf("a stale first size overwrote the restored width: %d", m2.desiredWidth)
	}
	if len(spy.published) != 0 {
		t.Errorf("a stale first size was published: %v", spy.published)
	}
}

// loadLastLaunch has the same contract as the layout file: a missing or
// corrupt file opens the modal on its defaults rather than refusing to open.
func TestLastLaunchFileRoundTrip(t *testing.T) {
	stateHome(t)

	if got := loadLastLaunch(); got != (lastLaunch{}) {
		t.Errorf("a missing file loaded as %+v", got)
	}
	if err := saveLastLaunch(lastLaunch{Cmd: "claude --resume x", Dir: "/w/x", Name: "x"}); err != nil {
		t.Fatal(err)
	}
	got := loadLastLaunch()
	if got.Cmd != "claude --resume x" || got.Dir != "/w/x" || got.Name != "x" {
		t.Errorf("round trip = %+v", got)
	}
	if got.At == "" {
		t.Error("no timestamp was stamped on the save")
	}

	writeStateFile(t, "sidebar-last-launch.json", `{"cmd": "claude"`)
	if got := loadLastLaunch(); got != (lastLaunch{}) {
		t.Errorf("a truncated file loaded as %+v, want no memory", got)
	}
	// and an unwritable directory is reported, not swallowed: the modal shows it
	t.Setenv("XDG_STATE_HOME", "/dev/null/nope")
	if err := saveLastLaunch(lastLaunch{Cmd: "x"}); err == nil {
		t.Error("saving into an unwritable directory reported success")
	}
	if _, err := os.Stat("/dev/null/nope"); err == nil {
		t.Error("the test wrote where it should not have")
	}
}
