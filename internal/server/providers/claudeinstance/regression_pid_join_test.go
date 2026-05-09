package claudeinstance_test

// regression_pid_join_test.go — issue #468 regression guard.
//
// This test proves that the production-adapter wiring path is BROKEN:
// the three concrete finders (NewProcessFinder, NewPaneFinder,
// NewAccountFinder) do not exist yet, so this file intentionally
// fails to compile. That compile error IS the regression signal —
// subsequent tasks will create those constructors and make this test
// buildable (and then passing once resolvePid is fixed to use
// pane.CurrentPid as a fallback).
//
// Scenario exercised (from claude-instances-pid-join.feature AC 5 + AC 6):
//   - heartbeat: tmux_session="alpha", state="working", no claudePid
//   - tmux pane "%59" in session "alpha" reports currentPid=88631
//   - ps snapshot: pid 88631 with command "claude"
//   - expected: result[0].Process != nil, result[0].Pane != nil,
//               result[0].State == graphql.InstanceStateWorking

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
)

// ---------------------------------------------------------------------------
// Narrow interfaces for the stub underlying providers.
// These are the contracts NewProcessFinder, NewPaneFinder, and
// NewAccountFinder will eventually accept. Keeping them narrow here
// lets the stubs below stay minimal (no goroutines, no file I/O).
// ---------------------------------------------------------------------------

// psProvider is the read surface NewProcessFinder needs from the ps package.
type psProvider interface {
	// GetByPid returns the process for the given pid, or (nil, false) on miss.
	GetByPid(ctx context.Context, hostID string, pid int) (*gql.Process, bool)
}

// tmuxProvider is the read surface NewPaneFinder needs from the tmux package.
type tmuxProvider interface {
	// PaneByPid returns the *gql.TmuxPane whose foreground pid matches,
	// or (nil, false) when no pane has that pid.
	PaneByPid(ctx context.Context, hostID string, pid int) (*gql.TmuxPane, bool)
	// PaneBySession returns the first pane in the named session, or
	// (nil, false) when the session is absent or has no panes.
	PaneBySession(ctx context.Context, hostID, session string) (*gql.TmuxPane, bool)
}

// claudeAccountProvider is the read surface NewAccountFinder needs.
type claudeAccountProvider interface {
	// ActiveAccount returns the active Claude account for the host,
	// or (nil, false) when no account is authenticated.
	ActiveAccount(ctx context.Context, hostID string) (*gql.ClaudeAccount, bool)
}

// ---------------------------------------------------------------------------
// Stub implementations — deterministic, no I/O.
// ---------------------------------------------------------------------------

// stubPsProvider returns exactly one process: pid 88631 with command "claude".
type stubPsProvider struct{}

func (s *stubPsProvider) GetByPid(_ context.Context, _ string, pid int) (*gql.Process, bool) {
	if pid == 88631 {
		return &gql.Process{
			ID:      "Process:local:88631",
			Pid:     int64(88631),
			Command: "claude",
		}, true
	}
	return nil, false
}

// stubTmuxProvider exposes one pane "%59" in session "alpha" with currentPid=88631.
type stubTmuxProvider struct{}

func (s *stubTmuxProvider) PaneByPid(_ context.Context, _ string, pid int) (*gql.TmuxPane, bool) {
	if pid == 88631 {
		return alphaPane(), true
	}
	return nil, false
}

func (s *stubTmuxProvider) PaneBySession(_ context.Context, _, session string) (*gql.TmuxPane, bool) {
	if session == "alpha" {
		return alphaPane(), true
	}
	return nil, false
}

func alphaPane() *gql.TmuxPane {
	pid := int64(88631)
	return &gql.TmuxPane{
		ID:         "TmuxPane:local:%59",
		CurrentPid: &pid,
	}
}

// stubClaudeAccountProvider returns a single account for any host.
type stubClaudeAccountProvider struct{}

func (s *stubClaudeAccountProvider) ActiveAccount(_ context.Context, _ string) (*gql.ClaudeAccount, bool) {
	return &gql.ClaudeAccount{ID: "ClaudeAccount:local:dev@example.com"}, true
}

// ---------------------------------------------------------------------------
// Regression test — FAILS TO COMPILE until NewProcessFinder, NewPaneFinder,
// and NewAccountFinder are created in the claudeinstance package.
// ---------------------------------------------------------------------------

// TestRegression_PidJoin_ProductionAdapters_Issue468 demonstrates that
// building a Composer with the three production adapter constructors
// (which don't exist yet) and running it against a heartbeat that has no
// claudePid still produces a non-null Process, non-null Pane, and
// state==working.
//
// Current failure mode:
//
//	(a) COMPILE: NewProcessFinder / NewPaneFinder / NewAccountFinder
//	    are undefined — the production wiring gap is AC#2 in the feature file.
//
//	(b) RUNTIME (after adapters are created): resolvePid returns 0 because
//	    pidFromPaneID is a no-op stub and the heartbeat carries no claudePid.
//	    This is AC#5: "composer.resolvePid falls back to pane.CurrentPid".
func TestRegression_PidJoin_ProductionAdapters_Issue468(t *testing.T) {
	// --- heartbeat file: no claudePid, state=working, fresh timestamp ---
	dir := t.TempDir()
	now := time.Now()
	freshTimestamp := now.Add(-2 * time.Second).Format(time.RFC3339)
	payload := map[string]any{
		"tmux_session":    "alpha",
		"state":           "working",
		"timestamp":       freshTimestamp,
		"lastHeartbeatAt": freshTimestamp,
		// NOTE: no claudePid — the heartbeat predates pid-recording.
		// The fix must source the pid from pane.CurrentPid.
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orchard-claude-alpha.json"), b, 0o600); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	// --- wire the three production adapters with stub underlying providers ---
	//
	// These calls DO NOT COMPILE YET — NewProcessFinder, NewPaneFinder, and
	// NewAccountFinder are the missing production adapters documented in
	// claude-instances-pid-join.feature AC#2. Once those constructors exist,
	// the test will compile and then fail at the assertions below, proving
	// the second half of the bug (resolvePid ignores pane.CurrentPid).
	psStub := &stubPsProvider{}
	tmuxStub := &stubTmuxProvider{}
	acctStub := &stubClaudeAccountProvider{}

	processFinder := claudeinstance.NewProcessFinder(psStub)   // undefined
	paneFinder := claudeinstance.NewPaneFinder(tmuxStub)       // undefined
	accountFinder := claudeinstance.NewAccountFinder(acctStub) // undefined

	// fixed clock so staleness logic is deterministic
	clock := func() time.Time { return now }

	// fakeLiveness (defined in e2e_test.go) marks pid 88631 alive so
	// deriveState does not short-circuit to no_claude on the liveness check.
	liveness := fakeLiveness{alive: map[int]bool{88631: true}}

	composer := claudeinstance.NewComposerWith(
		"local",
		paneFinder,
		processFinder,
		accountFinder,
		liveness,
		clock,
		claudeinstance.HeartbeatStaleAfter,
	)

	reader := claudeinstance.NewFileReader(dir)
	hbs, err := reader.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("want 1 heartbeat, got %d", len(hbs))
	}

	results := composer.Compose(context.Background(), hbs)
	if len(results) != 1 {
		t.Fatalf("want 1 composed instance, got %d", len(results))
	}

	inst := results[0]

	// AC#6: pane must be non-nil (heartbeat tmux_session "alpha" maps to a live pane).
	if inst.Pane == nil {
		t.Error("issue #468 regression: Pane is nil; expected non-nil because heartbeat.tmux_session='alpha' maps to tmux pane %59")
	}

	// AC#5: process must be non-nil (pane.CurrentPid=88631 maps to a live process).
	// This fails today because resolvePid ignores pane.CurrentPid (pidFromPaneID is a no-op).
	if inst.Process == nil {
		t.Error("issue #468 regression: Process is nil; expected non-nil because pane.currentPid=88631 maps to a claude process")
	}

	// AC#7: state must be working (heartbeat fresh + state=working + pid live).
	if inst.State != gql.InstanceStateWorking {
		t.Errorf("issue #468 regression: State=%q, want %q", inst.State, gql.InstanceStateWorking)
	}
}
