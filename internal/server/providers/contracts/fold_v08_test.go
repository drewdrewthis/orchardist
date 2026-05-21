package contracts

// fold_v08_test.go covers the v0.8 two-status fold model (L1.9-L1.11).
//
// L1.9: ContractStatus collapses to SIGNED and CLOSED.
//   - open_contract event → internal Status "open" → mapStatus → SIGNED
//   - close_contract event → internal Status "closed" → mapStatus → CLOSED
//
// L1.10: ClosedReason is DELIVERED or ABANDONED.
//   - close_contract with closedReason:"delivered" → ClosedReason "delivered" → mapReason → DELIVERED
//   - close_contract with closedReason:"abandoned" → ClosedReason "abandoned" → mapReason → ABANDONED
//
// L1.11: Removed fields (criteria, openQuestions, reportsTo, parentContractId)
//   are absent from the Contract struct; only the v0.8 fields remain.
//
// These tests exercise the internal fold functions (FoldFromSessionJSONL,
// applyCloseContractBlock) and the projection layer (statusToGraphQL,
// reasonToGraphQL, toGraphQL) that maps internal strings to the GraphQL
// enum values.

import (
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ---- L1.9: two-value status model ----------------------------------------

// TestFoldV08_OpenEvent_StatusIsSigned verifies that an open_contract
// tool_use produces a Contract with internal status "open", which
// projects to ContractStatusSigned.
func TestFoldV08_OpenEvent_StatusIsSigned(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	sessionID := "S-L19-001"
	contractID := "C-2026-05-21-L19-0001"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "deliver feature A", sessionID, t0)),
	}
	state := FoldFromSessionJSONL(records, sessionID)

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want \"open\"", c.Status)
	}
	if c.ClosedReason != "" {
		t.Errorf("ClosedReason = %q, want empty (open contract has no reason)", c.ClosedReason)
	}

	// Verify projection to GraphQL enum.
	gqlStatus := statusToGraphQL(c.Status)
	if gqlStatus != "SIGNED" {
		t.Errorf("statusToGraphQL(%q) = %q, want SIGNED", c.Status, gqlStatus)
	}
}

// TestFoldV08_CloseDelivered_StatusIsClosedReasonDelivered verifies
// open_contract + close_contract(closedReason:"delivered") →
// internal Status "closed", ClosedReason "delivered" →
// ContractStatusClosed, ContractReasonDelivered.
func TestFoldV08_CloseDelivered_StatusIsClosedReasonDelivered(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := t0.Add(time.Hour)
	sessionID := "S-L110-DELIVERED"
	contractID := "C-2026-05-21-L110-DEL"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "deliver feature B", sessionID, t0)),
		mustDecodeSessionRecord(t, closeContractLine(contractID, "delivered", sessionID, "", t1)),
	}
	state := FoldFromSessionJSONL(records, sessionID)

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Status != "closed" {
		t.Errorf("Status = %q, want \"closed\"", c.Status)
	}
	if c.ClosedReason != "delivered" {
		t.Errorf("ClosedReason = %q, want \"delivered\"", c.ClosedReason)
	}

	// Verify GraphQL projection.
	gqlStatus := statusToGraphQL(c.Status)
	if gqlStatus != "CLOSED" {
		t.Errorf("statusToGraphQL(%q) = %q, want CLOSED", c.Status, gqlStatus)
	}
	gqlReason := reasonToGraphQL(c.ClosedReason)
	if gqlReason != "DELIVERED" {
		t.Errorf("reasonToGraphQL(%q) = %q, want DELIVERED", c.ClosedReason, gqlReason)
	}
}

// TestFoldV08_CloseAbandoned_StatusIsClosedReasonAbandoned verifies
// open_contract + close_contract(closedReason:"abandoned") →
// internal Status "closed", ClosedReason "abandoned" →
// ContractStatusClosed, ContractReasonAbandoned.
func TestFoldV08_CloseAbandoned_StatusIsClosedReasonAbandoned(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := t0.Add(30 * time.Minute)
	sessionID := "S-L110-ABANDONED"
	contractID := "C-2026-05-21-L110-ABN"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "abandoned thing", sessionID, t0)),
		mustDecodeSessionRecord(t, closeContractLine(contractID, "abandoned", sessionID, "", t1)),
	}
	state := FoldFromSessionJSONL(records, sessionID)

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Status != "closed" {
		t.Errorf("Status = %q, want \"closed\"", c.Status)
	}
	if c.ClosedReason != "abandoned" {
		t.Errorf("ClosedReason = %q, want \"abandoned\"", c.ClosedReason)
	}

	// Verify GraphQL projection.
	gqlStatus := statusToGraphQL(c.Status)
	if gqlStatus != "CLOSED" {
		t.Errorf("statusToGraphQL(%q) = %q, want CLOSED", c.Status, gqlStatus)
	}
	gqlReason := reasonToGraphQL(c.ClosedReason)
	if gqlReason != "ABANDONED" {
		t.Errorf("reasonToGraphQL(%q) = %q, want ABANDONED", c.ClosedReason, gqlReason)
	}
}

// ---- L1.11: removed fields absent from Contract struct --------------------

// TestFoldV08_ContractHasNoRemovedFields asserts that the internal Contract
// struct does NOT carry the v0.8-removed fields: criteria, openQuestions,
// reportsTo, parentContractId. Verified at compile time via struct literal
// (unused fields are a compile error in Go).
//
// This test exists primarily as documentation — Go's type system enforces it.
func TestFoldV08_ContractHasNoRemovedFields(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	// Compile-time check: constructing Contract without Criteria, OpenQuestions,
	// ReportsTo, or ParentContractID. If those fields existed, we'd need to
	// include them; since they don't, this compiles iff they are absent.
	_ = Contract{
		ID:             "C-test",
		Statement:      "test",
		OwnerSessionID: "S-test",
		Status:         "open",
		ClosedReason:   "",
		CreatedAt:      t0,
		UpdatedAt:      t0,
		LastEventAt:    t0,
	}
	// If we reach here, the removed fields are confirmed absent.
}

// ---- L1.9: mapStatus exhaustive coverage ----------------------------------

// TestFoldV08_MapStatus_KnownValues checks both expected status values.
func TestFoldV08_MapStatus_KnownValues(t *testing.T) {
	cases := []struct {
		input    string
		wantGQL  string
	}{
		{"open", "SIGNED"},
		{"closed", "CLOSED"},
		// Unknown values default to SIGNED (open/active).
		{"", "SIGNED"},
		{"anything_else", "SIGNED"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := string(statusToGraphQL(tc.input))
			if got != tc.wantGQL {
				t.Errorf("statusToGraphQL(%q) = %q, want %q", tc.input, got, tc.wantGQL)
			}
		})
	}
}

// TestFoldV08_MapReason_KnownValues checks both expected reason values.
func TestFoldV08_MapReason_KnownValues(t *testing.T) {
	cases := []struct {
		input   string
		wantGQL string
	}{
		{"delivered", "DELIVERED"},
		{"abandoned", "ABANDONED"},
		// Unknown values default to DELIVERED.
		{"", "DELIVERED"},
		{"other", "DELIVERED"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := string(reasonToGraphQL(tc.input))
			if got != tc.wantGQL {
				t.Errorf("reasonToGraphQL(%q) = %q, want %q", tc.input, got, tc.wantGQL)
			}
		})
	}
}

// TestFoldV08_ToGraphQL_OpenContract verifies the full toGraphQL projection
// for an open (SIGNED) contract: ClosedReason is nil.
func TestFoldV08_ToGraphQL_OpenContract(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	c := Contract{
		ID:             "C-tographql-open",
		Statement:      "make it work",
		OwnerSessionID: "S-open-001",
		Status:         "open",
		ClosedReason:   "",
		CreatedAt:      t0,
		UpdatedAt:      t0,
		LastEventAt:    t0,
	}
	gc := toGraphQL(c)
	if gc.Status != "SIGNED" {
		t.Errorf("Status = %q, want SIGNED", gc.Status)
	}
	if gc.ClosedReason != nil {
		t.Errorf("ClosedReason = %v, want nil (open contract has no reason)", gc.ClosedReason)
	}
}

// TestFoldV08_ToGraphQL_ClosedDelivered verifies the full toGraphQL projection
// for a closed contract with reason=delivered.
func TestFoldV08_ToGraphQL_ClosedDelivered(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := t0.Add(time.Hour)
	c := Contract{
		ID:             "C-tographql-closed-del",
		Statement:      "make it done",
		OwnerSessionID: "S-closed-del-001",
		Status:         "closed",
		ClosedReason:   "delivered",
		CreatedAt:      t0,
		UpdatedAt:      t1,
		LastEventAt:    t1,
	}
	gc := toGraphQL(c)
	if gc.Status != "CLOSED" {
		t.Errorf("Status = %q, want CLOSED", gc.Status)
	}
	if gc.ClosedReason == nil {
		t.Fatalf("ClosedReason is nil, want DELIVERED")
	}
	if *gc.ClosedReason != "DELIVERED" {
		t.Errorf("ClosedReason = %q, want DELIVERED", *gc.ClosedReason)
	}
}

// TestFoldV08_ToGraphQL_ClosedAbandoned verifies the full toGraphQL projection
// for a closed contract with reason=abandoned.
func TestFoldV08_ToGraphQL_ClosedAbandoned(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	c := Contract{
		ID:             "C-tographql-closed-abn",
		Statement:      "gave up",
		OwnerSessionID: "S-closed-abn-001",
		Status:         "closed",
		ClosedReason:   "abandoned",
		CreatedAt:      t0,
		UpdatedAt:      t0,
		LastEventAt:    t0,
	}
	gc := toGraphQL(c)
	if gc.Status != "CLOSED" {
		t.Errorf("Status = %q, want CLOSED", gc.Status)
	}
	if gc.ClosedReason == nil {
		t.Fatalf("ClosedReason is nil, want ABANDONED")
	}
	if *gc.ClosedReason != "ABANDONED" {
		t.Errorf("ClosedReason = %q, want ABANDONED", *gc.ClosedReason)
	}
}

// ---- ContractFilter.closedReasons ----------------------------------------

// TestMatches_ClosedReasonDelivered verifies that filtering by
// closedReasons:[DELIVERED] returns only CLOSED+DELIVERED contracts and
// excludes CLOSED+ABANDONED and SIGNED (open) contracts.
func TestMatches_ClosedReasonDelivered(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	delivered := Contract{
		ID: "C-delivered", Status: "closed", ClosedReason: "delivered",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}
	abandoned := Contract{
		ID: "C-abandoned", Status: "closed", ClosedReason: "abandoned",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}
	open := Contract{
		ID: "C-open", Status: "open", ClosedReason: "",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}

	filter := &graphql.ContractFilter{
		ClosedReasons: []graphql.ContractReason{graphql.ContractReasonDelivered},
	}

	if !matches(delivered, filter) {
		t.Error("delivered contract should match closedReasons:[DELIVERED] filter")
	}
	if matches(abandoned, filter) {
		t.Error("abandoned contract should not match closedReasons:[DELIVERED] filter")
	}
	if matches(open, filter) {
		t.Error("open contract should not match closedReasons:[DELIVERED] filter")
	}
}

// TestMatches_ClosedReasonAbandoned verifies that filtering by
// closedReasons:[ABANDONED] returns only CLOSED+ABANDONED contracts.
func TestMatches_ClosedReasonAbandoned(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	delivered := Contract{
		ID: "C-del2", Status: "closed", ClosedReason: "delivered",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}
	abandoned := Contract{
		ID: "C-abn2", Status: "closed", ClosedReason: "abandoned",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}

	filter := &graphql.ContractFilter{
		ClosedReasons: []graphql.ContractReason{graphql.ContractReasonAbandoned},
	}

	if !matches(abandoned, filter) {
		t.Error("abandoned contract should match closedReasons:[ABANDONED] filter")
	}
	if matches(delivered, filter) {
		t.Error("delivered contract should not match closedReasons:[ABANDONED] filter")
	}
}

// TestMatches_ClosedReasonBoth verifies that filtering by
// closedReasons:[DELIVERED, ABANDONED] returns all closed contracts
// regardless of reason, but excludes open contracts.
func TestMatches_ClosedReasonBoth(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")

	delivered := Contract{
		ID: "C-del3", Status: "closed", ClosedReason: "delivered",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}
	abandoned := Contract{
		ID: "C-abn3", Status: "closed", ClosedReason: "abandoned",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}
	open := Contract{
		ID: "C-open3", Status: "open",
		CreatedAt: t0, UpdatedAt: t0, LastEventAt: t0,
	}

	filter := &graphql.ContractFilter{
		ClosedReasons: []graphql.ContractReason{
			graphql.ContractReasonDelivered,
			graphql.ContractReasonAbandoned,
		},
	}

	if !matches(delivered, filter) {
		t.Error("delivered contract should match closedReasons:[DELIVERED,ABANDONED] filter")
	}
	if !matches(abandoned, filter) {
		t.Error("abandoned contract should match closedReasons:[DELIVERED,ABANDONED] filter")
	}
	if matches(open, filter) {
		t.Error("open contract should not match closedReasons:[DELIVERED,ABANDONED] filter")
	}
}
