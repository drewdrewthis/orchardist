// nodes.go defines the GraphQL node projection types and project* helpers.
//
// These are the types resolvers return — they carry only the ID and any
// scalar fields embedded directly on the node id (name, paneId). All other
// fields are resolved lazily through the type-specific resolver files (O2).
//
// The project* functions are the single source of truth for id construction
// (S14: one resolver per logical field; no duplicate id-format logic).
package tmux

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TmuxServerNode is the projected GraphQL TmuxServer type.
type TmuxServerNode struct {
	ID         string
	SocketPath string
	// embedded for fast resolver access without a second service call
	serverInfo ServerInfo
	host       HostID
}

// TmuxSessionNode is the projected GraphQL TmuxSession type.
type TmuxSessionNode struct {
	ID   string
	Name string
	// cached for field resolvers that need key decomposition
	key SessionKey
}

// TmuxWindowNode is the projected GraphQL TmuxWindow type.
type TmuxWindowNode struct {
	ID    string
	Index int64
	// cached for field resolvers
	key WindowKey
}

// TmuxPaneNode is the projected GraphQL TmuxPane type.
type TmuxPaneNode struct {
	ID             string
	PaneID         string
	CurrentCommand string
	CurrentPid     *int64
	// cached for field resolvers
	key PaneKey
}

// TmuxClientNode is the projected GraphQL TmuxClient type.
type TmuxClientNode struct {
	ID string
	// cached for field resolvers
	key ClientKey
}

// projectServerNode projects a ServerInfo to a TmuxServerNode.
// id: TmuxServer:<host>:<socketPath>
func projectServerNode(host HostID, info ServerInfo) *TmuxServerNode {
	return &TmuxServerNode{
		ID:         fmt.Sprintf("TmuxServer:%s:%s", host, info.SocketPath),
		SocketPath: info.SocketPath,
		serverInfo: info,
		host:       host,
	}
}

// projectSessionNode projects a Session to a TmuxSessionNode.
// id: TmuxSession:<host>:<name>
func projectSessionNode(s Session) *TmuxSessionNode {
	return &TmuxSessionNode{
		ID:   fmt.Sprintf("TmuxSession:%s:%s", s.Key.Host, s.Key.Name),
		Name: s.Key.Name,
		key:  s.Key,
	}
}

// projectWindowNode projects a Window to a TmuxWindowNode.
// id: TmuxWindow:<host>:<session>:<index>
func projectWindowNode(w Window) *TmuxWindowNode {
	return &TmuxWindowNode{
		ID:    fmt.Sprintf("TmuxWindow:%s:%s:%d", w.Key.Host, w.Key.Session, w.Key.Index),
		Index: int64(w.Key.Index),
		key:   w.Key,
	}
}

// projectPaneNode projects a Pane to a TmuxPaneNode (thin — no pane data beyond id+paneId).
// id: TmuxPane:<host>:<paneId>
func projectPaneNode(p Pane) *TmuxPaneNode {
	n := &TmuxPaneNode{
		ID:             fmt.Sprintf("TmuxPane:%s:%s", p.Key.Host, p.Key.PaneID),
		PaneID:         p.Key.PaneID,
		CurrentCommand: p.CurrentCommand,
		key:            p.Key,
	}
	if p.CurrentPid > 0 {
		pid := int64(p.CurrentPid)
		n.CurrentPid = &pid
	}
	return n
}

// projectClientNode projects a Client to a TmuxClientNode.
// id: TmuxClient:<host>:<clientName>
func projectClientNode(c Client) *TmuxClientNode {
	return &TmuxClientNode{
		ID:  fmt.Sprintf("TmuxClient:%s:%s", c.Key.Host, c.Key.ClientName),
		key: c.Key,
	}
}

// ---- ID parsing helpers ----

// ParsePaneID splits a TmuxPane node id into (host, paneID, ok).
// id format: TmuxPane:<host>:<paneId>
func ParsePaneID(id string) (host, paneID string, ok bool) {
	const prefix = "TmuxPane:"
	rest, found := strings.CutPrefix(id, prefix)
	if !found {
		return "", "", false
	}
	// host may contain ':' (IPv6), but paneID is always a single token like "%26".
	// Split from the right.
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// ParseSessionID splits a TmuxSession node id into (host, name, ok).
func ParseSessionID(id string) (host, name string, ok bool) {
	const prefix = "TmuxSession:"
	rest, found := strings.CutPrefix(id, prefix)
	if !found {
		return "", "", false
	}
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// ParseWindowID splits a TmuxWindow node id into (host, session, index, ok).
// id format: TmuxWindow:<host>:<session>:<index>
func ParseWindowID(id string) (host, session string, index int, ok bool) {
	const prefix = "TmuxWindow:"
	rest, found := strings.CutPrefix(id, prefix)
	if !found {
		return "", "", 0, false
	}
	// index is always an integer at the end; everything before the last ":" is host:session.
	last := strings.LastIndex(rest, ":")
	if last <= 0 {
		return "", "", 0, false
	}
	indexStr := rest[last+1:]
	hostSession := rest[:last]
	n, err := strconv.Atoi(indexStr)
	if err != nil {
		return "", "", 0, false
	}
	// host:session — split on first ":"
	first := strings.Index(hostSession, ":")
	if first <= 0 {
		return "", "", 0, false
	}
	return hostSession[:first], hostSession[first+1:], n, true
}

// ParseClientID splits a TmuxClient node id into (host, clientName, ok).
func ParseClientID(id string) (host, clientName string, ok bool) {
	const prefix = "TmuxClient:"
	rest, found := strings.CutPrefix(id, prefix)
	if !found {
		return "", "", false
	}
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// formatRFC3339OrEmpty formats t as RFC3339 or returns "" for zero.
func formatRFC3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// formatRFC3339OrNil formats t as *string RFC3339 or nil for zero.
func formatRFC3339OrNil(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

