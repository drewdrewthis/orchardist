package ps

import (
	"context"
	"testing"
	"time"
)

// stubService is a minimal Service implementation for resolver unit tests.
// T1: every typed field has a resolver test against a stubbed service.
type stubService struct {
	hostID    string
	processes []Process
	argsMap   map[int][]string
	cwdMap    map[int]string
}

func (s *stubService) HostID() string { return s.hostID }
func (s *stubService) List() []Process {
	out := make([]Process, len(s.processes))
	copy(out, s.processes)
	return out
}
func (s *stubService) Get(key ProcessID) (Process, bool) {
	for _, p := range s.processes {
		if p.ID == key {
			return p, true
		}
	}
	return Process{}, false
}
func (s *stubService) Subscribe(_ context.Context) <-chan invalidationEvent {
	ch := make(chan invalidationEvent, 1)
	return ch
}
func (s *stubService) LoadArgs(_ context.Context, pids []int) (map[int][]string, error) {
	out := make(map[int][]string, len(pids))
	for _, pid := range pids {
		if argv, ok := s.argsMap[pid]; ok {
			out[pid] = argv
		}
	}
	return out, nil
}
func (s *stubService) LoadCwd(_ context.Context, pid int) (string, error) {
	return s.cwdMap[pid], nil
}
func (s *stubService) LoadCwds(_ context.Context, pids []int) (map[int]string, error) {
	out := make(map[int]string, len(pids))
	for _, pid := range pids {
		if cwd, ok := s.cwdMap[pid]; ok {
			out[pid] = cwd
		}
	}
	return out, nil
}

// stubGitService is a minimal GitService implementation for resolver tests.
type stubGitService struct {
	worktrees map[string]*WorktreeRef // cwd prefix → worktree
}

func (s *stubGitService) WorktreeByPath(_ context.Context, path string) (*WorktreeRef, error) {
	for prefix, wt := range s.worktrees {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return wt, nil
		}
	}
	return nil, nil
}

// stubClaudeInstanceService is a minimal ClaudeInstanceService for resolver tests.
type stubClaudeInstanceService struct {
	instances map[int]*ClaudeInstanceRef // pid → instance
}

func (s *stubClaudeInstanceService) InstanceByPID(_ context.Context, pid int) (*ClaudeInstanceRef, error) {
	if ci, ok := s.instances[pid]; ok {
		return ci, nil
	}
	return nil, nil
}

// makeTestProcess returns a Process with a TTY for field projection tests.
func makeTestProcess() Process {
	return Process{
		ID:         ProcessID{Host: "test-host", PID: 42},
		PPID:       1,
		User:       "alice",
		TTY:        "s001",
		CPUPercent: 1.5,
		MemBytes:   4096 * 1024,
		StartedAt:  time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		StartedRaw: "Thu May  1 12:00:00 2026",
		Command:    "sleep",
		CommandRaw: "/bin/sleep 30",
	}
}

// TestProcessResolver_Host asserts that Process.host returns the host
// embedded in the node id (T1, T3).
func TestProcessResolver_Host(t *testing.T) {
	svc := &stubService{hostID: "test-host"}
	r := NewProcessResolver(svc, nil, nil)

	proc := ProjectProcess(&Process{
		ID: ProcessID{Host: "test-host", PID: 99},
	}, "test-host")

	host, err := r.Host(context.Background(), proc)
	if err != nil {
		t.Fatalf("Host: %v", err)
	}
	if host == nil {
		t.Fatal("Host returned nil")
	}
	if host.ID != "test-host" {
		t.Errorf("Host.ID = %q, want %q", host.ID, "test-host")
	}
}

// TestProcessResolver_Args asserts that Process.args delegates to the
// loader and returns the stub's argv (T1, T3).
func TestProcessResolver_Args(t *testing.T) {
	svc := &stubService{
		hostID:  "local",
		argsMap: map[int][]string{42: {"/bin/sleep", "30"}},
	}
	r := NewProcessResolver(svc, nil, nil)
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	args, err := r.Args(context.Background(), proc)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if len(args) != 2 {
		t.Fatalf("Args len = %d, want 2", len(args))
	}
	if args[0] != "/bin/sleep" {
		t.Errorf("args[0] = %q, want %q", args[0], "/bin/sleep")
	}
}

// TestProcessResolver_Cwd asserts that Process.cwd returns the stub's
// cwd for the given pid (T1, T3).
func TestProcessResolver_Cwd(t *testing.T) {
	svc := &stubService{
		hostID: "local",
		cwdMap: map[int]string{42: "/workspace/myrepo"},
	}
	r := NewProcessResolver(svc, nil, nil)
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	cwd, err := r.Cwd(context.Background(), proc)
	if err != nil {
		t.Fatalf("Cwd: %v", err)
	}
	if cwd == nil {
		t.Fatal("Cwd returned nil")
	}
	if *cwd != "/workspace/myrepo" {
		t.Errorf("Cwd = %q, want %q", *cwd, "/workspace/myrepo")
	}
}

// TestProcessResolver_Cwd_NilWhenEmpty asserts that Process.cwd returns
// nil (not empty string) when cwd is unavailable (T1, T3).
func TestProcessResolver_Cwd_NilWhenEmpty(t *testing.T) {
	svc := &stubService{
		hostID: "local",
		cwdMap: map[int]string{}, // pid 42 has no cwd
	}
	r := NewProcessResolver(svc, nil, nil)
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	cwd, err := r.Cwd(context.Background(), proc)
	if err != nil {
		t.Fatalf("Cwd: %v", err)
	}
	if cwd != nil {
		t.Errorf("Cwd should be nil for missing cwd, got %q", *cwd)
	}
}

// TestProcessResolver_Worktree asserts cross-domain delegation to GitService
// (T1, T3).
func TestProcessResolver_Worktree(t *testing.T) {
	svc := &stubService{
		hostID: "local",
		cwdMap: map[int]string{42: "/workspace/myrepo/feature"},
	}
	git := &stubGitService{
		worktrees: map[string]*WorktreeRef{
			"/workspace/myrepo": {ID: "Worktree:local:/workspace/myrepo", Path: "/workspace/myrepo"},
		},
	}
	r := NewProcessResolver(svc, git, nil)
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	wt, err := r.Worktree(context.Background(), proc)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if wt == nil {
		t.Fatal("Worktree returned nil, want a match")
	}
	if wt.Path != "/workspace/myrepo" {
		t.Errorf("Worktree.Path = %q, want %q", wt.Path, "/workspace/myrepo")
	}
}

// TestProcessResolver_Worktree_NilWhenGitNotWired asserts that Process.worktree
// returns nil (not error) when git service is not wired (T1, T3).
func TestProcessResolver_Worktree_NilWhenGitNotWired(t *testing.T) {
	svc := &stubService{hostID: "local"}
	r := NewProcessResolver(svc, nil, nil) // git=nil
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	wt, err := r.Worktree(context.Background(), proc)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if wt != nil {
		t.Errorf("Worktree should be nil when git not wired, got %+v", wt)
	}
}

// TestProcessResolver_ClaudeInstance asserts cross-domain delegation to
// ClaudeInstanceService (T1, T3).
func TestProcessResolver_ClaudeInstance(t *testing.T) {
	svc := &stubService{hostID: "local"}
	ci := &stubClaudeInstanceService{
		instances: map[int]*ClaudeInstanceRef{42: {ID: "ClaudeInstance:local:42"}},
	}
	r := NewProcessResolver(svc, nil, ci)
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	inst, err := r.ClaudeInstance(context.Background(), proc)
	if err != nil {
		t.Fatalf("ClaudeInstance: %v", err)
	}
	if inst == nil {
		t.Fatal("ClaudeInstance returned nil, want a match")
	}
	if inst.ID != "ClaudeInstance:local:42" {
		t.Errorf("ClaudeInstance.ID = %q, want %q", inst.ID, "ClaudeInstance:local:42")
	}
}

// TestProcessResolver_ClaudeInstance_NilWhenNotWired asserts nil (not error)
// when the claude-instance service is not wired (T1, T3).
func TestProcessResolver_ClaudeInstance_NilWhenNotWired(t *testing.T) {
	svc := &stubService{hostID: "local"}
	r := NewProcessResolver(svc, nil, nil) // ci=nil
	proc := &ProcessProjection{ID: "local:42", Pid: 42}

	inst, err := r.ClaudeInstance(context.Background(), proc)
	if err != nil {
		t.Fatalf("ClaudeInstance: %v", err)
	}
	if inst != nil {
		t.Errorf("ClaudeInstance should be nil when not wired, got %+v", inst)
	}
}

// TestProjectProcess_TTY asserts that a non-empty TTY is included in the
// projection (T1, T3).
func TestProjectProcess_TTY(t *testing.T) {
	p := makeTestProcess()
	proj := ProjectProcess(&p, "test-host")
	if proj.Tty == nil {
		t.Fatal("Tty should not be nil for a process with a terminal")
	}
	if *proj.Tty != "s001" {
		t.Errorf("Tty = %q, want %q", *proj.Tty, "s001")
	}
}

// TestProjectProcess_NoTTY asserts that an empty TTY is nil in the projection
// (T1, T3).
func TestProjectProcess_NoTTY(t *testing.T) {
	p := makeTestProcess()
	p.TTY = "" // daemon process
	proj := ProjectProcess(&p, "test-host")
	if proj.Tty != nil {
		t.Errorf("Tty should be nil for daemon process, got %q", *proj.Tty)
	}
}

// TestProjectProcess_StartedAtRFC3339 asserts that a non-zero StartedAt
// is formatted as RFC3339 (T1, T3).
func TestProjectProcess_StartedAtRFC3339(t *testing.T) {
	p := makeTestProcess()
	proj := ProjectProcess(&p, "test-host")
	want := p.StartedAt.Format(time.RFC3339)
	if proj.StartedAt != want {
		t.Errorf("StartedAt = %q, want RFC3339 %q", proj.StartedAt, want)
	}
}

// TestProjectProcess_StartedAtFallback asserts that a zero StartedAt falls
// back to the raw lstart string (T1, T3).
func TestProjectProcess_StartedAtFallback(t *testing.T) {
	p := makeTestProcess()
	p.StartedAt = time.Time{} // zero
	p.StartedRaw = "Thu May  1 12:00:00 2026"
	proj := ProjectProcess(&p, "test-host")
	if proj.StartedAt != p.StartedRaw {
		t.Errorf("StartedAt fallback = %q, want %q", proj.StartedAt, p.StartedRaw)
	}
}

// TestApplyProcessFilter_PidIn asserts that pidIn filters the list to
// matching pids only (T1, T3).
func TestApplyProcessFilter_PidIn(t *testing.T) {
	svc := &stubService{hostID: "local"}
	procs := []Process{
		{ID: ProcessID{Host: "local", PID: 1}, Command: "init"},
		{ID: ProcessID{Host: "local", PID: 42}, Command: "sleep"},
		{ID: ProcessID{Host: "local", PID: 99}, Command: "bash"},
	}
	filter := &ProcessFilter{PidIn: []int{42}}
	out, err := ApplyProcessFilter(context.Background(), svc, procs, filter)
	if err != nil {
		t.Fatalf("ApplyProcessFilter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].ID.PID != 42 {
		t.Errorf("out[0].PID = %d, want 42", out[0].ID.PID)
	}
}

// TestApplyProcessFilter_CommandIn asserts that commandIn filters by command
// basename (T1, T3).
func TestApplyProcessFilter_CommandIn(t *testing.T) {
	svc := &stubService{hostID: "local"}
	procs := []Process{
		{ID: ProcessID{Host: "local", PID: 1}, Command: "init"},
		{ID: ProcessID{Host: "local", PID: 42}, Command: "claude"},
		{ID: ProcessID{Host: "local", PID: 43}, Command: "claude"},
	}
	filter := &ProcessFilter{CommandIn: []string{"claude"}}
	out, err := ApplyProcessFilter(context.Background(), svc, procs, filter)
	if err != nil {
		t.Fatalf("ApplyProcessFilter: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
}

// TestApplyProcessFilter_CwdPrefix asserts that cwdPrefix forces cwd
// resolution and filters by prefix (T1, T3).
func TestApplyProcessFilter_CwdPrefix(t *testing.T) {
	prefix := "/workspace/myrepo"
	svc := &stubService{
		hostID: "local",
		cwdMap: map[int]string{
			42: "/workspace/myrepo/feature",
			99: "/home/alice/other",
		},
	}
	procs := []Process{
		{ID: ProcessID{Host: "local", PID: 42}, Command: "claude"},
		{ID: ProcessID{Host: "local", PID: 99}, Command: "bash"},
	}
	filter := &ProcessFilter{CwdPrefix: &prefix}
	out, err := ApplyProcessFilter(context.Background(), svc, procs, filter)
	if err != nil {
		t.Fatalf("ApplyProcessFilter: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].ID.PID != 42 {
		t.Errorf("out[0].PID = %d, want 42", out[0].ID.PID)
	}
}

// TestApplyProcessFilter_NilFilter asserts that nil filter returns all
// processes unchanged (T1, T3).
func TestApplyProcessFilter_NilFilter(t *testing.T) {
	svc := &stubService{hostID: "local"}
	procs := []Process{
		{ID: ProcessID{Host: "local", PID: 1}},
		{ID: ProcessID{Host: "local", PID: 2}},
	}
	out, err := ApplyProcessFilter(context.Background(), svc, procs, nil)
	if err != nil {
		t.Fatalf("ApplyProcessFilter: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("len(out) = %d, want 2", len(out))
	}
}
