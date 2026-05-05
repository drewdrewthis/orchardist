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

// authHeader is the HTTP / WebSocket header carrying the shared-secret
// bearer. Centralised so the client and the server-side middleware
// stay in lockstep.
const authHeader = "Authorization"

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
type Client struct {
	address    string
	secret     string
	httpClient *http.Client
	dialer     *websocket.Dialer
	now        func() time.Time

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

// NewClient constructs a Client targeting `host:port` with the given
// shared secret. The secret may be empty (local-dev mode); in that case
// no Authorization header is sent.
func NewClient(address, secret string) *Client {
	return newClient(address, secret, http.DefaultClient, websocket.DefaultDialer, time.Now)
}

// newClient is the test-friendly constructor. Production callers go
// through NewClient; tests inject a stub HTTP client (httptest), a
// configured dialer (httptest.NewServer URL), and a frozen clock.
func newClient(address, secret string, httpc *http.Client, dialer *websocket.Dialer, clock func() time.Time) *Client {
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
		secret:     secret,
		httpClient: httpc,
		dialer:     &d,
		now:        clock,
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
// The remote endpoint is `http://<address>/graphql`; HTTPS is not
// supported in v1. Workstream F operates on a trusted localhost-only
// LAN — the shared secret is the security boundary, not transport
// encryption.
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
	if c.secret != "" {
		req.Header.Set(authHeader, "Bearer "+c.secret)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return QueryResult{}, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return QueryResult{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return QueryResult{}, fmt.Errorf("unauthorized: peer rejected shared secret")
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
		c.removeSub(id)
		return nil, fmt.Errorf("write subscribe: %w", err)
	}

	// Tear the subscription down when ctx fires.
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn != nil {
			_ = c.writeJSON(conn, map[string]any{"id": id, "type": "complete"})
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
		hdr := http.Header{}
		if c.secret != "" {
			hdr.Set(authHeader, "Bearer "+c.secret)
		}
		conn, _, err := c.dialer.DialContext(ctx, c.wsURL(), hdr)
		if err != nil {
			c.mu.Lock()
			c.connErr = fmt.Errorf("dial %s: %w", c.wsURL(), err)
			c.mu.Unlock()
			return
		}

		// graphql-transport-ws handshake: client → connection_init,
		// server → connection_ack. The server may attach a payload to
		// the ack; we ignore it in v1.
		init := map[string]any{"type": "connection_init"}
		if c.secret != "" {
			init["payload"] = map[string]any{"authToken": c.secret}
		}
		if err := c.writeJSON(conn, init); err != nil {
			_ = conn.Close()
			c.mu.Lock()
			c.connErr = fmt.Errorf("write connection_init: %w", err)
			c.mu.Unlock()
			return
		}
		var ack map[string]any
		if err := conn.ReadJSON(&ack); err != nil {
			_ = conn.Close()
			c.mu.Lock()
			c.connErr = fmt.Errorf("read connection_ack: %w", err)
			c.mu.Unlock()
			return
		}
		if t, _ := ack["type"].(string); t != "connection_ack" {
			_ = conn.Close()
			c.mu.Lock()
			c.connErr = fmt.Errorf("expected connection_ack, got %q", t)
			c.mu.Unlock()
			return
		}

		c.mu.Lock()
		c.conn = conn
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
		if err := conn.ReadJSON(&msg); err != nil {
			c.failAll(fmt.Errorf("ws read: %w", err))
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
			_ = c.writeJSON(conn, map[string]any{"type": "pong"})
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

// failAll closes every subscription channel after pushing one final
// error frame. Called when the websocket itself dies — every active
// stream needs to know.
func (c *Client) failAll(err error) {
	c.mu.Lock()
	subs := c.subs
	c.subs = map[string]chan QueryResult{}
	conn := c.conn
	c.conn = nil
	// Reset the once so the next Subscribe() reopens.
	c.connOnce = &sync.Once{}
	c.connErr = nil
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
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
	return conn.WriteJSON(v)
}

func (c *Client) httpURL() string {
	u := url.URL{Scheme: "http", Host: c.address, Path: "/graphql"}
	return u.String()
}

func (c *Client) wsURL() string {
	u := url.URL{Scheme: "ws", Host: c.address, Path: "/graphql"}
	return u.String()
}
