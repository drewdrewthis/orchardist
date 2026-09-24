package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/drewdrewthis/orchardist/internal/release"
)

// TestRun covers the four valid set names end-to-end through run(), plus the
// error paths (bogus set name, no args), asserting exact stdout and exit code
// so a future set added to release.SetsByName is caught if this table isn't
// updated alongside it.
func TestRun(t *testing.T) {
	for _, set := range release.SetNames() {
		t.Run(set, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{set}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("run(%q) exit = %d; want 0 (stderr: %s)", set, code, stderr.String())
			}
			want := strings.Join(release.SetsByName[set], "\n")
			if len(release.SetsByName[set]) > 0 {
				want += "\n"
			}
			if got := stdout.String(); got != want {
				t.Errorf("run(%q) stdout = %q; want %q", set, got, want)
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%q) stderr = %q; want empty", set, stderr.String())
			}
		})
	}

	t.Run("bogus set name", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"bogus"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("run(bogus) exit = %d; want 2", code)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(bogus) stdout = %q; want empty", stdout.String())
		}
		for _, name := range release.SetNames() {
			if !strings.Contains(stderr.String(), name) {
				t.Errorf("run(bogus) stderr %q does not mention valid set %q", stderr.String(), name)
			}
		}
	})

	t.Run("no args", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run(nil, &stdout, &stderr)
		if code != 2 {
			t.Errorf("run(nil) exit = %d; want 2", code)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(nil) stdout = %q; want empty", stdout.String())
		}
		for _, name := range release.SetNames() {
			if !strings.Contains(stderr.String(), name) {
				t.Errorf("run(nil) stderr %q does not mention valid set %q", stderr.String(), name)
			}
		}
	})
}
