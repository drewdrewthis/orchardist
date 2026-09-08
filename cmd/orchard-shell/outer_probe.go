package main

import "strconv"
import "strings"

// outerState is what already exists when orchard shell starts.
type outerState struct {
	sessionExists bool // the outer session is up
	paneCount     int  // how many panes window 0 currently has
	pane0Dead     bool // the sidebar pane itself has exited (remain-on-exit)
	pane1Dead     bool // the inner-client pane itself has exited
	innerLive     bool // 0.1's tty is an attached client on the inner server
	// (only meaningful when paneCount==2 and pane1 is alive)
}

// action is what orchard shell does about that state.
type action int

const (
	actionBoot    action = iota // nothing there: build the wrapper
	actionAttach                // healthy: just attach
	actionRespawn               // right shape, something inside it is dead
	actionRebuild               // wrong pane count: reconstruct the layout first
)

// decide is the reattach decision table. Re-running orchard shell is the
// normal way to get back to the wrapper, so the interesting questions are (1)
// does the window even have the right two panes, and (2) if so, is either
// pane itself dead or pane 0.1's inner client gone — attaching to a corpse
// presents as "the right pane is a dead shell", which is easy to mistake for
// the TMUX= nesting bug when triaging.
//
// remain-on-exit (outer.conf) keeps a pane whose process exited addressable
// as a DEAD pane instead of tmux closing it and renumbering its sibling —
// that renumbering was #747's live defect (a rerun's hardcoded "0.1" landed
// on the wrong, surviving pane and failed outright). Any pane count other
// than 2 is therefore something remain-on-exit does not smooth over on its
// own (manual pane close/split, or a session predating this option) and
// needs a full rebuild rather than a targeted respawn.
func decide(s outerState) action {
	switch {
	case !s.sessionExists:
		return actionBoot
	case s.paneCount != 2:
		return actionRebuild
	case s.pane0Dead || s.pane1Dead || !s.innerLive:
		return actionRespawn
	default:
		return actionAttach
	}
}

// probe reads the current state of the outer session's window 0 with a
// single list-panes call. #{pane_id} is deliberately not read here: it is
// only needed by rebuild(), which re-lists it itself when it actually needs
// to target individual panes for kill-pane.
func (w *wrapper) probe() outerState {
	if _, err := w.outer("has-session", "-t", outerSessionName); err != nil {
		return outerState{}
	}
	out, err := w.outer("list-panes", "-t", outerSessionName+":0",
		"-F", "#{pane_index} #{pane_dead} #{pane_tty}")
	if err != nil {
		return outerState{sessionExists: true}
	}

	var ttys [2]string
	var dead [2]bool
	count := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		count++
		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			continue
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil || idx < 0 || idx > 1 {
			continue
		}
		dead[idx] = fields[1] == "1"
		ttys[idx] = fields[2]
	}

	s := outerState{sessionExists: true, paneCount: count}
	if count != 2 {
		return s
	}
	s.pane0Dead, s.pane1Dead = dead[0], dead[1]
	if !s.pane1Dead && ttys[1] != "" {
		s.innerLive = w.innerHasClient(ttys[1])
	}
	return s
}

// innerHasClient reports whether tty is attached as a client on the inner
// server. Outer pane 0.1 runs the inner client on the pane's own pty, so that
// pane's #{pane_tty} is exactly the inner server's #{client_tty} for it.
func (w *wrapper) innerHasClient(tty string) bool {
	out, err := w.inner("list-clients", "-F", "#{client_tty}")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == tty {
			return true
		}
	}
	return false
}
