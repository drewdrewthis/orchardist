package contracts

// fold_exit_test.go tests the fold's exit/quit/bye auto-close extension.
//
// Scenarios:
//   - L2.11: /exit local_command record synthesizes a virtual close_contract
//     (reason:"delivered", note:"exit:exit") for the open conversation contract.
//   - L2.12: /quit and /bye produce the same virtual close with
//     note:"exit:quit" and note:"exit:bye" respectively.
//   - L2.13: Resume — after /exit, a subsequent open_contract for the
//     conversation deliverable creates a NEW contract (the dedup bypass
//     for a closed conversation contract). The result is two contract
//     records: one CLOSED/DELIVERED and one OPEN.
//   - L2.15: Explicit close_contract tool_use closes the conversation
//     contract without needing a virtual event — the fold processes it
//     identically to any other close_contract block.

import (
	"encoding/json"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// exitCommandLine returns a system/local_command JSONL record that Claude Code
// writes when the user invokes /exit, /quit, or /bye inside a session.
// The content field carries the command-name XML tag.
func exitCommandLine(verb, sessionID string, at time.Time) string {
	rec := map[string]any{
		"type":      "system",
		"content":   "<command-name>/" + verb + "</command-name>",
		"sessionId": sessionID,
		"timestamp": at.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// openConvContractLine returns an open_contract line for the fixed conversation
// contract deliverable (ConversationContractDeliverable).
func openConvContractLine(contractID, sessionID string, at time.Time) string {
	return openContractLine(contractID, ConversationContractDeliverable, sessionID, at)
}

// ---------------------------------------------------------------------------
// L2.11 — /exit auto-closes the conversation contract
// ---------------------------------------------------------------------------

// TestFoldExit_ExitSynthesizesVirtualClose verifies that when a session JSONL
// contains an open conversation contract followed by a /exit local_command
// record, the fold synthesizes a virtual close_contract(reason:"delivered",
// note:"exit:exit") and the contract is CLOSED/DELIVERED.
func TestFoldExit_ExitSynthesizesVirtualClose(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T10:30:00Z")
	sessionID := "S-EXIT-001"
	contractID := "C-2026-05-21-EXIT0001"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openConvContractLine(contractID, sessionID, t0)),
		mustDecodeSessionRecord(t, exitCommandLine("exit", sessionID, t1)),
	}

	state := FoldFromSessionJSONL(records, sessionID).Contracts

	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("L2.11: contract %s not in fold result", contractID)
	}
	if c.Status != "closed" {
		t.Errorf("L2.11: Status = %q, want closed (virtual close from /exit)", c.Status)
	}
	if c.ClosedReason != "delivered" {
		t.Errorf("L2.11: ClosedReason = %q, want delivered", c.ClosedReason)
	}
	if !c.UpdatedAt.Equal(t1) {
		t.Errorf("L2.11: UpdatedAt = %v, want %v (exit record timestamp)", c.UpdatedAt, t1)
	}
}

// ---------------------------------------------------------------------------
// L2.12 — /quit and /bye also auto-close
// ---------------------------------------------------------------------------

// TestFoldExit_QuitAndByeSynthesizeVirtualClose is a table-driven test for
// all three exit verbs. Each verb must produce Status=closed, ClosedReason=delivered.
func TestFoldExit_QuitAndByeSynthesizeVirtualClose(t *testing.T) {
	type tc struct {
		verb       string
		sessionID  string
		contractID string
	}
	cases := []tc{
		{"exit", "S-EXIT-001", "C-2026-05-21-EXIT0011"},
		{"quit", "S-EXIT-002", "C-2026-05-21-EXIT0012"},
		{"bye", "S-EXIT-003", "C-2026-05-21-EXIT0013"},
	}

	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T10:45:00Z")

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			records := []SessionRecord{
				mustDecodeSessionRecord(t, openConvContractLine(tc.contractID, tc.sessionID, t0)),
				mustDecodeSessionRecord(t, exitCommandLine(tc.verb, tc.sessionID, t1)),
			}

			state := FoldFromSessionJSONL(records, tc.sessionID).Contracts

			c, ok := state[ContractID(tc.contractID)]
			if !ok {
				t.Fatalf("L2.12/%s: contract %s not in fold result", tc.verb, tc.contractID)
			}
			if c.Status != "closed" {
				t.Errorf("L2.12/%s: Status = %q, want closed", tc.verb, c.Status)
			}
			if c.ClosedReason != "delivered" {
				t.Errorf("L2.12/%s: ClosedReason = %q, want delivered", tc.verb, c.ClosedReason)
			}
		})
	}
}

// TestFoldExit_NonConvContractNotAutoClosedByExit verifies that /exit only
// auto-closes the conversation contract. Non-conversation contracts remain open.
func TestFoldExit_NonConvContractNotAutoClosedByExit(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T10:30:00Z")
	sessionID := "S-EXIT-NC-001"
	convID := "C-2026-05-21-EXITNC01"
	otherID := "C-2026-05-21-EXITNC02"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openConvContractLine(convID, sessionID, t0)),
		mustDecodeSessionRecord(t, openContractLine(otherID, "ship the other widget", sessionID, t0)),
		mustDecodeSessionRecord(t, exitCommandLine("exit", sessionID, t1)),
	}

	state := FoldFromSessionJSONL(records, sessionID).Contracts

	// Conversation contract must be closed.
	conv, ok := state[ContractID(convID)]
	if !ok {
		t.Fatalf("conversation contract %s not in fold result", convID)
	}
	if conv.Status != "closed" {
		t.Errorf("conversation contract Status = %q, want closed", conv.Status)
	}

	// Non-conversation contract must remain open.
	other, ok := state[ContractID(otherID)]
	if !ok {
		t.Fatalf("non-conversation contract %s not in fold result", otherID)
	}
	if other.Status != "open" {
		t.Errorf("non-conversation contract Status = %q, want open (exit only closes conversation contract)", other.Status)
	}
}

// TestFoldExit_NoConvContractExitIsNoop verifies that /exit with no open
// conversation contract does not panic or corrupt state.
func TestFoldExit_NoConvContractExitIsNoop(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	sessionID := "S-EXIT-NOOP"
	contractID := "C-2026-05-21-NOOP0001"

	records := []SessionRecord{
		mustDecodeSessionRecord(t, openContractLine(contractID, "non-conversation work", sessionID, t0)),
		mustDecodeSessionRecord(t, exitCommandLine("exit", sessionID, t0.Add(time.Hour))),
	}

	state := FoldFromSessionJSONL(records, sessionID).Contracts

	// The other contract must be unaffected (still open).
	c, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("contract %s not in fold result", contractID)
	}
	if c.Status != "open" {
		t.Errorf("Status = %q, want open (exit must not close non-conversation contracts)", c.Status)
	}
}

// ---------------------------------------------------------------------------
// L2.13 — Resume after /exit creates two contract records
// ---------------------------------------------------------------------------

// TestFoldExit_ResumeAfterExitCreatesTwoContracts verifies the L2.13 resume
// scenario: after /exit closes the conversation contract, a subsequent
// open_contract for the same deliverable creates a NEW contract record.
// The fold must contain two contracts: one CLOSED and one OPEN.
func TestFoldExit_ResumeAfterExitCreatesTwoContracts(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T11:00:00Z")
	t2 := mustParse(t, "2026-05-21T12:00:00Z")
	sessionID := "S-RESUME-001"
	firstContractID := "C-2026-05-21-RSME0001"
	secondContractID := "C-2026-05-21-RSME0002"

	records := []SessionRecord{
		// First open → conversation contract is opened.
		mustDecodeSessionRecord(t, openConvContractLine(firstContractID, sessionID, t0)),
		// /exit → virtual close of the first contract.
		mustDecodeSessionRecord(t, exitCommandLine("exit", sessionID, t1)),
		// Resume (claude -r): UserPromptSubmit fires again → second open_contract.
		mustDecodeSessionRecord(t, openConvContractLine(secondContractID, sessionID, t2)),
	}

	state := FoldFromSessionJSONL(records, sessionID).Contracts

	// Must have exactly two contracts.
	if got := len(state); got != 2 {
		t.Fatalf("L2.13: expected 2 contracts in fold result, got %d", got)
	}

	first, ok := state[ContractID(firstContractID)]
	if !ok {
		t.Fatalf("L2.13: first contract %s not in fold result", firstContractID)
	}
	if first.Status != "closed" {
		t.Errorf("L2.13: first contract Status = %q, want closed (closed by /exit)", first.Status)
	}
	if first.ClosedReason != "delivered" {
		t.Errorf("L2.13: first contract ClosedReason = %q, want delivered", first.ClosedReason)
	}

	second, ok := state[ContractID(secondContractID)]
	if !ok {
		t.Fatalf("L2.13: second contract %s not in fold result", secondContractID)
	}
	if second.Status != "open" {
		t.Errorf("L2.13: second contract Status = %q, want open (fresh open after resume)", second.Status)
	}
	if second.OwnerSessionID != sessionID {
		t.Errorf("L2.13: second contract OwnerSessionID = %q, want %q", second.OwnerSessionID, sessionID)
	}
}

// ---------------------------------------------------------------------------
// L2.15 — Direct close_contract MCP call closes without inventory prompt
// ---------------------------------------------------------------------------

// TestFoldExit_ExplicitCloseContractClosesDirectly verifies L2.15: when the
// user explicitly calls the close_contract MCP tool (writing a close_contract
// tool_use into the assistant record), the fold closes the contract with
// reason=delivered immediately — no virtual event needed, no inventory prompt.
//
// This is structurally identical to any other close_contract block; the test
// confirms the fold handles it correctly when there is no /exit record.
func TestFoldExit_ExplicitCloseContractClosesDirectly(t *testing.T) {
	t0 := mustParse(t, "2026-05-21T10:00:00Z")
	t1 := mustParse(t, "2026-05-21T11:00:00Z")
	sessionID := "S-ESCAPE-001"
	contractID := "C-2026-05-21-ESC00001"
	childID := "C-2026-05-21-ESC00002"

	records := []SessionRecord{
		// Conversation contract opened.
		mustDecodeSessionRecord(t, openConvContractLine(contractID, sessionID, t0)),
		// A child contract opened at the same time.
		mustDecodeSessionRecord(t, openContractLine(childID, "child work", sessionID, t0)),
		// Explicit close_contract MCP call — written by the user/agent directly.
		mustDecodeSessionRecord(t, closeContractLine(contractID, "delivered", sessionID, "", t1)),
	}

	state := FoldFromSessionJSONL(records, sessionID).Contracts

	// Conversation contract must be closed.
	conv, ok := state[ContractID(contractID)]
	if !ok {
		t.Fatalf("L2.15: conversation contract %s not in fold result", contractID)
	}
	if conv.Status != "closed" {
		t.Errorf("L2.15: conversation contract Status = %q, want closed", conv.Status)
	}
	if conv.ClosedReason != "delivered" {
		t.Errorf("L2.15: conversation contract ClosedReason = %q, want delivered", conv.ClosedReason)
	}
	if !conv.UpdatedAt.Equal(t1) {
		t.Errorf("L2.15: conversation contract UpdatedAt = %v, want %v", conv.UpdatedAt, t1)
	}

	// Child contract must remain open (no inventory check in fold).
	child, ok := state[ContractID(childID)]
	if !ok {
		t.Fatalf("L2.15: child contract %s not in fold result", childID)
	}
	if child.Status != "open" {
		t.Errorf("L2.15: child contract Status = %q, want open (explicit close only closed the conversation contract)", child.Status)
	}
}
