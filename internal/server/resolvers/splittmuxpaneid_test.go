// Unit tests for the splitTmuxPaneID helper — the federation primitive
// the tmuxPane.process resolver depends on (issue #463 AC3).
//
// Full federation e2e (peer host pane resolved through Host.tmuxSessions)
// is deferred until tmux federation lands; see #463 plan open questions.
// These tests guard against a regression where someone "simplifies" the
// resolver by using r.Tmux.Host() instead of extracting the host from
// the pane id — which would break cross-host id projection.
package resolvers

import "testing"

// TestSplitTmuxPaneID_LocalHost verifies the happy path for a local host.
func TestSplitTmuxPaneID_LocalHost(t *testing.T) {
	host, paneID, ok := splitTmuxPaneID("TmuxPane:local-mac:%5")
	if !ok {
		t.Fatal("splitTmuxPaneID returned ok=false, want ok=true")
	}
	if host != "local-mac" {
		t.Errorf("host = %q, want %q", host, "local-mac")
	}
	if paneID != "%5" {
		t.Errorf("paneID = %q, want %q", paneID, "%5")
	}
}

// TestSplitTmuxPaneID_PeerHost verifies that a federated pane id preserves
// the peer's host segment rather than being replaced by the local daemon's
// host. This is the federation correctness invariant: the caller (tmuxPane.process
// resolver) must use the extracted host, not r.Tmux.Host(), when constructing
// the projected Process and Host nodes.
func TestSplitTmuxPaneID_PeerHost(t *testing.T) {
	host, paneID, ok := splitTmuxPaneID("TmuxPane:peer-host:%26")
	if !ok {
		t.Fatal("splitTmuxPaneID returned ok=false, want ok=true")
	}
	// Federation correctness: peer host id must be preserved, not replaced
	// with the local daemon's host.
	if host != "peer-host" {
		t.Errorf("host = %q, want %q (federation: peer host must be preserved)", host, "peer-host")
	}
	if paneID != "%26" {
		t.Errorf("paneID = %q, want %q", paneID, "%26")
	}
}

// TestSplitTmuxPaneID_Malformed verifies that ids not matching the
// "TmuxPane:<host>:<paneId>" schema return ok=false without panicking.
func TestSplitTmuxPaneID_Malformed(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"wrong_prefix", "not-a-pane-id"},
		{"only_prefix", "TmuxPane:"},
		{"missing_pane_segment", "TmuxPane:local-mac"},
		{"empty_host", "TmuxPane::%1"},
		{"empty_pane", "TmuxPane:local-mac:"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := splitTmuxPaneID(tc.id)
			if ok {
				t.Errorf("splitTmuxPaneID(%q) returned ok=true, want ok=false", tc.id)
			}
		})
	}
}
