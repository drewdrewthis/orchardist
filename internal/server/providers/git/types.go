// Package git implements the git provider per ADR-011 §5.1.
//
// It surfaces one node — Worktree — by reading the plain-text files
// under each registered project's `.git/worktrees/` directory plus the
// project's own `.git/HEAD`. fsnotify drives invalidation: one watcher
// per project, reused across re-fetches.
//
// We intentionally do NOT shell out to `git`. The on-disk layout is a
// stable, documented contract (see `man gitrepository-layout`); reading
// it directly is faster, has no fork/exec overhead, and keeps tests
// hermetic.
//
// Worktree IDs are formatted `<project_id>:<worktree_name>`:
//   - main checkout → `<project_id>:main`
//   - linked worktree → `<project_id>:<dir under .git/worktrees/>`
//
// IDs survive `git worktree repair` because the directory name under
// `.git/worktrees/` is stable; they break if a user manually renames
// the directory, which is rare and indistinguishable from
// add+remove from the daemon's perspective.
package git

import "path/filepath"

// MainWorktreeName is the conventional last segment of a project's main
// checkout's [WorktreeID]. The on-disk `.git/worktrees/` directory only
// lists *linked* worktrees; the main checkout has no entry there, so
// we synthesise this constant. Picked to be unambiguous because git
// itself doesn't allow a linked worktree directory called `main` (it
// would collide with `refs/heads/main` semantics for many users, and
// the practical convention is to name worktrees after issues / topics).
const MainWorktreeName = "main"

// WorktreeID identifies a worktree across the orchard graph. Format:
// `<project_id>:<worktree_name>`. The project_id portion is opaque to
// this package — it comes from the caller registering the project.
type WorktreeID string

// NewWorktreeID composes an ID from a project_id and a worktree name.
// Both arguments are required; empty strings produce a malformed id and
// will fail subsequent parses.
func NewWorktreeID(projectID, name string) WorktreeID {
	return WorktreeID(projectID + ":" + name)
}

// Project carries the minimum the provider needs to scan a project: a
// stable ID plus the absolute path to the working tree's `.git`
// directory's parent. The git provider doesn't care about the project's
// name or any other metadata — that's the config provider's concern.
type Project struct {
	ID  string
	Dir string
}

// Worktree is the value the git provider exposes. The fields mirror
// the GraphQL Worktree node (ADR-011 §5.1) so the resolver layer can
// project them 1:1.
type Worktree struct {
	ID        WorktreeID
	ProjectID string
	Name      string // last segment of the ID; "main" for the main checkout
	Path      string // absolute, cleaned with filepath.Clean
	Branch    string // empty when detached or bare
	Head      string // 40-char SHA, or "" when HEAD cannot be resolved
	Bare      bool
	// Ahead is the commit count this branch is ahead of its upstream (#483).
	// Nil when the branch has no upstream, HEAD is detached, or the count
	// could not be computed (e.g. transient git failure).
	Ahead *int64
	// Behind is the commit count this branch is behind its upstream (#483).
	// Same nil semantics as Ahead.
	Behind *int64
}

// CleanPath returns p with [filepath.Clean] applied. We keep this in a
// helper so every code path that produces a Worktree.Path normalises
// the same way (test fixtures rely on stable equality).
func CleanPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
