package ps

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProcessResolver implements the GraphQL field resolvers for the Process
// type (R6: one file per type, T1: tested against stubbed service).
//
// The resolver is intentionally thin: Load() via loader + project. No
// Snapshot() calls in the field path (R3).
type ProcessResolver struct {
	svc  Service
	git  GitService           // may be nil if git domain not wired
	ci   ClaudeInstanceService // may be nil if claude-instance domain not wired
}

// NewProcessResolver constructs a ProcessResolver.
func NewProcessResolver(svc Service, git GitService, ci ClaudeInstanceService) *ProcessResolver {
	return &ProcessResolver{svc: svc, git: git, ci: ci}
}

// Host resolves Process.host. The host is embedded in the Process.ID
// field (format: `<host>:<pid>`); we reconstruct the Host node from it
// without a cache lookup.
func (r *ProcessResolver) Host(_ context.Context, proc *ProcessProjection) (*HostRef, error) {
	host, _ := splitProcessNodeID(proc.ID)
	return &HostRef{ID: host}, nil
}

// Args resolves Process.args — slow-path opt-in (S10). The loader
// coalesces concurrent requests into a single shellout (R3, O10).
func (r *ProcessResolver) Args(ctx context.Context, proc *ProcessProjection) ([]string, error) {
	if r.svc == nil {
		return nil, fmt.Errorf("ps: service not wired")
	}
	pid := int(proc.Pid)
	got, err := r.svc.LoadArgs(ctx, []int{pid})
	if err != nil {
		return nil, err
	}
	return got[pid], nil
}

// Cwd resolves Process.cwd — slow-path opt-in (S10). The cwdLoader
// coalesces concurrent requests into a single `lsof -p <pids>` call
// per request (R3, O10).
func (r *ProcessResolver) Cwd(ctx context.Context, proc *ProcessProjection) (*string, error) {
	if r.svc == nil {
		return nil, nil
	}
	cwd, err := r.svc.LoadCwd(ctx, int(proc.Pid))
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		return nil, nil
	}
	return &cwd, nil
}

// Worktree resolves Process.worktree — cross-domain back-edge into git
// (S15b). Requires cwd resolution first, then delegates to GitService.
// Returns nil if the git service is not wired or cwd is unavailable.
func (r *ProcessResolver) Worktree(ctx context.Context, proc *ProcessProjection) (*WorktreeRef, error) {
	if r.git == nil {
		return nil, nil
	}
	cwd, err := r.svc.LoadCwd(ctx, int(proc.Pid))
	if err != nil || cwd == "" {
		return nil, nil
	}
	return r.git.WorktreeByPath(ctx, cwd)
}

// ClaudeInstance resolves Process.claudeInstance — cross-domain back-edge
// into claude-instance (S15b). Returns nil if the service is not wired.
func (r *ProcessResolver) ClaudeInstance(ctx context.Context, proc *ProcessProjection) (*ClaudeInstanceRef, error) {
	if r.ci == nil {
		return nil, nil
	}
	return r.ci.InstanceByPID(ctx, int(proc.Pid))
}

// ProcessProjection is the wire type that this resolver's methods accept.
// When gqlgen generates code for daemon/ps, this will be replaced by the
// generated graphql.Process type. The projection shape mirrors that type
// so the resolver bodies transfer without change.
type ProcessProjection struct {
	ID         string
	Pid        int64
	Ppid       int64
	Command    string
	StartedAt  string
	CPUPercent float64
	MemBytes   int64
	Tty        *string
}

// HostRef is a minimal host reference used by Process.host.
type HostRef struct {
	ID string
}

// ProjectProcess converts a domain Process into a ProcessProjection.
func ProjectProcess(p *Process, hostID string) *ProcessProjection {
	startedAt := p.StartedRaw
	if !p.StartedAt.IsZero() {
		startedAt = p.StartedAt.Format(time.RFC3339)
	}
	proj := &ProcessProjection{
		ID:         p.ID.String(),
		Pid:        int64(p.ID.PID),
		Ppid:       int64(p.PPID),
		Command:    p.Command,
		StartedAt:  startedAt,
		CPUPercent: p.CPUPercent,
		MemBytes:   p.MemBytes,
	}
	if p.TTY != "" {
		tty := p.TTY
		proj.Tty = &tty
	}
	_ = hostID // embedded in ID
	return proj
}

// splitProcessNodeID extracts (host, pidStr) from a `<host>:<pid>` id.
func splitProcessNodeID(s string) (string, string) {
	idx := strings.LastIndexByte(s, ':')
	if idx <= 0 {
		return "local", s
	}
	return s[:idx], s[idx+1:]
}

// ApplyProcessFilter applies a ProcessFilter to a slice of Processes.
// Returns the subset that matches all non-nil criteria (AND-combined).
// cwd prefix forces resolution via the cwdLoader (R3, O10).
func ApplyProcessFilter(ctx context.Context, svc Service, in []Process, filter *ProcessFilter) ([]Process, error) {
	if filter == nil {
		return in, nil
	}
	out := make([]Process, len(in))
	copy(out, in)

	if len(filter.PidIn) > 0 {
		want := make(map[int]struct{}, len(filter.PidIn))
		for _, pid := range filter.PidIn {
			want[pid] = struct{}{}
		}
		next := out[:0]
		for _, proc := range out {
			if _, ok := want[proc.ID.PID]; ok {
				next = append(next, proc)
			}
		}
		out = next
	}

	if len(filter.CommandIn) > 0 {
		want := make(map[string]struct{}, len(filter.CommandIn))
		for _, c := range filter.CommandIn {
			want[c] = struct{}{}
		}
		next := out[:0]
		for _, proc := range out {
			if _, ok := want[proc.Command]; ok {
				next = append(next, proc)
			}
		}
		out = next
	}

	if filter.CwdPrefix != nil && *filter.CwdPrefix != "" {
		prefix := *filter.CwdPrefix
		pids := make([]int, 0, len(out))
		for _, proc := range out {
			pids = append(pids, proc.ID.PID)
		}
		cwds, err := svc.LoadCwds(ctx, pids)
		if err != nil {
			return nil, err
		}
		next := out[:0]
		for _, proc := range out {
			if cwd, ok := cwds[proc.ID.PID]; ok && strings.HasPrefix(cwd, prefix) {
				next = append(next, proc)
			}
		}
		out = next
	}

	return out, nil
}
