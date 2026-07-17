package ps

import "testing"

// proc is a tiny constructor for test snapshots.
func proc(pid, ppid int, command string) Process {
	return Process{ID: ProcessID{Host: "local", PID: pid}, PPID: ppid, Command: command}
}

func TestProcTree_ResolveClaudePid(t *testing.T) {
	tests := []struct {
		name  string
		procs []Process
		root  int
		want  int
	}{
		{
			name: "shell-wrapped: bash -> claude resolves to the claude child (#711)",
			procs: []Process{
				proc(806301, 1, "-bash"),
				proc(806825, 806301, "claude"),
			},
			root: 806301,
			want: 806825,
		},
		{
			name: "exec'd claude keeps its own pid",
			procs: []Process{
				proc(1648, 1, "claude"),
				proc(2902, 1648, "bun"), // a child that must be ignored
			},
			root: 1648,
			want: 1648,
		},
		{
			name: "deep: bash -> sh -> claude found via BFS below depth 1",
			procs: []Process{
				proc(100, 1, "-bash"),
				proc(200, 100, "sh"),
				proc(300, 200, "claude"),
			},
			root: 100,
			want: 300,
		},
		{
			name: "shallowest claude wins over a deeper claude subagent",
			procs: []Process{
				proc(100, 1, "-bash"),
				proc(200, 100, "claude"), // foreground
				proc(300, 200, "claude"), // a subagent, deeper — must not win
			},
			root: 100,
			want: 200,
		},
		{
			name: "no claude in tree: falls back to root unchanged",
			procs: []Process{
				proc(100, 1, "-bash"),
				proc(200, 100, "vim"),
			},
			root: 100,
			want: 100,
		},
		{
			name:  "unknown root: returned unchanged",
			procs: []Process{proc(100, 1, "-bash")},
			root:  999,
			want:  999,
		},
		{
			name: "node-wrapped claude basename does not match (falls back to root)",
			procs: []Process{
				proc(100, 1, "-bash"),
				proc(200, 100, "node"),
			},
			root: 100,
			want: 100,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tree := NewProcTree(tc.procs)
			if got := tree.ResolveClaudePid(tc.root); got != tc.want {
				t.Errorf("ResolveClaudePid(%d) = %d, want %d", tc.root, got, tc.want)
			}
		})
	}
}

// TestProcTree_NilSafe guards the nil-receiver and non-positive-pid paths the
// resolver relies on when r.PS is absent.
func TestProcTree_NilSafe(t *testing.T) {
	var tree *ProcTree
	if got := tree.ResolveClaudePid(123); got != 123 {
		t.Errorf("nil tree: ResolveClaudePid(123) = %d, want 123", got)
	}
	empty := NewProcTree(nil)
	if got := empty.ResolveClaudePid(0); got != 0 {
		t.Errorf("empty tree: ResolveClaudePid(0) = %d, want 0", got)
	}
}
