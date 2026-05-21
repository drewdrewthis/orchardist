// Tests for the conversation-contracts MCP server (task L1.2).
//
// Scenario: open_contract MCP call writes a tool_use event to the calling
// session's jsonl.
//
// We test the handler function directly (no subprocess) so we can:
//   - Inject a temp-dir jsonl path (bypassing env-var resolution)
//   - Control the clock via an injected time.Time
//   - Inspect the written file deterministically
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// contractIDPattern is the canonical format: C-YYYY-MM-DD-XXXXXXXX
// where XXXXXXXX is exactly 8 lowercase hex characters.
var contractIDPattern = regexp.MustCompile(`^C-\d{4}-\d{2}-\d{2}-[0-9a-f]{8}$`)

// fixedNow is a deterministic clock value used across tests.
var fixedNow = time.Date(2026, 5, 21, 12, 0, 0, 123456789, time.UTC)

// readJsonlRecords reads all newline-delimited JSON records from path.
func readJsonlRecords(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open jsonl %s: %v", path, err)
	}
	defer f.Close()

	var records []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal jsonl line: %v (line: %s)", err, line)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan jsonl: %v", err)
	}
	return records
}

// extractToolUseRecords returns all records from recs whose message.content[*].type == "tool_use"
// and whose message.content[*].name == toolName.
func extractToolUseRecords(recs []map[string]interface{}, toolName string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, rec := range recs {
		if rec["type"] != "assistant" {
			continue
		}
		msg, ok := rec["message"].(map[string]interface{})
		if !ok {
			continue
		}
		contents, ok := msg["content"].([]interface{})
		if !ok {
			continue
		}
		for _, c := range contents {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if cm["type"] == "tool_use" && cm["name"] == toolName {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}

// inputOf extracts the first tool_use input from an assistant record.
func inputOf(t *testing.T, rec map[string]interface{}) map[string]interface{} {
	t.Helper()
	msg := rec["message"].(map[string]interface{})
	contents := msg["content"].([]interface{})
	for _, c := range contents {
		cm := c.(map[string]interface{})
		if cm["type"] == "tool_use" {
			inp, ok := cm["input"].(map[string]interface{})
			if !ok {
				t.Fatalf("tool_use input is not an object: %T", cm["input"])
			}
			return inp
		}
	}
	t.Fatalf("no tool_use content found in record")
	return nil
}

// TestOpenContract_AppendsExactlyOneToolUseEvent asserts that calling
// handleOpenContract with a non-empty deliverable appends exactly one
// "open_contract" tool_use event to the target jsonl.
func TestOpenContract_AppendsExactlyOneToolUseEvent(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-OPEN-001.jsonl")

	args := openContractArgs{Deliverable: "ship the widget"}
	_, err := handleOpenContract(args, jsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleOpenContract: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "open_contract")

	if len(toolUseRecs) != 1 {
		t.Errorf("want exactly 1 open_contract tool_use event; got %d", len(toolUseRecs))
	}
}

// TestOpenContract_EventCarriesRequiredFields asserts that the written
// tool_use event carries the fields {id, deliverable, createdAt}.
func TestOpenContract_EventCarriesRequiredFields(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-OPEN-001.jsonl")

	args := openContractArgs{Deliverable: "ship the widget"}
	contractID, err := handleOpenContract(args, jsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleOpenContract: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "open_contract")
	if len(toolUseRecs) == 0 {
		t.Fatal("no open_contract tool_use event found")
	}

	inp := inputOf(t, toolUseRecs[0])

	// id must be present and match the return value
	id, ok := inp["id"].(string)
	if !ok || id == "" {
		t.Error("event input missing field 'id'")
	}
	if id != contractID {
		t.Errorf("event id %q != returned contractID %q", id, contractID)
	}

	// deliverable must match the input
	deliverable, ok := inp["deliverable"].(string)
	if !ok || deliverable == "" {
		t.Error("event input missing field 'deliverable'")
	}
	if deliverable != args.Deliverable {
		t.Errorf("event deliverable %q != input %q", deliverable, args.Deliverable)
	}

	// createdAt must be present
	createdAt, ok := inp["createdAt"].(string)
	if !ok || createdAt == "" {
		t.Error("event input missing field 'createdAt'")
	}
}

// TestOpenContract_IDMatchesFormat asserts that the returned (and stored)
// contract id matches the canonical C-YYYY-MM-DD-XXXXXXXX format.
func TestOpenContract_IDMatchesFormat(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-OPEN-001.jsonl")

	args := openContractArgs{Deliverable: "ship the widget"}
	contractID, err := handleOpenContract(args, jsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleOpenContract: %v", err)
	}

	if !contractIDPattern.MatchString(contractID) {
		t.Errorf("contractID %q does not match pattern C-YYYY-MM-DD-XXXXXXXX (got %q)", contractID, contractID)
	}

	// Also verify the id stored in the event matches the same pattern.
	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "open_contract")
	if len(toolUseRecs) == 0 {
		t.Fatal("no open_contract event found")
	}
	inp := inputOf(t, toolUseRecs[0])
	id := inp["id"].(string)
	if !contractIDPattern.MatchString(id) {
		t.Errorf("stored event id %q does not match pattern C-YYYY-MM-DD-XXXXXXXX", id)
	}
}

// TestOpenContract_AppendsTwiceGivesTwoEvents verifies that calling
// handleOpenContract twice produces two events (append semantics, not overwrite).
func TestOpenContract_AppendsTwiceGivesTwoEvents(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-OPEN-001.jsonl")

	args := openContractArgs{Deliverable: "ship the widget"}
	now1 := fixedNow
	now2 := fixedNow.Add(time.Second)

	if _, err := handleOpenContract(args, jsonlPath, now1); err != nil {
		t.Fatalf("first handleOpenContract: %v", err)
	}
	if _, err := handleOpenContract(args, jsonlPath, now2); err != nil {
		t.Fatalf("second handleOpenContract: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "open_contract")
	if len(toolUseRecs) != 2 {
		t.Errorf("want 2 open_contract events after two calls; got %d", len(toolUseRecs))
	}
}

// TestEncodeCwd verifies the cwd-to-project-dir encoding.
// Both '/' and '.' are replaced with '-'.
func TestEncodeCwd(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{"/home/user/workspace", "-home-user-workspace"},
		// Paths with dots: both '/' and '.' map to '-'.
		// e.g. /home/user/project/.worktrees/branch-name
		// →    -home-user-project--worktrees-branch-name
		{"/home/user/project/.worktrees/branch-name",
			"-home-user-project--worktrees-branch-name"},
	}
	for _, tc := range cases {
		got := encodeCwd(tc.cwd)
		if got != tc.want {
			t.Errorf("encodeCwd(%q) = %q; want %q", tc.cwd, got, tc.want)
		}
	}
}

// TestNewContractID verifies the contract ID format for a fixed time.
func TestNewContractID(t *testing.T) {
	id := newContractID(fixedNow)
	if !contractIDPattern.MatchString(id) {
		t.Errorf("newContractID(%v) = %q; does not match pattern C-YYYY-MM-DD-XXXXXXXX", fixedNow, id)
	}
	// Date prefix must use the UTC date of fixedNow (2026-05-21)
	if got, want := id[:12], "C-2026-05-21"; got != want {
		t.Errorf("id date prefix = %q; want %q", got, want)
	}
}

// ---- L1.3 tests: close_contract ----------------------------------------

// TestCloseContract_AppendsExactlyOneToolUseEvent asserts that calling
// handleCloseContract appends exactly one "close_contract" tool_use event.
func TestCloseContract_AppendsExactlyOneToolUseEvent(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-CLOSE-001.jsonl")

	args := closeContractArgs{
		ID:     "C-2026-05-21-ABCD1234",
		Reason: "delivered",
	}
	err := handleCloseContract(args, jsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleCloseContract: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "close_contract")

	if len(toolUseRecs) != 1 {
		t.Errorf("want exactly 1 close_contract tool_use event; got %d", len(toolUseRecs))
	}
}

// TestCloseContract_EventCarriesRequiredFields asserts the close event carries
// {id, closedAt, closedReason} and that closedReason is the value passed in.
func TestCloseContract_EventCarriesRequiredFields(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-CLOSE-001.jsonl")

	args := closeContractArgs{
		ID:     "C-2026-05-21-ABCD1234",
		Reason: "delivered",
	}
	err := handleCloseContract(args, jsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleCloseContract: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "close_contract")
	if len(toolUseRecs) == 0 {
		t.Fatal("no close_contract tool_use event found")
	}

	inp := inputOf(t, toolUseRecs[0])

	id, ok := inp["id"].(string)
	if !ok || id == "" {
		t.Error("close event input missing field 'id'")
	}
	if id != args.ID {
		t.Errorf("close event id %q != input id %q", id, args.ID)
	}

	closedAt, ok := inp["closedAt"].(string)
	if !ok || closedAt == "" {
		t.Error("close event input missing field 'closedAt'")
	}

	closedReason, ok := inp["closedReason"].(string)
	if !ok || closedReason == "" {
		t.Error("close event input missing field 'closedReason'")
	}
	if closedReason != "delivered" {
		t.Errorf("closedReason = %q; want %q", closedReason, "delivered")
	}
}

// TestCloseContract_AbandonedReason verifies closedReason "abandoned" is accepted.
func TestCloseContract_AbandonedReason(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-CLOSE-001.jsonl")

	args := closeContractArgs{
		ID:     "C-2026-05-21-ABCD1234",
		Reason: "abandoned",
	}
	if err := handleCloseContract(args, jsonlPath, fixedNow); err != nil {
		t.Fatalf("handleCloseContract with reason=abandoned: %v", err)
	}

	recs := readJsonlRecords(t, jsonlPath)
	toolUseRecs := extractToolUseRecords(recs, "close_contract")
	if len(toolUseRecs) == 0 {
		t.Fatal("no close_contract event found")
	}
	inp := inputOf(t, toolUseRecs[0])
	if inp["closedReason"] != "abandoned" {
		t.Errorf("closedReason = %q; want %q", inp["closedReason"], "abandoned")
	}
}

// TestCloseContract_InvalidReasonReturnsError verifies that a bad reason is rejected.
func TestCloseContract_InvalidReasonReturnsError(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "S-MCP-CLOSE-001.jsonl")

	args := closeContractArgs{
		ID:     "C-2026-05-21-ABCD1234",
		Reason: "invalid",
	}
	err := handleCloseContract(args, jsonlPath, fixedNow)
	if err == nil {
		t.Error("expected error for invalid reason; got nil")
	}
}

// ---- L1.4 test: tools/list surface -----------------------------------------

// TestToolsList_OnlyOpenAndCloseContract asserts that the MCP tools/list result
// contains exactly "open_contract" and "close_contract", and does NOT contain
// "update_contract" or "accept_contract".
func TestToolsList_OnlyOpenAndCloseContract(t *testing.T) {
	req := rpcRequest{
		Jsonrpc: "2.0",
		ID:      1,
		Method:  "tools/list",
	}
	resp := handleRequest(req)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %v", resp.Error.Message)
	}

	result, ok := resp.Result.(toolsListResult)
	if !ok {
		t.Fatalf("tools/list result is not toolsListResult: %T", resp.Result)
	}

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}

	// Required tools must be present.
	for _, required := range []string{"open_contract", "close_contract"} {
		if !names[required] {
			t.Errorf("tools/list: missing required tool %q", required)
		}
	}

	// Forbidden tools must NOT be present.
	for _, forbidden := range []string{"update_contract", "accept_contract"} {
		if names[forbidden] {
			t.Errorf("tools/list: must NOT expose tool %q", forbidden)
		}
	}

	// Exactly two tools.
	if len(result.Tools) != 2 {
		t.Errorf("tools/list: want exactly 2 tools; got %d: %v", len(result.Tools), result.Tools)
	}
}

// ---- L1.5 test: F2 non-owner abandon ---------------------------------------

// TestCloseContract_NonOwnerAbandon_WritesToAbandonersJsonl verifies the F2
// scenario: a different session than the owner invokes close_contract with
// reason="abandoned" and aboutSessionId pointing at the owner. The event must
// be written to the ABANDONER's jsonl (the path passed in) with aboutSessionId set.
func TestCloseContract_NonOwnerAbandon_WritesToAbandonersJsonl(t *testing.T) {
	dir := t.TempDir()

	// The abandoner's jsonl (S-OTHER) — this is where the event should land.
	abandonerJsonlPath := filepath.Join(dir, "S-OTHER.jsonl")
	// The owner's jsonl (S-OWNER) — must remain untouched.
	ownerJsonlPath := filepath.Join(dir, "S-OWNER.jsonl")

	// Write an open_contract event to the owner's jsonl to simulate
	// the existing open contract.
	openArgs := openContractArgs{Deliverable: "some work"}
	_, err := handleOpenContract(openArgs, ownerJsonlPath, fixedNow)
	if err != nil {
		t.Fatalf("handleOpenContract (owner): %v", err)
	}

	// Now invoke close_contract AS the abandoner session.
	closeArgs := closeContractArgs{
		ID:             "C-2026-05-21-EEEE5555",
		Reason:         "abandoned",
		AboutSessionId: "S-OWNER",
	}
	err = handleCloseContract(closeArgs, abandonerJsonlPath, fixedNow.Add(time.Second))
	if err != nil {
		t.Fatalf("handleCloseContract (abandoner): %v", err)
	}

	// The close event must be in the abandoner's jsonl.
	abandonerRecs := readJsonlRecords(t, abandonerJsonlPath)
	closeRecs := extractToolUseRecords(abandonerRecs, "close_contract")
	if len(closeRecs) != 1 {
		t.Errorf("want 1 close_contract event in abandoner jsonl; got %d", len(closeRecs))
	}

	if len(closeRecs) > 0 {
		inp := inputOf(t, closeRecs[0])

		// aboutSessionId must be the owner's session id.
		aboutSessionId, ok := inp["aboutSessionId"].(string)
		if !ok || aboutSessionId == "" {
			t.Error("close event missing field 'aboutSessionId'")
		}
		if aboutSessionId != "S-OWNER" {
			t.Errorf("aboutSessionId = %q; want %q", aboutSessionId, "S-OWNER")
		}

		// closedReason must be "abandoned".
		if inp["closedReason"] != "abandoned" {
			t.Errorf("closedReason = %q; want %q", inp["closedReason"], "abandoned")
		}
	}

	// The owner's jsonl must only have the open event — no close event.
	ownerRecs := readJsonlRecords(t, ownerJsonlPath)
	ownerCloseRecs := extractToolUseRecords(ownerRecs, "close_contract")
	if len(ownerCloseRecs) != 0 {
		t.Errorf("owner jsonl must have 0 close_contract events; got %d", len(ownerCloseRecs))
	}
}
