package peerproxy

import (
	"time"

	"github.com/gorilla/websocket"
)

// defaultReadWait bounds how long a read on a peer websocket may sit with
// no frame at all. A peer daemon pings every 10s (internal/server/server.go,
// transport.Websocket KeepAlivePingInterval), so a healthy peer always
// delivers well inside this window — only a half-open socket (peer host
// gone without a FIN) goes silent this long.
//
// Without the deadline that socket parks ReadJSON forever: no error, no
// redial, and every subscription on it is dead while the peer looks merely
// quiet.
const defaultReadWait = 30 * time.Second

// defaultWriteWait bounds how long a single send on a peer websocket may sit
// unacknowledged before it fails. gorilla's WriteJSON blocks on the
// underlying net.Conn write, so a peer whose TCP receive window has filled
// and never drains (half-open socket, dead or stalled peer process) parks
// the write forever — no error, no redial — and worst case parks the
// ctx-teardown `complete` write while it holds writeMu, deadlocking every
// other send (issue #759, the write-side twin of the #732 read hang).
//
// 10s is a generous bound: a healthy peer drains a frame in microseconds, so
// only a stalled receive window reaches this, while the budget stays well
// clear of false positives on slow links. A write that trips it feeds
// failAll, so the subscription errors and Provider.runPeer redials (see
// writeJSON's callers) rather than hanging on a dead connection.
const defaultWriteWait = 10 * time.Second

// armWrite pushes conn's write deadline out by wait before a send. Mirrors
// rearm on the write side: armed fresh inside every writeJSON call so a
// healthy long-lived stream never trips, while a send to a peer whose
// receive window has filled fails with i/o timeout into the teardown/redial
// path instead of parking writeMu forever.
func armWrite(conn *websocket.Conn, wait time.Duration) {
	_ = conn.SetWriteDeadline(time.Now().Add(wait))
}

// armReads sets conn's first read deadline and keeps websocket control
// frames counting as liveness. gorilla consumes ping/pong frames inside
// ReadJSON without ever returning to the caller, so a peer whose keepalive
// is control frames alone would trip the deadline on a demonstrably live
// socket unless the handlers re-arm it. The graphql-transport-ws `ping`
// message is a data frame and is covered by the per-read rearm instead.
func armReads(conn *websocket.Conn, wait time.Duration) {
	rearm(conn, wait)
	ping := conn.PingHandler()
	conn.SetPingHandler(func(data string) error {
		rearm(conn, wait)
		return ping(data) // gorilla's default replies with a pong
	})
	conn.SetPongHandler(func(string) error {
		rearm(conn, wait)
		return nil
	})
}

// rearm pushes conn's read deadline out by wait. Called before every read
// and from the control-frame handlers, so any frame at all keeps the
// connection alive and only total silence errors out into the reconnect
// path.
func rearm(conn *websocket.Conn, wait time.Duration) {
	_ = conn.SetReadDeadline(time.Now().Add(wait))
}
