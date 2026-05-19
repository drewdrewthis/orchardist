package contracts

import (
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// strPtr returns a pointer to s.
func strPtr(s string) *string { return &s }

// timePtr is the time.Time analogue of strPtr.
func timePtr(t time.Time) *time.Time { return &t }

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

// TestFold_V7_SingleCreationEvent verifies that a single flat v0.7
// creation event produces a fully populated Contract with status=OPEN
// (started).
func TestFold_V7_SingleCreationEvent(t *testing.T) {
	ts := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{
			Timestamp:  timePtr(ts),
			ContractID: "C-2026-05-04-aaaa1111",
			Status:     "started",
			Summary:    strPtr("Deliver foo"),
			Reasoning:  "filing the contract",
			Owner:      strPtr("orchard:claude:session-1"),
			CreatedBy:  "agent-1",
			Source:     strPtr("issue:123"),
		},
	})

	c, ok := state["C-2026-05-04-aaaa1111"]
	if !ok {
		t.Fatalf("contract not in fold result")
	}
	if c.Summary != "Deliver foo" {
		t.Errorf("Summary = %q, want %q", c.Summary, "Deliver foo")
	}
	if c.Status != graphql.ContractStatusOpen {
		t.Errorf("Status = %v, want OPEN", c.Status)
	}
	if c.OwnerSessionID != "orchard:claude:session-1" {
		t.Errorf("OwnerSessionID = %q, want %q", c.OwnerSessionID, "orchard:claude:session-1")
	}
	if c.Reasoning != "filing the contract" {
		t.Errorf("Reasoning = %q, want %q", c.Reasoning, "filing the contract")
	}
	if c.CreatedBy != "agent-1" {
		t.Errorf("CreatedBy = %q, want %q", c.CreatedBy, "agent-1")
	}
	if c.Source != "issue:123" {
		t.Errorf("Source = %q, want %q", c.Source, "issue:123")
	}
	if !c.CreatedAt.Equal(ts) {
		t.Errorf("CreatedAt = %v, want %v", c.CreatedAt, ts)
	}
	if !c.LastEventAt.Equal(ts) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, ts)
	}
}

// TestFold_V7_CreationPlusDelivered verifies that creation followed by
// a "delivered" event produces status=DELIVERED and that CreatedAt is
// immutable while UpdatedAt and LastEventAt advance to the delivery
// timestamp.
func TestFold_V7_CreationPlusDelivered(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T13:00:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-05-04-bbbb2222",
			Status:     "started",
			Summary:    strPtr("Wire the foo provider"),
			Owner:      strPtr("orchard:claude:session-2"),
			CreatedBy:  "agent-2",
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-bbbb2222",
			Status:     "delivered",
			Reasoning:  "all ACs verified",
			CreatedBy:  "agent-2",
		},
	})

	c := state["C-2026-05-04-bbbb2222"]
	if c.Status != graphql.ContractStatusDelivered {
		t.Errorf("Status = %v, want DELIVERED", c.Status)
	}
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v (creation is immutable)", c.CreatedAt, t0)
	}
	if !c.LastEventAt.Equal(t1) {
		t.Errorf("LastEventAt = %v, want %v", c.LastEventAt, t1)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t1)
	}
	if c.Summary != "Wire the foo provider" {
		t.Errorf("Summary = %q, want %q (inherited from creation)", c.Summary, "Wire the foo provider")
	}
	if c.Reasoning != "all ACs verified" {
		t.Errorf("Reasoning = %q, want %q", c.Reasoning, "all ACs verified")
	}
}

// TestFold_V7_LegacyBlockedFoldsAsOpen is the regression test for the
// "blocked" → OPEN rule. A contract with a "blocked" status event must
// fold as open (not a separate state). This prevents the daemon from
// surfacing a status enum value that no longer exists in the schema.
func TestFold_V7_LegacyBlockedFoldsAsOpen(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-05-04-cccc3333",
			Status:     "started",
			Summary:    strPtr("Blocked contract"),
			Owner:      strPtr("orchard:claude:session-3"),
			CreatedBy:  "agent-3",
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-cccc3333",
			Status:     "blocked", // legacy v0.6 status — must fold as open
			Reasoning:  "waiting on external dependency",
			CreatedBy:  "agent-3",
		},
	})

	c, ok := state["C-2026-05-04-cccc3333"]
	if !ok {
		t.Fatalf("contract not in fold result")
	}
	if c.Status != graphql.ContractStatusOpen {
		t.Errorf("Status = %v, want OPEN (blocked folds as open)", c.Status)
	}
}

// TestFold_V7_NullOwnerInherited verifies that an update event with a
// null owner field preserves the prior owner (no overwrite to empty).
func TestFold_V7_NullOwnerInherited(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-05-04-dddd4444",
			Status:     "started",
			Summary:    strPtr("Ownership test"),
			Owner:      strPtr("orchard:claude:original-owner"),
			CreatedBy:  "agent-4",
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-dddd4444",
			Status:     "started",
			// Owner is nil — should inherit from prior event.
			Reasoning: "progress update",
			CreatedBy: "agent-4",
		},
	})

	c := state["C-2026-05-04-dddd4444"]
	if c.OwnerSessionID != "orchard:claude:original-owner" {
		t.Errorf("OwnerSessionID = %q, want %q (inherited)", c.OwnerSessionID, "orchard:claude:original-owner")
	}
}

// TestFold_V7_NonNullOwnerHandoff verifies that an update event with a
// non-null owner changes the ownerSessionId (handoff semantics).
func TestFold_V7_NonNullOwnerHandoff(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-05-04-eeee5555",
			Status:     "started",
			Summary:    strPtr("Handoff test"),
			Owner:      strPtr("orchard:claude:original-owner"),
			CreatedBy:  "agent-5",
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-eeee5555",
			Status:     "started",
			Owner:      strPtr("orchard:claude:new-owner"), // handoff
			Reasoning:  "delegating to new agent",
			CreatedBy:  "agent-5",
		},
	})

	c := state["C-2026-05-04-eeee5555"]
	if c.OwnerSessionID != "orchard:claude:new-owner" {
		t.Errorf("OwnerSessionID = %q, want %q (handoff)", c.OwnerSessionID, "orchard:claude:new-owner")
	}
}

// TestFold_V7_LegacyV6EventsDropped verifies that v0.6-shaped events
// (which have a `kind` discriminator but no `status` field on non-creation
// event types) are silently dropped and do not crash the fold. The test
// simulates a file that contains legacy v0.6 events mixed in with v0.7
// events.
func TestFold_V7_LegacyV6EventsDropped(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:10:00Z")

	// v0.6-shaped events: `kind` field present, no `contract_id` on the
	// creation event (v0.6 uses `id`), and no `status` on non-creation
	// event types. These should not crash and should be skipped by Fold.
	state := Fold([]Event{
		// v0.7 creation — should be processed.
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-05-04-ffff6666",
			Status:     "started",
			Summary:    strPtr("Normal v0.7 contract"),
			Owner:      strPtr("orchard:claude:session-6"),
			CreatedBy:  "agent-6",
		},
		// Legacy v0.6 event with no status — fold must skip.
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-ffff6666",
		},
		// Another legacy event — also no status, also dropped.
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-05-04-ffff6666",
		},
	})

	c, ok := state["C-2026-05-04-ffff6666"]
	if !ok {
		t.Fatalf("contract missing from fold after legacy event processing")
	}
	// Status must not have changed due to the legacy events.
	if c.Status != graphql.ContractStatusOpen {
		t.Errorf("Status = %v, want OPEN (legacy events should be no-ops)", c.Status)
	}
	// LastEventAt should still be t0 (legacy events are dropped, not applied).
	if !c.LastEventAt.Equal(t0) {
		t.Errorf("LastEventAt = %v, want %v (legacy events must not advance timestamp)", c.LastEventAt, t0)
	}
}

// TestFold_V7_TwoContractsSameFile verifies that events for two
// different contract IDs in the same slice fold independently without
// bleeding state between them. In practice each .jsonl file holds one
// contract, but the adapter reads a directory — so the union of events
// from all files passes through Fold together.
func TestFold_V7_TwoContractsSameFile(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T13:00:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-alpha",
			Status:     "started",
			Summary:    strPtr("Alpha contract"),
			Owner:      strPtr("orchard:claude:session-a"),
			CreatedBy:  "agent-a",
		},
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-beta",
			Status:     "started",
			Summary:    strPtr("Beta contract"),
			Owner:      strPtr("orchard:claude:session-b"),
			CreatedBy:  "agent-b",
		},
		// Deliver only alpha.
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-alpha",
			Status:     "delivered",
			Reasoning:  "alpha done",
			CreatedBy:  "agent-a",
		},
	})

	alpha := state["C-alpha"]
	if alpha.Status != graphql.ContractStatusDelivered {
		t.Errorf("alpha status = %v, want DELIVERED", alpha.Status)
	}
	beta := state["C-beta"]
	if beta.Status != graphql.ContractStatusOpen {
		t.Errorf("beta status = %v, want OPEN (independent of alpha)", beta.Status)
	}
	if beta.Summary != "Beta contract" {
		t.Errorf("beta summary = %q, want Beta contract", beta.Summary)
	}
}

// TestFold_V7_SummaryInheritedOnUpdate verifies that when a second event
// carries a null summary, the first event's summary is preserved.
func TestFold_V7_SummaryInheritedOnUpdate(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	t1 := mustParse(t, "2026-05-04T12:30:00Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-inherit-summary",
			Status:     "started",
			Summary:    strPtr("Original summary"),
			Owner:      strPtr("orchard:claude:session-x"),
			CreatedBy:  "agent-x",
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-inherit-summary",
			Status:     "delivered",
			// Summary is nil — must inherit.
			Reasoning: "delivered",
			CreatedBy: "agent-x",
		},
	})

	c := state["C-inherit-summary"]
	if c.Summary != "Original summary" {
		t.Errorf("Summary = %q, want %q (inherited)", c.Summary, "Original summary")
	}
}

// TestFold_V7_EmptyContractIDSkipped verifies that events with no
// contract_id are silently dropped (defensive forward-compat).
func TestFold_V7_EmptyContractIDSkipped(t *testing.T) {
	t0 := mustParse(t, "2026-05-04T12:00:00Z")
	state := Fold([]Event{
		{
			Timestamp: timePtr(t0),
			// ContractID is empty — must be dropped.
			Status:  "started",
			Summary: strPtr("orphan"),
		},
	})
	if len(state) != 0 {
		t.Errorf("expected empty fold map for no-id events, got %d contracts", len(state))
	}
}

// TestFold_V7_MigratedDelivered tests a realistic migrated v0.6 file
// shape: first event sets summary+status=started, second event sets
// status=delivered with null summary and null owner. This mirrors the
// live migration output.
func TestFold_V7_MigratedDelivered(t *testing.T) {
	t0 := mustParse(t, "2026-04-27T22:42:10.647Z")
	t1 := mustParse(t, "2026-04-27T22:42:10.648Z")

	state := Fold([]Event{
		{
			Timestamp:  timePtr(t0),
			ContractID: "C-2026-04-27-0398e48e",
			Status:     "started",
			Summary:    strPtr("Deliver contracts plugin v0.5"),
			Reasoning:  "migrated from v0.6 contract record (creation event)",
			Owner:      strPtr("orchard:claude:756a8f00-7529-470f-b13b-143bc2b2c9f7"),
			CreatedBy:  "migrate-v06-to-v07",
			Source:     strPtr("migration:v06-to-v07"),
		},
		{
			Timestamp:  timePtr(t1),
			ContractID: "C-2026-04-27-0398e48e",
			Status:     "delivered",
			// Summary nil — inherit.
			Reasoning: "migrated from satisfied (v0.6 → v0.7); judges had passed",
			// Owner nil — inherit.
			CreatedBy: "migrate-v06-to-v07",
			Source:    strPtr("migration:v06-to-v07"),
		},
	})

	c, ok := state["C-2026-04-27-0398e48e"]
	if !ok {
		t.Fatalf("contract not in fold result")
	}
	if c.Status != graphql.ContractStatusDelivered {
		t.Errorf("Status = %v, want DELIVERED", c.Status)
	}
	if c.Summary != "Deliver contracts plugin v0.5" {
		t.Errorf("Summary = %q, want inherited summary", c.Summary)
	}
	if c.OwnerSessionID != "orchard:claude:756a8f00-7529-470f-b13b-143bc2b2c9f7" {
		t.Errorf("OwnerSessionID = %q, want inherited owner", c.OwnerSessionID)
	}
	if !c.CreatedAt.Equal(t0) {
		t.Errorf("CreatedAt = %v, want %v (immutable)", c.CreatedAt, t0)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, t1)
	}
}
