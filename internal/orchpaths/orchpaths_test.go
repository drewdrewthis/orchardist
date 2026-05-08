package orchpaths_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drewdrewthis/git-orchard-rs/internal/orchpaths"
)

// TestConfigDir_ReturnsDotOrchard verifies that ConfigDir returns $HOME/.orchard,
// matching the dotdir convention used by ~/.aws, ~/.kube, ~/.ssh, etc.
func TestConfigDir_ReturnsDotOrchard(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-orchpaths")
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := orchpaths.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() unexpected error: %v", err)
	}
	want := "/tmp/test-home-orchpaths/.orchard"
	if got != want {
		t.Errorf("ConfigDir() = %q; want %q", got, want)
	}
}

// TestConfigFile_ReturnsDotOrchardConfigJson verifies that ConfigFile returns
// $HOME/.orchard/config.json.
func TestConfigFile_ReturnsDotOrchardConfigJson(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-orchpaths")
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := orchpaths.ConfigFile()
	if err != nil {
		t.Fatalf("ConfigFile() unexpected error: %v", err)
	}
	want := "/tmp/test-home-orchpaths/.orchard/config.json"
	if got != want {
		t.Errorf("ConfigFile() = %q; want %q", got, want)
	}
}

// TestConfigDir_IgnoresXDGConfigHome verifies that XDG_CONFIG_HOME has no effect
// on ConfigDir — orchard uses the dotdir convention unconditionally.
func TestConfigDir_IgnoresXDGConfigHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-orchpaths")
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg/sentinel")

	got, err := orchpaths.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() unexpected error: %v", err)
	}
	if strings.Contains(got, "/custom/xdg/sentinel") {
		t.Errorf("ConfigDir() = %q; should not contain XDG_CONFIG_HOME sentinel", got)
	}
	if strings.Contains(got, ".config/orchard") {
		t.Errorf("ConfigDir() = %q; should not contain legacy .config/orchard path", got)
	}
	want := "/tmp/test-home-orchpaths/.orchard"
	if got != want {
		t.Errorf("ConfigDir() = %q; want %q", got, want)
	}
}

// TestStateDir_RemainsLocalStateOrchard is an out-of-scope guard: the state
// directory must not be moved as part of the config-location change.
func TestStateDir_RemainsLocalStateOrchard(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-orchpaths")
	t.Setenv("XDG_STATE_HOME", "")

	got, err := orchpaths.StateDir()
	if err != nil {
		t.Fatalf("StateDir() unexpected error: %v", err)
	}
	want := "/tmp/test-home-orchpaths/.local/state/orchard"
	if got != want {
		t.Errorf("StateDir() = %q; want %q", got, want)
	}
}

// TestLegacyConfigFile_ReturnsLegacyPath verifies that LegacyConfigFile returns
// the expected path "$HOME/.config/orchard/config.json".
func TestLegacyConfigFile_ReturnsLegacyPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-legacy")

	path, _, err := orchpaths.LegacyConfigFile()
	if err != nil {
		// A stat error on a non-existent path is expected — the path is still returned.
		// Only fail if home resolution errored.
	}
	_ = err
	want := "/tmp/test-home-legacy/.config/orchard/config.json"
	if path != want {
		t.Errorf("LegacyConfigFile() path = %q; want %q", path, want)
	}
}

// TestLegacyConfigFile_DetectsExistence verifies that LegacyConfigFile correctly
// reports whether the legacy file exists.
func TestLegacyConfigFile_DetectsExistence(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		_, exists, err := orchpaths.LegacyConfigFile()
		if err != nil {
			t.Fatalf("LegacyConfigFile() unexpected error: %v", err)
		}
		if exists {
			t.Error("LegacyConfigFile() exists = true; want false when file is absent")
		}
	})

	t.Run("present", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)

		legacyDir := filepath.Join(home, ".config", "orchard")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		_, exists, err := orchpaths.LegacyConfigFile()
		if err != nil {
			t.Fatalf("LegacyConfigFile() unexpected error: %v", err)
		}
		if !exists {
			t.Error("LegacyConfigFile() exists = false; want true when file is present")
		}
	})
}

// TestMigrationHintMessage_ContainsRequiredStrings verifies that the hint text
// produced by MigrationHintMessage contains all required substrings.
func TestMigrationHintMessage_ContainsRequiredStrings(t *testing.T) {
	legacyPath := "~/.config/orchard/config.json"
	newPath := "~/.orchard/config.json"
	msg := orchpaths.MigrationHintMessage(legacyPath, newPath)

	required := []string{
		"Found legacy config at ~/.config/orchard/config.json",
		"mv ~/.config/orchard ~/.orchard",
		"~/.orchard/config.json",
	}
	for _, want := range required {
		if !strings.Contains(msg, want) {
			t.Errorf("MigrationHintMessage() = %q; must contain %q", msg, want)
		}
	}
}
