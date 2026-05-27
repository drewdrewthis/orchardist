// Tests for on-prompt-submit.sh — UserPromptSubmit hook.
//
// The hook auto-opens the conversation contract by appending exactly one
// `orchard_contract` open sentinel (source "auto-prompt-submit") to the session
// jsonl. It is idempotent: repeated prompts in a session yield exactly one
// auto-open sentinel. No MCP server, no resident process.
package hooks_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// promptHookScript returns the absolute path to on-prompt-submit.sh.
func promptHookScript(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p, err := filepath.Abs(filepath.Join(filepath.Dir(testFile), "on-prompt-submit.sh"))
	if err != nil {
		t.Fatalf("abs hook path: %v", err)
	}
	return p
}

// autoOpenSentinel parses each jsonl line as a bare object and returns those
// that are auto-prompt-submit open sentinels.
func autoOpenSentinels(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open jsonl %s: %v", path, err)
	}
	defer f.Close()

	var out []map[string]interface{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["orchard_contract"] == "open" && rec["source"] == "auto-prompt-submit" {
			out = append(out, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan jsonl %s: %v", path, err)
	}
	return out
}

// runPromptHook executes on-prompt-submit.sh with env + cwd. cmd.Dir is set
// because bash overwrites PWD on startup with the process cwd.
func runPromptHook(t *testing.T, env []string, cwd string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", promptHookScript(t))
	cmd.Env = env
	cmd.Dir = cwd
	return cmd.CombinedOutput()
}

// runPromptHookWithPayload executes the hook with a JSON payload on stdin.
func runPromptHookWithPayload(t *testing.T, env []string, cwd, payload string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", promptHookScript(t))
	cmd.Env = env
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(payload)
	return cmd.CombinedOutput()
}

// encodeCwd mirrors the hook's cwd encoding ('/' and '.' → '-').
func encodeCwd(cwd string) string {
	out := make([]byte, len(cwd))
	for i, b := range []byte(cwd) {
		if b == '/' || b == '.' {
			out[i] = '-'
		} else {
			out[i] = b
		}
	}
	return string(out)
}

const wantDeliverable = "user agrees conversation has come to a close and there are no loose ends"

// TestOnPromptSubmit_FirstMessage_WritesOneAutoOpenSentinel asserts that running
// the hook once produces exactly one auto-open sentinel with the fixed
// deliverable as its statement.
func TestOnPromptSubmit_FirstMessage_WritesOneAutoOpenSentinel(t *testing.T) {
	home := t.TempDir()
	sessionID := "S-HOOK-TEST-001"
	cwd := home

	encodedCwd := encodeCwd(cwd)
	projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")

	env := []string{
		"HOME=" + home,
		"PWD=" + cwd,
		"CLAUDE_SESSION_ID=" + sessionID,
		"PATH=" + os.Getenv("PATH"),
	}

	out, err := runPromptHook(t, env, cwd)
	if err != nil {
		t.Fatalf("hook exited non-zero: %v\n%s", err, out)
	}

	recs := autoOpenSentinels(t, jsonlPath)
	if len(recs) != 1 {
		t.Fatalf("want exactly 1 auto-open sentinel; got %d", len(recs))
	}
	if got, _ := recs[0]["statement"].(string); got != wantDeliverable {
		t.Errorf("statement = %q; want %q", got, wantDeliverable)
	}
	if id, _ := recs[0]["id"].(string); !strings.HasPrefix(id, "C-") {
		t.Errorf("id = %q; want a C-<date>-<hex> id", id)
	}
}

// TestOnPromptSubmit_Idempotent asserts running the hook multiple times still
// yields exactly one auto-open sentinel (idempotency replaces the old MCP
// fold-dedup).
func TestOnPromptSubmit_Idempotent(t *testing.T) {
	home := t.TempDir()
	sessionID := "S-HOOK-IDEMPOTENT"
	cwd := home

	encodedCwd := encodeCwd(cwd)
	projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")

	env := []string{
		"HOME=" + home,
		"PWD=" + cwd,
		"CLAUDE_SESSION_ID=" + sessionID,
		"PATH=" + os.Getenv("PATH"),
	}

	for i := 0; i < 3; i++ {
		if out, err := runPromptHook(t, env, cwd); err != nil {
			t.Fatalf("hook run %d exited non-zero: %v\n%s", i, err, out)
		}
	}

	recs := autoOpenSentinels(t, jsonlPath)
	if len(recs) != 1 {
		t.Errorf("want exactly 1 auto-open sentinel after 3 runs; got %d", len(recs))
	}
}

// TestOnPromptSubmit_ReadsSessionIdFromStdinPayload asserts the production
// path: real Claude Code does NOT set CLAUDE_SESSION_ID. It passes a JSON
// payload on stdin with .session_id and .cwd. The hook must derive them and
// write the auto-open sentinel to the correct jsonl.
func TestOnPromptSubmit_ReadsSessionIdFromStdinPayload(t *testing.T) {
	home := t.TempDir()
	sessionID := "S-STDIN-PAYLOAD-001"
	cwd := home

	encodedCwd := encodeCwd(cwd)
	projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")

	// Critically: do NOT set CLAUDE_SESSION_ID — the hook must derive it from
	// the stdin payload, matching real Claude Code behaviour.
	env := []string{
		"HOME=" + home,
		"PWD=" + cwd,
		"PATH=" + os.Getenv("PATH"),
	}
	payload := fmt.Sprintf(
		`{"hook_event_name":"UserPromptSubmit","session_id":%q,"cwd":%q,"prompt":"first message"}`,
		sessionID, cwd,
	)

	out, err := runPromptHookWithPayload(t, env, cwd, payload)
	if err != nil {
		t.Fatalf("hook exited non-zero: %v\n%s", err, out)
	}

	recs := autoOpenSentinels(t, jsonlPath)
	if len(recs) != 1 {
		t.Errorf("want exactly 1 auto-open sentinel; got %d (out=%s)", len(recs), out)
	}
}
