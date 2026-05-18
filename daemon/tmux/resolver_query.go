// resolver_query.go contains the Query-level resolvers for the tmux domain:
//   - Query.tmuxServer
//   - Query.tmuxSessions
//   - Query.tmuxPanes
//   - Query.tmux (S16b pass-through with L4 guards)
//
// All reads go through in-process caches — no shellout in field-resolver hot
// paths (L4). The pass-through is the only exception; it is guard-wrapped per
// S16b: top-level only, 30s timeout, concurrency cap 4.
package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// ---- pass-through guard state (S16b) ----

// passthroughInflight tracks current in-flight pass-through calls.
// cap is enforced atomically; no mutex needed for the counter alone.
var passthroughInflight atomic.Int32

const (
	// passthrough concurrency cap per S16b.
	passthroughCap     = 4
	// passthrough timeout per S16b.
	passthroughTimeout = 30 * time.Second
)

// PassthroughResult is the shape returned by Query.tmux pass-through.
type PassthroughResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// QueryTmuxServer resolves Query.tmuxServer.
// Returns nil when no tmux daemon is reachable (server.Alive == false).
func QueryTmuxServer(ctx context.Context, svc TmuxService) (*TmuxServerNode, error) {
	if svc == nil {
		return nil, nil
	}
	info := svc.Server()
	return projectServerNode(svc.Host(), info), nil
}

// QueryTmuxSessions resolves Query.tmuxSessions with optional filter.
// All reads are in-process cache — no shellout (L4).
func QueryTmuxSessions(ctx context.Context, svc TmuxService, filter *TmuxSessionFilterInput) ([]*TmuxSessionNode, error) {
	if svc == nil {
		return []*TmuxSessionNode{}, nil
	}
	all := svc.AllSessions()
	out := make([]*TmuxSessionNode, 0, len(all))
	for _, s := range all {
		if !sessionMatchesFilter(s, filter) {
			continue
		}
		out = append(out, projectSessionNode(s))
	}
	return out, nil
}

// QueryTmuxPanes resolves Query.tmuxPanes with optional filter.
// The cwd and command axes use the provider's secondary-axis methods which
// go through PanesByCwd / PanesByCommand respectively — not Snapshot() (R3, O1).
func QueryTmuxPanes(ctx context.Context, svc TmuxService, filter *TmuxPaneFilterInput, ps PanePsGetter) ([]*TmuxPaneNode, error) {
	if svc == nil {
		return []*TmuxPaneNode{}, nil
	}

	var raw []Pane

	// cwd and command axes use secondary-axis methods (ADR-022, R3).
	if filter != nil && filter.Cwd != nil && *filter.Cwd != "" {
		raw = svc.PanesByCwd(string(svc.Host()), *filter.Cwd, ps)
	} else if filter != nil && filter.Command != nil && *filter.Command != "" {
		raw = svc.PanesByCommand(string(svc.Host()), *filter.Command, ps)
	} else {
		raw = svc.AllPanes()
	}

	out := projectPaneNodes(raw, filter)
	return out, nil
}

// QueryTmuxPassthrough resolves Query.tmux (S16b pass-through).
//
// Guards (S16b):
//  1. Top-level query only — this is enforced structurally: the field is declared as
//     a top-level Query field in schema.graphql, not inside any object type.
//  2. 30s timeout.
//  3. Concurrency cap of 4 in-flight calls.
//  4. Not cached, not loader-batched, not subscribable.
func QueryTmuxPassthrough(ctx context.Context, args []string, runner CommandRunner) (json.RawMessage, error) {
	// Guard: concurrency cap (S16b).
	cur := passthroughInflight.Add(1)
	defer passthroughInflight.Add(-1)
	if cur > passthroughCap {
		return nil, fmt.Errorf("tmux pass-through: concurrency cap (%d) exceeded", passthroughCap)
	}

	// Guard: timeout (S16b).
	ctx, cancel := context.WithTimeout(ctx, passthroughTimeout)
	defer cancel()

	if len(args) == 0 {
		return nil, fmt.Errorf("tmux pass-through: args must not be empty")
	}
	// Sanitize: first arg must not be a flag to prevent injection.
	if strings.HasPrefix(args[0], "-") {
		return nil, fmt.Errorf("tmux pass-through: first arg must be a subcommand, not a flag")
	}

	out, err := runner.Run(ctx, "tmux", args...)
	res := PassthroughResult{
		Stdout: strings.TrimRight(string(out), "\n"),
	}
	if err != nil {
		res.Stderr = err.Error()
		res.ExitCode = 1
	}
	b, jsonErr := json.Marshal(res)
	if jsonErr != nil {
		return nil, fmt.Errorf("tmux pass-through: marshal result: %w", jsonErr)
	}
	return json.RawMessage(b), nil
}

// ---- Filter helpers ----

// TmuxSessionFilterInput mirrors the GraphQL TmuxSessionFilter input.
type TmuxSessionFilterInput struct {
	NameIn            []string
	AttachedOnly      *bool
	ActiveAttachedOnly *bool
}

func sessionMatchesFilter(s Session, f *TmuxSessionFilterInput) bool {
	if f == nil {
		return true
	}
	if len(f.NameIn) > 0 && !sliceContains(f.NameIn, s.Key.Name) {
		return false
	}
	if f.AttachedOnly != nil && *f.AttachedOnly && !s.Attached {
		return false
	}
	if f.ActiveAttachedOnly != nil && *f.ActiveAttachedOnly && !s.Attached {
		return false
	}
	return true
}

// TmuxPaneFilterInput mirrors the GraphQL TmuxPaneFilter input.
type TmuxPaneFilterInput struct {
	PaneIDIn         []string
	CurrentCommandIn []string
	SessionIn        []string
	TitleContains    *string
	Dead             *bool
	Cwd              *string
	Command          *string
}

func paneMatchesScalarFilter(p Pane, f *TmuxPaneFilterInput) bool {
	if f == nil {
		return true
	}
	if len(f.PaneIDIn) > 0 && !sliceContains(f.PaneIDIn, p.Key.PaneID) {
		return false
	}
	if len(f.CurrentCommandIn) > 0 && !sliceContains(f.CurrentCommandIn, p.CurrentCommand) {
		return false
	}
	if len(f.SessionIn) > 0 && !sliceContains(f.SessionIn, p.WindowKey.Session) {
		return false
	}
	if f.TitleContains != nil && *f.TitleContains != "" && !strings.Contains(p.Title, *f.TitleContains) {
		return false
	}
	if f.Dead != nil && *f.Dead != p.Dead {
		return false
	}
	return true
}

func projectPaneNodes(raw []Pane, filter *TmuxPaneFilterInput) []*TmuxPaneNode {
	out := make([]*TmuxPaneNode, 0, len(raw))
	for _, p := range raw {
		if !paneMatchesScalarFilter(p, filter) {
			continue
		}
		out = append(out, projectPaneNode(p))
	}
	if len(out) == 0 {
		return []*TmuxPaneNode{}
	}
	return out
}

func sliceContains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
