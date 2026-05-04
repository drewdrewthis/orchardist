package ps

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProcessID is the provider's key type. macOS pids are int32, but Linux
// allows the full 32-bit range, so we use int. Composed with HostID for
// the GraphQL Node identity (`<host>:<pid>`).
type ProcessID struct {
	Host string
	PID  int
}

// String returns the wire-format Node id (`<host>:<pid>`).
func (p ProcessID) String() string {
	return fmt.Sprintf("%s:%d", p.Host, p.PID)
}

// ParseProcessID parses the wire-format Node id. Returns an error if the
// shape is malformed; callers can use this to resolve `node(id:)` lookups
// once that resolver lands.
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

// Process is the domain type the provider stores in its cache. Resolvers
// project this onto the gqlgen wire type (graphql.Process) and lazily
// fetch slow-path fields (args, cwd) only when requested.
type Process struct {
	ID         ProcessID
	PPID       int
	User       string
	TTY        string  // empty when ps reports "??"
	CPUPercent float64
	MemBytes   int64   // RSS (1024-byte pages on macOS; bytes after multiplication)
	StartedAt  time.Time
	StartedRaw string  // original lstart string, kept for cases where parsing failed
	Command    string  // basename (e.g. "sleep") — stripped path + argv
	CommandRaw string  // full path-and-argv as ps emitted it (used for command-line searches)
}
