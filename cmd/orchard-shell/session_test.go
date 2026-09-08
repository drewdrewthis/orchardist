package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSortSessionsByRecency_NewestAttachedFirst(t *testing.T) {
	got := sortSessionsByRecency("100 old\n300 newest\n200 middle\n")
	want := []string{"newest", "middle", "old"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sortSessionsByRecency = %v; want %v", got, want)
	}
}

// A never-attached session reports 0 and must sort last rather than being
// dropped or landing at the front.
func TestSortSessionsByRecency_NeverAttachedSortsLast(t *testing.T) {
	got := sortSessionsByRecency("0 fresh\n500 used\n")
	if len(got) != 2 || got[0] != "used" || got[1] != "fresh" {
		t.Errorf("sortSessionsByRecency = %v; want [used fresh]", got)
	}
}

// A tmux without #{session_last_attached} renders it empty, leaving the name
// alone on the line. A name with a space in it must survive that path whole.
func TestSortSessionsByRecency_ToleratesANameOnlyLine(t *testing.T) {
	got := sortSessionsByRecency("solo\n")
	if len(got) != 1 || got[0] != "solo" {
		t.Errorf("sortSessionsByRecency = %v; want [solo] — a session must never be dropped", got)
	}
	got = sortSessionsByRecency("my session\n")
	if len(got) != 1 || got[0] != "my session" {
		t.Errorf("sortSessionsByRecency = %v; want [\"my session\"] — an unstamped name must not be truncated", got)
	}
	got = sortSessionsByRecency("400 my session\n")
	if len(got) != 1 || got[0] != "my session" {
		t.Errorf("sortSessionsByRecency = %v; want [\"my session\"] — only the stamp is split off", got)
	}
}

func TestResolveSession_DefaultsToMostRecentlyAttached(t *testing.T) {
	f := newFakeTmux().reply(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"),
		"100 a\n900 b\n")
	w := testWrapper(f)

	got, err := w.resolveSession()
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got != "b" {
		t.Errorf("resolveSession() = %q; want the most recently attached session b", got)
	}
}

func TestResolveSession_ExplicitSessionWins(t *testing.T) {
	f := newFakeTmux().reply(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"),
		"100 a\n900 b\n")
	w := testWrapper(f, func(o *Options) { o.Session = "a" })

	got, err := w.resolveSession()
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got != "a" {
		t.Errorf("resolveSession() = %q; want a", got)
	}
}

// @scenario Requesting a missing inner session lists what exists
//
// AC3: `orchard shell --session nope` with sessions a,b exits 2 and prints
// both names.
func TestResolveSession_MissingSessionNamesWhatExists(t *testing.T) {
	f := newFakeTmux().reply(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"),
		"100 a\n200 b\n")
	w := testWrapper(f, func(o *Options) { o.Session = "nope" })

	_, err := w.resolveSession()
	if err == nil {
		t.Fatal("resolveSession succeeded for a session that does not exist")
	}
	var missing *sessionMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("error is %T; want *sessionMissingError", err)
	}
	for _, name := range []string{"nope", "a", "b"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %q", err, name)
		}
	}
	if got := exitCodeFor(err); got != exitSessionMissing {
		t.Errorf("exit code = %d; want %d", got, exitSessionMissing)
	}
}

// @scenario No inner server creates a default session and boots
//
// AC1: with no inner server, ensureReady creates exactly one default session
// named "main" on the inner socket, then boots the outer wrapper — instead of
// the old fail-fast (issue #747 AC3, retired by #851).
func TestEnsureReady_NoInnerServerCreatesDefaultThenBoots(t *testing.T) {
	f := newFakeTmux().
		fail(outerCall("has-session", "-t", outerSessionName), "no server running").
		fail(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"), "no server running")
	w := testWrapper(f)

	if err := w.ensureReady(); err != nil {
		t.Fatalf("ensureReady failed with no inner server: %v", err)
	}
	home, _ := os.UserHomeDir()
	if !f.called(strings.Join(innerArgs("inner-test", "new-session", "-d", "-s", defaultNewSessionName, "-c", home), " ")) {
		t.Errorf("did not create the default inner session %q; calls: %v", defaultNewSessionName, f.calls)
	}
	if !f.called("-s " + outerSessionName) {
		t.Errorf("outer wrapper was not booted; mutations: %v", f.mutations())
	}
}

// AC2: an inner server that is up but has zero sessions is treated exactly
// like an absent one — create the default session, then boot.
func TestEnsureReady_EmptyInnerServerCreatesDefaultThenBoots(t *testing.T) {
	f := newFakeTmux().
		fail(outerCall("has-session", "-t", outerSessionName), "no server running").
		reply(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"), "")
	w := testWrapper(f)

	if err := w.ensureReady(); err != nil {
		t.Fatalf("ensureReady failed on an empty inner server: %v", err)
	}
	if !f.called("new-session -d -s " + defaultNewSessionName) {
		t.Errorf("did not create the default inner session; calls: %v", f.calls)
	}
}

// AC3: with existing inner sessions, resolveSession attaches one and creates
// nothing new on the inner server.
func TestResolveSession_ExistingSessionsCreateNothing(t *testing.T) {
	f := newFakeTmux().reply(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"),
		"100 a\n900 b\n")
	w := testWrapper(f)

	got, err := w.resolveSession()
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got != "b" {
		t.Errorf("resolveSession() = %q; want the most recent session b", got)
	}
	if f.called("new-session") {
		t.Errorf("a session was created despite existing sessions; calls: %v", f.calls)
	}
}

// AC4: a genuine tmux error when creating the default session fails fast with
// exit 1 and mutates nothing on the outer server — the create runs before boot
// touches the outer socket.
func TestEnsureReady_InnerCreateFailureFailsFastLeavesOuterUntouched(t *testing.T) {
	home, _ := os.UserHomeDir()
	f := newFakeTmux().
		fail(outerCall("has-session", "-t", outerSessionName), "no server running").
		fail(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"), "no server running").
		fail(strings.Join(innerArgs("inner-test", "new-session", "-d", "-s", defaultNewSessionName, "-c", home), " "),
			"error connecting to /tmp/nosuchsocket (Permission denied)")
	w := testWrapper(f)

	err := w.ensureReady()
	if err == nil {
		t.Fatal("ensureReady succeeded despite a failing inner new-session")
	}
	if got := exitCodeFor(err); got != 1 {
		t.Errorf("exit code = %d; want 1 (2 is reserved for a missing session)", got)
	}
	for _, m := range f.mutations() {
		if strings.Contains(m, "outer-test") {
			t.Errorf("outer server was mutated despite the create failure: %q", m)
		}
	}
}

// AC7: the default-session create sets cwd to $HOME, so a fresh session starts
// where a login shell would.
func TestResolveSession_CreatesInHomeDir(t *testing.T) {
	f := newFakeTmux().fail(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"), "no server running")
	w := testWrapper(f)

	if _, err := w.resolveSession(); err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir available: %v", err)
	}
	if !f.called("new-session -d -s " + defaultNewSessionName + " -c " + home) {
		t.Errorf("create did not set -c %q; calls: %v", home, f.calls)
	}
}

// AC8: --session foo on an absent/empty inner server creates a session named
// foo (the name the user asked for), not the default.
func TestResolveSession_FlagNamesTheCreatedSession(t *testing.T) {
	f := newFakeTmux().fail(innerCall("list-sessions", "-F", "#{session_last_attached} #{session_name}"), "no server running")
	w := testWrapper(f, func(o *Options) { o.Session = "foo" })

	got, err := w.resolveSession()
	if err != nil {
		t.Fatalf("resolveSession: %v", err)
	}
	if got != "foo" {
		t.Errorf("resolveSession() = %q; want foo", got)
	}
	if !f.called("new-session -d -s foo") {
		t.Errorf("did not create a session named foo; calls: %v", f.calls)
	}
}
