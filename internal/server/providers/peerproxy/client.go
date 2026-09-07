package peerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// graphqlTransportWSProtocol is the websocket subprotocol token Apollo
// (and gqlgen via newer transports) negotiates for subscription
// streaming. We only support this one — graphql-ws (the legacy
// subprotocol) is intentionally not implemented.
const graphqlTransportWSProtocol = "graphql-transport-ws"

// QueryResult is the payload of a single GraphQL response. The Data
// field is left as a json.RawMessage so callers can decode into the
// concrete shape they expect.
//
// Errors are surfaced verbatim from the remote daemon — peerproxy does
// not transform them. Callers that want a Go error from a non-empty
// Errors slice should call Result.AsError().
type QueryResult struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []GraphQLError  `json:"errors,omitempty"`
}

// AsError flattens any GraphQL errors into a single Go error. Returns
// nil when the result has no errors. Used by callers that treat any
// error as fatal (e.g. the node-lookup proxy).
func (r QueryResult) AsError() error {
	if len(r.Errors) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		msgs = append(msgs, e.Message)
	}
	return fmt.Errorf("graphql errors: %v", msgs)
}

// GraphQLError mirrors the standard error shape (message + path +
// locations + extensions). Only Message is consumed by peerproxy today;
// the rest pass through verbatim for diagnostics.
type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Locations  []any          `json:"locations,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Client is a single peer's transport. One client per peer is enough —
// the websocket multiplexes any number of concurrent subscriptions, and
// the HTTP client handles one-shot queries.
//
// Lifecycle: NewClient is cheap (no I/O). The websocket is opened on
// the first Subscribe() call and reused across subsequent calls. Close
// tears it down; subsequent Subscribe() calls reopen.
//
// When tls is true the client speaks HTTPS for queries and WSS for
// subscriptions; the underlying http.Client and websocket.Dialer carry
// the TLS configuration the caller supplied (system trust store by
// default — production code MUST verify certificates).
type Client struct {
	address    string
	tls        bool
	httpClient *http.Client
	dialer     *websocket.Dialer
	now        func() time.Time

	// readWait bounds how long a read may sit with no frame at all —
	// see defaultReadWait in keepalive.go. Immutable after construction;
	// tests shrink it before the first Subscribe, which is what starts
	// the read loop.
	readWait time.Duration

	// writeWait bounds how long a single send may park before it fails —
	// see defaultWriteWait in keepalive.go. Armed fresh per writeJSON call.
	// Immutable after construction; tests shrink it before the first
	// Subscribe, mirroring readWait.
	writeWait time.Duration

	mu       sync.Mutex
	conn     *websocket.Conn
	connOnce *sync.Once
	connErr  error
	nextSub  uint64
	subs     map[string]chan QueryResult
	closed   bool

	// writeMu serialises every send on `conn`. gorilla/websocket
	// rejects concurrent writers, and the readLoop's pong replies
	// race with subscription frames otherwise.
	writeMu sync.Mutex
}

// NewClient constructs a Client targeting `host:port`. When tls is true
// the client speaks HTTPS/WSS using the default trust store — callers
// needing a custom tls.Config can construct via newClient with a
// configured http.Client + websocket.Dialer.
//
// No bearer-secret is supported: peer authentication is delegated to
// the transport (TLS + boxd-fronted endpoint allowlists). See issue #412.
func NewClient(address string, tls bool) *Client {
	return newClient(address, tls, http.DefaultClient, websocket.DefaultDialer, time.Now)
}

// newClient is the test-friendly constructor. Production callers go
// through NewClient; tests inject a stub HTTP client (httptest), a
// configured dialer (httptest.NewServer URL), and a frozen clock.
func newClient(address string, tls bool, httpc *http.Client, dialer *websocket.Dialer, clock func() time.Time) *Client {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	if clock == nil {
		clock = time.Now
	}
	d := *dialer
	d.Subprotocols = []string{graphqlTransportWSProtocol}
	if d.HandshakeTimeout == 0 {
		d.HandshakeTimeout = 5 * time.Second
	}
	return &Client{
		address:    address,
		tls:        tls,
		httpClient: httpc,
		dialer:     &d,
		now:        clock,
		readWait:   defaultReadWait,
		writeWait:  defaultWriteWait,
		connOnce:   &sync.Once{},
		subs:       map[string]chan QueryResult{},
	}
}

// Address returns the configured `host:port` for diagnostics.
func (c *Client) Address() string { return c.address }

// Query issues a one-shot GraphQL POST and returns the decoded result.
// Used for transparent node-lookup proxying — Subscribe() is for
// long-lived streams.
//
// The endpoint URL is `<scheme>://<address>/graphql` where scheme is
// `https` when the client was constructed with tls=true (boxd-fronted
// peers) and `http` otherwise (trusted-LAN peers — the LAN itself is
// then the security boundary).
func (c *Client) Query(ctx context.Context, query string, variables map[string]any) (QueryResult, error) {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return QueryResult{}, fmt.Errorf("marshal query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.httpURL(), bytes.NewReader(body))
	if err != nil {
		return QueryResult{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return QueryResult{}, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return QueryResult{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return QueryResult{}, fmt.Errorf("http status %d: %s", resp.StatusCode, string(raw))
	}
	var out QueryResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return QueryResult{}, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// Ping is a cheap reachability probe. It POSTs `{ health { status } }`
// and returns nil on success. Adapter uses this to decide whether the
// peer is reachable enough to mark `Host.reachable = true`.
func (c *Client) Ping(ctx context.Context) error {
	res, err := c.Query(ctx, `{ health { status } }`, nil)
	if err != nil {
		return err
	}
	return res.AsError()
}

// Subscribe opens a streaming GraphQL subscription. The returned
// channel emits one QueryResult per `next` frame received from the
// peer; it closes when the subscription completes or ctx is cancelled.
//
// Errors during open (websocket dial / connection_init / subscribe
// frame) are returned synchronously. Errors mid-stream surface as a
// final QueryResult whose Errors slice is non-empty, then the channel
// closes.
func (c *Client) Subscribe(ctx context.Context, query string, variables map[string]any) (<-chan QueryResult, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("client closed")
	}
	// A write-deadline failAll (#759) can nil c.conn between ensureConn
	// returning and this lock; without this check writeJSON(nil, ...)
	// below would deref a nil conn.
	if c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("connection lost before subscribe")
	}
	c.nextSub++
	id := fmt.Sprintf("sub-%d", c.nextSub)
	ch := make(chan QueryResult, 8)
	c.subs[id] = ch
	conn := c.conn
	c.mu.Unlock()

	subscribeMsg := map[string]any{
		"id":   id,
		"type": "subscribe",
		"payload": map[string]any{
			"query":     query,
			"variables": variables,
		},
	}
	if err := c.writeJSON(conn, subscribeMsg); err != nil {
		// A post-handshake write error means the connection is dead (e.g. a
		// stalled peer tripped the write deadline). failAll errors every open
		// stream and resets connOnce so the next Subscribe redials instead of
		// reusing this dead conn — writeJSON has already released writeMu.
		werr := fmt.Errorf("write subscribe: %w", err)
		c.failAll(conn, werr)
		// failAll no-ops when a redial already superseded conn; drop this
		// call's own entry either way so it cannot be orphaned in c.subs.
		c.removeSub(id)
		return nil, werr
	}

	// Tear the subscription down when ctx fires. Uses the conn this
	// subscription was registered on (captured above), not a late re-read
	// of c.conn — a re-read could pick up a redialed conn, and a failure
	// on that write would then tear down the replacement instead of being
	// the no-op failAll's conn check expects.
	go func() {
		<-ctx.Done()
		if conn != nil {
			// Without a write deadline this send parks forever while holding
			// writeMu on a stalled peer, deadlocking every other send (#759).
			// The deadline bounds it; a timeout tears the conn down so the
			// next Subscribe redials.
			if err := c.writeJSON(conn, map[string]any{"id": id, "type": "complete"}); err != nil {
				c.failAll(conn, fmt.Errorf("write complete: %w", err))
			}
		}
		c.removeSub(id)
	}()

	return ch, nil
}

// Close tears down the websocket and closes every active subscription
// channel. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	subs := c.subs
	c.subs = map[string]chan QueryResult{}
	c.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// ensureConn opens the websocket on demand. The first call performs the
// handshake and starts the read loop; subsequent calls return the
// cached connection (or the cached error if the open failed).
func (c *Client) ensureConn(ctx context.Context) error {
	c.mu.Lock()
	once := c.connOnce
	c.mu.Unlock()

	once.Do(func() {
		conn, _, err := c.dialer.DialContext(ctx, c.wsURL(), nil)
		if err != nil {
			c.failOpen(nil, fmt.Errorf("dial %s: %w", c.wsURL(), err))
			return
		}
		// HandshakeTimeout is spent by the time the upgrade completes, so
		// the ack read below needs its own bound.
		armReads(conn, c.readWait)

		// graphql-transport-ws handshake: client → connection_init,
		// server → connection_ack. The server may attach a payload to
		// the ack; we ignore it in v1.
		init := map[string]any{"type": "connection_init"}
		if err := c.writeJSON(conn, init); err != nil {
			c.failOpen(conn, fmt.Errorf("write connection_init: %w", err))
			return
		}
		var ack map[string]any
		if err := conn.ReadJSON(&ack); err != nil {
			c.failOpen(conn, fmt.Errorf("read connection_ack: %w", err))
			return
		}
		if t, _ := ack["type"].(string); t != "connection_ack" {
			c.failOpen(conn, fmt.Errorf("expected connection_ack, got %q", t))
			return
		}

		c.mu.Lock()
		c.conn = conn
		c.connErr = nil
		c.mu.Unlock()
		go c.readLoop(conn)
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connErr
}

// readLoop drains incoming frames and routes each `next` / `complete`
// to the right subscription channel. Exits when the connection drops.
func (c *Client) readLoop(conn *websocket.Conn) {
	for {
		var msg struct {
			ID      string          `json:"id"`
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload,omitempty"`
		}
		// Every read gets a fresh deadline, so any frame — data or
		// keepalive — re-arms it; only total silence trips it.
		rearm(conn, c.readWait)
		if err := conn.ReadJSON(&msg); err != nil {
			c.failAll(conn, fmt.Errorf("ws read: %w", err))
			return
		}
		switch msg.Type {
		case "next":
			var payload QueryResult
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				c.failOne(msg.ID, fmt.Errorf("decode next payload: %w", err))
				continue
			}
			c.deliver(msg.ID, payload)
		case "error":
			var errs []GraphQLError
			_ = json.Unmarshal(msg.Payload, &errs)
			c.deliver(msg.ID, QueryResult{Errors: errs})
			c.removeSub(msg.ID)
		case "complete":
			c.removeSub(msg.ID)
		case "ping":
			// A parked pong reply would wedge the read loop (and writeMu) on
			// a peer that stopped reading; the write deadline fails it
			// instead, and failAll tears the conn down into the redial path.
			if err := c.writeJSON(conn, map[string]any{"type": "pong"}); err != nil {
				c.failAll(conn, fmt.Errorf("write pong: %w", err))
				return
			}
		case "pong":
			// no-op
		default:
			// Unknown frame — ignore. graphql-transport-ws is small
			// enough that anything we don't recognise is either a
			// future extension or noise.
		}
	}
}

func (c *Client) deliver(id string, r QueryResult) {
	c.mu.Lock()
	ch, ok := c.subs[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- r:
	default:
		// Subscriber lagging — drop the event rather than block the
		// read loop. Matches the host/config provider drop policy.
	}
}

func (c *Client) removeSub(id string) {
	c.mu.Lock()
	ch, ok := c.subs[id]
	if ok {
		delete(c.subs, id)
	}
	c.mu.Unlock()
	if ok {
		close(ch)
	}
}

// failOpen records why an open failed and re-arms connOnce so the next
// Subscribe redials. Without the re-arm the first failure is cached for
// the life of the Client: Provider.runPeer retries Subscribe every 5s and
// would get the same stale error back forever, so a handshake that hits
// readWait would trade a hang for a permanently dead peer.
func (c *Client) failOpen(conn *websocket.Conn, err error) {
	if conn != nil {
		_ = conn.Close()
	}
	c.mu.Lock()
	c.connErr = err
	c.connOnce = &sync.Once{}
	c.mu.Unlock()
}

// failAll closes every subscription channel after pushing one final
// error frame. Called when the websocket itself dies — every active
// stream needs to know.
//
// conn-scoped: the caller passes the connection it wrote or read on, and
// failAll no-ops unless that is still the client's current connection. A
// single dead conn produces two failAll calls racing a redial — the write
// timeout (Subscribe / teardown / pong) closes the conn, then the old
// readLoop's ReadJSON errors on that closed conn. Without the scope check the
// second call would close the channels and drop the connection of whatever
// Subscribe redialed in between, killing a healthy new stream.
func (c *Client) failAll(conn *websocket.Conn, err error) {
	c.mu.Lock()
	if c.conn != conn {
		// Already torn down, or superseded by a redial — not ours to touch.
		c.mu.Unlock()
		return
	}
	subs := c.subs
	c.subs = map[string]chan QueryResult{}
	c.conn = nil
	// Reset the once so the next Subscribe() reopens.
	c.connOnce = &sync.Once{}
	c.connErr = nil
	c.mu.Unlock()

	_ = conn.Close()
	for _, ch := range subs {
		select {
		case ch <- QueryResult{Errors: []GraphQLError{{Message: err.Error()}}}:
		default:
		}
		close(ch)
	}
}

func (c *Client) failOne(id string, err error) {
	c.mu.Lock()
	ch, ok := c.subs[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- QueryResult{Errors: []GraphQLError{{Message: err.Error()}}}:
	default:
	}
	c.removeSub(id)
}

// writeJSON serialises a single frame on conn under writeMu. Every
// outbound frame goes through this helper — the readLoop, the
// subscription teardown goroutine, and the connection-init handshake
// are all parallel writers from gorilla's perspective.
func (c *Client) writeJSON(conn *websocket.Conn, v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	// Armed fresh per call so a stalled receive window fails the send with
	// i/o timeout instead of parking writeMu forever (#759). Callers route a
	// post-handshake write error into failAll. Kept tiny and lock-local: the
	// deadline is the only thing writeMu needs to guard besides the write.
	armWrite(conn, c.writeWait)
	return conn.WriteJSON(v)
}

func (c *Client) httpURL() string {
	scheme := "http"
	if c.tls {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: c.address, Path: "/graphql"}
	return u.String()
}

func (c *Client) wsURL() string {
	scheme := "ws"
	if c.tls {
		scheme = "wss"
	}
	u := url.URL{Scheme: scheme, Host: c.address, Path: "/graphql"}
	return u.String()
}
