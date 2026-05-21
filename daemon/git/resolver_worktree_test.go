package git

import (
	"context"
	"testing"
)

// --- nop cross-domain readers for testing ---

type countingPsReader struct {
	calls int
	procs []Process
}

func (r *countingPsReader) ProcessesByCwd(_ context.Context, _ string) ([]Process, error) {
	r.calls++
	return r.procs, nil
}

// TestWorktreeResolverScalars verifies each scalar field resolver projects
// correctly (T1: every typed field has a resolver test).
func TestWorktreeResolverScalars(t *testing.T) {
	ahead := int64(2)
	behind := int64(1)
	slug := "owner/repo"
	wt := Worktree{
		ID:        "proj:feature",
		ProjectID: "proj",
		Name:      "feature",
		Path:      "/tmp/feature",
		Branch:    "feature/issue-42",
		Head:      "abc1234567890123456789012345678901234567890",
		Bare:      false,
		Ahead:     &ahead,
		Behind:    &behind,
		RepoSlug:  &slug,
	}

	loader := NewWorktreeLoader(newStubService())
	r := NewWorktreeResolver(loader, NopPsReader{}, NopTmuxReader{}, NopClaudeReader{}, NopGhReader{})
	ctx := context.Background()

	// id
	if id, err := r.WorktreeID(ctx, wt); err != nil || id != "proj:feature" {
		t.Errorf("WorktreeID: got %q, err %v", id, err)
	}
	// path
	if path, err := r.WorktreePath(ctx, wt); err != nil || path != "/tmp/feature" {
		t.Errorf("WorktreePath: got %q, err %v", path, err)
	}
	// branch
	if branch, err := r.WorktreeBranch(ctx, wt); err != nil || branch != "feature/issue-42" {
		t.Errorf("WorktreeBranch: got %q, err %v", branch, err)
	}
	// head
	if head, err := r.WorktreeHead(ctx, wt); err != nil || head != "abc1234567890123456789012345678901234567890" {
		t.Errorf("WorktreeHead: got %q, err %v", head, err)
	}
	// bare
	if bare, err := r.WorktreeBare(ctx, wt); err != nil || bare {
		t.Errorf("WorktreeBare: got %v, err %v", bare, err)
	}
	// host
	if host, err := r.WorktreeHost(ctx, wt); err != nil || host != "local" {
		t.Errorf("WorktreeHost: got %q, err %v", host, err)
	}
	// repo
	if repo, err := r.WorktreeRepo(ctx, wt); err != nil || repo == nil || *repo != "owner/repo" {
		t.Errorf("WorktreeRepo: got %v, err %v", repo, err)
	}
	// ahead
	if a, err := r.WorktreeAhead(ctx, wt); err != nil || a == nil || *a != 2 {
		t.Errorf("WorktreeAhead: got %v, err %v", a, err)
	}
	// behind
	if b, err := r.WorktreeBehind(ctx, wt); err != nil || b == nil || *b != 1 {
		t.Errorf("WorktreeBehind: got %v, err %v", b, err)
	}
}

// TestWorktreeResolverAheadBehindNil verifies nil ahead/behind map to nil (T1).
func TestWorktreeResolverAheadBehindNil(t *testing.T) {
	wt := Worktree{Bare: true}
	loader := NewWorktreeLoader(newStubService())
	r := NewWorktreeResolver(loader, NopPsReader{}, NopTmuxReader{}, NopClaudeReader{}, NopGhReader{})

	if a, _ := r.WorktreeAhead(context.Background(), wt); a != nil {
		t.Errorf("expected nil ahead, got %v", a)
	}
	if b, _ := r.WorktreeBehind(context.Background(), wt); b != nil {
		t.Errorf("expected nil behind, got %v", b)
	}
}

// TestWorktreeResolverProcessesDelegatesToPsReader verifies the cross-domain
// delegation goes through PsReader (T1, R4, S15b).
func TestWorktreeResolverProcessesDelegatesToPsReader(t *testing.T) {
	ps := &countingPsReader{
		procs: []Process{{ID: "1", Pid: 42, Command: "vim", Cwd: "/tmp/feature"}},
	}
	loader := NewWorktreeLoader(newStubService())
	r := NewWorktreeResolver(loader, ps, NopTmuxReader{}, NopClaudeReader{}, NopGhReader{})

	wt := Worktree{Path: "/tmp/feature"}
	procs, err := r.WorktreeProcesses(context.Background(), wt)
	if err != nil {
		t.Fatalf("WorktreeProcesses: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("expected 1 process, got %d", len(procs))
	}
	if ps.calls != 1 {
		t.Errorf("expected 1 PsReader call, got %d", ps.calls)
	}
}

// TestWorktreeResolverPRNilOnNoBranch verifies PR is nil when branch is empty (T1).
func TestWorktreeResolverPRNilOnNoBranch(t *testing.T) {
	loader := NewWorktreeLoader(newStubService())
	r := NewWorktreeResolver(loader, NopPsReader{}, NopTmuxReader{}, NopClaudeReader{}, NopGhReader{})

	wt := Worktree{Branch: ""} // no branch → no PR
	pr, err := r.WorktreePR(context.Background(), wt)
	if err != nil {
		t.Fatalf("WorktreePR: unexpected error: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil PR for empty branch, got %v", pr)
	}
}

// TestWorktreeResolverIssueNilOnNoSlug verifies Issue is nil when no repo slug (T1).
func TestWorktreeResolverIssueNilOnNoSlug(t *testing.T) {
	loader := NewWorktreeLoader(newStubService())
	r := NewWorktreeResolver(loader, NopPsReader{}, NopTmuxReader{}, NopClaudeReader{}, NopGhReader{})

	wt := Worktree{Branch: "feature", RepoSlug: nil} // no slug → no issue
	issue, err := r.WorktreeIssue(context.Background(), wt)
	if err != nil {
		t.Fatalf("WorktreeIssue: unexpected error: %v", err)
	}
	if issue != nil {
		t.Errorf("expected nil issue when no repo slug, got %v", issue)
	}
}
