// Tests for on-prompt-submit.sh — UserPromptSubmit hook.
//
// Scenarios verified:
//
//   L2.1: First user message — hook fires and writes exactly one
//   open_contract tool_use event to the session jsonl.
//
//   L2.3: Plugin writes no state to ${CLAUDE_PLUGIN_DATA}.
//   After the hook runs, the conversation-contracts/ subdirectory under
//   CLAUDE_PLUGIN_DATA remains absent or empty.
package hooks_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildMCPBinary compiles the conversation-contracts MCP binary into a
// temporary directory and returns its path. The binary is the real
// MCP server; the test supplies a synthetic session jsonl path via env.
func buildMCPBinary(t *testing.T) string {
	t.Helper()

	// Locate mcp/main.go relative to this test file.
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// hooks/ is one level above mcp/.
	mcpDir := filepath.Join(filepath.Dir(testFile), "..", "mcp")
	mcpDir, err := filepath.Abs(mcpDir)
	if err != nil {
		t.Fatalf("abs mcp dir: %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "conversation-contracts-mcp")

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = mcpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build MCP binary: %v\n%s", err, out)
	}
	return binPath
}

// hookScript returns the absolute path to on-prompt-submit.sh.
func hookScript(t *testing.T) string {
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

// readJSONLToolUseEvents reads all lines from path and returns the
// assistant records whose content contains a tool_use block named toolName.
func readJSONLToolUseEvents(t *testing.T, path string, toolName string) []map[string]interface{} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open jsonl %s: %v", path, err)
	}
	defer f.Close()

	var out []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
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

// runHook executes on-prompt-submit.sh with the given environment overrides.
// Returns the command's combined output and any exit error.
func runHook(t *testing.T, env []string) ([]byte, error) {
	t.Helper()
	script := hookScript(t)
	cmd := exec.Command("bash", script)
	cmd.Env = env
	return cmd.CombinedOutput()
}

// TestOnPromptSubmit_FirstMessage_WritesOneToolUseEvent asserts that
// running the hook once produces exactly one open_contract tool_use
// event in the session jsonl (L2.1).
func TestOnPromptSubmit_FirstMessage_WritesOneToolUseEvent(t *testing.T) {
	mcpBin := buildMCPBinary(t)

	home := t.TempDir()
	sessionID := "S-HOOK-TEST-001"
	cwd := home // simplest encoded cwd is just home itself

	// Claude Code encodes cwd by replacing '/' with '-' and '.' with '-'.
	// For a temp dir like /tmp/TestXxx123, encoded = -tmp-TestXxx123.
	encodedCwd := encodeCwd(cwd)
	projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")

	// CLAUDE_PLUGIN_DATA is an isolated temp dir (L2.3 — nothing written there).
	pluginData := t.TempDir()

	env := []string{
		"HOME=" + home,
		"PWD=" + cwd,
		"CLAUDE_SESSION_ID=" + sessionID,
		"CONTRACTS_MCP_BIN=" + mcpBin,
		"CLAUDE_PLUGIN_DATA=" + pluginData,
		// PATH must include system tools (bash, python3, etc.).
		"PATH=" + os.Getenv("PATH"),
	}

	out, err := runHook(t, env)
	if err != nil {
		t.Fatalf("hook exited non-zero: %v\n%s", err, out)
	}

	recs := readJSONLToolUseEvents(t, jsonlPath, "open_contract")
	if len(recs) != 1 {
		t.Errorf("want exactly 1 open_contract tool_use event; got %d", len(recs))
	}

	if len(recs) > 0 {
		msg := recs[0]["message"].(map[string]interface{})
		contents := msg["content"].([]interface{})
		for _, c := range contents {
			cm := c.(map[string]interface{})
			if cm["type"] != "tool_use" {
				continue
			}
			inp, ok := cm["input"].(map[string]interface{})
			if !ok {
				t.Fatal("tool_use input is not an object")
			}
			deliverable, _ := inp["deliverable"].(string)
			const want = "user agrees conversation has come to a close and there are no loose ends"
			if deliverable != want {
				t.Errorf("deliverable = %q; want %q", deliverable, want)
			}
		}
	}
}

// TestOnPromptSubmit_NoStateWrittenToPluginData asserts that running the
// hook does not create any files under ${CLAUDE_PLUGIN_DATA}/conversation-contracts/
// (L2.3). Idempotency is derived entirely from the ContractFold.
func TestOnPromptSubmit_NoStateWrittenToPluginData(t *testing.T) {
	mcpBin := buildMCPBinary(t)

	home := t.TempDir()
	sessionID := "S-HOOK-TEST-L23"
	cwd := home

	encodedCwd := encodeCwd(cwd)
	projectDir := filepath.Join(home, ".claude", "projects", encodedCwd)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	pluginData := t.TempDir()

	env := []string{
		"HOME=" + home,
		"PWD=" + cwd,
		"CLAUDE_SESSION_ID=" + sessionID,
		"CONTRACTS_MCP_BIN=" + mcpBin,
		"CLAUDE_PLUGIN_DATA=" + pluginData,
		"PATH=" + os.Getenv("PATH"),
	}

	// Run the hook multiple times (simulating multiple prompts).
	for i := 0; i < 3; i++ {
		out, err := runHook(t, env)
		if err != nil {
			t.Fatalf("hook run %d exited non-zero: %v\n%s", i, err, out)
		}
	}

	// Assert the plugin-data directory for conversation-contracts is empty.
	ccDir := filepath.Join(pluginData, "conversation-contracts")
	entries, err := os.ReadDir(ccDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Perfect — directory was never created.
			return
		}
		t.Fatalf("ReadDir %s: %v", ccDir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("want empty ${CLAUDE_PLUGIN_DATA}/conversation-contracts/; got files: %v", names)
	}
}

// encodeCwd mirrors the MCP server's cwd encoding so the test can
// construct the expected jsonl path without importing the MCP package
// (which is a main package and therefore not importable).
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
