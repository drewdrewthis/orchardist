package contracts

import (
	"testing"
	"time"
)

// strPtr returns a pointer to s. The fold uses *string for nullable
// fields per the on-disk JSONL shape.
func strPtr(s string) *string { return &s }

// timePtr is the time.Time analogue of strPtr.
func timePtr(t time.Time) *time.Time { return &t }

// boolPtr lets tests pass `false` for blocks_close (the JSON unmarshal
// has no other way to disambiguate "field omitted" from "field=false").
func boolPtr(b bool) *bool { return &b }

// mustParse is the test-side time parser. Fixtures use stable strings
// so test failures show the original timestamp instead of a long
// time.Now() drift.
func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

// agent constructs a Party for an agent owner. Used in fixtures so the
// shape stays in lockstep with how the plugin writes JSONL.
func agent(name, sessionID string) *Party {
	return &Party{Kind: "agent", AgentName: strPtr(name), SessionID: sessionID}
}

func drew() *Party {
	return &Party{Kind: "drew"}
}

// TestFold_Creation checks that a single `kind: contract` row produces
// a fully populated Contract record.
func TestFold_Creation(t *testing.T) {
	created := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{
			Kind:          "contract",
			ID:            "C-2026-05-04-aaaa1111",
			Statement:     "Deliver foo",
			Owner:         agent("agent-1", "session-1"),
			ReportsTo:     drew(),
			CreatedOn:     timePtr(created),
			UpdatedOn:     timePtr(created),
			InitialStatus: "open",
		},
	})

	c, ok := state["C-2026-05-04-aaaa1111"]
	if !ok {
		t.Fatalf("contract not in fold result")
	}
	if c.Statement != "Deliver foo" {
		t.Errorf("Statement = %q, want %q", c.Statement, "Deliver foo")
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want %q", c.Status, "open")
	}
	if c.OwnerSessionID != "session-1" {
		t.Errorf("OwnerSessionID = %q, want %q", c.OwnerSessionID, "session-1")
	}
	if c.OwnerAgentName != "agent-1" {
		t.Errorf("OwnerAgentName = %q, want %q", c.OwnerAgentName, "agent-1")
	}
	if c.ReportsTo != "drew" {
		t.Errorf("ReportsTo = %q, want %q", c.ReportsTo, "drew")
	}
	if !c.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", c.CreatedAt, created)
	}
	if !c.LastEventAt.Equal(created) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, created)
	}
}

// TestFold_LifecycleHappyPath traces created → delivered → judge_passed
// → satisfied: the canonical AC scenario.
//
// Status moves are driven by status_change events in the live JSONL,
// so our fold mirrors that — the final status reflects the last
// status_change row.
func TestFold_LifecycleHappyPath(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")
	t2 := mustParse(t, "2026-05-04T12:45:00Z")
	t3 := mustParse(t, "2026-05-04T12:50:00Z")

	state := Fold([]Event{
		{
			Kind:          "contract",
			ID:            "C-2026-05-04-bbbb2222",
			Statement:     "Wire the foo provider",
			Owner:         agent("agent-2", "session-2"),
			ReportsTo:     drew(),
			CreatedOn:     timePtr(t0),
			UpdatedOn:     timePtr(t0),
			InitialStatus: "open",
		},
		{
			Kind:      "judge_run",
			ID:        "C-2026-05-04-bbbb2222",
			Timestamp: timePtr(t1),
			Verdict:   "PASS",
			Reason:    "AC verified end-to-end.",
		},
		{
			Kind:      "status_change",
			ID:        "C-2026-05-04-bbbb2222",
			Timestamp: timePtr(t2),
			From:      "open",
			To:        "delivered_pending_validation",
			Trigger:   "owner_judge_pass",
		},
		{
			Kind:      "status_change",
			ID:        "C-2026-05-04-bbbb2222",
			Timestamp: timePtr(t3),
			From:      "delivered_pending_validation",
			To:        "satisfied",
			Trigger:   "drew_approve",
		},
	})

	c := state["C-2026-05-04-bbbb2222"]
	if c.Status != "satisfied" {
		t.Errorf("Status = %q, want satisfied", c.Status)
	}
	if !c.LastEventAt.Equal(t3) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, t3)
	}
	if !c.UpdatedAt.Equal(t3) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t3)
	}
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v (creation is immutable)", c.CreatedAt, t0)
	}
}

// TestFold_CancelMidLife asserts that a cancel triggers the
// status_change-driven transition to cancelled.
func TestFold_CancelMidLife(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:10:00Z")
	t2 := mustParse(t, "2026-05-04T12:11:00Z")

	state := Fold([]Event{
		{
			Kind: "contract", ID: "C-2026-05-04-cccc3333",
			Statement: "abandoned thing",
			Owner:     agent("agent-3", "session-3"),
			ReportsTo: drew(),
			CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0),
			InitialStatus: "open",
		},
		{
			Kind: "cancel_requested", ID: "C-2026-05-04-cccc3333",
			Timestamp: timePtr(t1), CancelRequestID: "CR-deadbe",
			Reason: "scope ballooned",
			By:     "agent-3",
		},
		{
			Kind: "status_change", ID: "C-2026-05-04-cccc3333",
			Timestamp: timePtr(t2),
			From:      "open", To: "cancelled",
			Trigger: "drew_cancel",
		},
	})
	c := state["C-2026-05-04-cccc3333"]
	if c.Status != "cancelled" {
		t.Errorf("Status = %q, want cancelled", c.Status)
	}
	if !c.LastEventAt.Equal(t2) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, t2)
	}
}

// TestFold_CriteriaInOrder asserts criterion_added events accumulate
// in original order — the brief explicitly lists this as part of the
// AC set.
func TestFold_CriteriaInOrder(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-2026-05-04-dddd4444", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "criterion_added", ID: "C-2026-05-04-dddd4444", Timestamp: timePtr(t0), Criterion: "first"},
		{Kind: "criterion_added", ID: "C-2026-05-04-dddd4444", Timestamp: timePtr(t0), Criterion: "second"},
		{Kind: "criterion_added", ID: "C-2026-05-04-dddd4444", Timestamp: timePtr(t0), Criterion: "third"},
	})
	got := state["C-2026-05-04-dddd4444"].Criteria
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("Criteria = %v, want %v", got, want)
	}
	for i, c := range got {
		if c != want[i] {
			t.Errorf("Criteria[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// TestFold_OpenQuestionsLifecycle covers question_asked → answered:
// the answered question must drop out of OpenQuestions.
func TestFold_OpenQuestionsLifecycle(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	tAsked := mustParse(t, "2026-05-04T12:30:00Z")
	tAns := mustParse(t, "2026-05-04T12:45:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-2026-05-04-eeee5555", Owner: agent("agent-1", "session-1"), CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{
			Kind: "question_asked", ID: "C-2026-05-04-eeee5555",
			Timestamp:    timePtr(tAsked),
			QuestionID:   "Q-zzz1",
			QuestionText: "are we go?",
			By:           "agent-1",
		},
		{
			Kind: "question_asked", ID: "C-2026-05-04-eeee5555",
			Timestamp:    timePtr(tAsked.Add(time.Second)),
			QuestionID:   "Q-zzz2",
			QuestionText: "second q",
			By:           "agent-1",
		},
	})

	c := state["C-2026-05-04-eeee5555"]
	if got, want := len(c.OpenQuestions), 2; got != want {
		t.Fatalf("OpenQuestions count = %d, want %d", got, want)
	}

	// Resolve the first question; second must remain.
	state2 := Fold([]Event{
		{Kind: "contract", ID: "C-2026-05-04-eeee5555", Owner: agent("agent-1", "session-1"), CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "question_asked", ID: "C-2026-05-04-eeee5555", Timestamp: timePtr(tAsked), QuestionID: "Q-zzz1", QuestionText: "are we go?", By: "agent-1"},
		{Kind: "question_asked", ID: "C-2026-05-04-eeee5555", Timestamp: timePtr(tAsked.Add(time.Second)), QuestionID: "Q-zzz2", QuestionText: "second q", By: "agent-1"},
		{Kind: "question_answered", ID: "C-2026-05-04-eeee5555", Timestamp: timePtr(tAns), QuestionID: "Q-zzz1", QuestionAnswer: "yes"},
	})
	c2 := state2["C-2026-05-04-eeee5555"]
	if got, want := len(c2.OpenQuestions), 1; got != want {
		t.Fatalf("OpenQuestions after answer = %d, want %d", got, want)
	}
	if c2.OpenQuestions[0].QuestionID != "Q-zzz2" {
		t.Errorf("remaining question id = %q, want Q-zzz2", c2.OpenQuestions[0].QuestionID)
	}
}

// TestFold_BlocksCloseDefault covers the wrinkle that blocks_close
// defaults to true when the plugin omits the field on a question_asked
// row (which it does in some older versions of the log).
func TestFold_BlocksCloseDefault(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-x", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{
			Kind: "question_asked", ID: "C-x",
			Timestamp: timePtr(t0.Add(time.Second)), QuestionID: "Q-default",
			QuestionText: "no blocks_close on the wire",
		},
	})
	c := state["C-x"]
	if !c.OpenQuestions[0].BlocksClose {
		t.Errorf("BlocksClose = false, want true (default when field omitted)")
	}

	state2 := Fold([]Event{
		{Kind: "contract", ID: "C-y", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{
			Kind: "question_asked", ID: "C-y",
			Timestamp: timePtr(t0.Add(time.Second)), QuestionID: "Q-explicit",
			QuestionText:   "explicit false",
			QuestionBlocks: boolPtr(false),
		},
	})
	if state2["C-y"].OpenQuestions[0].BlocksClose {
		t.Errorf("BlocksClose = true, want false (explicit)")
	}
}

// TestFold_UnknownKindIgnored asserts plugin extensions don't break us.
func TestFold_UnknownKindIgnored(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-1", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "future_extension_event", ID: "C-1", Timestamp: timePtr(t0)},
		{Kind: "another_unknown", ID: "C-1", Timestamp: timePtr(t0)},
	})
	if c := state["C-1"]; c.Status != "open" {
		t.Errorf("Status = %q, want open (unknown events should be no-ops)", c.Status)
	}
}

// TestFold_OutOfOrderEventsBeforeCreation asserts that an event
// arriving before its creation row is dropped silently. This shouldn't
// happen in practice because the JSONL is append-order, but the fold
// must be robust to corrupted input.
func TestFold_OutOfOrderEventsBeforeCreation(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		// status_change before creation — drop.
		{Kind: "status_change", ID: "C-orphan", Timestamp: timePtr(t0), To: "satisfied"},
	})
	if _, ok := state["C-orphan"]; ok {
		t.Errorf("orphan status_change created a contract; expected drop")
	}
}

// TestFold_HandoffPreservesCreated covers the `handoff` shape — a
// future contract handoff (status_change with new owner). Today the
// plugin doesn't change owner mid-life, but Fold's implementation
// keeps CreatedAt immutable so a hypothetical handoff still surfaces a
// stable creation timestamp.
func TestFold_HandoffPreservesCreated(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-h", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open", Owner: agent("a", "s1")},
		{Kind: "status_change", ID: "C-h", Timestamp: timePtr(t1), From: "open", To: "open", Trigger: "handoff"},
	})
	c := state["C-h"]
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v (creation is immutable)", c.CreatedAt, t0)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t1)
	}
}

// TestFold_CancelledByDrewVsAgent demonstrates that fold reflects
// whatever the live status_change wrote; the trigger field is
// preserved nowhere on the Contract — by design, since the resolver
// surfaces only `status`, not `trigger`.
func TestFold_CancelledByDrewVsAgent(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-d", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "cancel_requested", ID: "C-d", Timestamp: timePtr(t0.Add(time.Minute)), CancelRequestID: "CR-1", Reason: "drew said so", By: "drew"},
		{Kind: "status_change", ID: "C-d", Timestamp: timePtr(t0.Add(2 * time.Minute)), From: "open", To: "cancelled", Trigger: "drew_cancel"},
	})
	if state["C-d"].Status != "cancelled" {
		t.Errorf("Status = %q, want cancelled", state["C-d"].Status)
	}
}

// TestFold_MultipleContractsIndependent confirms IDs do not bleed
// between distinct contracts in a single Fold.
func TestFold_MultipleContractsIndependent(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-aaa", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open", Statement: "alpha"},
		{Kind: "contract", ID: "C-bbb", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open", Statement: "beta"},
		{Kind: "criterion_added", ID: "C-aaa", Timestamp: timePtr(t0), Criterion: "alpha-only"},
	})
	if got := state["C-aaa"].Criteria; len(got) != 1 || got[0] != "alpha-only" {
		t.Errorf("alpha criteria = %v, want [alpha-only]", got)
	}
	if got := len(state["C-bbb"].Criteria); got != 0 {
		t.Errorf("beta criteria count = %d, want 0", got)
	}
}

// TestRenderParty exhaustively covers the Party → schema string mapping.
func TestRenderParty(t *testing.T) {
	cases := []struct {
		name string
		in   *Party
		want string
	}{
		{"nil", nil, ""},
		{"drew", &Party{Kind: "drew"}, "drew"},
		{"agent_with_name", &Party{Kind: "agent", AgentName: strPtr("agent-7")}, "agent:agent-7"},
		{"agent_no_name", &Party{Kind: "agent"}, "agent"},
		{"empty_kind", &Party{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderParty(tc.in); got != tc.want {
				t.Errorf("renderParty(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
