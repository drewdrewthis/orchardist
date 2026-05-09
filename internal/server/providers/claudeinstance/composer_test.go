package claudeinstance

import (
	"context"
	"testing"
	"time"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// fakePaneFinder is the in-package fake the composer tests inject for
// the PaneFinder dependency. Tests configure ByPid / BySession; the
// composer is exercised through its public Compose method only.
type fakePaneFinder struct {
	byPid     map[int]*graphql.TmuxPane
	bySession map[string]*graphql.TmuxPane
}

func (f *fakePaneFinder) FindByPid(_ context.Context, _ string, pid int) (*graphql.TmuxPane, bool) {
	if p, ok := f.byPid[pid]; ok {
		return p, true
	}
	return nil, false
}

func (f *fakePaneFinder) FindBySession(_ context.Context, _, session string) (*graphql.TmuxPane, bool) {
	if p, ok := f.bySession[session]; ok {
		return p, true
	}
	return nil, false
}

// fakeProcessFinder mirrors fakePaneFinder for the ProcessFinder dep.
type fakeProcessFinder struct {
	byPid map[int]*graphql.Process
}

func (f *fakeProcessFinder) FindByPid(_ context.Context, _ string, pid int) (*graphql.Process, bool) {
	if p, ok := f.byPid[pid]; ok {
		return p, true
	}
	return nil, false
}

// fakeAccountFinder returns a single account when set; useful to verify
// the composer pumps it through to every instance.
type fakeAccountFinder struct {
	account *graphql.ClaudeAccount
}

func (f *fakeAccountFinder) Active(_ context.Context, _ string) (*graphql.ClaudeAccount, bool) {
	if f.account == nil {
		return nil, false
	}
	return f.account, true
}

// fakeLiveness exposes a deterministic alive map.
type fakeLiveness struct {
	alive map[int]bool
}

func (f fakeLiveness) IsAlive(pid int) bool { return f.alive[pid] }

// TestComposer_Compose_Working asserts the happy path — a fresh
// heartbeat with a known pid produces a working ClaudeInstance with
// every cross-provider edge populated.
func TestComposer_Compose_Working(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "alpha",
		SessionID:       "uuid-alpha",
		State:           "working",
		ClaudePid:       42100,
		RcURL:           "https://claude.ai/code/session_xyz",
		RcEnabled:       true,
		Timestamp:       now.Add(-5 * time.Second),
		LastHeartbeatAt: now.Add(-5 * time.Second),
	}

	pane := &graphql.TmuxPane{ID: "TmuxPane:local:%26"}
	proc := &graphql.Process{ID: "Process:local:42100"}
	acct := &graphql.ClaudeAccount{ID: "ClaudeAccount:local:dev@example.com"}

	c := NewComposerWith(
		"local",
		&fakePaneFinder{byPid: map[int]*graphql.TmuxPane{42100: pane}},
		&fakeProcessFinder{byPid: map[int]*graphql.Process{42100: proc}},
		&fakeAccountFinder{account: acct},
		fakeLiveness{alive: map[int]bool{42100: true}},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)

	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	inst := out[0]
	if inst.State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working", inst.State)
	}
	if inst.Pane != pane {
		t.Errorf("pane = %v, want %v", inst.Pane, pane)
	}
	if inst.Process != proc {
		t.Errorf("process = %v, want %v", inst.Process, proc)
	}
	if inst.Account != acct {
		t.Errorf("account = %v, want %v", inst.Account, acct)
	}
	if inst.RcURL == nil || *inst.RcURL != hb.RcURL {
		t.Errorf("rcUrl = %v, want %s", inst.RcURL, hb.RcURL)
	}
	if !inst.RcEnabled {
		t.Error("rcEnabled = false, want true")
	}
	if inst.SessionUUID == nil || *inst.SessionUUID != hb.SessionID {
		t.Errorf("sessionUuid = %v, want %s", inst.SessionUUID, hb.SessionID)
	}
	wantID := "ClaudeInstance:local:42100"
	if inst.ID != wantID {
		t.Errorf("id = %s, want %s", inst.ID, wantID)
	}
}

// TestComposer_Compose_StaleHeartbeat asserts a fresh-state heartbeat
// from > staleAfter ago collapses to no_claude — covers the briefing's
// "touch a heartbeat backward → state goes to no_claude" assertion.
func TestComposer_Compose_StaleHeartbeat(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "stale",
		State:           "working",
		ClaudePid:       11,
		Timestamp:       now.Add(-2 * time.Minute),
		LastHeartbeatAt: now.Add(-2 * time.Minute),
	}

	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{11: true}},
		func() time.Time { return now },
		30*time.Second,
	)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d, want 1", len(out))
	}
	if out[0].State != graphql.InstanceStateNoClaude {
		t.Errorf("state = %s, want no_claude", out[0].State)
	}
}

// TestComposer_Compose_DeadPid asserts a fresh heartbeat for a dead
// pid still collapses to no_claude — liveness wins.
func TestComposer_Compose_DeadPid(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "deadpid",
		State:           "working",
		ClaudePid:       99,
		Timestamp:       now.Add(-2 * time.Second),
		LastHeartbeatAt: now.Add(-2 * time.Second),
	}
	c := NewComposerWith(
		"local",
		nil,
		nil,
		nil,
		fakeLiveness{alive: map[int]bool{99: false}},
		func() time.Time { return now },
		30*time.Second,
	)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State != graphql.InstanceStateNoClaude {
		t.Errorf("state = %s, want no_claude (dead pid)", out[0].State)
	}
}

// TestComposer_Compose_NoPaneStillEmits asserts the briefing's
// requirement: when the composer can't find a pane (no PaneFinder match
// for either pid or session), the instance is still emitted with
// pane=null. The dashboard sees the heartbeat-only ghost.
func TestComposer_Compose_NoPaneStillEmits(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "ghost",
		State:           "idle",
		ClaudePid:       55,
		Timestamp:       now.Add(-1 * time.Second),
		LastHeartbeatAt: now.Add(-1 * time.Second),
	}
	c := NewComposerWith(
		"local",
		&fakePaneFinder{}, // empty maps — nothing matches
		&fakeProcessFinder{},
		&fakeAccountFinder{},
		fakeLiveness{alive: map[int]bool{55: true}},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if len(out) != 1 {
		t.Fatalf("got %d, want 1 (ghost still emitted)", len(out))
	}
	if out[0].Pane != nil {
		t.Errorf("pane = %v, want nil for unmatched pid", out[0].Pane)
	}
	if out[0].State != graphql.InstanceStateIdle {
		t.Errorf("state = %s, want idle", out[0].State)
	}
}

// TestComposer_Compose_FallbackToSession asserts that when ClaudePid is
// 0 (legacy heartbeat without pid), the composer falls back to
// FindBySession so it still finds a pane.
func TestComposer_Compose_FallbackToSession(t *testing.T) {
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	hb := Heartbeat{
		TmuxSession:     "legacy",
		State:           "working",
		ClaudePid:       0, // legacy hook didn't write pid
		Timestamp:       now.Add(-1 * time.Second),
		LastHeartbeatAt: now.Add(-1 * time.Second),
	}
	pane := &graphql.TmuxPane{ID: "TmuxPane:local:%99"}
	c := NewComposerWith(
		"local",
		&fakePaneFinder{bySession: map[string]*graphql.TmuxPane{"legacy": pane}},
		nil,
		nil,
		fakeLiveness{},
		func() time.Time { return now },
		HeartbeatStaleAfter,
	)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].Pane != pane {
		t.Errorf("pane = %v, want %v (session-keyed fallback)", out[0].Pane, pane)
	}
	// State trusts the heartbeat when pid is unknown.
	if out[0].State != graphql.InstanceStateWorking {
		t.Errorf("state = %s, want working when pid unknown", out[0].State)
	}
}

// TestComposer_Compose_UnknownStateStaysNoClaude asserts that an
// unrecognised state string (e.g. "" or "?") does not silently default
// to working — it collapses to no_claude.
func TestComposer_Compose_UnknownStateStaysNoClaude(t *testing.T) {
	now := time.Now()
	hb := Heartbeat{
		TmuxSession:     "unknown",
		State:           "garbage",
		ClaudePid:       7,
		Timestamp:       now,
		LastHeartbeatAt: now,
	}
	c := NewComposerWith("local", nil, nil, nil, fakeLiveness{alive: map[int]bool{7: true}}, func() time.Time { return now }, HeartbeatStaleAfter)
	out := c.Compose(context.Background(), []Heartbeat{hb})
	if out[0].State != graphql.InstanceStateNoClaude {
		t.Errorf("state = %s, want no_claude for unknown state string", out[0].State)
	}
}

// TestComposer_ResolvePid_HeartbeatPidWins asserts that when hb.ClaudePid is
// non-zero, resolvePid returns it regardless of any pane values (feature file
// lines 174-179: "prefers heartbeat ClaudePid over pane.CurrentPid when ClaudePid
// is non-zero").
func TestComposer_ResolvePid_HeartbeatPidWins(t *testing.T) {
	c := &Composer{}
	panePid := int64(88631)
	pane := &graphql.TmuxPane{
		ID:         "TmuxPane:local:%59",
		CurrentPid: &panePid,
	}
	hb := Heartbeat{ClaudePid: 12345}
	got := c.resolvePid(hb, pane)
	if got != 12345 {
		t.Errorf("resolvePid = %d, want 12345 (heartbeat ClaudePid must win)", got)
	}
}

// TestComposer_ResolvePid_FallsBackToPaneCurrentPid asserts that when
// hb.ClaudePid is 0 and the matched pane has a non-nil, positive CurrentPid,
// resolvePid returns int(*pane.CurrentPid) (feature file lines 181-186:
// "falls back to pane.CurrentPid when heartbeat ClaudePid is zero and a pane
// is matched").
func TestComposer_ResolvePid_FallsBackToPaneCurrentPid(t *testing.T) {
	c := &Composer{}
	panePid := int64(88631)
	pane := &graphql.TmuxPane{
		ID:         "TmuxPane:local:%59",
		CurrentPid: &panePid,
	}
	hb := Heartbeat{ClaudePid: 0}
	got := c.resolvePid(hb, pane)
	if got != 88631 {
		t.Errorf("resolvePid = %d, want 88631 (pane.CurrentPid fallback)", got)
	}
}

// TestComposer_ResolvePid_NilPaneReturnsZero asserts that when hb.ClaudePid is
// 0 and pane is nil, resolvePid returns 0 (feature file lines 188-193: "returns 0
// when heartbeat ClaudePid is zero and no pane is matched").
func TestComposer_ResolvePid_NilPaneReturnsZero(t *testing.T) {
	c := &Composer{}
	hb := Heartbeat{ClaudePid: 0}
	got := c.resolvePid(hb, nil)
	if got != 0 {
		t.Errorf("resolvePid = %d, want 0 (nil pane)", got)
	}
}

// TestComposer_ResolvePid_NilCurrentPidReturnsZero asserts that when
// hb.ClaudePid is 0 and pane.CurrentPid is nil, resolvePid returns 0
// (second nil-CurrentPid case from feature file lines 188-193).
func TestComposer_ResolvePid_NilCurrentPidReturnsZero(t *testing.T) {
	c := &Composer{}
	pane := &graphql.TmuxPane{
		ID:         "TmuxPane:local:%59",
		CurrentPid: nil, // explicitly nil — no pid recorded by tmux provider
	}
	hb := Heartbeat{ClaudePid: 0}
	got := c.resolvePid(hb, pane)
	if got != 0 {
		t.Errorf("resolvePid = %d, want 0 (nil pane.CurrentPid)", got)
	}
}

// TestParseInstanceID asserts the round-trip from buildID → parseInstanceID.
func TestParseInstanceID(t *testing.T) {
	id := buildID("local", 12345, "alpha")
	host, pid, ok := parseInstanceID(id)
	if !ok || host != "local" || pid != 12345 {
		t.Errorf("parseInstanceID(%s) = (%s, %d, %v), want (local, 12345, true)", id, host, pid, ok)
	}

	sessionID := buildID("local", 0, "alpha")
	host, pid, ok = parseInstanceID(sessionID)
	if ok {
		t.Errorf("parseInstanceID(%s) ok=true, want false for session-keyed id", sessionID)
	}
	if host != "local" {
		t.Errorf("parseInstanceID host = %s, want local", host)
	}
	_ = pid
}
