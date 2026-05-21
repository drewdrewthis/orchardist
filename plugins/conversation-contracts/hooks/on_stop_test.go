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

	// Bash overwrites PWD on startup with the process's actual cwd, so we
	// must set cmd.Dir to whatever PWD env entry says. Otherwise the
	// _encode_cwd path resolution in the script will mismatch what tests
	// wrote to disk under tmpDir/.claude/projects/<encoded-cwd>/.
	for _, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			cmd.Dir = strings.TrimPrefix(e, "PWD=")
			break
		}
	}

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

// TestL2_6_QuestionMarksInUserMessagesDoNotProduceInventory verifies the AC
// behaviourally: when a session jsonl contains user messages with `?`
// characters but no open child contracts and no open TodoWrite items, the
// stop hook emits the empty-inventory response. The hook must derive
// inventory only from hard signals — never from question-mark-laden user
// message bodies.
func TestL2_6_QuestionMarksInUserMessagesDoNotProduceInventory(t *testing.T) {
	const sessionID = "S-STOP-L26"

	tmpDir := t.TempDir()

	// Session jsonl: user messages containing `?` characters and a TodoWrite
	// with every item completed. No remaining hard signals.
	lines := []string{
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-05-21T12:00:00Z","message":{"role":"user","content":"What about the cache invalidation? And the retry policy? Did we ever decide?"}}`,
		`{"type":"user","sessionId":"` + sessionID + `","timestamp":"2026-05-21T12:01:00Z","message":{"role":"user","content":"Is this safe under concurrent writes?"}}`,
		`{"type":"assistant","uuid":"u1","sessionId":"` + sessionID + `","timestamp":"2026-05-21T12:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"TodoWrite","input":{"todos":[{"content":"already done","status":"completed"}]}}]}}`,
	}
	writeSessionJsonl(t, tmpDir, sessionID, lines)

	// Daemon returns no open child contracts.
	srv := stubDaemon(t, func(_ string) interface{} {
		return graphqlContractsResponse([]map[string]string{})
	})

	env := baseEnv(t, tmpDir, srv.URL)
	out, _, err := runStopHook(t, env, hookPayload(sessionID))
	if err != nil {
		t.Logf("exit error: %v", err)
	}

	// AC: empty inventory → response is `{"continue":true}` with NO systemMessage.
	var resp struct {
		Continue      bool   `json:"continue"`
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("L2.6: hook output is not valid JSON: %v\noutput: %s", err, out)
	}
	if !resp.Continue {
		t.Error("L2.6: hook must return continue:true")
	}
	if resp.SystemMessage != "" {
		t.Errorf("L2.6: question marks must NOT produce inventory; got systemMessage = %q", resp.SystemMessage)
	}

	// Defensive: the literal "unanswered" must never appear in output. The
	// hook should not even mention the concept since there's no `?` heuristic.
	if strings.Contains(strings.ToLower(out), "unanswered") {
		t.Errorf("L2.6: output contains 'unanswered' — question-mark heuristic leaked into inventory: %s", out)
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

// ---- CLAUDE_PROJECTS_DIR override ------------------------------------------

// TestOnStop_HonorsCLAUDEProjectsDirOverride locks in compatibility with the
// provider-side CLAUDE_PROJECTS_DIR override: when the env var is set, the
// hook must read the session jsonl from <override>/<encoded-cwd>/<sid>.jsonl
// rather than the default $HOME/.claude/projects/ tree.
func TestOnStop_HonorsCLAUDEProjectsDirOverride(t *testing.T) {
	const sessionID = "S-PROJECTS-DIR-OVR"
	const todoText = "verify override path"

	tmpDir := t.TempDir()
	// Override projects root sits OUTSIDE the default HOME tree so the
	// default path resolution would miss the jsonl entirely.
	projectsRoot := filepath.Join(tmpDir, "custom", "projects")
	cwd := filepath.Join(tmpDir, "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	// Layout: <projectsRoot>/<encoded-cwd>/<sid>.jsonl
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	projectDir := filepath.Join(projectsRoot, encoded)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}
	todoRecord := fmt.Sprintf(
		`{"type":"assistant","uuid":"u1","timestamp":"2026-05-21T12:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"TodoWrite","input":{"todos":[{"content":%q,"status":"pending"}]}}]}}`,
		todoText,
	)
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(todoRecord+"\n"), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// Daemon returns no child contracts; the inventory must come from TodoWrite.
	srv := stubDaemon(t, func(_ string) interface{} {
		return graphqlContractsResponse(nil)
	})

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmpDir,
		"PWD=" + cwd,
		"ORCHARD_DAEMON_URL=" + srv.URL,
		"CLAUDE_PROJECTS_DIR=" + projectsRoot,
	}
	out, _, err := runStopHook(t, env, hookPayload(sessionID))
	if err != nil {
		t.Logf("stop hook exit: %v\noutput:\n%s", err, out)
	}

	if !strings.Contains(out, todoText) {
		t.Errorf("expected hook output to surface TodoWrite item %q from override projects dir;\ngot: %s", todoText, out)
	}
}
