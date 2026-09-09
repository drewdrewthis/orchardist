package main

import "testing"

// The deterministic drag-vs-mechanical verdict (#854): a mechanical resize
// always moves the OUTER window width, a border drag never does. widthSpy and
// its readWindowWidth stub live in width_test.go.

// A changed OUTER window marks an intermediate pane width as mechanical, not a
// drag: it must not be published (the #854 corruption); the hooks fix the pane.
// @scenario A mechanical resize publishes nothing
func TestMechanicalResizePublishesNothing(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 133
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 133}

	spy.winWidth = 266 // the window grew: an attach reflow, not a drag
	if cmd := m.applyWidth(93); cmd == nil {
		t.Fatal("a divergent width armed no settle timer")
	}
	m.Update(widthSettledMsg{seq: m.widthSeq})

	if len(spy.published) != 0 || len(spy.saved) != 0 {
		t.Fatalf("a mechanical resize was published: %v / %v", spy.published, spy.saved)
	}
	if m.desiredWidth != 40 {
		t.Errorf("desiredWidth = %d, want the untouched 40", m.desiredWidth)
	}
}

// A width diverging while the OUTER window is unchanged is a drag: it publishes.
// @scenario A drag that stays in a fixed window is published after the settle
func TestDragPublishesAfterSettle(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 266
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 266}

	m.applyWidth(60) // window unchanged: a drag
	m.Update(widthSettledMsg{seq: m.widthSeq})

	if len(spy.published) != 1 || spy.published[0] != 60 {
		t.Fatalf("drag published %v, want [60]", spy.published)
	}
	if len(spy.saved) != 1 || spy.saved[0].Width != 60 {
		t.Fatalf("drag persisted %+v, want width 60", spy.saved)
	}
}

// A newer size re-arms the settle timer; the earlier one, when it lands, is
// stale and must publish nothing (the debounce on widthSeq).
// @scenario A stale settle timer publishes nothing
func TestStaleSettleIsIgnored(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 266
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 266}

	m.applyWidth(34)
	firstSeq := m.widthSeq
	m.applyWidth(50) // re-arms before the first settle lands

	m.Update(widthSettledMsg{seq: firstSeq})
	if len(spy.published) != 0 {
		t.Fatalf("the stale settle published: %v", spy.published)
	}
	m.Update(widthSettledMsg{seq: m.widthSeq})
	if len(spy.published) != 1 || spy.published[0] != 50 {
		t.Fatalf("published %v, want [50] after the live settle", spy.published)
	}
}

// A failed/timed-out window read (0 = unknown) must not be guessed as mechanical
// and swallow a drag: with a valid baseline the divergence is still published,
// while with no baseline at all nothing can be judged, so nothing is armed.
func TestUnknownWindowReadDoesNotSwallowDrag(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 0                                                        // readWindowWidth reports unknown
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 266} // valid prior baseline

	m.applyWidth(60)
	m.Update(widthSettledMsg{seq: m.widthSeq})
	if len(spy.published) != 1 || spy.published[0] != 60 {
		t.Fatalf("a drag was swallowed on an unknown window read: %v", spy.published)
	}

	windowReadWarned = false
	spy2 := newWidthSpy(t)
	spy2.winWidth = 0
	m2 := &model{desiredWidth: 40, width: 40, sized: true} // no baseline
	if cmd := m2.applyWidth(60); cmd != nil {
		t.Error("armed a settle with no window knowledge at all")
	}
	if len(spy2.published) != 0 {
		t.Errorf("published with no window knowledge: %v", spy2.published)
	}
}

// A sidebar that boots collapsed still records a window baseline, so the first
// drag after it expands is judged in a fixed window and published.
func TestCollapsedStartThenExpandThenDragPublishes(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 266
	m := &model{desiredWidth: 40}

	m.applyWidth(collapsedWidth) // boots collapsed; must still seed the baseline
	if m.windowWidth != 266 {
		t.Fatalf("collapsed start left no window baseline: %d", m.windowWidth)
	}
	m.applyWidth(60) // expand and drag, window still 266
	m.Update(widthSettledMsg{seq: m.widthSeq})
	if len(spy.published) != 1 || spy.published[0] != 60 {
		t.Fatalf("first drag after expand not published: %v", spy.published)
	}
}

// A drag delivers a size per pixel; the window is read once for the whole
// gesture, not once per size — one tmux fork per drag, never per pixel.
func TestWindowReadOncePerGesture(t *testing.T) {
	spy := newWidthSpy(t)
	spy.winWidth = 266
	m := &model{desiredWidth: 40, width: 40, sized: true, windowWidth: 266}

	m.applyWidth(58)
	m.applyWidth(59)
	m.applyWidth(60) // three sizes, one gesture
	if spy.reads != 1 {
		t.Errorf("readWindowWidth called %d times in one gesture, want 1", spy.reads)
	}
	m.Update(widthSettledMsg{seq: m.widthSeq})
	if len(spy.published) != 1 || spy.published[0] != 60 {
		t.Fatalf("gesture published %v, want [60]", spy.published)
	}
}
