package main

import (
	"fmt"
	"strings"
)

// exitSessionMissing is the exit code for "you asked for a session that is not
// there" — distinct from 1 so a caller can tell a wrong session name from any
// other failure without parsing stderr.
const exitSessionMissing = 2

// sessionMissingError means the inner server is up but does not have the
// requested session. It carries the sessions that DO exist: a bare "not
// found" makes the user go and run list-sessions themselves, and the answer
// is already in hand at the point of failure.
type sessionMissingError struct {
	want   string
	socket string
	have   []string
}

func (e *sessionMissingError) Error() string {
	return fmt.Sprintf("inner session %q not found on socket %q\nSessions on %s:\n  %s",
		e.want, e.socket, e.socket, strings.Join(e.have, "\n  "))
}
