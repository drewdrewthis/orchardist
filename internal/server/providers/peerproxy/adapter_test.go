package peerproxy

import "testing"

// TestHostFromNodeID locks down the parser the proxy uses to route
// node ids — the rule is "second segment is the host" except for
// Host:<machineId>, where the suffix IS the host. Bugs here silently
// route ids to the wrong peer, so the table is intentionally explicit.
func TestHostFromNodeID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no colon", "junk", ""},
		{"host typename", "Host:peer-1", "peer-1"},
		{"tmux pane", "TmuxPane:peer-1:%26", "peer-1"},
		{"tmux session", "TmuxSession:peer-1:alpha", "peer-1"},
		{"process", "Process:peer-1:1234", "peer-1"},
		{"single colon non-host", "TmuxPane:onlyhost", ""},
		{"multi colon ws-host", "TmuxWindow:peer-1:alpha:0", "peer-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HostFromNodeID(tc.in)
			if got != tc.want {
				t.Fatalf("HostFromNodeID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
