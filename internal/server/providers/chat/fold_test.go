package chat

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func TestFold_MessagesAndMembership(t *testing.T) {
	events := []Event{
		{Type: "member.joined", Timestamp: mustTime(t, "2026-05-09T17:00:00Z"), Handle: "@alice", Machine: "drew-mac", TmuxSession: "card-alice"},
		{Type: "message", Timestamp: mustTime(t, "2026-05-09T17:01:00Z"), ID: "01J1", Sender: "@alice", SenderMachine: "drew-mac", Text: "hi", Source: "internal"},
		{Type: "member.joined", Timestamp: mustTime(t, "2026-05-09T17:02:00Z"), Handle: "@bob", Machine: "drew-linux", TmuxSession: "card-bob"},
		{Type: "message", Timestamp: mustTime(t, "2026-05-09T17:03:00Z"), ID: "01J2", Sender: "@bob", SenderMachine: "drew-linux", Text: "hey"},
		{Type: "member.left", Timestamp: mustTime(t, "2026-05-09T17:04:00Z"), Handle: "@alice"},
	}
	room := Fold("general", events)

	if room.ID != "general" {
		t.Errorf("room id: got %q want general", room.ID)
	}
	if got, want := len(room.Messages), 2; got != want {
		t.Fatalf("messages: got %d want %d", got, want)
	}
	if room.Messages[0].Text != "hi" || room.Messages[1].Text != "hey" {
		t.Errorf("message order broken: %#v", room.Messages)
	}
	if got := room.Messages[1].Source; got != "internal" {
		t.Errorf("missing source default: got %q want internal", got)
	}
	if got, want := len(room.Members), 1; got != want {
		t.Fatalf("members: got %d want %d (only @bob should remain)", got, want)
	}
	if room.Members[0].Handle != "@bob" {
		t.Errorf("member: got %q want @bob", room.Members[0].Handle)
	}
	if !room.LastEventAt.Equal(mustTime(t, "2026-05-09T17:04:00Z")) {
		t.Errorf("last event ts: got %v", room.LastEventAt)
	}
}

func TestFold_RejoinAfterLeave(t *testing.T) {
	events := []Event{
		{Type: "member.joined", Timestamp: mustTime(t, "2026-05-09T17:00:00Z"), Handle: "@alice", Machine: "m", TmuxSession: "s"},
		{Type: "member.left", Timestamp: mustTime(t, "2026-05-09T17:01:00Z"), Handle: "@alice"},
		{Type: "member.joined", Timestamp: mustTime(t, "2026-05-09T17:02:00Z"), Handle: "@alice", Machine: "m2", TmuxSession: "s2"},
	}
	room := Fold("r", events)
	if got, want := len(room.Members), 1; got != want {
		t.Fatalf("members: got %d want %d", got, want)
	}
	if got := room.Members[0].TmuxSession; got != "s2" {
		t.Errorf("rejoin should overwrite tmux_session: got %q want s2", got)
	}
}

func TestFold_SkipsUnknownAndMalformed(t *testing.T) {
	events := []Event{
		{Type: "future.event.kind", Timestamp: mustTime(t, "2026-05-09T17:00:00Z")},
		{Type: "message", Timestamp: mustTime(t, "2026-05-09T17:01:00Z"), ID: "", Sender: "@a", Text: "missing-id"},
		{Type: "message", Timestamp: mustTime(t, "2026-05-09T17:02:00Z"), ID: "01J3", Sender: "", Text: "missing-sender"},
		{Type: "member.joined", Timestamp: mustTime(t, "2026-05-09T17:03:00Z"), Handle: ""},
		{Type: "message", Timestamp: mustTime(t, "2026-05-09T17:04:00Z"), ID: "01J4", Sender: "@a", Text: "ok"},
	}
	room := Fold("r", events)
	if got, want := len(room.Messages), 1; got != want {
		t.Fatalf("messages: got %d want %d", got, want)
	}
	if room.Messages[0].ID != "01J4" {
		t.Errorf("kept wrong message: %#v", room.Messages[0])
	}
}
