// Inner-session resolution: which inner tmux session the wrapper attaches,
// creating a default one when the inner server has none.
package main

import (
	"cmp"
	"os"
	"slices"
	"strconv"
	"strings"
)

// innerSessions lists the inner server's sessions, most recently attached
// first, so an omitted --session picks up where the user left off.
func (w *wrapper) innerSessions() ([]string, error) {
	out, err := w.inner("list-sessions", "-F", "#{session_last_attached} #{session_name}")
	if err != nil {
		// A raw error here is almost always "no server running" — an absent
		// inner server, not a fault. resolveSession() and the recovery paths
		// treat that the same as an empty list (create a default session), so
		// the error is passed through unwrapped rather than dressed up.
		return nil, err
	}
	return sortSessionsByRecency(out), nil
}

// sortSessionsByRecency parses `<last_attached> <name>` lines into names,
// newest first. A session that has never been attached reports 0 and sorts
// last, keeping the listing stable rather than random.
func sortSessionsByRecency(out string) []string {
	type entry struct {
		at   int64
		name string
	}
	var entries []entry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// A tmux old enough not to know #{session_last_attached} renders it
		// empty, leaving the name alone on the line. Anything whose first
		// field is not a number is therefore a bare name — including one
		// with a space in it, which splitting blindly would truncate.
		stamp, name, ok := strings.Cut(line, " ")
		at, err := strconv.ParseInt(stamp, 10, 64)
		if !ok || err != nil {
			entries = append(entries, entry{name: line})
			continue
		}
		entries = append(entries, entry{at: at, name: name})
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(b.at, a.at) })

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.name)
	}
	return names
}

// resolveSession picks the inner session to attach. An absent inner server or
// one with zero sessions is not a failure: it creates one default session so
// the wrapper always has something to attach to, instead of bailing out.
func (w *wrapper) resolveSession() (string, error) {
	have, err := w.innerSessions()
	if err != nil || len(have) == 0 {
		return w.createDefaultInnerSession()
	}
	if w.opts.Session == "" {
		return have[0], nil
	}
	for _, s := range have {
		if s == w.opts.Session {
			return s, nil
		}
	}
	return "", &sessionMissingError{want: w.opts.Session, socket: w.opts.InnerSocket, have: have}
}

// createDefaultInnerSession creates one detached session on the inner socket
// and returns its name — the --session value if the user named one, else
// defaultNewSessionName. Its cwd is $HOME so a fresh session starts where a
// login shell would, not in orchard-shell's own working directory.
//
// It runs BEFORE boot() touches the outer socket, so a genuine tmux error (no
// binary, unwritable socket) fails new-session here and leaves the outer
// server untouched — nothing is half-built.
func (w *wrapper) createDefaultInnerSession() (string, error) {
	name := cmp.Or(w.opts.Session, defaultNewSessionName)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if _, err := w.inner("new-session", "-d", "-s", name, "-c", home); err != nil {
		return "", err
	}
	return name, nil
}
