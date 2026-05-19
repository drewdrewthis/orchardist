// Package ps implements the orchard ps domain per the repo constitution (RULES.md).
//
// Owns: Process, ProcessFilter.
// Read-only — no mutations.
// Typed core: Host.processes(filter), Subscription.processes.
// Pass-through escape hatch: Query.ps(tool, args) per S16b.
//
// Slow-path opt-in fields (S10):
//   - Process.args — separate ps -wwax lookup via argsLoader
//   - Process.cwd  — lsof on macOS, batched via cwdLoader per O10
package ps

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProcessID is the provider key. Composed with HostID for the Node id
// (`<host>:<pid>`).
type ProcessID struct {
	Host string
	PID  int
}

// String returns the canonical GraphQL Node id for this process.
func (p ProcessID) String() string {
	return fmt.Sprintf("%s:%d", p.Host, p.PID)
}

// ParseProcessID parses the wire-format Node id. Returns an error if the
// shape is malformed.
func ParseProcessID(s string) (ProcessID, error) {
	idx := strings.LastIndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return ProcessID{}, fmt.Errorf("ps: malformed process id %q (want <host>:<pid>)", s)
	}
	pid, err := strconv.Atoi(s[idx+1:])
	if err != nil {
		return ProcessID{}, fmt.Errorf("ps: malformed pid in %q: %w", s, err)
	}
	return ProcessID{Host: s[:idx], PID: pid}, nil
}

// Process is the domain type stored in the cache. Resolvers project this
// onto gqlgen wire types and lazily fetch slow-path fields (args, cwd).
type Process struct {
	ID         ProcessID
	PPID       int
	User       string
	TTY        string  // empty when ps reports "??" / "?"
	CPUPercent float64
	MemBytes   int64  // RSS pages * 1024
	StartedAt  time.Time
	StartedRaw string // raw lstart string kept when parsing failed
	Command    string // basename (e.g. "sleep")
	CommandRaw string // full path+argv as ps emitted it
}

// ProcessFilter is applied at the resolver layer over the cached process
// list. Multiple criteria are AND-combined.
type ProcessFilter struct {
	CommandIn []string
	CwdPrefix *string
	PidIn     []int
}

// processEqualsHotPath compares the stable fields the watcher uses for
// invalidation. CPU and memory shift constantly; we ignore them to
// avoid emitting an event per pid per tick.
func processEqualsHotPath(a, b Process) bool {
	return a.PPID == b.PPID &&
		a.User == b.User &&
		a.TTY == b.TTY &&
		a.Command == b.Command &&
		a.StartedRaw == b.StartedRaw
}
