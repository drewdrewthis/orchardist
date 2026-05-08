package orchpaths_test

import (
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
