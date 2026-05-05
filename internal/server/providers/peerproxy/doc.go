// Package peerproxy implements the federation provider — the layer that
// turns a remote orchard daemon into a backend just like git, tmux or
// ps. Per ADR-011 §7, federation is not a special protocol: the local
// daemon talks GraphQL to the remote daemon and the resolvers cannot
// tell the difference between a local provider and the proxy.
//
// Layering (see ADR-011 §2):
//
//	provider.go — Provider[NodeID, Node]; resolver-facing surface,
//	              fans out to per-peer adapters.
//	adapter.go  — one Adapter per peer; owns its Subscribe loop and
//	              fans the watcher channel out as InvalidationEvent.
//	client.go   — websocket + HTTP transport; speaks the
//	              graphql-transport-ws subprotocol for subscriptions
//	              and POSTs JSON for one-shot queries. Owns the
//	              shared-secret bearer header.
//	config.go   — loads peer addresses + shared secret from
//	              ~/.config/orchard/config.json. Read-only.
//
// Auth (§11): the client attaches `Authorization: Bearer <secret>` to
// every websocket open and HTTP request when a non-empty secret is
// configured. The server side mirrors this — when `peer_secret` is set
// in config the daemon enforces the bearer; when unset, no auth is
// required (local-dev escape hatch).
//
// Failure model: the adapter never silently swallows errors. A failed
// websocket open marks the peer unreachable, the next dashboard query
// reflects `reachable: false`, and Subscribe() retries on a coarse
// backoff. Cross-host node lookups bubble the underlying network error
// to the resolver.
package peerproxy
