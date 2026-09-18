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
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// fastRefetchMsg asks the update loop to re-read the fast lane. Carries no
// payload: the supergraph event envelope has no snapshot to apply, so the only
// action is a refetch (main.go's update handles it).
type fastRefetchMsg struct{}

const (
	sgTmuxSubQuery   = `subscription { tmuxEvents { type key ts } }`
	sgClaudeSubQuery = `subscription { claudeSessionUpdated { type key ts } }`
)

// streamTmuxSupergraph opens the two supergraph event subscriptions on one
// socket and emits a fastRefetchMsg on every pushed event. It delegates the
// handshake, read loop and redial signal to streamGraphqlWS (subscribe.go),
// differing only in the subscribe frames and the next-frame handler.
func streamTmuxSupergraph(ctx context.Context, send func(tea.Msg)) (bool, time.Duration, error) {
	// Two subscriptions on the one socket: a tmux attach/detach and a claude
	// session state change both mean "refetch the fast lane".
	frames := []map[string]any{
		{"id": "tmux", "type": "subscribe", "payload": map[string]any{"query": sgTmuxSubQuery}},
		{"id": "claude", "type": "subscribe", "payload": map[string]any{"query": sgClaudeSubQuery}},
	}
	return streamGraphqlWS(ctx, frames, func(json.RawMessage) {
		send(fastRefetchMsg{})
	})
}
