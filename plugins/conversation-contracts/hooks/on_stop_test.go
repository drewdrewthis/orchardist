// Tests for plugins/conversation-contracts/hooks/on-stop.sh
//
// Scenarios: L2.4 – L2.8 (issue #650, Layer 2 — Stop hook loose-ends inventory)
//
// L2.4 — Stop hook surfaces the loose-ends inventory when the conversation
//         contract is open.
// L2.5 — Stop hook excludes the conversation contract itself from the
//         open-child-contracts inventory.
// L2.6 — Inventory does not run a regex `?` heuristic over user messages.
// L2.7 — Inventory includes only hard signals (child contracts and TodoWrite).
// L2.8 — Inventory degrades to open-child-contracts only when TodoWrite
//         extraction is unavailable.
//
// All tests exec the actual on-stop.sh script with a stubbed HTTP daemon (net/http
// test server) and injected env vars — no subprocess mocking frameworks needed.
package hooks_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- constants ---------------------------------------------------------------

const convContractDeliverable = "user agrees conversation has come to a close and there are no loose ends"

// ---- helpers -----------------------------------------------------------------

// hooksDir returns the absolute path to the hooks directory under the plugin.
func hooksDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

// scriptPath returns the absolute path to on-stop.sh.
func scriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(hooksDir(t), "on-stop.sh")
}

// hookPayload constructs a Stop hook JSON payload.
func hookPayload(sessionID string) string {
	return fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q}`, sessionID)
}

// runStopHook executes on-stop.sh with the given env overrides and stdin.
// Returns stdout and stderr output.
func runStopHook(t *testing.T, env []string, payload string) (string, string, error) {
	t.Helper()
	script := scriptPath(t)

	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = env

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// stubDaemon creates an httptest.Server that responds to POST /graphql with
// the given handler.  Returns the server and its base URL.
func stubDaemon(t *testing.T, handler func(query string) interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result := handler(body.Query)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// baseEnv returns the minimal env the script needs to run (PATH, HOME, PWD).
// Additional entries from extra are appended.
func baseEnv(t *testing.T, tmpDir, daemonURL string, extra ...string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmpDir,
		"PWD=" + tmpDir,
		"ORCHARD_DAEMON_URL=" + daemonURL,
	}
	env = append(env, extra...)
	return env
}

// writeSessionJsonl writes a session jsonl file with the given lines.
func writeSessionJsonl(t *testing.T, dir, sessionID string, lines []string) string {
	t.Helper()
	// The script uses _encode_cwd to derive the project dir from PWD.
	// Since we set PWD=dir, encoded = dir with '/' → '-' and '.' → '-'.
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(dir)
	projectDir := filepath.Join(dir, ".claude", "projects", encoded)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", projectDir, err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	return jsonlPath
}

// graphqlContractsResponse builds a fake GraphQL response for contracts.
func graphqlContractsResponse(contracts []map[string]string) map[string]interface{} {
	items := make([]interface{}, 0, len(contracts))
	for _, c := range contracts {
		items = append(items, map[string]interface{}{
			"contractId": c["contractId"],
			"statement":  c["statement"],
		})
	}
	return map[string]interface{}{
		"data": map[string]interface{}{
			"contracts": items,
		},
	}
}

// ---- L2.4 — Inventory surfaces child contracts and TodoWrite -----------------

// TestL2_4_InventorySurfacesChildContractsAndTodoWrite verifies that when the
// session has an open child contract and an open TodoWrite item, the stop hook
// emits a systemMessage that lists both.
func TestL2_4_InventorySurfacesChildContractsAndTodoWrite(t *testing.T) {
	const sessionID = "S-STOP-001"
	const childContractID = "C-CHILD-001"
	const todoText = "fix flaky test"

	tmpDir := t.TempDir()

	// Write a session jsonl with one open TodoWrite item.
	todoRecord := fmt.Sprintf(
		`{"type":"assistant","uuid":"u1","timestamp":"2026-05-21T12:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"TodoWrite","input":{"todos":[{"content":%q,"status":"pending"}]}}]}}`,
		todoText,
	)
	writeSessionJsonl(t, tmpDir, sessionID, []string{todoRecord})

	// Daemon returns one child contract (not the conversation contract).
	srv := stubDaemon(t, func(_ string) interface{} {
		return graphqlContractsResponse([]map[string]string{
			{"contractId": childContractID, "statement": "implement the widget"},
		})
	})

	env := baseEnv(t, tmpDir, srv.URL)
	out, _, err := runStopHook(t, env, hookPayload(sessionID))
	if err != nil {
		// Exit code 0 is expected; non-zero suggests a script error.
		t.Logf("stderr not captured separately; exit error: %v", err)
	}

	if !strings.Contains(out, childContractID) {
		t.Errorf("L2.4: expected systemMessage to contain child contract ID %q; got:\n%s", childContractID, out)
	}
	if !strings.Contains(out, todoText) {
		t.Errorf("L2.4: expected systemMessage to contain TodoWrite item %q; got:\n%s", todoText, out)
	}

	// Response must be valid JSON with continue:true.
	var resp struct {
		Continue      bool   `json:"continue"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("L2.4: hook output is not valid JSON: %v\noutput: %s", err, out)
	}
	if !resp.Continue {
		t.Error("L2.4: hook must always return continue:true")
	}
	if resp.SystemMessage == "" {
		t.Error("L2.4: systemMessage must not be empty when inventory is non-empty")
	}
}

// ---- L2.5 — Conversation contract excluded from child inventory --------------

// TestL2_5_ConversationContractExcludedFromInventory verifies that when the
// only open contract for the session IS the conversation contract itself, the
// "open child contracts" section is empty.
func TestL2_5_ConversationContractExcludedFromInventory(t *testing.T) {
	const sessionID = "S-STOP-002"

	tmpDir := t.TempDir()
	// No TodoWrite records.
	writeSessionJsonl(t, tmpDir, sessionID, []string{})

	// Daemon returns only the conversation contract (should be excluded).
	srv := stubDaemon(t, func(_ string) interface{} {
		return graphqlContractsResponse([]map[string]string{
			{"contractId": "C-2026-05-21-CONVXXX", "statement": convContractDeliverable},
		})
	})

	env := baseEnv(t, tmpDir, srv.URL)
	out, _, _ := runStopHook(t, env, hookPayload(sessionID))

	// When inventory is empty, we expect no systemMessage (just continue:true).
	var resp struct {
		Continue      bool   `json:"continue"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("L2.5: hook output is not valid JSON: %v\noutput: %s", err, out)
	}
	if !resp.Continue {
		t.Error("L2.5: hook must always return continue:true")
	}
	// systemMessage must either be absent or not mention the conversation contract.
	if strings.Contains(resp.SystemMessage, convContractDeliverable) {
		t.Errorf("L2.5: conversation contract deliverable must not appear in inventory;\ngot systemMessage: %s", resp.SystemMessage)
	}
}

// ---- L2.6 — No regex `?` heuristic ------------------------------------------

// TestL2_6_NoQuestionMarkRegexInScript verifies that the on-stop.sh script
// does not contain any regex pattern matching "?" against user message bodies.
// This is a code-search test: we read the script source and assert the
// character "?" does not appear in a regex or pattern context.
func TestL2_6_NoQuestionMarkRegexInScript(t *testing.T) {
	script := scriptPath(t)
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("L2.6: read script %s: %v", script, err)
	}
	content := string(raw)

	// Assert no grep/awk/sed/python regex matching literal "?" against
	// message bodies.  The simplest proxy: the script must not use common
	// regex operators containing "?" in a context that looks like user
	// message scanning.  We look for the pattern indicators that would
	// accompany such heuristics.
	//
	// Prohibited patterns:
	//   grep.*\?              — grepping for question marks
	//   match.*\?.*message    — regex match on message text
	//   re\.search.*\?        — Python regex search for "?"
	//   \[[\?\*]\]            — shell glob/regex with ?
	//
	// We keep this test simple and non-brittle: assert the script does NOT
	// contain any of the specific strings that would indicate user-message
	// body scanning via "?".

	prohibitedPhrases := []string{
		`grep.*".*\?.*"`,       // grep "...?..." style
		`re.search.*\?`,        // python regex
		`re.findall.*\?`,       // python regex
		`awk.*\?`,              // awk regex
		`user_messages`,        // scanning user message collection
		`role.*user.*content`,  // extracting user message bodies
		`"role":"user"`,        // parsing user messages by role
		`.role == "user"`,      // jq user message filter
	}

	for _, phrase := range prohibitedPhrases {
		// Use simple substring check; the prohibitedPhrases are literal strings.
		if strings.Contains(content, phrase) {
			t.Errorf("L2.6: script contains prohibited user-message scanning pattern %q", phrase)
		}
	}

	// Explicit check: the string '?"' or "?'" should not appear in a regex
	// context (i.e., not just in jq's optional operator .foo? which is allowed).
	// The jq optional .foo? is legitimate; what's prohibited is grepping/matching
	// literal "?" against message text.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip comments.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Flag grep commands that look for "?" in message text.
		if strings.Contains(trimmed, "grep") && strings.Contains(trimmed, "?") &&
			(strings.Contains(trimmed, "message") || strings.Contains(trimmed, "user") || strings.Contains(trimmed, "content")) {
			t.Errorf("L2.6: line %d looks like grep-based question-mark heuristic: %q", i+1, trimmed)
		}
	}
}

// ---- L2.7 — Only hard signals -----------------------------------------------

// TestL2_7_OnlyHardSignalsInInventory verifies that the on-stop.sh script
// does not reference user message bodies at all, and only uses the two hard
// signal sources: open child contracts (daemon GraphQL) and TodoWrite (session jsonl).
func TestL2_7_OnlyHardSignalsInInventory(t *testing.T) {
	script := scriptPath(t)
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("L2.7: read script %s: %v", script, err)
	}
	content := string(raw)

	// The script must NOT reference user message role scanning.
	userMessageIndicators := []string{
		`"role":"user"`,
		`.role == "user"`,
		`role.*==.*user`,
		`unanswered`,
		`user_questions`,
		`user messages`,
	}
	for _, indicator := range userMessageIndicators {
		if strings.Contains(content, indicator) {
			t.Errorf("L2.7: script references user message bodies via %q", indicator)
		}
	}

	// The script MUST reference the daemon GraphQL endpoint (open child contracts).
	if !strings.Contains(content, "graphql") && !strings.Contains(content, "DAEMON_URL") {
		t.Error("L2.7: script must query daemon GraphQL for open child contracts")
	}

	// The script MUST reference TodoWrite as a source.
	if !strings.Contains(content, "TodoWrite") {
		t.Error("L2.7: script must reference TodoWrite as a source for open items")
	}
}

// ---- L2.8 — Degrades gracefully when TodoWrite extraction unavailable --------

// TestL2_8_DegradesToContractsWhenTodoUnavailable verifies that when the
// session jsonl has no TodoWrite records (or the jsonl doesn't exist), the
// hook still surfaces open child contracts and does not fail.
func TestL2_8_DegradesToContractsWhenTodoUnavailable(t *testing.T) {
	const sessionID = "S-INV-003"
	const childContractID = "C-2026-05-21-CHILDABC"

	tmpDir := t.TempDir()
	// No session jsonl written — simulates "no stop_hook_summary records".

	// Daemon returns one open child contract.
	srv := stubDaemon(t, func(_ string) interface{} {
		return graphqlContractsResponse([]map[string]string{
			{"contractId": childContractID, "statement": "fix the flaky login test"},
		})
	})

	env := baseEnv(t, tmpDir, srv.URL)
	out, _, err := runStopHook(t, env, hookPayload(sessionID))

	// Script must not fail hard (bash -e would have exited non-zero).
	// We accept err here because the script uses set -uo pipefail and may
	// exit non-zero if jq is absent; but on any reasonable dev machine jq
	// is available.  If it does exit non-zero, output is likely empty and
	// we flag that.
	if err != nil {
		t.Logf("L2.8: script exited non-zero (err=%v); output:\n%s", err, out)
	}

	// Parse the output as JSON — the hook must emit valid JSON.
	// If the output is empty the script did nothing, which is also acceptable
	// only if it exited 0.
	if strings.TrimSpace(out) == "" {
		if err != nil {
			t.Fatal("L2.8: script produced no output and exited non-zero — hook failed")
		}
		// Exited 0, empty output: the daemon was unreachable and degraded.
		// This is acceptable for L2.8 only if no jsonl exists and daemon is down.
		// Since we have a daemon in this test, non-empty output is expected.
		t.Fatal("L2.8: expected hook to emit JSON output with child contracts; got empty output")
	}

	var resp struct {
		Continue      bool   `json:"continue"`
		SystemMessage string `json:"systemMessage"`
	}
	if err2 := json.Unmarshal([]byte(out), &resp); err2 != nil {
		t.Fatalf("L2.8: hook output is not valid JSON: %v\noutput: %s", err2, out)
	}
	if !resp.Continue {
		t.Error("L2.8: hook must always return continue:true")
	}
	if !strings.Contains(resp.SystemMessage, childContractID) {
		t.Errorf("L2.8: expected systemMessage to contain child contract %q;\ngot: %s", childContractID, resp.SystemMessage)
	}
	// TodoWrite section must be absent (no TodoWrite data available).
	if strings.Contains(resp.SystemMessage, "TodoWrite") {
		t.Errorf("L2.8: TodoWrite section should be absent when no todo data; got: %s", resp.SystemMessage)
	}
}
