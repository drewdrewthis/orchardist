// worktree_tmux_test.go covers Worktree.tmuxPanes AC1 scenarios (#511):
//
//   - Exact cwd match (feature scenario @ line 25)
//   - cwd under path with trailing component (feature scenario @ line 32)
//   - Sibling path excluded — no false-prefix match (feature scenario @ line 39)
//   - Panes sorted deterministically by paneId ascending (feature scenario @ line 47)
//   - tmuxPanes is [TmuxPane!]! (non-nullable) in the schema (feature scenario @ line 53)
//
// The @integration scenarios (lines 25, 32, 47) that require cwd resolution
// via lsof are Darwin-only because the PS adapter's FetchCwds falls back to
// an empty map on Linux until the /proc path lands (ps.FetchCwds #platform).
// The @unit scenarios (lines 39, 53) run on all platforms.

package resolvers

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	psprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/ps"
	tmuxprovider "github.com/drewdrewthis/git-orchard-rs/internal/server/providers/tmux"
)

// ----------------------------------------------------------------------
// Shared fakes
// ----------------------------------------------------------------------

// tmuxTestRunner is a CommandRunner for the tmux adapter. It handles
// the four list-* commands and the info probe using a fixed field
// separator (0x01) matching the adapter's fieldSep constant.
type tmuxTestRunner struct {
	// paneRows is the list of raw pane lines in the format:
	// session\x01windowIdx\x01paneId\x01title\x01command\x01pid\x01width\x01height\x01dead
	paneRows []string
	// sessionRows: session\x01created\x01attached\x01activity\x01windows\x01curwindow
	sessionRows []string
	// windowRows: session\x01index\x01name\x01active\x01panes\x01curpane
	windowRows []string
}

func (r *tmuxTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "tmux" {
		return nil, fmt.Errorf("unexpected command: %s", name)
	}
	// find the subcommand (first non-flag positional arg)
	sub := firstNonFlagTmuxArg(args)
	switch sub {
	case "info":
		return []byte("ok\n"), nil
	case "list-sessions":
		return []byte(strings.Join(r.sessionRows, "\n") + "\n"), nil
	case "list-windows":
		return []byte(strings.Join(r.windowRows, "\n") + "\n"), nil
	case "list-panes":
		return []byte(strings.Join(r.paneRows, "\n") + "\n"), nil
	case "list-clients":
		return []byte(""), nil
	default:
		return []byte(""), nil
	}
}

// firstNonFlagTmuxArg returns the first positional arg in a tmux command line,
// skipping -S / -L flag-value pairs.
func firstNonFlagTmuxArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-S" || args[i] == "-L" {
			i++ // skip value
			continue
		}
		return args[i]
	}
	return ""
}

// paneRow builds a list-panes row in the adapter's field-separated format.
// Fields: session, windowIdx, paneId, title, command, pid, width, height, dead
func paneRow(session, paneID string, pid int) string {
	return strings.Join([]string{
		session, "0", paneID, "title", "zsh",
		fmt.Sprintf("%d", pid),
		"200", "50", "0",
	}, "\x01")
}

// sessionRow builds a list-sessions row.
func sessionRow(name string, activityUnix int64) string {
	return strings.Join([]string{
		name,
		"1700000000",         // created
		"0",                  // attached
		fmt.Sprintf("%d", activityUnix), // last activity
		"1",                  // window count
		"0",                  // current window index
	}, "\x01")
}

// windowRow builds a list-windows row.
func windowRow(session string) string {
	return strings.Join([]string{session, "0", "main", "1", "1", "%1"}, "\x01")
}

// psTestRunner is a CommandRunner for the ps and lsof adapters.
// It maps pid → cwd for lsof calls and returns a minimal ps header for FetchAll.
type psTestRunner struct {
	// cwdByPid maps pid → cwd for fake lsof responses.
	cwdByPid map[int]string
}

const psHeader = "  PID  PPID USER             TT  %CPU    RSS                 STARTED COMMAND\n"

func (r *psTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "ps":
		// Return header-only so FetchAll succeeds with an empty process table.
		return []byte(psHeader), nil
	case "lsof":
		// lsof -a -d cwd -p <pid> -F n
		pid := parseLsofPid(args)
		if cwd, ok := r.cwdByPid[pid]; ok {
			return []byte(fmt.Sprintf("p%d\nn%s\n", pid, cwd)), nil
		}
		// Unknown pid: return non-zero exit (lsof convention for gone process)
		return nil, fmt.Errorf("lsof: no entry for pid %d", pid)
	default:
		return nil, fmt.Errorf("unexpected command: %s", name)
	}
}

// parseLsofPid extracts the pid argument from `lsof -a -d cwd -p <pid> -F n`.
func parseLsofPid(args []string) int {
	for i, a := range args {
		if a == "-p" && i+1 < len(args) {
			var n int
			fmt.Sscanf(args[i+1], "%d", &n)
			return n
		}
	}
	return 0
}

// buildResolver wires a worktreeResolver backed by fake tmux and ps providers.
// The fake tmux runner serves the given pane rows; the fake ps runner maps
// pid → cwd for lsof calls.
//
// On non-Darwin platforms FetchCwds always returns an empty map (no /proc
// path wired), so callers that need cwd-based assertions must skip on non-Darwin
// via t.Skip in the test body.
func buildResolver(t *testing.T, tr *tmuxTestRunner, pidToCwd map[int]string) *worktreeResolver {
	t.Helper()

	hostID := "local"

	// Tmux provider
	tmuxAdapter := tmuxprovider.NewAdapter(tmuxprovider.HostID(hostID)).
		WithRunner(tr).
		WithSocket("/tmp/orchard-test-worktree-tmux.sock")
	tmuxProv := tmuxprovider.New(tmuxAdapter, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tmuxProv.Refresh(ctx); err != nil {
		t.Fatalf("tmux Refresh: %v", err)
	}

	// PS provider
	psRunner := &psTestRunner{cwdByPid: pidToCwd}
	psAdapter := psprovider.NewAdapter(hostID).WithRunner(psRunner)
	psProv := psprovider.New(psAdapter, nil)
	if err := psProv.Start(ctx); err != nil {
		t.Fatalf("ps Start: %v", err)
	}

	return &worktreeResolver{&Resolver{
		Tmux: tmuxProv,
		PS:   psProv,
	}}
}

// ----------------------------------------------------------------------
// AC1 — cwd exact match (feature scenario @ line 25)
// @integration @issue-511
// ----------------------------------------------------------------------

// TestTmuxPanesResolver_ExactCwdMatch verifies that a pane whose cwd exactly
// equals the worktree path is included in tmuxPanes.
func TestTmuxPanesResolver_ExactCwdMatch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cwd resolution via lsof is darwin-only (Linux /proc path not yet wired)")
	}

	const worktreePath = "/Users/me/repo/.worktrees/feat-x"
	const paneID = "%1"
	const fakePID = 10001

	tr := &tmuxTestRunner{
		sessionRows: []string{sessionRow("main", 1700000000)},
		windowRows:  []string{windowRow("main")},
		paneRows:    []string{paneRow("main", paneID, fakePID)},
	}
	r := buildResolver(t, tr, map[int]string{
		fakePID: worktreePath, // cwd == worktree path exactly
	})

	wt := &graphql1.Worktree{
		ID:   "proj:feat-x",
		Path: worktreePath,
		Host: "local",
	}

	got, err := r.TmuxPanes(context.Background(), wt)
	if err != nil {
		t.Fatalf("TmuxPanes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("TmuxPanes = %d panes, want 1; got: %v", len(got), paneIDsOf(got))
	}
	if got[0].PaneID != paneID {
		t.Errorf("TmuxPanes[0].PaneID = %q, want %q", got[0].PaneID, paneID)
	}
}

// ----------------------------------------------------------------------
// AC1 — cwd under path (feature scenario @ line 32)
// @integration @issue-511
// ----------------------------------------------------------------------

// TestTmuxPanesResolver_CwdUnderPath verifies that a pane whose cwd sits
// under the worktree path (cwd has path + "/" as prefix) is included.
func TestTmuxPanesResolver_CwdUnderPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cwd resolution via lsof is darwin-only (Linux /proc path not yet wired)")
	}

	const worktreePath = "/Users/me/repo/.worktrees/feat-x"
	const paneCwd = worktreePath + "/internal/server"
	const paneID = "%2"
	const fakePID = 10002

	tr := &tmuxTestRunner{
		sessionRows: []string{sessionRow("main", 1700000000)},
		windowRows:  []string{windowRow("main")},
		paneRows:    []string{paneRow("main", paneID, fakePID)},
	}
	r := buildResolver(t, tr, map[int]string{
		fakePID: paneCwd,
	})

	wt := &graphql1.Worktree{
		ID:   "proj:feat-x",
		Path: worktreePath,
		Host: "local",
	}

	got, err := r.TmuxPanes(context.Background(), wt)
	if err != nil {
		t.Fatalf("TmuxPanes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("TmuxPanes = %d panes, want 1; got: %v", len(got), paneIDsOf(got))
	}
	if got[0].PaneID != paneID {
		t.Errorf("TmuxPanes[0].PaneID = %q, want %q", got[0].PaneID, paneID)
	}
}

// ----------------------------------------------------------------------
// AC1 — sibling path excluded (feature scenario @ line 39)
// @unit @issue-511
// ----------------------------------------------------------------------

// TestCwdMatchesWorktree_SiblingExcluded verifies the "exact OR path+/"
// match rule: a cwd that merely shares a prefix string with worktree.path
// but continues without a "/" separator must NOT match.
func TestCwdMatchesWorktree_SiblingExcluded(t *testing.T) {
	const path = "/Users/me/repo/.worktrees/feat-x"

	// Sibling path: shares prefix but no "/".
	if cwdMatchesWorktree(path, path+"extra") {
		t.Errorf("cwdMatchesWorktree(%q, %q) = true, want false (sibling path must not match)", path, path+"extra")
	}

	// Exact match must still pass.
	if !cwdMatchesWorktree(path, path) {
		t.Errorf("cwdMatchesWorktree(%q, %q) = false, want true (exact match must pass)", path, path)
	}

	// Under-path must still pass.
	if !cwdMatchesWorktree(path, path+"/sub/dir") {
		t.Errorf("cwdMatchesWorktree(%q, %q) = false, want true (sub-path must pass)", path, path+"/sub/dir")
	}
}

// ----------------------------------------------------------------------
// AC1 — sort order (feature scenario @ line 47)
// @unit @issue-511
// ----------------------------------------------------------------------

// TestTmuxPanesResolver_SortedByPaneId verifies that matching panes are
// returned ordered by paneId ascending (lex order — "%2" < "%5" < "%9").
func TestTmuxPanesResolver_SortedByPaneId(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cwd resolution via lsof is darwin-only (Linux /proc path not yet wired)")
	}

	const worktreePath = "/Users/me/repo/.worktrees/feat-x"
	const fakePID5 = 10005
	const fakePID2 = 10002
	const fakePID9 = 10009

	tr := &tmuxTestRunner{
		sessionRows: []string{sessionRow("main", 1700000000)},
		windowRows:  []string{windowRow("main")},
		paneRows: []string{
			paneRow("main", "%5", fakePID5),
			paneRow("main", "%2", fakePID2),
			paneRow("main", "%9", fakePID9),
		},
	}
	r := buildResolver(t, tr, map[int]string{
		fakePID5: worktreePath,
		fakePID2: worktreePath,
		fakePID9: worktreePath,
	})

	wt := &graphql1.Worktree{
		ID:   "proj:feat-x",
		Path: worktreePath,
		Host: "local",
	}

	got, err := r.TmuxPanes(context.Background(), wt)
	if err != nil {
		t.Fatalf("TmuxPanes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("TmuxPanes = %d panes, want 3; got: %v", len(got), paneIDsOf(got))
	}
	want := []string{"%2", "%5", "%9"}
	for i, id := range want {
		if got[i].PaneID != id {
			t.Errorf("TmuxPanes[%d].PaneID = %q, want %q (not sorted by paneId)", i, got[i].PaneID, id)
		}
	}
}

// ----------------------------------------------------------------------
// AC1 — non-nullable in schema (feature scenario @ line 53)
// @integration @issue-511
// ----------------------------------------------------------------------

// TestWorktreeTmuxPanes_SchemaIsNonNullable verifies that the Worktree.tmuxPanes
// field is declared as [TmuxPane!]! (non-null list of non-null TmuxPane) in
// the embedded schema SDL.
func TestWorktreeTmuxPanes_SchemaIsNonNullable(t *testing.T) {
	sdl := SchemaSDL()
	// The schema must contain the non-nullable declaration.
	needle := "tmuxPanes: [TmuxPane!]!"
	if !strings.Contains(sdl, needle) {
		t.Errorf("schema SDL does not contain %q — Worktree.tmuxPanes must be non-nullable ([TmuxPane!]!)", needle)
	}
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

// paneIDsOf extracts pane IDs for diagnostic messages.
func paneIDsOf(panes []*graphql1.TmuxPane) []string {
	ids := make([]string, len(panes))
	for i, p := range panes {
		ids[i] = p.PaneID
	}
	return ids
}
