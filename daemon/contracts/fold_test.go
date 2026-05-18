package contracts

import (
	"testing"
	"time"
)

// strPtr returns a pointer to s.
func strPtr(s string) *string { return &s }

// timePtr returns a pointer to t.
func timePtr(t time.Time) *time.Time { return &t }

// boolPtr lets tests pass `false` for blocks_close.
func boolPtr(b bool) *bool { return &b }

// mustParse is the test-side time parser.
func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

// agent constructs a Party for an agent owner.
func agent(name, sessionID string) *Party {
	return &Party{Kind: "agent", AgentName: strPtr(name), SessionID: sessionID}
}

func drew() *Party {
	return &Party{Kind: "drew"}
}

// TestFold_Creation checks that a single `kind: contract` row produces a
// fully populated Contract record (T1: stub event stream → assert fold).
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
	if c.Status != StatusOpen {
		t.Errorf("Status = %q, want %q", c.Status, StatusOpen)
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

// TestFold_LifecycleHappyPath traces created → delivered → satisfied.
// Status moves are driven by status_change events; the fold reflects the last
// status_change row. Covers the 9-status state machine (T1).
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
		{Kind: "judge_run", ID: "C-2026-05-04-bbbb2222", Timestamp: timePtr(t1), Verdict: "PASS"},
		{Kind: "status_change", ID: "C-2026-05-04-bbbb2222", Timestamp: timePtr(t2), From: "open", To: "delivered_pending_validation", Trigger: "owner_judge_pass"},
		{Kind: "status_change", ID: "C-2026-05-04-bbbb2222", Timestamp: timePtr(t3), From: "delivered_pending_validation", To: "satisfied", Trigger: "drew_approve"},
	})

	c := state["C-2026-05-04-bbbb2222"]
	if c.Status != StatusSatisfied {
		t.Errorf("Status = %q, want %q", c.Status, StatusSatisfied)
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

// TestFold_PendingUserApproval verifies that both the old plugin status string
// ("pending_drew_approval") and the new one ("pending_user_approval") map to
// StatusPendingUserApproval. This covers the PENDING_DREW_APPROVAL →
// PENDING_USER_APPROVAL rename.
func TestFold_PendingUserApproval(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	for _, rawStatus := range []string{"pending_drew_approval", "pending_user_approval"} {
		rawStatus := rawStatus
		t.Run(rawStatus, func(t *testing.T) {
			state := Fold([]Event{
				{Kind: "contract", ID: "C-pua", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
				{Kind: "status_change", ID: "C-pua", Timestamp: timePtr(t1), From: "open", To: rawStatus},
			})
			c := state["C-pua"]
			if c.Status != StatusPendingUserApproval {
				t.Errorf("raw %q → Status = %q, want %q", rawStatus, c.Status, StatusPendingUserApproval)
			}
		})
	}
}

// TestFold_CancelMidLife asserts a cancel triggers the status_change-driven
// transition to cancelled.
func TestFold_CancelMidLife(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:10:00Z")
	t2 := mustParse(t, "2026-05-04T12:11:00Z")

	state := Fold([]Event{
		{Kind: "contract", ID: "C-cccc3333", Statement: "abandoned thing", Owner: agent("agent-3", "session-3"), ReportsTo: drew(), CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "cancel_requested", ID: "C-cccc3333", Timestamp: timePtr(t1), CancelRequestID: "CR-deadbe", By: "agent-3"},
		{Kind: "status_change", ID: "C-cccc3333", Timestamp: timePtr(t2), From: "open", To: "cancelled", Trigger: "drew_cancel"},
	})
	c := state["C-cccc3333"]
	if c.Status != StatusCancelled {
		t.Errorf("Status = %q, want %q", c.Status, StatusCancelled)
	}
	if !c.LastEventAt.Equal(t2) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, t2)
	}
}

// TestFold_JudgeRejectedTerminal verifies the terminal rejection status.
func TestFold_JudgeRejectedTerminal(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{Kind: "contract", ID: "C-jrt", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "status_change", ID: "C-jrt", Timestamp: timePtr(t1), From: "open", To: "judge_rejected_terminal"},
	})
	c := state["C-jrt"]
	if c.Status != StatusJudgeRejectedTerminal {
		t.Errorf("Status = %q, want %q", c.Status, StatusJudgeRejectedTerminal)
	}
}

// TestFold_CriteriaInOrder asserts criterion_added events accumulate in
// original order.
func TestFold_CriteriaInOrder(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-dddd4444", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "criterion_added", ID: "C-dddd4444", Timestamp: timePtr(t0), Criterion: "first"},
		{Kind: "criterion_added", ID: "C-dddd4444", Timestamp: timePtr(t0), Criterion: "second"},
		{Kind: "criterion_added", ID: "C-dddd4444", Timestamp: timePtr(t0), Criterion: "third"},
	})
	got := state["C-dddd4444"].Criteria
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

// TestFold_OpenQuestionsLifecycle covers question_asked → answered: the
// answered question must drop out of OpenQuestions.
func TestFold_OpenQuestionsLifecycle(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	tAsked := mustParse(t, "2026-05-04T12:30:00Z")
	tAns := mustParse(t, "2026-05-04T12:45:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-eeee5555", Owner: agent("agent-1", "session-1"), CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "question_asked", ID: "C-eeee5555", Timestamp: timePtr(tAsked), QuestionID: "Q-zzz1", QuestionText: "are we go?", By: "agent-1"},
		{Kind: "question_asked", ID: "C-eeee5555", Timestamp: timePtr(tAsked.Add(time.Second)), QuestionID: "Q-zzz2", QuestionText: "second q", By: "agent-1"},
	})

	c := state["C-eeee5555"]
	if got, want := len(c.OpenQuestions), 2; got != want {
		t.Fatalf("OpenQuestions count = %d, want %d", got, want)
	}

	// Resolve the first question; second must remain.
	state2 := Fold([]Event{
		{Kind: "contract", ID: "C-eeee5555", Owner: agent("agent-1", "session-1"), CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "question_asked", ID: "C-eeee5555", Timestamp: timePtr(tAsked), QuestionID: "Q-zzz1", QuestionText: "are we go?", By: "agent-1"},
		{Kind: "question_asked", ID: "C-eeee5555", Timestamp: timePtr(tAsked.Add(time.Second)), QuestionID: "Q-zzz2", QuestionText: "second q", By: "agent-1"},
		{Kind: "question_answered", ID: "C-eeee5555", Timestamp: timePtr(tAns), QuestionID: "Q-zzz1", QuestionAnswer: "yes"},
	})
	c2 := state2["C-eeee5555"]
	if got, want := len(c2.OpenQuestions), 1; got != want {
		t.Fatalf("OpenQuestions after answer = %d, want %d", got, want)
	}
	if c2.OpenQuestions[0].QuestionID != "Q-zzz2" {
		t.Errorf("remaining question id = %q, want Q-zzz2", c2.OpenQuestions[0].QuestionID)
	}
}

// TestFold_BlocksCloseDefault covers the wrinkle that blocks_close defaults to
// true when the plugin omits the field.
func TestFold_BlocksCloseDefault(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-x", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "question_asked", ID: "C-x", Timestamp: timePtr(t0.Add(time.Second)), QuestionID: "Q-default", QuestionText: "no blocks_close on the wire"},
	})
	if !state["C-x"].OpenQuestions[0].BlocksClose {
		t.Errorf("BlocksClose = false, want true (default when field omitted)")
	}

	state2 := Fold([]Event{
		{Kind: "contract", ID: "C-y", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "question_asked", ID: "C-y", Timestamp: timePtr(t0.Add(time.Second)), QuestionID: "Q-explicit", QuestionText: "explicit false", QuestionBlocks: boolPtr(false)},
	})
	if state2["C-y"].OpenQuestions[0].BlocksClose {
		t.Errorf("BlocksClose = true, want false (explicit)")
	}
}

// TestFold_UnknownKindIgnored asserts plugin extensions don't break the fold.
func TestFold_UnknownKindIgnored(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-1", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "future_extension_event", ID: "C-1", Timestamp: timePtr(t0)},
		{Kind: "another_unknown", ID: "C-1", Timestamp: timePtr(t0)},
	})
	if c := state["C-1"]; c.Status != StatusOpen {
		t.Errorf("Status = %q, want open (unknown events should be no-ops)", c.Status)
	}
}

// TestFold_OutOfOrderEventsBeforeCreation asserts events before creation are
// dropped silently.
func TestFold_OutOfOrderEventsBeforeCreation(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "status_change", ID: "C-orphan", Timestamp: timePtr(t0), To: "satisfied"},
	})
	if _, ok := state["C-orphan"]; ok {
		t.Errorf("orphan status_change created a contract; expected drop")
	}
}

// TestFold_HandoffPreservesCreated covers that CreatedAt is immutable.
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

// TestFold_MultipleContractsIndependent confirms IDs do not bleed between
// distinct contracts in a single Fold.
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

// TestFold_AllNineStatuses asserts all 9 status strings map to the correct
// domain constant. This is the T1 state-machine fold test.
func TestFold_AllNineStatuses(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	cases := []struct {
		raw  string
		want ContractStatus
	}{
		{"open", StatusOpen},
		{"delivered_pending_validation", StatusDeliveredPendingValidation},
		{"delivered_pending_parent_validation", StatusDeliveredPendingParentValidation},
		{"pending_user_approval", StatusPendingUserApproval},
		{"pending_drew_approval", StatusPendingUserApproval}, // backward compat
		{"awaiting_cancel_ack", StatusAwaitingCancelAck},
		{"waiting_external", StatusWaitingExternal},
		{"satisfied", StatusSatisfied},
		{"cancelled", StatusCancelled},
		{"judge_rejected_terminal", StatusJudgeRejectedTerminal},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			events := []Event{
				{Kind: "contract", ID: "C-" + tc.raw, CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
			}
			if tc.raw != "open" {
				events = append(events, Event{
					Kind: "status_change", ID: "C-" + tc.raw, Timestamp: timePtr(t1), From: "open", To: tc.raw,
				})
			}
			state := Fold(events)
			c, ok := state[ContractID("C-"+tc.raw)]
			if !ok {
				t.Fatalf("contract C-%s not in fold result", tc.raw)
			}
			if c.Status != tc.want {
				t.Errorf("raw %q → Status = %q, want %q", tc.raw, c.Status, tc.want)
			}
		})
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
