package tmux

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

// TestRelevantSocketEvent_MatchesDefaultSocketBasename verifies that an event
// on a file named "default" is considered relevant when the adapter uses the
// default tmux socket (no -S flag). The tmux daemon names its socket "default"
// inside the socket directory on both macOS and Linux.
func TestRelevantSocketEvent_MatchesDefaultSocketBasename(t *testing.T) {
	a := NewAdapter("local")
	// No WithSocket call → SocketBasename returns "default".
	basename := a.SocketBasename()
	if basename != "default" {
		t.Fatalf("expected SocketBasename()=%q, got %q", "default", basename)
	}

	ev := fsnotify.Event{Name: "/tmp/tmux-1000/default", Op: fsnotify.Write}
	if !relevantSocketEvent(ev, basename) {
		t.Error("expected relevantSocketEvent to return true for default socket event")
	}
}

// TestRelevantSocketEvent_IgnoresOtherNonHiddenFiles verifies that non-hidden
// files in the socket directory that are NOT the configured socket are ignored.
// Without this filter, temp/lock files written by tmux itself would cause a
// feedback loop: event → PokeRefresh → tmux exec → tmux writes → event.
func TestRelevantSocketEvent_IgnoresOtherNonHiddenFiles(t *testing.T) {
	a := NewAdapter("local")
	basename := a.SocketBasename() // "default"

	cases := []struct {
		name string
		path string
	}{
		{"lock file", "/tmp/tmux-1000/lock"},
		{"other socket", "/tmp/tmux-1000/tmux-other.sock"},
		{"temp file", "/tmp/tmux-1000/tmpXXXXXX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := fsnotify.Event{Name: tc.path, Op: fsnotify.Create}
			if relevantSocketEvent(ev, basename) {
				t.Errorf("expected relevantSocketEvent to return false for %q", tc.path)
			}
		})
	}
}

// TestRelevantSocketEvent_HonoursCustomSocketPath verifies that when the adapter
// is configured with a custom -S socket path, only events on that socket's
// basename trigger a refresh. Events on "default" (the standard tmux socket
// name) must be ignored.
func TestRelevantSocketEvent_HonoursCustomSocketPath(t *testing.T) {
	a := NewAdapter("local").WithSocket("/tmp/custom/mysocket")
	basename := a.SocketBasename()
	if basename != "mysocket" {
		t.Fatalf("expected SocketBasename()=%q, got %q", "mysocket", basename)
	}

	// Event on the configured socket → should match.
	evMatch := fsnotify.Event{Name: "/tmp/custom/mysocket", Op: fsnotify.Write}
	if !relevantSocketEvent(evMatch, basename) {
		t.Error("expected relevantSocketEvent to return true for configured socket event")
	}

	// Event on "default" in the same dir → must not match.
	evDefault := fsnotify.Event{Name: "/tmp/custom/default", Op: fsnotify.Write}
	if relevantSocketEvent(evDefault, basename) {
		t.Error("expected relevantSocketEvent to return false for 'default' when custom socket is configured")
	}
}

// TestRelevantSocketEvent_IgnoresHiddenFiles preserves the existing behaviour
// that hidden files (basename starting with ".") are always ignored, regardless
// of whether they match the expected socket name.
func TestRelevantSocketEvent_IgnoresHiddenFiles(t *testing.T) {
	// Use "default" as expected — even if somehow a hidden file were named
	// ".default" it should still be ignored.
	basename := "default"

	cases := []struct {
		name string
		path string
	}{
		{"dot-prefixed hidden", "/tmp/tmux-1000/.hidden"},
		{"dot-prefixed default-look-alike", "/tmp/tmux-1000/.default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := fsnotify.Event{Name: tc.path, Op: fsnotify.Create}
			if relevantSocketEvent(ev, basename) {
				t.Errorf("expected relevantSocketEvent to return false for hidden file %q", tc.path)
			}
		})
	}
}

// TestRelevantSocketEvent_ZeroOpIsIgnored verifies the edge case: an event
// with Op==0 (no operation bits set) is always a no-op and must return false
// to prevent spurious refreshes.
func TestRelevantSocketEvent_ZeroOpIsIgnored(t *testing.T) {
	ev := fsnotify.Event{Name: "/tmp/tmux-1000/default", Op: 0}
	if relevantSocketEvent(ev, "default") {
		t.Error("expected relevantSocketEvent to return false for Op==0 event")
	}
}
