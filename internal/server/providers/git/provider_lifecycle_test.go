package git_test

// Tests for the per-project lifecycle of the git provider (issue #571):
// RemoveProject + ApplyProjects diff. Mirrors the shape of the peerproxy
// AddPeer/RemovePeer/ApplyPeers tests so the patterns stay symmetric.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gitprovider "github.com/drewdrewthis/orchardist/internal/server/providers/git"
)

// TestRemoveProject_CancelsAndDrops verifies that RemoveProject cancels
// the per-project watcher, removes its entries from the provider's
// internal state, and stops emitting invalidations for the project.
//
// Steps:
//  1. NewProvider, AddProject for a real git repo on disk.
//  2. Subscribe; drain the cold-load invalidation if any.
//  3. RemoveProject — must return nil.
//  4. HasProject must be false; ListByProject must be empty/nil.
//  5. Mutate the repo's HEAD; subscriber must NOT receive any event
//     within a generous wait window (watcher goroutine was cancelled).
//  6. RemoveProject again — idempotent, must return nil.
func TestRemoveProject_CancelsAndDrops(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; provider lifecycle tests need real git")
	}

	repo := setupRepoForLifecycle(t)
	const projectID = "demo"

	p := gitprovider.NewProvider(slog.Default())
	t.Cleanup(p.Stop)

	if err := p.AddProject(gitprovider.Project{ID: projectID, Dir: repo}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := p.Subscribe(ctx)

	// Drain the cold-load invalidation(s), if any are produced. We only
	// care that the channel is empty before we test "RemoveProject stops
	// future events".
	drainFor(events, 150*time.Millisecond)

	if !p.HasProject(projectID) {
		t.Fatalf("HasProject(%q) before RemoveProject = false; want true", projectID)
	}

	if err := p.RemoveProject(projectID); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	if p.HasProject(projectID) {
		t.Fatalf("HasProject(%q) after RemoveProject = true; want false", projectID)
	}
	if got, _ := p.ListByProject(ctx, projectID); len(got) != 0 {
		t.Fatalf("ListByProject(%q) after RemoveProject returned %d entries; want 0",
			projectID, len(got))
	}

	// Mutate the repo's HEAD. With the watcher gone, no invalidation
	// should arrive within a generous wait window.
	runGitLifecycle(t, repo, "checkout", "-b", "post-remove")

	select {
	case ev, ok := <-events:
		if ok {
			t.Fatalf("received invalidation %v after RemoveProject (watcher still alive)", ev)
		}
		// Channel closed — also acceptable; cancel() in cleanup may have
		// fired between RemoveProject and the select. Either way we got
		// no spurious event for the removed project.
	case <-time.After(300 * time.Millisecond):
		// Expected path: silence.
	}

	// Idempotent second call.
	if err := p.RemoveProject(projectID); err != nil {
		t.Fatalf("RemoveProject (second call) = %v; want nil (idempotent)", err)
	}
	// RemoveProject for an unknown id is also a no-op.
	if err := p.RemoveProject("never-existed"); err != nil {
		t.Fatalf("RemoveProject(unknown) = %v; want nil", err)
	}
}

// TestApplyProjects_BasicDiff verifies the diff algorithm: a project
// in both old and new sets with unchanged Dir is left alone (no respawn);
// a new id is added; a removed id is dropped.
//
// Steps:
//  1. NewProvider with no projects.
//  2. AddProject("keep") and AddProject("drop").
//  3. SpawnCount("keep") must be 1.
//  4. ApplyProjects([keep, add]) — keep unchanged, drop removed, add new.
//  5. Verify HasProject for keep (true), drop (false), add (true).
//  6. SpawnCount("keep") must still be 1 (goroutine NOT restarted).
//  7. SpawnCount("add") must be 1.
func TestApplyProjects_BasicDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; provider lifecycle tests need real git")
	}

	keepRepo := setupRepoForLifecycle(t)
	dropRepo := setupRepoForLifecycle(t)
	addRepo := setupRepoForLifecycle(t)

	p := gitprovider.NewProvider(slog.Default())
	t.Cleanup(p.Stop)

	if err := p.AddProject(gitprovider.Project{ID: "keep", Dir: keepRepo}); err != nil {
		t.Fatalf("AddProject keep: %v", err)
	}
	if err := p.AddProject(gitprovider.Project{ID: "drop", Dir: dropRepo}); err != nil {
		t.Fatalf("AddProject drop: %v", err)
	}

	if got := p.SpawnCount("keep"); got != 1 {
		t.Fatalf("SpawnCount(keep) before ApplyProjects = %d; want 1", got)
	}

	err := p.ApplyProjects([]gitprovider.Project{
		{ID: "keep", Dir: keepRepo},
		{ID: "add", Dir: addRepo},
	})
	if err != nil {
		t.Fatalf("ApplyProjects: %v", err)
	}

	if !p.HasProject("keep") {
		t.Fatal("HasProject(keep) = false after ApplyProjects; want true")
	}
	if p.HasProject("drop") {
		t.Fatal("HasProject(drop) = true after ApplyProjects; want false (removed)")
	}
	if !p.HasProject("add") {
		t.Fatal("HasProject(add) = false after ApplyProjects; want true (added)")
	}

	if got := p.SpawnCount("keep"); got != 1 {
		t.Fatalf("SpawnCount(keep) after ApplyProjects = %d; want 1 (goroutine restarted)", got)
	}
	if got := p.SpawnCount("add"); got != 1 {
		t.Fatalf("SpawnCount(add) after ApplyProjects = %d; want 1", got)
	}
}

// TestApplyProjects_DirChangeRemoveAdd verifies that changing a
// project's Dir is treated as remove + add (the watcher rebinds).
//
// Steps:
//  1. AddProject id="lw" pointing at repoA.
//  2. SpawnCount must be 1.
//  3. ApplyProjects with same id but Dir=repoB.
//  4. SpawnCount must be 2 (re-spawned against the new Dir).
//  5. HasProject must still be true.
func TestApplyProjects_DirChangeRemoveAdd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; provider lifecycle tests need real git")
	}

	repoA := setupRepoForLifecycle(t)
	repoB := setupRepoForLifecycle(t)

	p := gitprovider.NewProvider(slog.Default())
	t.Cleanup(p.Stop)

	if err := p.AddProject(gitprovider.Project{ID: "lw", Dir: repoA}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if got := p.SpawnCount("lw"); got != 1 {
		t.Fatalf("SpawnCount before = %d; want 1", got)
	}

	if err := p.ApplyProjects([]gitprovider.Project{
		{ID: "lw", Dir: repoB},
	}); err != nil {
		t.Fatalf("ApplyProjects: %v", err)
	}

	if got := p.SpawnCount("lw"); got != 2 {
		t.Fatalf("SpawnCount after Dir change = %d; want 2 (remove+add)", got)
	}
	if !p.HasProject("lw") {
		t.Fatal("HasProject(lw) = false after Dir change; want true")
	}
}

// TestRemoveProject_GoroutineLeak verifies that RemoveProject brings the
// goroutine count back to baseline. Add → check growth → Remove → check
// drain.
//
// The exact baseline depends on the test runtime, so we compare
// post-remove against the pre-add baseline, allowing a small slack for
// goroutines the runtime spawns asynchronously.
func TestRemoveProject_GoroutineLeak(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; provider lifecycle tests need real git")
	}

	p := gitprovider.NewProvider(slog.Default())
	t.Cleanup(p.Stop)

	baseline := runtime.NumGoroutine()

	repo := setupRepoForLifecycle(t)
	if err := p.AddProject(gitprovider.Project{ID: "leak-check", Dir: repo}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	// Wait briefly for the watcher + consumer goroutines to actually start.
	time.Sleep(50 * time.Millisecond)

	added := runtime.NumGoroutine()
	if added <= baseline {
		t.Fatalf("goroutine count did not grow after AddProject: baseline=%d after_add=%d",
			baseline, added)
	}

	if err := p.RemoveProject("leak-check"); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	// Give the runtime a moment to schedule the goroutines' exit, then
	// poll back to baseline with a deadline. We tolerate up to 2 extra
	// goroutines for runtime-internal scheduling noise.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+2 {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines did not drain after RemoveProject: baseline=%d, now=%d",
		baseline, runtime.NumGoroutine())
}

// setupRepoForLifecycle creates a minimal git repo with one commit so
// HEAD resolves and fsnotify has something to watch. Distinct from the
// e2e setup helper to keep package-local tests self-contained.
func setupRepoForLifecycle(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitLifecycle(t, repo, "init", "-b", "main")
	runGitLifecycle(t, repo, "config", "user.email", "issue571@example.com")
	runGitLifecycle(t, repo, "config", "user.name", "issue571")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitLifecycle(t, repo, "add", "README.md")
	runGitLifecycle(t, repo, "commit", "-m", "initial")
	return repo
}

func runGitLifecycle(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
}

// drainFor reads any pending invalidations off ch for d, discarding
// them. Used to clear cold-load events before asserting silence.
func drainFor[T any](ch <-chan T, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case <-ch:
		case <-time.After(20 * time.Millisecond):
			if time.Now().After(deadline) {
				return
			}
		}
	}
}
