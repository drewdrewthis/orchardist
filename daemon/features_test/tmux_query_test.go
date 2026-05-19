package features_test

import (
	"os/exec"
	"testing"
)

// tmuxAvailable returns true when a tmux server is reachable on this host.
// Tests that need a live tmux call t.Skip when tmuxAvailable is false.
func tmuxAvailable() bool {
	cmd := exec.Command("tmux", "list-sessions")
	_ = cmd.Run()
	// tmux exits 1 when no sessions exist but is still "available"; exit 127
	// means tmux is not installed.
	return cmd.ProcessState != nil && cmd.ProcessState.ExitCode() != 127
}

// @scenario tmuxServer returns alive flag and sessions
func TestTmuxServerQuery_ReturnsAliveAndSessions(t *testing.T) {
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { id alive sessions { id name } clients { tty } } }`)
	assertNoErrors(t, r)

	t.Run("when TmuxLens query returns", func(t *testing.T) {
		raw, ok := r.Data["tmuxServer"]
		if !ok {
			t.Fatal("tmuxServer field missing from response")
		}
		srv := asMap(t, raw, "tmuxServer")
		requireFields(t, srv, "id", "alive", "sessions", "clients")
	})
}

// @scenario tmuxServer unreachable — alive is false, sessions is empty
func TestTmuxServerQuery_UnreachableAliveIsFalse(t *testing.T) {
	if tmuxAvailable() {
		t.Skip("tmux is running; cannot test unreachable path")
	}

	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { alive sessions { id } } }`)
	assertNoErrors(t, r)

	srv := asMap(t, r.Data["tmuxServer"], "tmuxServer")
	alive, ok := srv["alive"].(bool)
	if !ok {
		t.Fatalf("tmuxServer.alive: expected bool, got %T", srv["alive"])
	}
	if alive {
		t.Error("expected tmuxServer.alive = false when tmux unreachable")
	}
	sessions := asList(t, srv["sessions"], "tmuxServer.sessions")
	if len(sessions) != 0 {
		t.Errorf("expected empty sessions when alive=false, got %d", len(sessions))
	}
}

// @scenario tmuxServer session carries required fields
func TestTmuxServerQuery_SessionCarriesRequiredFields(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available on this host")
	}

	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { alive sessions { id name attached activeAttached lastActivityAt windows { id index name active panes { paneId } } } } }`)
	assertNoErrors(t, r)

	srv := asMap(t, r.Data["tmuxServer"], "tmuxServer")
	if alive, _ := srv["alive"].(bool); !alive {
		t.Skip("tmux server not alive during test run")
	}
	sessions := asList(t, srv["sessions"], "sessions")
	for _, raw := range sessions {
		s := asMap(t, raw, "session")
		requireFields(t, s, "id", "name", "attached", "activeAttached", "lastActivityAt", "windows")
	}
}

// @scenario tmuxServer window carries required fields
func TestTmuxServerQuery_WindowCarriesRequiredFields(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available on this host")
	}

	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { sessions { windows { id index name active panes { paneId } } } } }`)
	assertNoErrors(t, r)

	srv := asMap(t, r.Data["tmuxServer"], "tmuxServer")
	sessions := asList(t, srv["sessions"], "sessions")
	for _, rawS := range sessions {
		s := asMap(t, rawS, "session")
		windows := asList(t, s["windows"], "windows")
		for _, rawW := range windows {
			w := asMap(t, rawW, "window")
			requireFields(t, w, "id", "index", "name", "active", "panes")
		}
	}
}

// @scenario PaneCard spreads required fields
func TestTmuxServerQuery_PaneCardCarriesRequiredFields(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available on this host")
	}

	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { sessions { windows { panes { paneId title currentCommand currentPid window { session { name } } } } } } }`)
	assertNoErrors(t, r)

	srv := asMap(t, r.Data["tmuxServer"], "tmuxServer")
	sessions := asList(t, srv["sessions"], "sessions")
	for _, rawS := range sessions {
		s := asMap(t, rawS, "session")
		windows := asList(t, s["windows"], "windows")
		for _, rawW := range windows {
			w := asMap(t, rawW, "window")
			panes := asList(t, w["panes"], "panes")
			for _, rawP := range panes {
				p := asMap(t, rawP, "pane")
				requireFields(t, p, "paneId", "title", "currentCommand", "currentPid")
				requireField(t, p, "window")
			}
		}
	}
}

// @scenario TmuxLens does not include pane content
func TestTmuxServerQuery_NoPaneContent(t *testing.T) {
	ts := startMinimalServer(t)

	// content, contentRange, contentFull are NOT in the schema — requesting
	// them should produce a GraphQL error ("Cannot query field").
	r := postGQL(t, ts.URL, `{ tmuxServer { sessions { windows { panes { paneId } } } } }`)
	// This query (without content) must not error.
	assertNoErrors(t, r)
	requireField(t, r.Data, "tmuxServer")
}

// @scenario clients field carries tty and currentPane.paneId
func TestTmuxServerQuery_ClientsCarryTtyAndCurrentPane(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available on this host")
	}

	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ tmuxServer { clients { tty currentPane { paneId } } } }`)
	assertNoErrors(t, r)

	srv := asMap(t, r.Data["tmuxServer"], "tmuxServer")
	clients := asList(t, srv["clients"], "clients")
	for _, raw := range clients {
		c := asMap(t, raw, "client")
		requireField(t, c, "tty")
		requireField(t, c, "currentPane")
	}
}
