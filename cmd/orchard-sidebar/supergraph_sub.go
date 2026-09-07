package main

// Supergraph push lane (#844). Supergraph's tmuxEvents/claudeSessionUpdated
// subscriptions push discrete {type,key,ts} event envelopes, not a session
// snapshot like the daemon's tmuxSessionsChanged — there is nothing to fold, so
// any event means "something changed, re-read the fast lane". This stream runs
// the identical graphql-transport-ws handshake as the daemon lane (wire-
// compatible, confirmed against supergraph's own CLI), subscribing to both
// event fields on one socket and emitting a fastRefetchMsg on every next frame.
// subscribeTmux's existing backoff/redial loop drives it unchanged.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// fastRefetchMsg asks the update loop to re-read the fast lane. Carries no
// payload: the supergraph event envelope has no snapshot to apply, so the only
// action is a refetch (main.go's update handles it).
type fastRefetchMsg struct{}

const (
	sgTmuxSubQuery   = `subscription { tmuxEvents { type key ts } }`
	sgClaudeSubQuery = `subscription { claudeSessionUpdated { type key ts } }`
)

// streamTmuxSupergraph holds one connection open, emitting a fastRefetchMsg on
// each pushed event and returning on the first error so subscribeTmux can
// redial. acked reports whether the handshake completed — the caller's signal
// to reset its backoff.
func streamTmuxSupergraph(ctx context.Context, send func(tea.Msg)) (acked bool, idle time.Duration, _ error) {
	dialer := websocket.Dialer{
		Subprotocols:     []string{"graphql-transport-ws"},
		HandshakeTimeout: 5 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = conn.Close() }()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if err := conn.WriteJSON(map[string]any{"type": "connection_init", "payload": map[string]any{}}); err != nil {
		return false, 0, err
	}
	lastFrame := time.Now()
	for {
		var env struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
		}
		_ = conn.SetReadDeadline(time.Now().Add(readWait))
		if err := conn.ReadJSON(&env); err != nil {
			return acked, time.Since(lastFrame), err
		}
		lastFrame = time.Now()
		switch env.Type {
		case "connection_ack":
			if acked {
				continue
			}
			acked = true
			// Two subscriptions on the one socket: a tmux attach/detach and a
			// claude session state change both mean "refetch the fast lane".
			for id, q := range map[string]string{"tmux": sgTmuxSubQuery, "claude": sgClaudeSubQuery} {
				if err := conn.WriteJSON(map[string]any{
					"id": id, "type": "subscribe",
					"payload": map[string]any{"query": q},
				}); err != nil {
					return acked, time.Since(lastFrame), err
				}
			}
		case "next":
			send(fastRefetchMsg{})
		case "error", "complete":
			return acked, time.Since(lastFrame), errSubEnded
		case "ping":
			_ = conn.WriteJSON(map[string]any{"type": "pong"})
		}
	}
}
