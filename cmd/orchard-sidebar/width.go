package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The width contract, in one place.
//
// The OUTER server owns the sidebar's width. It holds main-pane-width (tmux's
// own option, widthOption in tmux.go — not a custom @orchard one, see the
// comment above that const), its M-s binding reads it, and its
// client-resized / window-resized / after-respawn-pane hooks re-pin the pane to
// it (scripts/outer-shell/outer.conf). The sidebar's job is to notice the ONE
// event the outer server cannot see — the user dragging the pane border — and
// publish it.
//
// The trap is that a drag and a mechanical resize both reach the sidebar as the
// same tea.WindowSizeMsg. On a terminal resize tmux redistributes the panes
// proportionally BEFORE the outer hook runs select-layout main-vertical, so the
// sidebar first sees an intermediate width and only a beat later the re-pinned
// one. Publishing the intermediate width immediately (what this used to do)
// pinned that corrupted value as main-pane-width and to disk, and the hook then
// re-pinned to it — a terminal resize could shrink a 60-column sidebar to 34
// for good (#854).
//
// So a divergent width is not published on sight. The drag-vs-mechanical
// question is answered DETERMINISTICALLY rather than by racing the re-pin: a
// mechanical resize (an attach reflow, a terminal resize) always changes the
// OUTER window's total width, while a border drag only moves the split inside a
// fixed window. applyWidth reads the window width the moment the divergent size
// arrives and compares it to the last-observed one — the verdict is taken THEN,
// because by settle time the window change is old news and a drag arriving mid
// transient would inherit a stale baseline. A short settle timer only coalesces
// the burst before acting; a newer size re-arms and the stale timer is dropped.
// The window read is bounded and happens ONCE per gesture: a drag delivers a
// size per pixel, and that is one tmux fork per gesture, never one per pixel.
//
// Known limit: when the outer window is too narrow to seat desiredWidth, the
// hook's re-pin is itself clamped, and since that clamp arrives in an unchanged
// window it reads as a drag — desiredWidth then follows the clamp. Acceptable:
// the width could not be honoured in that window anyway.

// widthSettle only coalesces the burst of sizes a single gesture produces; it
// is not load-bearing for correctness (the window-width check is). A var so a
// test can shorten it.
var widthSettle = 150 * time.Millisecond

// widthSettledMsg fires widthSettle after a post-boot width diverged. seq ties
// it to the arming size so a later size that re-armed can invalidate it.
type widthSettledMsg struct{ seq int }

// applyWidth records the width tmux just handed this pane and, when it diverges
// from the published one, arms the settle timer rather than publishing now. It
// returns the timer command (nil when there is nothing to arm).
func (m *model) applyWidth(w int) tea.Cmd {
	if isCollapsedWidth(w) {
		// A collapsed pane is not a drag, whoever collapsed it (this sidebar's
		// own button, outer.conf's M-s, or its resize hooks re-pinning after a
		// terminal resize). Publishing 3 as the width would collapse the pane
		// for good, and enforcing the readable floor back over it would fight
		// the collapse open again on the next tick.
		m.width, m.collapsed, m.sized = w, true, true
		m.windowWidth = readWindowWidth() // keep a live baseline for the first drag after expand
		return nil
	}
	m.collapsed = false
	if !m.sized {
		// the first size is the wrapper's own split, not a gesture. It seeds
		// the width only when nothing was restored from disk: a restore that
		// has not landed yet must not be overwritten by the pre-restore size.
		m.sized = true
		if m.desiredWidth == 0 {
			m.desiredWidth = w
		}
		m.width = w
		m.windowWidth = readWindowWidth() // the baseline every later size is judged against
		return nil
	}
	m.width = w
	if w == m.desiredWidth {
		// a re-pin landing on the published width: the window has settled at its
		// new size and the gesture is over, so adopt it as the baseline the next
		// size is judged against (this consumes a mechanical resize even if
		// bubbletea coalesced away the intermediate size that armed the timer).
		m.windowWidth = readWindowWidth()
		m.widthPending = false
		return nil
	}
	if m.widthPending {
		// mid-gesture: keep the verdict already taken for this drag and just
		// re-arm, so the window is read once per gesture, not once per pixel.
		m.widthSeq++
		return tickAfter(widthSettle, widthSettledMsg{seq: m.widthSeq})
	}
	// The verdict is taken HERE, not at settle: by settle time a mechanical
	// resize's window change is old news, and a drag arriving mid-transient
	// would inherit a stale baseline. A changed window means this divergence is
	// tmux's proportional redistribution; an unchanged one means a border drag.
	cw := readWindowWidth()
	if cw == 0 {
		// unknown read (timeout/error): do not guess mechanical and swallow a
		// drag. With a valid baseline, take the drag path; with none, we truly
		// know nothing about the window — arm nothing and warn once.
		if m.windowWidth == 0 {
			if !windowReadWarned {
				windowReadWarned = true
				logf("applyWidth w=%d: window width unknown and no baseline; not arming", w)
			}
			return nil
		}
		m.widthMechanical = false
	} else {
		m.widthMechanical = cw != m.windowWidth
		m.windowWidth = cw
	}
	m.widthPending = true
	m.widthSeq++
	logf("applyWidth w=%d != desired=%d: armed seq=%d mechanical=%v", w, m.desiredWidth, m.widthSeq, m.widthMechanical)
	return tickAfter(widthSettle, widthSettledMsg{seq: m.widthSeq})
}

// windowReadWarned keeps the "window width unknown" note to once per process.
var windowReadWarned bool

// settleWidth is the arming size's timer landing, after the burst it belongs to
// has coalesced. A stale timer (a newer size re-armed) is dropped. The verdict
// was already taken at arm time: a mechanical resize re-pins and publishes
// nothing; a drag publishes.
func (m *model) settleWidth(seq int) {
	if seq != m.widthSeq {
		logf("settleWidth seq=%d stale (live=%d): drop", seq, m.widthSeq)
		return
	}
	m.widthPending = false // this gesture's timer has landed; the next read is a new gesture
	if m.collapsed || !m.sized || m.width == m.desiredWidth {
		logf("settleWidth seq=%d nothing to do: collapsed=%v sized=%v width=%d desired=%d",
			seq, m.collapsed, m.sized, m.width, m.desiredWidth)
		return
	}
	if m.widthMechanical {
		// A reflow, not a drag: publish nothing. The pane itself is corrected by
		// the outer server's own resize hooks (window-resized, after-respawn-pane),
		// whose re-pin arrives as a later WindowSizeMsg that resets m.width — the
		// sidebar must NOT re-pin here, an async select-layout can land after a
		// following drag and yank the pane back off it (#854).
		logf("settleWidth seq=%d mechanical: no publish", seq)
		return
	}
	logf("settleWidth seq=%d drag: publish width=%d (desired=%d)", seq, m.width, m.desiredWidth)
	m.publishWidth(m.width)
}

// publishWidth makes a dragged width the shared one: the outer server's
// option (what the hooks and M-s re-pin to for the rest of this tmux server's
// life) and the state file (what survives it).
func (m *model) publishWidth(w int) {
	clamped := max(w, minWidth)
	m.desiredWidth = clamped
	setWidthOption(clamped)
	m.persistState()
	if clamped != w {
		resizePane(clamped) // the readable floor kicked in
	}
}

// toggleCollapse drives the pane between its full width and the 3-column
// strip, and remembers which one the user left it in.
func (m *model) toggleCollapse() {
	m.collapsed = !m.collapsed
	w := m.expandWidth()
	if m.collapsed {
		w = collapsedWidth
	}
	m.width = w
	setCollapsed(m.collapsed, w)
	m.persistState()
	handBackFocus(m.activeOuter())
}

// expandWidth is the width a collapsed sidebar reopens to: the width the user
// dragged to when there is one (restored from disk at startup, so it survives
// a restart), else the 40 columns the wrapper splits at.
func (m *model) expandWidth() int {
	if m.desiredWidth >= minWidth {
		return m.desiredWidth
	}
	return defaultWidth
}

// persistState writes every preference the sidebar remembers, in one go: the
// file is one object, so a writer that knew only about the layout would drop
// the bell setting on the next drag. Every caller is a single deliberate
// gesture — a drag, a collapse, a bell toggle — so this is a few dozen bytes
// at human frequency, not a write loop.
func (m *model) persistState() {
	st := sidebarState{Width: m.desiredWidth, Collapsed: m.collapsed, Bell: m.bell,
		Pinned: m.pinned}
	if err := saveSidebarState(st); err != nil {
		logf("saving sidebar state: %v", err)
	}
}
