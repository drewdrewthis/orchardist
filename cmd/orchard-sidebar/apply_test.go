package main

import (
	"testing"
	"time"
)

// TestApplyFastPaneMapOwnership is the unit guard for subscribe.go's
// documented split: the push lane owns paneToSess while it's live, and a
// stale fast poll landing after it must not revert the map.
func TestApplyFastPaneMapOwnership(t *testing.T) {
	t.Run("live subscription: fast poll does not overwrite the map", func(t *testing.T) {
		m := &model{
			subAt:      time.Now(),
			paneToSess: map[string]string{"%0": "live-session"},
		}
		m.applyFast(fastDataMsg{paneToSess: map[string]string{"%0": "stale-session"}})
		if got := m.paneToSess["%0"]; got != "live-session" {
			t.Errorf("paneToSess[%%0] = %q, want unchanged %q (subscription owns the map while live)",
				got, "live-session")
		}
	})

	t.Run("no live subscription: fast poll populates the map", func(t *testing.T) {
		m := &model{}
		m.applyFast(fastDataMsg{paneToSess: map[string]string{"%0": "polled-session"}})
		if got := m.paneToSess["%0"]; got != "polled-session" {
			t.Errorf("paneToSess[%%0] = %q, want %q (poll owns the map without a live push lane)",
				got, "polled-session")
		}
	})
}

// TestApplyFastPushLaneCarriesSnapshot covers the branch review flagged as
// untested on PR #847: pushLaneCarriesSnapshot decides who owns the pane map
// and attach flags while the push lane is live. The daemon's push snapshot is
// fresher than the poll (pushLaneCarriesSnapshot=true, unchanged default), so
// it wins; the supergraph push lane only sends bare "something changed"
// events with no map (pushLaneCarriesSnapshot=false, set by applyBackend), so
// the contemporaneous fast poll is the only source and must win instead.
func TestApplyFastPushLaneCarriesSnapshot(t *testing.T) {
	saved := pushLaneCarriesSnapshot
	t.Cleanup(func() { pushLaneCarriesSnapshot = saved })

	cases := []struct {
		name                string
		carriesSnapshot     bool
		wantPaneToSess      string
		wantAttached        bool
		wantAttachedComment string
	}{
		{
			name:            "daemon backend: push lane keeps ownership of pane map and attach flags",
			carriesSnapshot: true,
			wantPaneToSess:  "push-session",
			wantAttached:    true,
		},
		{
			name:            "supergraph backend: fast lane takes ownership of pane map and attach flags",
			carriesSnapshot: false,
			wantPaneToSess:  "poll-session",
			wantAttached:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pushLaneCarriesSnapshot = c.carriesSnapshot
			m := &model{
				subAt:          time.Now(), // subLive() == true
				paneToSess:     map[string]string{"%0": "push-session"},
				attachedBySess: map[string]bool{"a": true},
			}
			m.applyFast(fastDataMsg{
				rows:       []row{{session: "a", attached: false}},
				paneToSess: map[string]string{"%0": "poll-session"},
			})
			if got := m.paneToSess["%0"]; got != c.wantPaneToSess {
				t.Errorf("paneToSess[%%0] = %q, want %q", got, c.wantPaneToSess)
			}
			if got := m.rows[0].attached; got != c.wantAttached {
				t.Errorf("rows[0].attached = %v, want %v", got, c.wantAttached)
			}
		})
	}
}

// TestApplyBackendSetsPushLaneCarriesSnapshot: the daemon backend leaves the
// default true (it always has); only the supergraph branch clears it, per the
// comment on the pushLaneCarriesSnapshot var in config.go.
func TestApplyBackendSetsPushLaneCarriesSnapshot(t *testing.T) {
	savedFlag := pushLaneCarriesSnapshot
	savedGraphQL, savedWS, savedHealth := graphqlURL, wsURL, healthURL
	savedFetchFast, savedFetchSlow, savedStreamTmux := fetchFast, fetchSlow, streamTmux
	t.Cleanup(func() {
		pushLaneCarriesSnapshot = savedFlag
		graphqlURL, wsURL, healthURL = savedGraphQL, savedWS, savedHealth
		fetchFast, fetchSlow, streamTmux = savedFetchFast, savedFetchSlow, savedStreamTmux
	})

	applyBackend(endpoints{backend: backendDaemon, httpURL: daemonHTTPURL, wsURL: "ws://127.0.0.1:7777/graphql"})
	if !pushLaneCarriesSnapshot {
		t.Error("daemon backend: pushLaneCarriesSnapshot = false, want true (unchanged default)")
	}

	applyBackend(endpoints{backend: backendSupergraph, httpURL: supergraphHTTPURL, wsURL: "ws://127.0.0.1:7788/graphql"})
	if pushLaneCarriesSnapshot {
		t.Error("supergraph backend: pushLaneCarriesSnapshot = true, want false (push lane has no snapshot)")
	}
}
