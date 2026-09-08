package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The two panes of the wrapper's single window. 0.0 is the sidebar, 0.1 the
// nested inner client.
const (
	paneSidebar = outerSessionName + ":0.0"
	paneInner   = outerSessionName + ":0.1"
)

// wrapper drives one outer tmux server.
type wrapper struct {
	opts     Options
	conf     string
	tmux     tmuxExec
	log      io.Writer
	lookPath pathLookup // exec.LookPath; injected so sidebar-found/missing is pinnable in tests
}

func (w *wrapper) outer(args ...string) (string, error) {
	return w.tmux(outerArgs(w.opts.OuterSocket, w.conf, args...)...)
}

func (w *wrapper) inner(args ...string) (string, error) {
	return w.tmux(innerArgs(w.opts.InnerSocket, args...)...)
}

// bootPane is boot's single starting pane before the sidebar split pushes
// it to paneInner (0.1) — the same window-level address boot's own
// split-window targets.
const bootPane = outerSessionName + ":0"

// boot builds the wrapper from nothing. detach-on-destroy is NOT set here:
// ensureReady() sets it once for every path (boot/respawn/rebuild/attach)
// after the inner server is confirmed present, so setting it in boot too was a
// redundant second set-option on the boot path.
func (w *wrapper) boot(session string) error {
	cols, rows := termSize()
	if _, err := w.outer("new-session", "-d", "-s", outerSessionName,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)); err != nil {
		return err
	}

	// bootPane is unambiguous here: only one pane exists, so sending the
	// inner attach before splitting cannot land in the wrong pane.
	if _, err := w.outer("send-keys", "-t", bootPane, innerAttachCommand(w.opts.InnerSocket, session), "Enter"); err != nil {
		return err
	}
	return w.startSidebar(bootPane)
}

// startSidebar splits off pane 0.0 running the sidebar directly, as
// split-window's own command argument rather than a follow-up send-keys —
// send-keys races the pane's default shell starting up and reading the
// keystrokes (the 5s flake seen in verify.sh); handing tmux the command
// up front removes the race, matching respawn()'s directness.
//
// inner is the address of the already-created pane that becomes 0.1 —
// bootPane on boot, paneInner on any later rebuild. split-window -b puts
// the NEW pane before it, so inner is read for its env (tty, pane id)
// BEFORE the split, then becomes 0.1 once the split runs.
func (w *wrapper) startSidebar(inner string) error {
	tty, err := w.outer("display", "-p", "-t", inner, "#{pane_tty}")
	if err != nil {
		return err
	}
	// #{pane_id} (e.g. %1), not a tty path: it is stable across resizes and
	// redraws, and it is what the sidebar hands keyboard focus back to.
	paneID, err := w.outer("display", "-p", "-t", inner, "#{pane_id}")
	if err != nil {
		return err
	}

	cmd := placeholderCommand(w.opts.InnerSocket)
	if bin := resolveSidebarWith(w.lookPath); bin != "" {
		cmd = sidebarCommand(bin, w.opts.InnerSocket, tty, paneID)
	} else {
		fmt.Fprintf(w.log, "orchard shell: no orchard-sidebar found beside %s or on $PATH; using the watch(1) placeholder\n", selfPath())
	}
	// split-window -h -b -l <width>: the new pane goes before (-b, left of)
	// the target at an exact width, running cmd directly instead of the
	// default shell — same remain-on-exit semantics (outer.conf) apply to
	// whatever pane process exits, launched or not via send-keys.
	_, err = w.outer("split-window", "-h", "-b", "-l", strconv.Itoa(w.opts.Width), "-t", inner, cmd)
	return err
}

// respawn rebuilds pane 0.1's inner client, and then pane 0.0's sidebar.
//
// Both, in that order, because respawn-pane gives the pane a new pty: the
// sidebar's ORCHARD_TMUX_CLIENT names 0.1's OLD tty and would scope every
// switch-client to a client that no longer exists. Re-reading the tty first
// and relaunching the sidebar second is what keeps the two facts agreeing.
func (w *wrapper) respawn(session string) error {
	if _, err := w.outer("respawn-pane", "-k", "-t", paneInner,
		innerAttachCommand(w.opts.InnerSocket, session)); err != nil {
		return err
	}
	return w.respawnSidebarPane()
}

// respawnSidebarPane rebuilds pane 0.0 with fresh env. It re-reads 0.1's tty
// FIRST: a respawn of 0.1 gives it a new pty, and the sidebar's
// ORCHARD_TMUX_CLIENT must name the tty that is live NOW — the ordering both
// respawn() (after it respawns 0.1) and recover-pane's sidebar path depend on,
// which is why the two share this one helper rather than each keeping a copy.
func (w *wrapper) respawnSidebarPane() error {
	tty, err := w.outer("display", "-p", "-t", paneInner, "#{pane_tty}")
	if err != nil {
		return err
	}
	paneID, err := w.outer("display", "-p", "-t", paneInner, "#{pane_id}")
	if err != nil {
		return err
	}
	cmd := placeholderCommand(w.opts.InnerSocket)
	if bin := resolveSidebarWith(w.lookPath); bin != "" {
		cmd = sidebarCommand(bin, w.opts.InnerSocket, tty, paneID)
	}
	_, err = w.outer("respawn-pane", "-k", "-t", paneSidebar, cmd)
	return err
}

// rebuild reconstructs the wrapper's two-pane layout from whatever window 0
// currently holds — 0, 1 or 3+ panes, any of them dead. It keeps one pane
// (by #{pane_id}, which is stable across kills and splits — unlike
// #{pane_index}, which renumbers as panes are removed), kills the rest,
// splits the survivor to recreate the sidebar/inner shape, re-pins the
// width, and hands off to respawn() to (re)launch both commands. Which pane
// survives the cull is not a decision that matters: respawn() overwrites
// both regardless of what rebuild kept.
func (w *wrapper) rebuild(session string) error {
	out, err := w.outer("list-panes", "-t", outerSessionName+":0", "-F", "#{pane_id}")
	if err != nil {
		return w.boot(session)
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	if len(ids) == 0 {
		return w.boot(session)
	}

	for _, id := range ids[1:] {
		if _, err := w.outer("kill-pane", "-t", id); err != nil {
			return err
		}
	}

	// Same split shape as boot(): -b puts the NEW pane at 0.0, pushing the
	// kept survivor to 0.1. respawn() below relaunches both regardless.
	if _, err := w.outer("split-window", "-h", "-b", "-l", strconv.Itoa(w.opts.Width),
		"-t", ids[0]); err != nil {
		return err
	}
	if _, err := w.outer("set-window-option", "-t", outerSessionName+":0",
		"main-pane-width", strconv.Itoa(w.opts.Width)); err != nil {
		return err
	}
	if _, err := w.outer("select-layout", "-t", outerSessionName+":0", "main-vertical"); err != nil {
		return err
	}
	return w.respawn(session)
}

// disarmDetachOnDestroy turns off detach-on-destroy on the INNER server
// (AC0). tmux's default is to DETACH a client when the session it is viewing
// is destroyed; on the inner client running in pane 0.1 that would leave the
// pane a dead shell. Off, tmux switches the client to another session
// instead, so killing the session the user is looking at never strands the
// pane. Set here, on the server orchard-shell attaches to, rather than in the
// user's ~/.tmux.conf, so the guarantee holds regardless of their config.
// Best-effort: an inner server that is not up yet has nothing to set, and the
// caller's own attach will surface that.
func (w *wrapper) disarmDetachOnDestroy() {
	_, _ = w.inner("set-option", "-g", "detach-on-destroy", "off")
}

// focusInner moves focus to 0.1, unconditionally, on boot AND on every
// re-run. tmux leaves the newly-split pane (0.0) active by default, and with
// mouse-only focus and no prefix a user landing on 0.0 has no way to move
// off it at all — #747's original live defect.
func (w *wrapper) focusInner() error {
	_, err := w.outer("select-pane", "-t", paneInner)
	return err
}
