package contracts

import (
	"testing"
	"time"
)

// timePtr is the time.Time analogue of strPtr — used in v0.7 Event fixtures.
func timePtr(t time.Time) *time.Time { return &t }

// strPtr returns a pointer to s.
func strPtr(s string) *string { return &s }

// mustParse parses an RFC3339 timestamp or fails the test. Retained for
// backward-compat with helper callers in this file.
func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

// agentParty constructs a Party for an agent owner in v0.7 Event fixtures.
func agentParty(name, sessionID string) *Party {
	return &Party{Kind: "agent", AgentName: strPtr(name), SessionID: sessionID}
}

// TestFold_Creation checks that a single `kind: contract` row produces
// a Contract record with the expected id, statement, status, owner, and timestamps.
func TestFold_Creation(t *testing.T) {
	created := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{
			Kind:          "contract",
			ID:            "C-2026-05-04-aaaa1111",
			Statement:     "Deliver foo",
			Owner:         agentParty("agent-1", "session-1"),
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
	if !c.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", c.CreatedAt, created)
	}
	if !c.LastEventAt.Equal(created) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, created)
	}
}

// TestFold_StatusChange_ClosesContract verifies that a status_change event
// with a non-open `to` value marks the contract as closed in the v0.8 model.
func TestFold_StatusChange_ClosesContract(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{
			Kind:          "contract",
			ID:            "C-2026-05-04-bbbb2222",
			Statement:     "Wire the foo provider",
			Owner:         agentParty("agent-2", "session-2"),
			CreatedOn:     timePtr(t0),
			UpdatedOn:     timePtr(t0),
			InitialStatus: "open",
		},
		{
			Kind:      "status_change",
			ID:        "C-2026-05-04-bbbb2222",
			Timestamp: timePtr(t1),
			From:      "open",
			To:        "satisfied",
			Trigger:   "drew_approve",
		},
	})

	c := state["C-2026-05-04-bbbb2222"]
	// v0.8 maps all non-open v0.7 statuses to "closed".
	if c.Status != "closed" {
		t.Errorf("Status = %q, want closed (v0.8 maps satisfied→closed)", c.Status)
	}
	if !c.LastEventAt.Equal(t1) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, t1)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t1)
	}
	// CreatedAt is immutable.
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v (creation is immutable)", c.CreatedAt, t0)
	}
}

// TestFold_StatusChange_ReturnsToOpen verifies a status_change to "open"
// keeps the contract open.
func TestFold_StatusChange_ReturnsToOpen(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:10:00Z")

	state := Fold([]Event{
		{
			Kind:          "contract",
			ID:            "C-2026-05-04-cccc3333",
			Statement:     "re-open test",
			Owner:         agentParty("agent-3", "session-3"),
			CreatedOn:     timePtr(t0),
			UpdatedOn:     timePtr(t0),
			InitialStatus: "open",
		},
		{
			Kind:      "status_change",
			ID:        "C-2026-05-04-cccc3333",
			Timestamp: timePtr(t1),
			From:      "open",
			To:        "open",
			Trigger:   "handoff",
		},
	})
	c := state["C-2026-05-04-cccc3333"]
	if c.Status != "open" {
		t.Errorf("Status = %q, want open (status_change to open keeps open)", c.Status)
	}
}

// TestFold_UnknownKindTouchesLastEventAt asserts non-creation, non-status_change
// events only advance LastEventAt (and do not create or crash).
func TestFold_UnknownKindTouchesLastEventAt(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:01:00Z")

	state := Fold([]Event{
		{Kind: "contract", ID: "C-1", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "judge_run", ID: "C-1", Timestamp: timePtr(t1)},
	})
	c := state["C-1"]
	if c.Status != "open" {
		t.Errorf("Status = %q, want open (judge_run does not change status)", c.Status)
	}
	if !c.LastEventAt.Equal(t1) {
		t.Errorf("LastEventAt = %v, want %v (judge_run advances LastEventAt)", c.LastEventAt, t1)
	}
}

// TestFold_UnknownKindIgnored asserts truly unknown kinds don't crash.
func TestFold_UnknownKindIgnored(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-1", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "future_extension_event", ID: "C-1", Timestamp: timePtr(t0)},
		{Kind: "another_unknown", ID: "C-1", Timestamp: timePtr(t0)},
	})
	if c := state["C-1"]; c.Status != "open" {
		t.Errorf("Status = %q, want open (unknown events should be no-ops on status)", c.Status)
	}
}

// TestFold_OutOfOrderEventsBeforeCreation asserts that an event
// arriving before its creation row is dropped silently.
func TestFold_OutOfOrderEventsBeforeCreation(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "status_change", ID: "C-orphan", Timestamp: timePtr(t0), To: "satisfied"},
	})
	// touchLastEvent returns early if the contract doesn't exist; the map stays empty.
	if _, ok := state["C-orphan"]; ok {
		t.Errorf("orphan status_change should not create a contract in the map")
	}
}

// TestFold_MultipleContractsIndependent confirms IDs do not bleed
// between distinct contracts in a single Fold.
func TestFold_MultipleContractsIndependent(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := t0.Add(time.Minute)
	state := Fold([]Event{
		{Kind: "contract", ID: "C-aaa", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open", Statement: "alpha"},
		{Kind: "contract", ID: "C-bbb", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open", Statement: "beta"},
		{Kind: "status_change", ID: "C-aaa", Timestamp: timePtr(t1), From: "open", To: "satisfied"},
	})
	if state["C-aaa"].Status != "closed" {
		t.Errorf("alpha status = %q, want closed", state["C-aaa"].Status)
	}
	if state["C-bbb"].Status != "open" {
		t.Errorf("beta status = %q, want open (must not be affected by alpha's change)", state["C-bbb"].Status)
	}
}

// TestFold_CreationDefaultStatus verifies status defaults to "open"
// when the creation row omits it.
func TestFold_CreationDefaultStatus(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{Kind: "contract", ID: "C-default", CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0)},
	})
	c := state["C-default"]
	if c.Status != "open" {
		t.Errorf("Status = %q, want open (default when InitialStatus omitted)", c.Status)
	}
}

// ---- L2.2: fold dedup for conversation contracts -------------------------

// TestFold_ConversationContractDedup_SingleSession asserts that five
// open_contract events all carrying the conversation-contract deliverable
// for the same session produce exactly one Contract record (L2.2).
//
// The UserPromptSubmit hook fires on every prompt; ContractFold deduplicates
// by (ownerSessionId, deliverable) so only the first event per session
// creates a Contract.
func TestFold_ConversationContractDedup_SingleSession(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	owner := &Party{Kind: "agent", AgentName: strPtr("claude"), SessionID: "S-DEDUP-002"}

	// Simulate five UserPromptSubmit hook fires, each writing a new
	// open_contract event with a unique id but the same deliverable and owner.
	events := make([]Event, 5)
	for i := range events {
		events[i] = Event{
			Kind:          "contract",
			ID:            "C-DEDUP-" + string(rune('A'+i)),
			Statement:     ConversationContractDeliverable,
			Owner:         owner,
			CreatedOn:     timePtr(t0.Add(time.Duration(i) * time.Second)),
			UpdatedOn:     timePtr(t0.Add(time.Duration(i) * time.Second)),
			InitialStatus: "open",
		}
	}

	state := Fold(events)
	if got, want := len(state), 1; got != want {
		t.Errorf("fold produced %d contracts for 5 duplicate conversation-contract events; want exactly 1", got)
	}

	// The surviving contract must be the first one (C-DEDUP-A).
	if _, ok := state["C-DEDUP-A"]; !ok {
		t.Errorf("first contract C-DEDUP-A missing from fold; want it to be the survivor")
	}
}

// TestFold_ConversationContractDedup_TwoSessions asserts that two
// sessions each produce their own conversation contract — dedup is
// per session, not global.
func TestFold_ConversationContractDedup_TwoSessions(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	ownerA := &Party{Kind: "agent", AgentName: strPtr("claude"), SessionID: "S-SESSION-A"}
	ownerB := &Party{Kind: "agent", AgentName: strPtr("claude"), SessionID: "S-SESSION-B"}

	state := Fold([]Event{
		// Session A fires twice.
		{Kind: "contract", ID: "C-A-1", Statement: ConversationContractDeliverable, Owner: ownerA, CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "contract", ID: "C-A-2", Statement: ConversationContractDeliverable, Owner: ownerA, CreatedOn: timePtr(t0.Add(time.Second)), UpdatedOn: timePtr(t0.Add(time.Second)), InitialStatus: "open"},
		// Session B fires once.
		{Kind: "contract", ID: "C-B-1", Statement: ConversationContractDeliverable, Owner: ownerB, CreatedOn: timePtr(t0.Add(2 * time.Second)), UpdatedOn: timePtr(t0.Add(2 * time.Second)), InitialStatus: "open"},
	})

	if got, want := len(state), 2; got != want {
		t.Errorf("fold produced %d contracts for 2-session scenario; want 2 (one per session)", got)
	}
	if _, ok := state["C-A-1"]; !ok {
		t.Errorf("C-A-1 (first for session A) should survive dedup")
	}
	if _, ok := state["C-B-1"]; !ok {
		t.Errorf("C-B-1 (only for session B) should survive dedup")
	}
}

// TestFold_NonConversationContractNotDeduped asserts that contracts
// with a different (non-fixed) deliverable are NOT deduplicated — only
// conversation contracts get the per-session dedup treatment.
func TestFold_NonConversationContractNotDeduped(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	owner := &Party{Kind: "agent", AgentName: strPtr("claude"), SessionID: "S-USER-001"}

	state := Fold([]Event{
		{Kind: "contract", ID: "C-USR-1", Statement: "implement the login page", Owner: owner, CreatedOn: timePtr(t0), UpdatedOn: timePtr(t0), InitialStatus: "open"},
		{Kind: "contract", ID: "C-USR-2", Statement: "implement the login page", Owner: owner, CreatedOn: timePtr(t0.Add(time.Second)), UpdatedOn: timePtr(t0.Add(time.Second)), InitialStatus: "open"},
	})

	if got, want := len(state), 2; got != want {
		t.Errorf("fold produced %d contracts; want 2 (user contracts with same deliverable are not deduped)", got)
	}
}
