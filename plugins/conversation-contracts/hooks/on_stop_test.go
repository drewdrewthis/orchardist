// Tests for plugins/conversation-contracts/hooks/on-stop.sh
//
// The Stop hook folds the `orchard_contract` open/close sentinels out of the
// session jsonl (via scripts/fold-contracts.sh) and HARD-BLOCKS Stop while any
// contract is open, emitting {"decision":"block","reason":"..."}. When no
// contract is open it emits nothing and exits 0 (Stop allowed).
//
// All tests exec the actual on-stop.sh with injected env vars and a crafted
// session jsonl — no daemon, no network, no mocking framework.
package hooks_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
func runStopHook(t *testing.T, env []string, payload string) (string, string, error) {
	t.Helper()
	script := scriptPath(t)

	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = env

	// Bash overwrites PWD on startup with the process's actual cwd, so we must
	// set cmd.Dir to whatever PWD env entry says. Otherwise the _encode_cwd
	// path resolution in the script mismatches what tests wrote to disk.
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

// baseEnv returns the minimal env the script needs (PATH, HOME, PWD).
func baseEnv(t *testing.T, tmpDir string, extra ...string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmpDir,
		"PWD=" + tmpDir,
	}
	return append(env, extra...)
}

// openSentinelLine builds a session jsonl line carrying an open sentinel inside
// a tool_result content string — the shape a /open-contract Bash echo produces.
func openSentinelLine(id, statement string) string {
	inner := fmt.Sprintf(`{\"orchard_contract\":\"open\",\"id\":\"%s\",\"statement\":\"%s\",\"ts\":\"2026-05-27T10:00:00Z\"}`, id, statement)
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}`, inner)
}

// closeSentinelLine builds a session jsonl line carrying a close sentinel.
func closeSentinelLine(id, reason string) string {
	inner := fmt.Sprintf(`{\"orchard_contract\":\"close\",\"id\":\"%s\",\"reason\":\"%s\",\"ts\":\"2026-05-27T11:00:00Z\"}`, id, reason)
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}`, inner)
}

// writeSessionJsonl writes a session jsonl with the given lines under the
// encoded-cwd path the hook resolves from PWD.
func writeSessionJsonl(t *testing.T, projectsRoot, cwd, sessionID string, lines []string) string {
	t.Helper()
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	projectDir := filepath.Join(projectsRoot, encoded)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", projectDir, err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(jsonlPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	return jsonlPath
}

// parseBlock unmarshals the hook stdout as a decision-block response. ok is
// false when stdout is empty (Stop allowed).
func parseBlock(t *testing.T, out string) (decision, reason string, ok bool) {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return "", "", false
	}
	var resp struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not valid JSON: %v\noutput: %s", err, out)
	}
	return resp.Decision, resp.Reason, true
}

// ---- open with no close → block ---------------------------------------------

func TestOnStop_OpenContract_Blocks(t *testing.T) {
	const sessionID = "S-STOP-OPEN"
	const id = "C-2026-05-27-aaaa1111"
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, ".claude", "projects")
	writeSessionJsonl(t, root, tmpDir, sessionID, []string{
		openSentinelLine(id, "ship the X refactor"),
	})

	out, _, _ := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	decision, reason, ok := parseBlock(t, out)
	if !ok {
		t.Fatalf("expected a block response; got empty output")
	}
	if decision != "block" {
		t.Errorf("decision = %q; want %q", decision, "block")
	}
	if !strings.Contains(reason, id) {
		t.Errorf("block reason must list the open contract id %q; got:\n%s", id, reason)
	}
	// The reason must be self-documenting (discovery surface): name the verbs.
	if !strings.Contains(reason, "/close-contract") {
		t.Errorf("block reason must tell the agent to /close-contract; got:\n%s", reason)
	}
	if !strings.Contains(reason, "/my-contracts") {
		t.Errorf("block reason must mention /my-contracts; got:\n%s", reason)
	}
}

// ---- open + matching close → allow ------------------------------------------

func TestOnStop_OpenThenClose_Allows(t *testing.T) {
	const sessionID = "S-STOP-CLOSED"
	const id = "C-2026-05-27-bbbb2222"
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, ".claude", "projects")
	writeSessionJsonl(t, root, tmpDir, sessionID, []string{
		openSentinelLine(id, "do the thing"),
		closeSentinelLine(id, "delivered: done"),
	})

	out, _, err := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	if err != nil {
		t.Logf("exit: %v", err)
	}
	if _, _, ok := parseBlock(t, out); ok {
		t.Errorf("a closed contract must not block Stop; got output:\n%s", out)
	}
}

// ---- two opens, one closed → block lists only the still-open one ------------

func TestOnStop_PartialClose_ListsOnlyOpen(t *testing.T) {
	const sessionID = "S-STOP-PARTIAL"
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, ".claude", "projects")
	writeSessionJsonl(t, root, tmpDir, sessionID, []string{
		openSentinelLine("C-FIRST", "first task"),
		openSentinelLine("C-SECOND", "second task"),
		closeSentinelLine("C-FIRST", "delivered"),
	})

	out, _, _ := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	_, reason, ok := parseBlock(t, out)
	if !ok {
		t.Fatalf("expected a block (C-SECOND still open); got empty output")
	}
	if !strings.Contains(reason, "C-SECOND") {
		t.Errorf("block reason must list the open C-SECOND; got:\n%s", reason)
	}
	if strings.Contains(reason, "C-FIRST") {
		t.Errorf("block reason must NOT list the closed C-FIRST; got:\n%s", reason)
	}
}

// ---- auto-open conversation contract + skill-open both block (uniform fold) --

func TestOnStop_AutoOpenAndSkillOpen_BothBlock(t *testing.T) {
	const sessionID = "S-STOP-BOTH"
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, ".claude", "projects")
	// Auto-open sentinel is a bare self-contained line (as on-prompt-submit
	// appends it); the skill-open is nested in a tool_result content string.
	autoLine := fmt.Sprintf(
		`{"orchard_contract":"open","id":"C-CONV-1","statement":%q,"ts":"t","source":"auto-prompt-submit"}`,
		convContractDeliverable,
	)
	writeSessionJsonl(t, root, tmpDir, sessionID, []string{
		autoLine,
		openSentinelLine("C-SKILL-1", "a sub commitment"),
	})

	out, _, _ := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	_, reason, ok := parseBlock(t, out)
	if !ok {
		t.Fatalf("expected a block (two open contracts); got empty output")
	}
	if !strings.Contains(reason, "C-CONV-1") {
		t.Errorf("block reason must list the auto-opened conversation contract; got:\n%s", reason)
	}
	if !strings.Contains(reason, "C-SKILL-1") {
		t.Errorf("block reason must list the skill-opened contract; got:\n%s", reason)
	}
}

// ---- no sentinels / missing jsonl → allow, exit 0 ---------------------------

func TestOnStop_NoContracts_Allows(t *testing.T) {
	const sessionID = "S-STOP-NONE"
	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, ".claude", "projects")
	writeSessionJsonl(t, root, tmpDir, sessionID, []string{
		`{"type":"user","message":{"role":"user","content":"just a normal message, no contracts"}}`,
	})

	out, _, err := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	if err != nil {
		t.Logf("exit: %v", err)
	}
	if _, _, ok := parseBlock(t, out); ok {
		t.Errorf("no open contracts must allow Stop; got output:\n%s", out)
	}
}

func TestOnStop_MissingJsonl_Allows(t *testing.T) {
	const sessionID = "S-STOP-MISSING"
	tmpDir := t.TempDir()
	// No jsonl written at all.
	out, _, err := runStopHook(t, baseEnv(t, tmpDir), hookPayload(sessionID))
	if err != nil {
		t.Logf("exit: %v", err)
	}
	if _, _, ok := parseBlock(t, out); ok {
		t.Errorf("missing jsonl must allow Stop (degrade gracefully); got output:\n%s", out)
	}
}

// ---- CLAUDE_PROJECTS_DIR override -------------------------------------------

// TestOnStop_HonorsCLAUDEProjectsDirOverride locks in compatibility with the
// CLAUDE_PROJECTS_DIR override: when set, the hook must read the session jsonl
// from <override>/<encoded-cwd>/<sid>.jsonl rather than $HOME/.claude/projects/.
func TestOnStop_HonorsCLAUDEProjectsDirOverride(t *testing.T) {
	const sessionID = "S-PROJECTS-DIR-OVR"
	const id = "C-OVERRIDE-1"

	tmpDir := t.TempDir()
	projectsRoot := filepath.Join(tmpDir, "custom", "projects")
	cwd := filepath.Join(tmpDir, "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	writeSessionJsonl(t, projectsRoot, cwd, sessionID, []string{
		openSentinelLine(id, "verify override path"),
	})

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + tmpDir,
		"PWD=" + cwd,
		"CLAUDE_PROJECTS_DIR=" + projectsRoot,
	}
	out, _, err := runStopHook(t, env, hookPayload(sessionID))
	if err != nil {
		t.Logf("exit: %v", err)
	}
	_, reason, ok := parseBlock(t, out)
	if !ok {
		t.Fatalf("expected a block reading the override projects dir; got empty output")
	}
	if !strings.Contains(reason, id) {
		t.Errorf("expected block to surface contract %q from override projects dir; got:\n%s", id, reason)
	}
}
