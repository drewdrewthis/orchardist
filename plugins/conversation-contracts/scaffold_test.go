// Tests for the conversation-contracts marketplace + plugin scaffold
// (issue #650, Layer 1 — Scenario: "Marketplace and plugin scaffold load cleanly").
//
// These tests verify that the three JSON files required by the Claude Code
// plugin system are present, are valid JSON, and carry the fields asserted in
// the feature spec:
//
//   .claude-plugin/marketplace.json
//     - name == "orchard"
//     - exactly one plugin entry
//     - that entry's source path == "plugins/conversation-contracts"
//
//   plugins/conversation-contracts/.claude-plugin/plugin.json
//     - name, description, version ("0.8.0"), author all present
//
//   plugins/conversation-contracts/hooks/hooks.json
//     - UserPromptSubmit and Stop hook keys present
package conversation_contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot returns the absolute path of the git-orchard-rs worktree root by
// walking up from this test file's directory until a go.mod is found.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}

// TestMarketplaceJSON_ValidAndNamedOrchard asserts that
// .claude-plugin/marketplace.json is valid JSON with name "orchard" and
// exactly one plugin entry whose source path points at
// "plugins/conversation-contracts".
func TestMarketplaceJSON_ValidAndNamedOrchard(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, ".claude-plugin", "marketplace.json")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				// Support both flat string and object forms.
				// When Source is a JSON object, Path carries the plugin directory.
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("marketplace.json is not valid JSON: %v", err)
	}

	if m.Name != "orchard" {
		t.Errorf("marketplace name = %q; want %q", m.Name, "orchard")
	}

	// After collapsing to a single plugin, the marketplace hosts exactly one
	// entry: conversation-contracts at the expected path. The legacy
	// claude-contracts plugin was removed.
	if len(m.Plugins) != 1 {
		names := make([]string, 0, len(m.Plugins))
		for _, p := range m.Plugins {
			names = append(names, p.Name)
		}
		t.Fatalf("marketplace must host exactly one plugin; have %d: %v", len(m.Plugins), names)
	}
	if m.Plugins[0].Name != "conversation-contracts" {
		t.Errorf("plugin name = %q; want %q", m.Plugins[0].Name, "conversation-contracts")
	}
	if m.Plugins[0].Source.Path != "plugins/conversation-contracts" {
		t.Errorf("plugin source.path = %q; want %q",
			m.Plugins[0].Source.Path, "plugins/conversation-contracts")
	}
}

// TestPluginJSON_ValidWithExpectedFields asserts that
// plugins/conversation-contracts/.claude-plugin/plugin.json is valid JSON with
// the required fields: name, description, version "0.8.0", and author.
func TestPluginJSON_ValidWithExpectedFields(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "plugins", "conversation-contracts", ".claude-plugin", "plugin.json")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var p struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Author      *struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("plugin.json is not valid JSON: %v", err)
	}

	if p.Name == "" {
		t.Error("plugin.json: name is empty")
	}
	if p.Description == "" {
		t.Error("plugin.json: description is empty")
	}
	if p.Version != "0.9.0" {
		t.Errorf("plugin.json: version = %q; want %q", p.Version, "0.9.0")
	}
	if p.Author == nil || p.Author.Name == "" {
		t.Error("plugin.json: author.name is missing or empty")
	}
}

// TestHooksJSON_ValidWithRequiredHooks asserts that
// plugins/conversation-contracts/hooks/hooks.json is valid JSON and declares
// both UserPromptSubmit and Stop hook entries.
func TestHooksJSON_ValidWithRequiredHooks(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "plugins", "conversation-contracts", "hooks", "hooks.json")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var h struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}

	for _, key := range []string{"UserPromptSubmit", "Stop"} {
		if _, ok := h.Hooks[key]; !ok {
			t.Errorf("hooks.json: missing hook key %q", key)
		}
	}
}
