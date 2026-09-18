package main

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// AC9: a pushed tmuxEvents frame triggers a fast-lane refetch. The stub
// completes the handshake for both subscriptions (ackAndSubscribe with n==2),
// then sends one "next" frame; the stream must emit a fastRefetchMsg within the
// timeout. Handshake and stream runner are shared with the daemon lane tests
// (subscribe_test.go).
func TestSupergraphPushTriggersRefetch(t *testing.T) {
	fakeGqlws(t, 2*time.Second, func(t *testing.T, conn *websocket.Conn) {
		if !ackAndSubscribe(t, conn, 2) { // tmuxEvents + claudeSessionUpdated
			return
		}
		_ = conn.WriteJSON(map[string]any{
			"type": "next", "id": "tmux",
			"payload": map[string]any{"data": map[string]any{
				"tmuxEvents": map[string]any{"type": "attached", "key": "work-a", "ts": "2026-09-07T15:55:10Z"},
			}},
		})
		time.Sleep(300 * time.Millisecond)
	})

	got := make(chan struct{}, 4)
	runStreamFn(t, streamTmuxSupergraph, func(m tea.Msg) {
		if _, ok := m.(fastRefetchMsg); ok {
			got <- struct{}{}
		}
	})
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no fast-lane refetch after a pushed tmuxEvents frame")
	}
}

// A dial failure reports acked=false so subscribeTmux keeps climbing its
// backoff — the same contract the daemon stream honors, reused unchanged.
func TestSupergraphStreamDialFailureIsNotAcked(t *testing.T) {
	prev := wsURL
	wsURL = "ws://127.0.0.1:1/graphql"
	t.Cleanup(func() { wsURL = prev })
	acked, _, err := streamTmuxSupergraph(context.Background(), func(tea.Msg) {})
	if acked || err == nil {
		t.Fatalf("dial failure: acked=%v err=%v, want false + non-nil", acked, err)
	}
}

// The refetch message re-reads the fast lane and clears the degraded push
// marker (a live event is proof the push lane recovered).
func TestFastRefetchMsgReadsFastLaneAndClearsSubErr(t *testing.T) {
	m := &model{subErr: errSubEnded}
	cmd := m.update(fastRefetchMsg{})
	if cmd == nil {
		t.Fatal("fastRefetchMsg should issue a fast-lane fetch")
	}
	if m.subErr != nil {
		t.Errorf("subErr not cleared: %v", m.subErr)
	}
}
