// Tests for the /close-conversation skill.
//
// Scenarios:
//
//   L2.9 — User confirms closure via /close-conversation skill:
//     The SKILL.md must describe a flow that calls close_contract with
//     reason "delivered" when the user confirms the session is complete.
//     The close-note must include the inventory summary.
//
//   L2.10 — User names open items (contract stays open; named items become
//     child contracts):
//     The SKILL.md must describe a flow that opens child contracts for each
//     named item and does NOT close the conversation contract.
//
// These are content tests: the skill is a human-readable markdown document;
// correctness is verified by asserting that the required phrases and steps
// appear in the document.
package skill_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// skillPath returns the absolute path to SKILL.md relative to this test file.
func skillPath(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p, err := filepath.Abs(filepath.Join(filepath.Dir(testFile), "SKILL.md"))
	if err != nil {
		t.Fatalf("abs skill path: %v", err)
	}
	return p
}

// readSkill reads SKILL.md and returns its content.
func readSkill(t *testing.T) string {
	t.Helper()
	path := skillPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SKILL.md at %s: %v", path, err)
	}
	return string(raw)
}

// ---- L2.9 — /close-conversation skill closes the contract as delivered ------

// TestL2_9_SkillDescribesCloseContractDelivered verifies that SKILL.md
// documents a flow that calls close_contract with reason "delivered" when the
// user confirms the session is complete (L2.9).
func TestL2_9_SkillDescribesCloseContractDelivered(t *testing.T) {
	content := readSkill(t)

	// The skill must emit a close sentinel (orchard_contract close) — the
	// jsonl-sentinel mechanism that replaced the close_contract MCP tool.
	if !strings.Contains(content, "orchard_contract") || !strings.Contains(content, "close") {
		t.Error("L2.9: SKILL.md must describe emitting an orchard_contract close sentinel")
	}

	// The skill must specify reason "delivered".
	if !strings.Contains(content, "delivered") {
		t.Error(`L2.9: SKILL.md must specify reason "delivered" for the close`)
	}

	// The skill must describe folding open contracts first (the inventory step).
	if !strings.Contains(content, "contracts") && !strings.Contains(content, "fold") {
		t.Error("L2.9: SKILL.md must describe folding open contracts for the inventory step")
	}

	// The skill must mention the inventory at the moment of close.
	if !strings.Contains(content, "inventory") {
		t.Error("L2.9: SKILL.md must reference including the inventory summary in the close reason")
	}
}

// TestL2_9_SkillIdentifiesConversationContractDeliverable verifies that the
// SKILL.md identifies the conversation contract by its fixed deliverable string
// so the agent can distinguish it from child contracts.
func TestL2_9_SkillIdentifiesConversationContractDeliverable(t *testing.T) {
	content := readSkill(t)

	// The deliverable is the exact string used by the fold dedup and the MCP hook.
	const deliverable = "user agrees conversation has come to a close and there are no loose ends"
	if !strings.Contains(content, deliverable) {
		t.Errorf("L2.9: SKILL.md must include the conversation contract deliverable %q so the agent can identify it", deliverable)
	}
}

// ---- L2.10 — User names open items; child contracts filed; conv stays open --

// TestL2_10_SkillDescribesChildContractPath verifies that SKILL.md documents
// the path where the user names unresolved items: the skill must describe
// calling open_contract for each named item and NOT closing the conversation
// contract (L2.10).
func TestL2_10_SkillDescribesChildContractPath(t *testing.T) {
	content := readSkill(t)

	// The skill must describe opening child contracts (via /open-contract).
	if !strings.Contains(content, "/open-contract") {
		t.Error("L2.10: SKILL.md must reference /open-contract for filing child contracts")
	}

	// The skill must state that the conversation contract stays open when items are named.
	if !strings.Contains(content, "stays OPEN") && !strings.Contains(content, "stays open") && !strings.Contains(content, "remain") {
		t.Error("L2.10: SKILL.md must state that the conversation contract remains open when the user names open items")
	}

	// The skill must describe child contracts as distinct from the conversation contract.
	if !strings.Contains(content, "child") {
		t.Error("L2.10: SKILL.md must use the term 'child' to distinguish item contracts from the conversation contract")
	}
}

// TestL2_10_SkillCoversAllClosePathsInSummary verifies that the SKILL.md
// enumerates all close paths (L2.9, L2.11/L2.12, L2.15) so developers and
// agents know the full contract lifecycle.
func TestL2_10_SkillCoversAllClosePathsInSummary(t *testing.T) {
	content := readSkill(t)

	// Must mention /exit (L2.11).
	if !strings.Contains(content, "/exit") {
		t.Error("L2.10: SKILL.md must document the /exit auto-close path")
	}

	// Must mention /quit (L2.12).
	if !strings.Contains(content, "/quit") {
		t.Error("L2.10: SKILL.md must document the /quit auto-close path")
	}

	// Must mention /bye (L2.12).
	if !strings.Contains(content, "/bye") {
		t.Error("L2.10: SKILL.md must document the /bye auto-close path")
	}

	// Must mention the direct /close-contract escape hatch (L2.15).
	if !strings.Contains(content, "Direct") && !strings.Contains(content, "direct") && !strings.Contains(content, "escape") {
		t.Error("L2.10: SKILL.md must document the direct /close-contract bypass path (L2.15)")
	}
}
