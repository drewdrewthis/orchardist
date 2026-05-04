package peerproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// NodeID is the cache key the peerproxy provider uses. It is the
// globally-unique GraphQL node id (e.g. `TmuxPane:peer-1:%26`) — the
// same string the resolver receives from `Query.node(id)`.
type NodeID string

// PeerNode is the value type carried back from a peer. We keep the
// payload as raw JSON so the provider stays decoupled from the
// schema-generated Node concrete types — the resolver layer maps the
// raw bytes onto the right shape.
//
// TypeName is the GraphQL `__typename` returned by the peer; it lets
// resolvers project the correct Node implementation without parsing
// the id again.
type PeerNode struct {
	ID       NodeID
	TypeName string
	Raw      json.RawMessage
}

// PeerEvent is what the adapter emits on its Subscribe channel. One
// event per remote invalidation; resolvers re-fetch on receipt.
type PeerEvent struct {
	Peer   string
	NodeID NodeID
	Reason string
	At     time.Time
	// Node is the materialised payload. Subscribers that care about the
	// most recent state can read it without an extra round-trip; it may
	// be nil when the peer pushed only an id.
	Node *PeerNode
}

// PeerAdapter wraps a single peer's transport and exposes the
// reachability state the local resolver needs. Nothing about
// concurrency, caching, or fan-out lives here — the Provider above it
// owns those concerns.
type PeerAdapter struct {
	peer   PeerConfig
	client *Client
	now    func() time.Time

	mu             sync.RWMutex
	reachable      bool
	lastError      error
	lastReachedAt  time.Time
	lastProbedAt   time.Time
	subscriptionOn bool
}

// NewPeerAdapter wires a PeerConfig up to a transport Client.
func NewPeerAdapter(peer PeerConfig, client *Client) *PeerAdapter {
	return newPeerAdapter(peer, client, time.Now)
}

func newPeerAdapter(peer PeerConfig, client *Client, clock func() time.Time) *PeerAdapter {
	if clock == nil {
		clock = time.Now
	}
	return &PeerAdapter{peer: peer, client: client, now: clock}
}

// Peer returns the underlying configuration row.
func (a *PeerAdapter) Peer() PeerConfig { return a.peer }

// Reachable returns the last-known reachability and the time of the
// last successful probe. Reachable is false until the first Probe
// succeeds.
func (a *PeerAdapter) Reachable() (bool, time.Time) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reachable, a.lastReachedAt
}

// Probe runs a one-shot health query against the peer and records the
// outcome. Used by the dashboard to decide whether to mark the peer
// reachable; safe to call concurrently with Subscribe.
func (a *PeerAdapter) Probe(ctx context.Context) error {
	err := a.client.Ping(ctx)
	now := a.now()
	a.mu.Lock()
	a.lastProbedAt = now
	a.lastError = err
	if err == nil {
		a.reachable = true
		a.lastReachedAt = now
	} else {
		a.reachable = false
	}
	a.mu.Unlock()
	return err
}

// FetchNode proxies a single `node(id)` lookup through the client.
// Returns a PeerNode wrapping the raw payload + __typename. The
// returned PeerNode.ID echoes the requested id so callers can match
// responses to requests when batching.
func (a *PeerAdapter) FetchNode(ctx context.Context, id NodeID) (*PeerNode, error) {
	const q = `query NodeProxy($id: ID!) {
  node(id: $id) {
    __typename
    id
  }
}`
	res, err := a.client.Query(ctx, q, map[string]any{"id": string(id)})
	if err != nil {
		return nil, err
	}
	if err := res.AsError(); err != nil {
		return nil, err
	}
	var data struct {
		Node json.RawMessage `json:"node"`
	}
	if err := json.Unmarshal(res.Data, &data); err != nil {
		return nil, fmt.Errorf("decode node payload: %w", err)
	}
	if len(data.Node) == 0 || string(data.Node) == "null" {
		return nil, nil
	}
	var meta struct {
		TypeName string `json:"__typename"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(data.Node, &meta); err != nil {
		return nil, fmt.Errorf("decode node meta: %w", err)
	}
	if meta.ID == "" {
		meta.ID = string(id)
	}
	return &PeerNode{
		ID:       NodeID(meta.ID),
		TypeName: meta.TypeName,
		Raw:      data.Node,
	}, nil
}

// Subscribe opens a long-running subscription against the peer's
// `peer` (or another invalidation-emitting field) and converts each
// pushed payload into a PeerEvent on the returned channel. The channel
// closes when ctx is cancelled or the underlying websocket dies.
//
// The remote subscription used here is `subscription { peer(host: "*")
// { id __typename } }` — the wildcard "*" tells the remote daemon to
// emit every invalidation it observes. A future v2 may grow filters.
func (a *PeerAdapter) Subscribe(ctx context.Context) (<-chan PeerEvent, error) {
	const q = `subscription PeerStream {
  peer(host: "*") {
    __typename
    id
  }
}`
	stream, err := a.client.Subscribe(ctx, q, nil)
	if err != nil {
		return nil, fmt.Errorf("open peer subscription: %w", err)
	}
	out := make(chan PeerEvent, 16)
	go func() {
		defer close(out)
		for r := range stream {
			if r.AsError() != nil {
				// Surface the error as a synthetic event so subscribers
				// that care about reachability can react. The event has
				// an empty NodeID because the cause is connection-wide.
				select {
				case out <- PeerEvent{
					Peer:   a.peer.Name,
					Reason: r.AsError().Error(),
					At:     a.now(),
				}:
				default:
				}
				continue
			}
			ev, ok := decodePeerEvent(a.peer.Name, r.Data, a.now())
			if !ok {
				continue
			}
			select {
			case out <- ev:
			default:
				// Slow subscriber — drop. Same policy as host/config.
			}
		}
	}()
	a.mu.Lock()
	a.subscriptionOn = true
	a.mu.Unlock()
	return out, nil
}

// decodePeerEvent extracts the node id + __typename from a subscription
// `data` payload. Returns false when the payload is null (which is
// allowed — the schema field is nullable and a remote daemon may push
// a heartbeat with no node).
func decodePeerEvent(peerName string, data json.RawMessage, at time.Time) (PeerEvent, bool) {
	var envelope struct {
		Peer json.RawMessage `json:"peer"`
	}
	if len(data) == 0 {
		return PeerEvent{}, false
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return PeerEvent{}, false
	}
	if len(envelope.Peer) == 0 || string(envelope.Peer) == "null" {
		return PeerEvent{}, false
	}
	var meta struct {
		ID       string `json:"id"`
		TypeName string `json:"__typename"`
	}
	if err := json.Unmarshal(envelope.Peer, &meta); err != nil {
		return PeerEvent{}, false
	}
	return PeerEvent{
		Peer:   peerName,
		NodeID: NodeID(meta.ID),
		Reason: "remote-push",
		At:     at,
		Node: &PeerNode{
			ID:       NodeID(meta.ID),
			TypeName: meta.TypeName,
			Raw:      envelope.Peer,
		},
	}, true
}

// HostFromNodeID extracts the host segment from a GraphQL Node id of
// the form `<TypeName>:<host>:<rest>`. Returns "" when the id has
// fewer than two ":" separators (e.g. the "Host:<machineId>" shape,
// where the host segment is the rest).
//
// Special-case: ids that begin with "Host:" carry the host as the
// remainder, so we treat the entire suffix after the colon as the
// host. Every other typename follows the three-segment convention.
func HostFromNodeID(id string) string {
	if id == "" {
		return ""
	}
	first := strings.IndexByte(id, ':')
	if first < 0 {
		return ""
	}
	typeName := id[:first]
	rest := id[first+1:]
	if typeName == "Host" {
		return rest
	}
	second := strings.IndexByte(rest, ':')
	if second < 0 {
		return ""
	}
	return rest[:second]
}
