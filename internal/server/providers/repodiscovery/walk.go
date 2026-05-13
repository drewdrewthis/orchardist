package repodiscovery

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errNoRepoRoot is returned by walkToRepoRoot when no ancestor of the
// input path contains a `.git` entry. Tested via [errors.Is].
var errNoRepoRoot = errors.New("repodiscovery: no .git ancestor")

// walkToRepoRoot returns the absolute, symlink-resolved path of the
// nearest ancestor of path that contains a `.git` entry. The function
// distinguishes three on-disk shapes:
//
//   - `.git` as a *directory*: a top-level git repo. The containing
//     directory is the repo root and is returned as-is.
//   - `.git` as a *file* whose content starts with `gitdir: …`: a
//     [git worktree](https://git-scm.com/docs/git-worktree) checkout.
//     The function follows the gitdir to recover the **main** repo's
//     working tree and returns that — so all worktrees of one repo
//     collapse to a single discovery key. This is the fix for issue
//     #527's "every worktree shows up as its own repo" symptom.
//   - `.git` as something else (corrupt, dangling): the function falls
//     back to the directory containing it.
//
// Returns [errNoRepoRoot] when no ancestor up to and including the
// filesystem root has a `.git` entry. Symlinks in any path segment are
// resolved via [filepath.EvalSymlinks] on the final result so the
// caller can use the return value as a stable dedupe key.
//
// The walk terminates at the filesystem root (`/`) or when
// [filepath.Dir] stops shrinking (Windows drive root).
func walkToRepoRoot(path string) (string, error) {
	if path == "" {
		return "", errNoRepoRoot
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cur := abs
	for {
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Lstat(gitPath)
		switch {
		case err == nil:
			if info.IsDir() {
				return canonicalise(cur), nil
			}
			// `.git` is a file → worktree pointer. Resolve to main
			// repo when the gitdir matches the standard worktree
			// layout. For submodule worktrees (and other unrecognised
			// shapes) we keep climbing so the walker eventually lands
			// on the superproject's `.git` directory — better than
			// surfacing every submodule worktree as its own ghost
			// repo (issue #527).
			if main, ok := mainRepoFromGitFile(gitPath); ok {
				return canonicalise(main), nil
			}
		case errors.Is(err, fs.ErrNotExist):
			// keep climbing
		default:
			// Permission errors — treat as not-here and keep climbing.
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errNoRepoRoot
		}
		cur = parent
	}
}

// canonicalise resolves symlinks on dir so the discovery union has a
// stable dedupe key. Falls back to the un-resolved path when symlink
// resolution fails (the directory was just stat'd, so the result is
// still real; the dedupe is just imperfect).
func canonicalise(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// mainRepoFromGitFile reads a `.git` file (the worktree marker) and
// returns the main repo's working-tree path.
//
// The file looks like:
//
//	gitdir: /Users/me/work/repo/.git/worktrees/feature-x
//
// The main repo's working tree is the directory containing `.git`,
// which sits at `/Users/me/work/repo`. We derive it by trimming
// `/.git/worktrees/<name>` from the gitdir path.
//
// Submodules complicate matters: a submodule worktree gitdir looks like:
//
//	gitdir: /Users/me/work/super/.git/modules/<sub>/worktrees/<name>
//
// Here the submodule's main working tree is `super/<sub>`, NOT
// derivable purely from the gitdir text. We return ok=false in that
// case so the caller falls back to the worktree path itself — better
// than mis-pointing at the superproject.
func mainRepoFromGitFile(gitFilePath string) (string, bool) {
	data, err := os.ReadFile(gitFilePath)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitdir == "" {
		return "", false
	}
	// Relative gitdir paths are resolved against the file's directory.
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFilePath), gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Look for the `.git/worktrees/<name>` tail. Anything before it is
	// the main repo's working tree (a regular `.git` directory's
	// parent). Submodule worktree gitdirs contain
	// `.git/modules/<sub>/worktrees/<name>` instead; the parent of
	// `.git` there is the superproject, not the submodule, so we
	// refuse and fall back.
	const worktreesSeg = "/.git/worktrees/"
	if idx := strings.Index(gitdir, worktreesSeg); idx >= 0 {
		return gitdir[:idx], true
	}
	return "", false
}

// pathExists is a phantom-guard helper. Returns true when path resolves
// to a directory readable by the daemon process. Permission errors and
// dangling symlinks return false.
func pathExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
