package main

import "testing"

// TestRowBucketCurrentNeverDone is the unit guard for issue #856: the row the
// client-tty lane reports as current (m.cursorSess) must never render the
// done glyph, even while the coarse push lane (row.attached) is stale-false
// right after boot or a switch. row.current is the render-time cross-check
// that stops bucketDone from trusting the slower lane alone.
func TestRowBucketCurrentNeverDone(t *testing.T) {
	t.Run("idle, hooked, unattached, current -> running (AC1)", func(t *testing.T) {
		r := row{state: "idle", hooked: true, attached: false, current: true}
		if got := rowBucket(r); got != bucketRunning {
			t.Errorf("rowBucket() = %v, want bucketRunning (current row must never be bucketDone)", got)
		}
	})

	t.Run("idle, hooked, unattached, not current -> done (AC3, no regression)", func(t *testing.T) {
		r := row{state: "idle", hooked: true, attached: false, current: false}
		if got := rowBucket(r); got != bucketDone {
			t.Errorf("rowBucket() = %v, want bucketDone (a genuinely idle, unattached, non-current row still shows done)", got)
		}
	})
}

// TestMarkerCurrentRowNeverDoneGlyph checks the glyph the user actually sees:
// the current row must never render "✓", matching the bug's user-facing
// symptom (issue #856 "Observed failure").
func TestMarkerCurrentRowNeverDoneGlyph(t *testing.T) {
	r := row{state: "idle", hooked: true, attached: false, current: true}
	glyph, _ := marker(r, 0)
	if glyph == "✓" {
		t.Errorf("marker() glyph = %q, want anything but the done glyph for the current row", glyph)
	}
}

// buildRebuildModel constructs the minimal model rebuild() needs: rows for
// each session with hooksBySess populated (applyHooks is what actually stamps
// row.hooked/state from the hook lane on each rebuild — a hand-set row.hooked
// would not survive rebuild()), and cursorSess set to the session the
// client-tty lane currently reports.
func buildRebuildModel(sessions []string, cursorSess string) *model {
	m := &model{
		hooksBySess: map[string]hookState{},
		cursorSess:  cursorSess,
	}
	for _, s := range sessions {
		m.hooksBySess[s] = hookState{state: "idle"}
	}
	return m
}

func rowFor(m *model, session string) row {
	for _, r := range m.rows {
		if r.session == session {
			return r
		}
	}
	return row{}
}

// TestRebuildStampsCurrentFromCursorSess is the model-level guard for AC1/AC4:
// rebuild() must stamp row.current from m.cursorSess on every rebuild, so the
// running/done split follows focus rather than the coarse attached flag, and
// the suppression is not sticky when focus moves to a different session.
func TestRebuildStampsCurrentFromCursorSess(t *testing.T) {
	m := buildRebuildModel([]string{"A", "B"}, "A")
	m.rebuild()

	if got := rowBucket(rowFor(m, "A")); got != bucketRunning {
		t.Errorf("A: rowBucket() = %v, want bucketRunning (A is cursorSess)", got)
	}
	if got := rowBucket(rowFor(m, "B")); got != bucketDone {
		t.Errorf("B: rowBucket() = %v, want bucketDone (B is idle/hooked/unattached and not current)", got)
	}

	// Focus moves to B (AC4: suppression is not sticky).
	m.cursorSess = "B"
	m.rebuild()

	if got := rowBucket(rowFor(m, "B")); got != bucketRunning {
		t.Errorf("B after switch: rowBucket() = %v, want bucketRunning (B is now cursorSess)", got)
	}
	if got := rowBucket(rowFor(m, "A")); got != bucketDone {
		t.Errorf("A after switch: rowBucket() = %v, want bucketDone (A is no longer current, suppression must not stick)", got)
	}
}
