package ps

import "strings"

// maxDescendantDepth bounds the descendant-tree walk. Mirrors the depth-6
// bound in ~/.claude/scripts/session-truth: claude sits one to two levels
// under the pane's shell in every launch style live on the box, so 6 is
// generous headroom while still capping the walk on a large process table.
const maxDescendantDepth = 6

// ProcTree indexes a ps snapshot by parentage so callers can walk a process's
// descendants without re-shelling. Build it once per request from
// Provider.List(); it is read-only after construction.
type ProcTree struct {
	childrenByPPID map[int][]int
	commandByPID   map[int]string
}

// NewProcTree builds a ProcTree from a process snapshot. Safe to call with a
// nil or empty slice — the resulting tree resolves every pid to itself.
func NewProcTree(procs []Process) *ProcTree {
	t := &ProcTree{
		childrenByPPID: make(map[int][]int, len(procs)),
		commandByPID:   make(map[int]string, len(procs)),
	}
	for _, p := range procs {
		t.childrenByPPID[p.PPID] = append(t.childrenByPPID[p.PPID], p.ID.PID)
		t.commandByPID[p.ID.PID] = p.Command
	}
	return t
}

// ResolveClaudePid returns the pid of the foreground claude process associated
// with rootPid — the pane's root process (tmux pane_pid).
//
//   - If rootPid is itself claude (claude exec'd directly), rootPid is returned.
//   - Otherwise it BFS-walks rootPid's descendants (bounded depth) and returns
//     the first — i.e. shallowest — claude pid found (the `bash -> claude`
//     shell-wrapped launch every worker uses).
//   - If no claude is found, or the tree is unknown, rootPid is returned
//     unchanged, preserving prior behavior rather than dropping the instance.
//
// This is schema.graphql's identity contract ("the foreground claude pid scoped
// to a host") and mirrors session-truth's resolve_claude_pid descendant walk.
func (t *ProcTree) ResolveClaudePid(rootPid int) int {
	if t == nil || rootPid <= 0 {
		return rootPid
	}
	if isClaudeCommand(t.commandByPID[rootPid]) {
		return rootPid
	}
	frontier := []int{rootPid}
	for depth := 0; depth <= maxDescendantDepth && len(frontier) > 0; depth++ {
		var next []int
		for _, pid := range frontier {
			for _, child := range t.childrenByPPID[pid] {
				if isClaudeCommand(t.commandByPID[child]) {
					return child
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return rootPid
}

// isClaudeCommand reports whether a process command basename identifies a
// Claude CLI process. Mirrors paneCommandMatchesClaude in the tmux provider
// (case-insensitive substring) so the two claude detectors agree — one source
// of truth for "is this a claude process".
func isClaudeCommand(command string) bool {
	return strings.Contains(strings.ToLower(command), "claude")
}
