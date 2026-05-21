package git

import "path/filepath"

// MainWorktreeName is the conventional last segment of a project's main
// checkout's WorktreeID. The on-disk `.git/worktrees/` directory only
// lists *linked* worktrees; the main checkout has no entry there, so
// we synthesise this constant.
const MainWorktreeName = "main"

// RepoID is the stable identifier for a repo across config edits and
// daemon restarts. Derived from the slug.
type RepoID string

// Repo is the in-memory representation of a configured repo.
// On-disk serialisation goes through RepoRow — Repo itself never
// hits a marshaller.
type Repo struct {
	ID   RepoID
	Slug string
	Path string
}

// WorktreeID identifies a worktree across the orchard graph. Format:
// `<project_id>:<worktree_name>`. The project_id portion is opaque to
// this package — it comes from the caller registering the project.
type WorktreeID string

// NewWorktreeID composes an ID from a project_id and a worktree name.
func NewWorktreeID(projectID, name string) WorktreeID {
	return WorktreeID(projectID + ":" + name)
}

// Project carries the minimum the provider needs to scan a project.
type Project struct {
	ID  string
	Dir string
}

// Worktree is the value the git provider exposes.
type Worktree struct {
	ID        WorktreeID
	ProjectID string
	Name      string  // last segment of the ID; "main" for the main checkout
	Path      string  // absolute, cleaned with filepath.Clean
	Branch    string  // empty when detached or bare
	Head      string  // 40-char SHA, or "" when HEAD cannot be resolved
	Bare      bool
	Ahead     *int64
	Behind    *int64
	// RepoSlug is the owner/repo slug derived from the origin remote.
	// Nil when origin is not a GitHub URL.
	RepoSlug *string
}

// CleanPath returns p with filepath.Clean applied.
func CleanPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
