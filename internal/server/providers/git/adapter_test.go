package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReadMainWorktree_AttachedHead is a fixture-grade unit test that
// constructs a minimal `.git/` layout by hand and asserts that the
// adapter resolves HEAD → branch → SHA correctly.
//
// The E2E test (git_e2e_test.go) covers the real-git path. This test
// is here because the bare-handling branches are easier to exercise
// with hand-crafted on-disk shapes than by deleting refs through git.
func TestReadMainWorktree_AttachedHead(t *testing.T) {
	repo := newFakeRepo(t)
	repo.writeRef("refs/heads/main", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	repo.writeHEAD("ref: refs/heads/main")

	a := NewGitWorktreeAdapter(func() []Project {
		return []Project{{ID: "demo", Dir: repo.workdir}}
	})

	w, err := a.Fetch(context.Background(), NewWorktreeID("demo", MainWorktreeName))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if w.Branch != "main" {
		t.Errorf("branch: got %q, want %q", w.Branch, "main")
	}
	if w.Head != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("head: got %q", w.Head)
	}
	if w.Bare {
		t.Errorf("bare: got true, want false")
	}
}

// TestReadMainWorktree_BareWhenRefMissing covers AC4: a HEAD that
// points at a deleted branch surfaces as bare:true without crashing.
func TestReadMainWorktree_BareWhenRefMissing(t *testing.T) {
	repo := newFakeRepo(t)
	// HEAD points at a branch that does NOT exist on disk.
	repo.writeHEAD("ref: refs/heads/lost-branch")

	a := NewGitWorktreeAdapter(func() []Project {
		return []Project{{ID: "demo", Dir: repo.workdir}}
	})

	w, err := a.Fetch(context.Background(), NewWorktreeID("demo", MainWorktreeName))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !w.Bare {
		t.Errorf("bare: got false, want true")
	}
	if w.Branch != "lost-branch" {
		t.Errorf("branch: got %q, want %q", w.Branch, "lost-branch")
	}
	if w.Head != "" {
		t.Errorf("head: got %q, want empty", w.Head)
	}
}

// TestReadMainWorktree_DetachedHead covers detached HEAD: branch is
// empty, head is the SHA, bare is false.
func TestReadMainWorktree_DetachedHead(t *testing.T) {
	repo := newFakeRepo(t)
	repo.writeHEAD("cafebabecafebabecafebabecafebabecafebabe")

	a := NewGitWorktreeAdapter(func() []Project {
		return []Project{{ID: "demo", Dir: repo.workdir}}
	})

	w, err := a.Fetch(context.Background(), NewWorktreeID("demo", MainWorktreeName))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if w.Branch != "" {
		t.Errorf("branch: got %q, want empty", w.Branch)
	}
	if w.Head != "cafebabecafebabecafebabecafebabecafebabe" {
		t.Errorf("head: got %q", w.Head)
	}
	if w.Bare {
		t.Errorf("bare: got true, want false")
	}
}

// TestReadHeadFile_PackedRef covers loose-ref-missing-but-packed-ref-
// present, which happens after `git gc`.
func TestReadHeadFile_PackedRef(t *testing.T) {
	repo := newFakeRepo(t)
	repo.writeFile("packed-refs",
		"# pack-refs with: peeled fully-peeled sorted\n"+
			"feedfacefeedfacefeedfacefeedfacefeedface refs/heads/packed\n")
	repo.writeHEAD("ref: refs/heads/packed")

	branch, head, bare, err := readHeadFile(repo.gitdir, filepath.Join(repo.gitdir, "HEAD"))
	if err != nil {
		t.Fatalf("readHeadFile: %v", err)
	}
	if branch != "packed" {
		t.Errorf("branch: got %q", branch)
	}
	if head != "feedfacefeedfacefeedfacefeedfacefeedface" {
		t.Errorf("head: got %q", head)
	}
	if bare {
		t.Errorf("bare: got true, want false")
	}
}

// TestSplitID round-trips the worktree-id format.
func TestSplitID(t *testing.T) {
	cases := []struct {
		in    WorktreeID
		pid   string
		name  string
		valid bool
	}{
		{NewWorktreeID("demo", "main"), "demo", "main", true},
		{NewWorktreeID("foo/bar", "branch"), "foo/bar", "branch", true},
		{WorktreeID(""), "", "", false},
		{WorktreeID("nocolon"), "", "", false},
		{WorktreeID(":onlyname"), "", "", false},
		{WorktreeID("onlyid:"), "", "", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			pid, name, ok := splitID(c.in)
			if ok != c.valid {
				t.Fatalf("ok: got %v, want %v", ok, c.valid)
			}
			if !ok {
				return
			}
			if pid != c.pid {
				t.Errorf("pid: got %q, want %q", pid, c.pid)
			}
			if name != c.name {
				t.Errorf("name: got %q, want %q", name, c.name)
			}
		})
	}
}

// fakeRepo is a hand-crafted .git directory under a temp workdir. It
// exists so the adapter's bare/detached/packed-refs branches can be
// exercised without invoking the real git binary.
type fakeRepo struct {
	t       *testing.T
	workdir string
	gitdir  string
}

func newFakeRepo(t *testing.T) *fakeRepo {
	t.Helper()
	dir := t.TempDir()
	gitdir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(filepath.Join(gitdir, "refs", "heads"), 0o755); err != nil {
		t.Fatalf("mkdir gitdir: %v", err)
	}
	return &fakeRepo{t: t, workdir: dir, gitdir: gitdir}
}

func (r *fakeRepo) writeFile(rel, body string) {
	r.t.Helper()
	full := filepath.Join(r.gitdir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", rel, err)
	}
}

func (r *fakeRepo) writeHEAD(body string) {
	r.writeFile("HEAD", body+"\n")
}

func (r *fakeRepo) writeRef(ref, sha string) {
	r.writeFile(ref, sha+"\n")
}
