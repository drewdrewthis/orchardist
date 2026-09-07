package main

// Supergraph fast lane (#844). Supergraph has no workView join type, so this
// adapter assembles the same []row the daemon fast lane produces from three
// typed leaves — tmuxSessions (the row inventory), claudeInstances (state,
// model, activity per session) and tmuxPanes (the pane->session map, and the
// key that ties a claudeInstance's pane to its session). GET /health replaces
// workView.meta.failureReason: its per-plugin array is reduced to the first
// non-ok plugin, surfaced as an advisory that names the plugin without
// blanking the rows the data query did resolve.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const sgFastQuery = `{
  claudeInstances { pane pid session { sessionId state model lastEventAt cwd gitBranch issueNumber prNumber } }
  tmuxSessions { name worktree branch }
  tmuxPanes { key session window pane active }
}`

type sgFastResp struct {
	Data struct {
		ClaudeInstances []sgClaudeInstance `json:"claudeInstances"`
		TmuxSessions    []sgTmuxSession    `json:"tmuxSessions"`
		TmuxPanes       []sgTmuxPane       `json:"tmuxPanes"`
	} `json:"data"`
}

type sgClaudeInstance struct {
	Pane    string          `json:"pane"`
	Pid     int             `json:"pid"`
	Session sgClaudeSession `json:"session"`
}

type sgClaudeSession struct {
	SessionID   string  `json:"sessionId"`
	State       string  `json:"state"`
	Model       *string `json:"model"`
	LastEventAt string  `json:"lastEventAt"`
	Cwd         string  `json:"cwd"`
	GitBranch   *string `json:"gitBranch"`
	IssueNumber *int    `json:"issueNumber"`
	PrNumber    *int    `json:"prNumber"`
	PrURL       *string `json:"prUrl"`
}

type sgTmuxSession struct {
	Name     string  `json:"name"`
	Worktree *string `json:"worktree"`
	Branch   *string `json:"branch"`
}

type sgTmuxPane struct {
	Key     string `json:"key"`
	Session string `json:"session"`
	Window  int    `json:"window"`
	Pane    int    `json:"pane"`
	Active  bool   `json:"active"`
}

// fetchFastSupergraph is the supergraph fast lane, drop-in for fetchFastDaemon.
// It resolves rows from the typed leaves and folds GET /health into the same
// failure-reason surface the daemon fills from meta. A health advisory rides
// alongside the rows (applyFast treats err-with-rows as non-fatal); only a
// failed data query is a hard failure that degrades the lane like a daemon
// outage.
func fetchFastSupergraph() tea.Msg {
	var out sgFastResp
	if err := post(sgFastQuery, 4*time.Second, &out); err != nil {
		return fastDataMsg{err: err}
	}
	rows, p2s := supergraphRows(out.Data.ClaudeInstances, out.Data.TmuxSessions, out.Data.TmuxPanes)
	// AC11 use-proof: the resolved row count is logged every fast tick so "real
	// rows rendered" is checkable from the log, not only the screenshot.
	logf("supergraph fast lane: resolved %d rows", len(rows))
	msg := fastDataMsg{rows: rows, paneToSess: p2s}
	if reason := supergraphHealthReason(); reason != "" {
		msg.err = fmt.Errorf("supergraph: %s", reason)
	}
	return msg
}

// supergraphRows folds the three leaves into the row model. One row per tmux
// session is the inventory (matching the daemon, whose rows are keyed on
// tmuxSessions too); a claudeInstance enriches the row for the session its pane
// belongs to. A session with no claude instance stays a plain "shell" row, and
// a claude instance whose pane maps to no known session is dropped rather than
// inventing a row — exactly the daemon's shape. attached is left false:
// supergraph's TmuxSession carries no attached flag (TODO(supergraph#27)), so
// the honest value is "not known attached", never a fabricated true.
func supergraphRows(cis []sgClaudeInstance, sessions []sgTmuxSession, panes []sgTmuxPane) ([]row, map[string]string) {
	paneToSession := make(map[string]string, len(panes))
	p2s := make(map[string]string, len(panes))
	for _, p := range panes {
		paneToSession[p.Key] = p.Session
		p2s[p.Key] = p.Session
	}

	byName := make(map[string]*row, len(sessions))
	order := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if _, ok := byName[s.Name]; ok {
			continue
		}
		byName[s.Name] = &row{session: s.Name, state: "shell"}
		order = append(order, s.Name)
	}

	seen := map[string]bool{}
	for _, ci := range cis {
		name := paneToSession[ci.Pane]
		if name == "" {
			continue // a claude instance whose pane maps to no tmux session
		}
		if ci.Session.SessionID != "" {
			if seen[ci.Session.SessionID] {
				continue
			}
			seen[ci.Session.SessionID] = true
		}
		r, ok := byName[name]
		if !ok {
			r = &row{session: name}
			byName[name] = r
			order = append(order, name)
		}
		r.state = ci.Session.State
		if ci.Session.Model != nil {
			r.model = shortModel(*ci.Session.Model)
		}
		if ci.Session.Cwd != "" {
			r.cwd = ci.Session.Cwd
		}
		r.lastAct, _ = time.Parse(time.RFC3339, ci.Session.LastEventAt)
	}

	rows := make([]row, 0, len(order))
	for _, name := range order {
		rows = append(rows, *byName[name])
	}
	sortRows(rows)
	return rows, p2s
}

// sgHealthPlugin is one row of GET /health's per-plugin array.
type sgHealthPlugin struct {
	Plugin string `json:"plugin"`
	State  string `json:"state"`
}

// supergraphHealthReason reduces GET /health to the first plugin not in the
// "ok" state, named as "<plugin> <state>" (e.g. "github stale"). Empty when
// every plugin is ok, or when health itself cannot be read — a health probe
// that fails must not masquerade as a named-plugin failure. First-offender
// wins: the simplest deterministic rule matching the daemon's single-string
// failure reason.
func supergraphHealthReason() string {
	if healthURL == "" {
		return ""
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(healthURL)
	if err != nil {
		logf("supergraph health: %v", err)
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var plugins []sgHealthPlugin
	if err := json.NewDecoder(resp.Body).Decode(&plugins); err != nil {
		logf("supergraph health: decode: %v", err)
		return ""
	}
	for _, p := range plugins {
		if p.State != "ok" {
			return fmt.Sprintf("%s %s", p.Plugin, p.State)
		}
	}
	return ""
}
