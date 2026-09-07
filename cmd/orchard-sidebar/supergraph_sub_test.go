package main

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// ackAndSubscribeN completes the handshake for a stream that opens n
// subscriptions on one socket: read connection_init, send connection_ack, then
// read n subscribe frames. Returns false if any frame is not what was expected.
func ackAndSubscribeN(t *testing.T, conn *websocket.Conn, n int) bool {
	t.Helper()
	var env map[string]any
	if err := conn.ReadJSON(&env); err != nil || env["type"] != "connection_init" {
		return false
	}
	if err := conn.WriteJSON(map[string]any{"type": "connection_ack"}); err != nil {
		return false
	}
	for i := 0; i < n; i++ {
		if err := conn.ReadJSON(&env); err != nil || env["type"] != "subscribe" {
			return false
		}
	}
	return true
}

// runSupergraphStream starts streamTmuxSupergraph in a goroutine and joins it in
// a cleanup BEFORE fakeGqlws restores the globals — the goroutine can never
// outlive the test or race the restore (mirrors runStream, keeps AC10 clean).
func runSupergraphStream(t *testing.T, send func(tea.Msg)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _, _ = streamTmuxSupergraph(ctx, send)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// AC9: a pushed tmuxEvents frame triggers a fast-lane refetch. The stub
// completes the handshake for both subscriptions, then sends one "next" frame;
// the stream must emit a fastRefetchMsg within the timeout.
func TestSupergraphPushTriggersRefetch(t *testing.T) {
	fakeGqlws(t, 2*time.Second, func(t *testing.T, conn *websocket.Conn) {
		if !ackAndSubscribeN(t, conn, 2) { // tmuxEvents + claudeSessionUpdated
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
	runSupergraphStream(t, func(m tea.Msg) {
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
