package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GitWorktreeAdapter reads `.git/worktrees/<name>/HEAD` + `gitdir` files
// and the project's own `.git/HEAD` directly via stdlib os + bufio.
// It is stateless — every call resolves against the live filesystem.
// Per L4, this is called only from the provider's cache-miss or refresh
// path, never directly from a field resolver.
type GitWorktreeAdapter struct {
	// projects is captured by reference: the provider mutates the slice
	// when projects are registered; the adapter sees the latest set on
	// every call.
	projects func() []Project
}

// NewGitWorktreeAdapter constructs an adapter that scans the projects
// returned by the supplier function on each call.
func NewGitWorktreeAdapter(projects func() []Project) *GitWorktreeAdapter {
	return &GitWorktreeAdapter{projects: projects}
}

// Fetch returns a single Worktree by id. Returns os.ErrNotExist when
// the project or the named worktree no longer exists.
func (a *GitWorktreeAdapter) Fetch(ctx context.Context, id WorktreeID) (Worktree, error) {
	if err := ctx.Err(); err != nil {
		return Worktree{}, err
	}
	projectID, name, ok := splitID(id)
	if !ok {
		return Worktree{}, fmt.Errorf("git: malformed worktree id %q", id)
	}
	for _, p := range a.projects() {
		if p.ID != projectID {
			continue
		}
		if name == MainWorktreeName {
			return readMainWorktree(p)
		}
		return readLinkedWorktree(p, name)
	}
	return Worktree{}, fmt.Errorf("git: project %q: %w", projectID, fs.ErrNotExist)
}

// FetchAll lists every worktree across every registered project. Used
// for cold boot and for full refreshes after fsnotify wakes up.
func (a *GitWorktreeAdapter) FetchAll(ctx context.Context) (map[WorktreeID]Worktree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[WorktreeID]Worktree{}
	for _, p := range a.projects() {
		main, err := readMainWorktree(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("git: project %q main worktree: %w", p.ID, err)
		}
		out[main.ID] = main

		linked, err := listLinkedWorktrees(p)
		if err != nil {
			return nil, fmt.Errorf("git: project %q linked worktrees: %w", p.ID, err)
		}
		for _, w := range linked {
			out[w.ID] = w
		}
	}
	return out, nil
}

// readMainWorktree composes the Worktree value for a project's primary checkout.
func readMainWorktree(p Project) (Worktree, error) {
	gitDir, err := resolveGitDir(p.Dir)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve gitdir: %w", err)
	}
	headFile := filepath.Join(gitDir, "HEAD")
	branch, head, bare, err := readHeadFile(gitDir, headFile)
	if err != nil {
		return Worktree{}, fmt.Errorf("read HEAD: %w", err)
	}
	ahead, behind := computeAheadBehind(p.Dir, branch, bare)
	return Worktree{
		ID:        NewWorktreeID(p.ID, MainWorktreeName),
		ProjectID: p.ID,
		Name:      MainWorktreeName,
		Path:      CleanPath(p.Dir),
		Branch:    branch,
		Head:      head,
		Bare:      bare,
		Ahead:     ahead,
		Behind:    behind,
	}, nil
}

// listLinkedWorktrees enumerates entries under `.git/worktrees/`.
func listLinkedWorktrees(p Project) ([]Worktree, error) {
	gitDir, err := resolveGitDir(p.Dir)
	if err != nil {
		return nil, err
	}
	wtRoot := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Worktree
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		w, err := readLinkedWorktree(p, e.Name())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("worktree %q: %w", e.Name(), err)
		}
		out = append(out, w)
	}
	return out, nil
}

// readLinkedWorktree reads `.git/worktrees/<name>/HEAD` and `gitdir`
// to build a Worktree.
func readLinkedWorktree(p Project, name string) (Worktree, error) {
	gitDir, err := resolveGitDir(p.Dir)
	if err != nil {
		return Worktree{}, err
	}
	wtDir := filepath.Join(gitDir, "worktrees", name)

	gitdirPath := filepath.Join(wtDir, "gitdir")
	gitdirContents, err := readTrimmed(gitdirPath)
	if err != nil {
		return Worktree{}, fmt.Errorf("read gitdir: %w", err)
	}
	worktreeRoot := filepath.Dir(gitdirContents)

	headFile := filepath.Join(wtDir, "HEAD")
	branch, head, bare, err := readHeadFile(gitDir, headFile)
	if err != nil {
		return Worktree{}, fmt.Errorf("read HEAD for %q: %w", name, err)
	}

	ahead, behind := computeAheadBehind(worktreeRoot, branch, bare)
	return Worktree{
		ID:        NewWorktreeID(p.ID, name),
		ProjectID: p.ID,
		Name:      name,
		Path:      CleanPath(worktreeRoot),
		Branch:    branch,
		Head:      head,
		Bare:      bare,
		Ahead:     ahead,
		Behind:    behind,
	}, nil
}

// computeAheadBehind shells out to `git rev-list --left-right --count
// @{u}...HEAD` from worktreePath and parses the two-column output (#483).
// Returns (nil, nil) on any failure — ahead/behind is enrichment, not load-bearing.
// This is the ONE intentional L4 exception: git shellout is in the adapter
// (stateless I/O layer), not in a field resolver hot path.
func computeAheadBehind(worktreePath, branch string, bare bool) (*int64, *int64) {
	if branch == "" || bare {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-list",
		"--left-right", "--count", "@{u}...HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 2 {
		return nil, nil
	}
	behindN, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, nil
	}
	aheadN, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, nil
	}
	return &aheadN, &behindN
}

// readHeadFile parses HEAD. Returns (branch, head, bare, err).
func readHeadFile(gitDir, headFile string) (string, string, bool, error) {
	raw, err := readTrimmed(headFile)
	if err != nil {
		return "", "", false, err
	}
	if raw == "" {
		return "", "", true, nil
	}
	if strings.HasPrefix(raw, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(raw, "ref:"))
		branch := strings.TrimPrefix(ref, "refs/heads/")
		sha, err := resolveRef(gitDir, ref)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return branch, "", true, nil
			}
			return "", "", false, err
		}
		return branch, sha, false, nil
	}
	if isSHA1(raw) {
		return "", raw, false, nil
	}
	return "", "", true, nil
}

// resolveRef resolves a `refs/heads/<branch>`-style ref to a 40-char SHA.
func resolveRef(gitDir, ref string) (string, error) {
	loose := filepath.Join(gitDir, ref)
	if sha, err := readTrimmed(loose); err == nil {
		if isSHA1(sha) {
			return sha, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	packed := filepath.Join(gitDir, "packed-refs")
	f, err := os.Open(packed) //nolint:gosec // trusted internal path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fs.ErrNotExist
		}
		return "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		if parts[1] == ref && isSHA1(parts[0]) {
			return parts[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan packed-refs: %w", err)
	}
	return "", fs.ErrNotExist
}

// resolveGitDir returns the repo's .git directory for a project rooted
// at workdir. Handles both a real directory and a gitfile.
func resolveGitDir(workdir string) (string, error) {
	candidate := filepath.Join(workdir, ".git")
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return candidate, nil
	}
	body, err := readTrimmed(candidate)
	if err != nil {
		return "", err
	}
	gd := strings.TrimSpace(strings.TrimPrefix(body, "gitdir:"))
	if gd == "" {
		return "", fmt.Errorf("malformed gitfile at %q", candidate)
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(workdir, gd)
	}
	return filepath.Clean(gd), nil
}

// readTrimmed reads a file and returns its contents with whitespace stripped.
func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // trusted internal path
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// isSHA1 returns true when s is a 40-char hex string.
func isSHA1(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// splitID parses a `<project_id>:<name>` worktree id.
func splitID(id WorktreeID) (string, string, bool) {
	s := string(id)
	idx := strings.LastIndex(s, ":")
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}
