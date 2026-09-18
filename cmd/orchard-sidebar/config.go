package main

// Backend selection (issue #844). The sidebar reads its data from one of two
// GraphQL servers, chosen once at startup by ORCHARD_SIDEBAR_BACKEND and
// defaulting to today's daemon so a user who sets nothing sees no change:
//
//   - daemon     (default) http://127.0.0.1:7777/graphql — the workView join
//     type the sidebar has always read.
//   - supergraph           http://127.0.0.1:7788/graphql — typed leaves
//     (claudeInstances/tmuxSessions/tmuxPanes/issue/pullRequest), no workView;
//     a separate adapter (supergraph.go) fills the same row model from them.
//
// ORCHARD_SIDEBAR_GRAPHQL_URL overrides the HTTP endpoint for either backend;
// the WS endpoint is always DERIVED from it by a scheme swap (http→ws,
// https→wss) so a custom host/port/path carries through to the push lane
// without a second variable to keep in sync.

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	backendDaemon     = "daemon"
	backendSupergraph = "supergraph"

	daemonHTTPURL     = "http://127.0.0.1:7777/graphql"
	supergraphHTTPURL = "http://127.0.0.1:7788/graphql"
)

// endpoints is the resolved, validated backend configuration: which server,
// its HTTP GraphQL URL, and the WS URL derived from it.
type endpoints struct {
	backend string
	httpURL string
	wsURL   string
}

// resolveEndpoints turns the environment into a validated endpoints value or a
// startup error. It is pure over its getenv argument (os.Getenv in main, a map
// lookup in tests) and performs no I/O, so the failure cases can be asserted
// without a network or a tmux server — the sidebar calls it before any of
// either happens. An unknown backend, or a URL override that is not a valid
// http(s) URL, is a hard error rather than a silent fallback.
func resolveEndpoints(getenv func(string) string) (endpoints, error) {
	backend := getenv("ORCHARD_SIDEBAR_BACKEND")
	if backend == "" {
		backend = backendDaemon
	}
	var defaultHTTP string
	switch backend {
	case backendDaemon:
		defaultHTTP = daemonHTTPURL
	case backendSupergraph:
		defaultHTTP = supergraphHTTPURL
	default:
		return endpoints{}, fmt.Errorf(
			"ORCHARD_SIDEBAR_BACKEND=%q is not a valid backend; use %q or %q",
			backend, backendDaemon, backendSupergraph)
	}

	httpURL := defaultHTTP
	if override := getenv("ORCHARD_SIDEBAR_GRAPHQL_URL"); override != "" {
		httpURL = override
	}

	wsURL, err := wsFromHTTP(httpURL)
	if err != nil {
		return endpoints{}, err
	}
	return endpoints{backend: backend, httpURL: httpURL, wsURL: wsURL}, nil
}

// wsFromHTTP derives the websocket URL from the HTTP GraphQL URL by swapping
// only the scheme, preserving host, port and path. This is a general rule, not
// a match against the two known localhost constants, so an override on any
// host/port/path derives the right WS endpoint. A URL that does not parse, or
// whose scheme is not http/https, is the malformed-override failure case.
func wsFromHTTP(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("ORCHARD_SIDEBAR_GRAPHQL_URL=%q is not a valid URL: %w", raw, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf(
			"ORCHARD_SIDEBAR_GRAPHQL_URL=%q must be an http:// or https:// URL", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("ORCHARD_SIDEBAR_GRAPHQL_URL=%q has no host", raw)
	}
	return u.String(), nil
}

// healthURL is the supergraph /health endpoint, derived from the resolved HTTP
// GraphQL URL by replacing the trailing /graphql path. Set by applyBackend for
// the supergraph backend; empty for the daemon backend (which surfaces its
// failure reason inline via workView.meta instead).
var healthURL string

// pushLaneCarriesSnapshot is whether the push lane delivers a full tmux
// snapshot (the daemon's tmuxSessionsChanged, folded by applySessions into a
// fresh pane map + attach flags) or only bare "something changed" events (the
// supergraph lane's fastRefetchMsg, which carries no map). applyFast reads it
// to decide who owns the pane map / attach flags when the push lane is live:
// on the daemon the push snapshot is fresher than the poll, so it wins; on
// supergraph the contemporaneous fast refetch is the only map source, so the
// poll must always win. True by default (daemon); applyBackend clears it for
// supergraph.
var pushLaneCarriesSnapshot = true

// applyBackend wires the resolved configuration into the package-level read
// sites. For the daemon backend this only sets the two URLs (the default
// behavior). For supergraph it additionally swaps the fast/slow fetchers and
// the subscription stream for the adapter implementations, and derives the
// /health URL. Called once from main before tea.NewProgram.
func applyBackend(cfg endpoints) {
	graphqlURL = cfg.httpURL
	wsURL = cfg.wsURL
	if cfg.backend == backendSupergraph {
		// Trim only the path's trailing /graphql, not the raw URL string: a
		// query-bearing override (e.g. ...?token=x) has no literal "/graphql"
		// suffix, so a string TrimSuffix left it untouched and the "/health"
		// landed after the query, producing a URL that still resolves to the
		// /graphql path.
		if u, err := url.Parse(cfg.httpURL); err == nil { // already validated by resolveEndpoints
			u.Path = strings.TrimSuffix(u.Path, "/graphql") + "/health"
			healthURL = u.String()
		} else {
			healthURL = strings.TrimSuffix(cfg.httpURL, "/graphql") + "/health"
		}
		fetchFast = fetchFastSupergraph
		fetchSlow = fetchSlowSupergraph
		streamTmux = streamTmuxSupergraph
		// The supergraph push lane emits bare events, not a snapshot, so the
		// fast lane stays the pane-map/attach authority even while push is live.
		pushLaneCarriesSnapshot = false
	}
}
