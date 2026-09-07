package peerproxy

// Hermetic write-deadline tests for the peer websocket client (issue #759,
// the write-side twin of #732). Each stall test holds readWait large (>= 5s,
// above the 2s guard) and shrinks only writeWait (~150ms), so the #755 read
// deadline cannot mask the write path: the trip must originate from a write.
//
// A stalled send is faked at the net.Conn boundary (stallableConn) rather
// than by starving a real TCP socket: loopback does not apply backpressure
// to a stream of small frames (a pong/complete reply is ~25 bytes), so real
// sockets cannot reproduce a parked small write deterministically. The conn
// is the transport boundary the client does not own; stallableConn honours
// the real contract — SetWriteDeadline then a Write returns an i/o-timeout
// net.Error — and with no deadline armed (origin/main) the write parks, which
// is exactly the bug. AC4 uses pass-through writes on the real socket, where
// a deadline left in the past is what a non-re-armed send trips.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// stallableConn wraps a net.Conn. While stalled, every Write parks until the
// armed write deadline and then returns an i/o-timeout net.Error — modelling
// a peer whose TCP receive window has filled and never drains. With no
// deadline armed (the origin/main defect) the Write parks until the conn is
// closed, reproducing the hang. Reads pass straight through.
type stallableConn struct {
	net.Conn
	stall  atomic.Bool
	mu     sync.Mutex
	wdl    time.Time
	closed chan struct{}
	once   sync.Once

	// freezeRead parks the next Read on readHold instead of the socket, so a
	// test can hold an old readLoop inside its read (not erroring on a closed
	// conn) until a redial has landed — isolating the stale-failAll race.
	freezeRead atomic.Bool
	readHold   chan struct{}
	relOnce    sync.Once
}

// Read parks on readHold while frozen (released by releaseReads), so the
// caller's read does not observe the conn closing until the test allows it.
func (s *stallableConn) Read(b []byte) (int, error) {
	if s.freezeRead.Load() {
		<-s.readHold
		return 0, &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	}
	return s.Conn.Read(b)
}

func (s *stallableConn) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.wdl = t
	s.mu.Unlock()
	return s.Conn.SetWriteDeadline(t)
}

func (s *stallableConn) Write(b []byte) (int, error) {
	if !s.stall.Load() {
		return s.Conn.Write(b)
	}
	s.mu.Lock()
	dl := s.wdl
	s.mu.Unlock()
	if dl.IsZero() {
		// No write deadline armed — the origin/main defect: park forever
		// (until the conn is closed during teardown/cleanup).
		<-s.closed
		return 0, &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded}
	}
	timer := time.NewTimer(time.Until(dl))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.closed:
	}
	return 0, &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded}
}

func (s *stallableConn) Close() error {
	s.once.Do(func() { close(s.closed) })
	return s.Conn.Close()
}

// stallGate tracks the stallableConns a client has dialled so a test can
// stall the established connection. Conns dialled after stallAll (redials)
// start un-stalled, which is what lets the redial handshake complete.
type stallGate struct {
	mu    sync.Mutex
	conns []*stallableConn
}

func (g *stallGate) add(c *stallableConn) {
	g.mu.Lock()
	g.conns = append(g.conns, c)
	g.mu.Unlock()
}

func (g *stallGate) stallAll() {
	g.mu.Lock()
	for _, c := range g.conns {
		c.stall.Store(true)
	}
	g.mu.Unlock()
}

// freezeReads parks the next Read on every dialled conn (conns added later —
// redials — are unaffected). releaseReads unblocks them.
func (g *stallGate) freezeReads() {
	g.mu.Lock()
	for _, c := range g.conns {
		c.freezeRead.Store(true)
	}
	g.mu.Unlock()
}

func (g *stallGate) releaseReads() {
	g.mu.Lock()
	for _, c := range g.conns {
		c.relOnce.Do(func() { close(c.readHold) })
	}
	g.mu.Unlock()
}

// stallPeer is fakePeerWS's write-side sibling: the Client dials through a
// stallGate so a test can freeze the established connection's send path, and
// is built with readWait large (so the #755 read deadline cannot mask the
// write) and writeWait shrunk. Returns the Client, the accepted-upgrade
// counter (for redial assertions), and the gate.
func stallPeer(t *testing.T, readWait, writeWait time.Duration, script wsScript) (*Client, *atomic.Int64, *stallGate) {
	t.Helper()

	var dials atomic.Int64
	gate := &stallGate{}
	stop := make(chan struct{})
	up := websocket.Upgrader{Subprotocols: []string{graphqlTransportWSProtocol}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		dials.Add(1)
		script(t, conn, stop)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })

	d := *websocket.DefaultDialer
	base := &net.Dialer{}
	d.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := base.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		sc := &stallableConn{Conn: conn, closed: make(chan struct{}), readHold: make(chan struct{})}
		gate.add(sc)
		return sc, nil
	}

	c := newClient(strings.TrimPrefix(srv.URL, "http://"), false, nil, &d, nil)
	// Set before the first Subscribe — that call starts the read/write path,
	// so the write happens-before every read of the fields.
	c.readWait = readWait
	c.writeWait = writeWait
	t.Cleanup(func() { _ = c.Close() })
	return c, &dials, gate
}

// establishThenIdle completes the handshake and keeps the connection open
// without sending or reading anything — the send path is frozen by the
// stallGate, not by the peer.
func establishThenIdle(t *testing.T, conn *websocket.Conn, stop <-chan struct{}) {
	t.Helper()
	if _, ok := ackAndSubscribe(t, conn); !ok {
		return
	}
	<-stop
}

// subscribeQuery is the query every write-deadline test subscribes with.
const subscribeQuery = `subscription { peerChanged { id } }`

// AC1: once a peer's send path is frozen, a send through writeJSON returns a
// net.Error with Timeout()==true within writeWait rather than parking.
func TestSubscribeWriteToNonReadingPeerTimesOut(t *testing.T) {
	c, _, gate := stallPeer(t, 5*time.Second, 150*time.Millisecond, establishThenIdle)

	// First Subscribe completes the handshake; then freeze the send path so
	// the next subscribe-frame write cannot drain.
	_ = subscribeOrFail(t, c)
	gate.stallAll()

	done := make(chan error, 1)
	go func() {
		_, err := c.Subscribe(context.Background(), subscribeQuery, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write to a frozen send path returned nil — want a write timeout")
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("want a net.Error with Timeout()==true, got %T: %v", err, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe-frame write parked on a non-reading peer — no write deadline (#759)")
	}
}

// AC2: when the readLoop's pong reply times out, the connection tears down
// via failAll — the stream errors and closes, and the next Subscribe redials.
func TestWritePongTimeoutErrorsStreamIntoReconnect(t *testing.T) {
	c, dials, gate := stallPeer(t, 5*time.Second, 150*time.Millisecond, func(t *testing.T, conn *websocket.Conn, stop <-chan struct{}) {
		if _, ok := ackAndSubscribe(t, conn); !ok {
			return
		}
		// Ping steadily; once the send path is frozen a pong reply cannot
		// drain and trips the write deadline.
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := conn.WriteJSON(map[string]any{"type": "ping"}); err != nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	})

	ch := subscribeOrFail(t, c)
	gate.stallAll()

	select {
	case res, ok := <-ch:
		if !ok {
			t.Fatal("stream closed with no error frame on a pong-write timeout")
		}
		if len(res.Errors) == 0 {
			t.Fatalf("pong-write timeout delivered %+v, want an error frame", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pong reply parked on a non-reading peer — no write deadline (#759)")
	}

	// The stream closes after the error frame — the reconnect signal runPeer
	// waits on.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("want the stream closed after the error frame")
		}
	case <-time.After(time.Second):
		t.Fatal("stream never closed after the pong-write timeout — no reconnect")
	}

	// A subsequent Subscribe redials: a new upgrade is accepted.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_, _ = c.Subscribe(ctx2, subscribeQuery, nil)
	if got := dials.Load(); got < 2 {
		t.Fatalf("upgrades accepted = %d, want >= 2 after a pong-write timeout", got)
	}
}

// AC3: a subscribe-frame write timeout resets the cached connection so the
// next Subscribe redials rather than reusing the dead conn.
func TestSubscribeWriteTimeoutRedials(t *testing.T) {
	c, dials, gate := stallPeer(t, 5*time.Second, 150*time.Millisecond, establishThenIdle)

	_ = subscribeOrFail(t, c) // establish (dial 1)
	gate.stallAll()

	// This Subscribe's frame write times out and must reset the conn.
	first := make(chan error, 1)
	go func() {
		_, err := c.Subscribe(context.Background(), subscribeQuery, nil)
		first <- err
	}()
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("subscribe to a frozen send path returned nil — want a write timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe-frame write parked — no write deadline (#759)")
	}

	// The next Subscribe must redial (dial 2) rather than reuse the dead conn.
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	go func() { _, _ = c.Subscribe(ctx3, subscribeQuery, nil) }()

	deadline := time.After(2 * time.Second)
	for dials.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("upgrades accepted = %d, want >= 2 — dead conn was reused, not redialed", dials.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// AC4: healthy traffic is never falsely timed out. The peer reads normally
// and pings across a span exceeding writeWait with each gap under it, so the
// client's pong writes straddle writeWait; only a deadline armed fresh per
// writeJSON call survives. A mutation arming it once cumulatively goes red.
func TestHealthyTrafficNotFalselyTimedOut(t *testing.T) {
	c, _, _ := stallPeer(t, 5*time.Second, 300*time.Millisecond, func(t *testing.T, conn *websocket.Conn, stop <-chan struct{}) {
		id, ok := ackAndSubscribe(t, conn)
		if !ok {
			return
		}
		drainClient(conn) // read the client's pongs — a healthy peer
		for i := 0; i < 6; i++ { // 600ms span (> writeWait), each 100ms gap (< writeWait)
			if err := conn.WriteJSON(map[string]any{"type": "ping"}); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = conn.WriteJSON(nextFrame(id))
		<-stop
	})

	ch := subscribeOrFail(t, c) // never stalled

	select {
	case res, ok := <-ch:
		if !ok {
			t.Fatal("stream closed during healthy ping/pong traffic — write deadline not re-armed per call")
		}
		if len(res.Errors) != 0 {
			t.Fatalf("healthy traffic falsely errored: %v", res.Errors)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no payload delivered after the healthy ping run")
	}
}

// AC8: teardown never parks the write path while holding writeMu. With the
// send path frozen, the teardown `complete` write must fail on the deadline
// and release writeMu, so a concurrent Subscribe returns instead of
// deadlocking. On origin/main the parked write holds writeMu forever and the
// second Subscribe hangs.
func TestTeardownCompleteWriteDoesNotParkWritePath(t *testing.T) {
	c, _, gate := stallPeer(t, 5*time.Second, 150*time.Millisecond, establishThenIdle)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.Subscribe(ctx, subscribeQuery, nil)
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}
	_ = ch

	gate.stallAll()
	// Drive the teardown `complete` write against the frozen send path.
	cancel()

	// A concurrent Subscribe must not block on a writeMu held by a parked write.
	done := make(chan struct{})
	go func() {
		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()
		_, _ = c.Subscribe(ctx2, subscribeQuery, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Subscribe blocked on writeMu — teardown write parked on a dead peer (#759)")
	}
}

// A stale readLoop failAll for a dead conn must not tear down the connection a
// redial established in the meantime. conn1's subscribe-frame write times out
// (failAll(conn1) #1, closing conn1); a redial then opens conn2 with a fresh
// subscription; only then does conn1's parked readLoop error (failAll(conn1)
// #2). With failAll conn-scoped #2 is a no-op, so conn2's stream still
// delivers — without it, #2 would close conn2's channels and drop conn2.
func TestStaleReadLoopErrorDoesNotTearDownRedialedConn(t *testing.T) {
	var connNo atomic.Int64
	sendNext := make(chan struct{}) // released to let conn2 deliver its next frame
	c, _, gate := stallPeer(t, 5*time.Second, 150*time.Millisecond, func(t *testing.T, conn *websocket.Conn, stop <-chan struct{}) {
		n := connNo.Add(1)
		id, ok := ackAndSubscribe(t, conn)
		if !ok {
			return
		}
		if n == 1 {
			// conn1: trickle ignorable frames so its readLoop keeps looping
			// (so freezeReads can park its next Read), never writing back.
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := conn.WriteJSON(map[string]any{"type": "pong"}); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		// conn2 (the redial): deliver a next frame once the test allows it.
		select {
		case <-sendNext:
		case <-stop:
			return
		}
		_ = conn.WriteJSON(nextFrame(id))
		<-stop
	})

	_ = subscribeOrFail(t, c) // establish on conn1

	// Park conn1's readLoop inside its read so it cannot error yet, then
	// freeze its send path so the next subscribe-frame write times out.
	gate.freezeReads()
	time.Sleep(60 * time.Millisecond) // let conn1's readLoop reach the frozen read
	gate.stallAll()

	// This Subscribe's frame write times out -> failAll(conn1) #1 closes conn1.
	first := make(chan error, 1)
	go func() {
		_, err := c.Subscribe(context.Background(), subscribeQuery, nil)
		first <- err
	}()
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("subscribe to a frozen send path returned nil — want a write timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe-frame write parked — no write deadline")
	}

	// Redial: a new Subscribe opens conn2 (un-stalled, un-frozen) with a fresh
	// subscription.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	ch2, err := c.Subscribe(ctx2, subscribeQuery, nil)
	if err != nil {
		t.Fatalf("redial Subscribe: %v", err)
	}

	// Now let conn1's parked readLoop error -> failAll(conn1) #2 (the stale
	// one). Give it time to (wrongly) fire before probing conn2's stream.
	gate.releaseReads()
	time.Sleep(60 * time.Millisecond)

	close(sendNext) // conn2 delivers its next frame
	select {
	case res, ok := <-ch2:
		if !ok {
			t.Fatal("redialed subscription was closed — a stale readLoop failAll tore down the new conn (#759)")
		}
		if len(res.Errors) != 0 {
			t.Fatalf("redialed subscription errored: %v — stale failAll tore down the new conn", res.Errors)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("redialed subscription delivered nothing")
	}
}
