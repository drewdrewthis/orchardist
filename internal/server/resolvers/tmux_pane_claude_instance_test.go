package resolvers

// Tests for tmuxPaneResolver.ClaudeInstance (AC 4 of issue #468).
//
// Scenarios covered:
//  1. Provider has an instance whose Pane.ID matches obj.ID → resolver returns it.
//  2. Provider has an instance whose Pane.ID does NOT match → resolver returns (nil, nil).
//  3. r.ClaudeInstance is nil → resolver returns (nil, nil).
//
// Deeper integration coverage (multiple instances, pane resolve chain, state
// derivation) is exercised end-to-end via
// internal/server/providers/claudeinstance/e2e_test.go, which boots a real
// httptest GraphQL server and queries the full resolver chain.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeinstance"
)

// --------------------------------------------------------------------------
// Test-local fakes — minimal implementations of the claudeinstance
// dependency interfaces so we don't need a real tmux/ps/account provider.
// --------------------------------------------------------------------------

// staticPaneFinder always returns a single pre-built pane regardless of which
// pid or session is requested. It satisfies claudeinstance.PaneFinder.
type staticPaneFinder struct{ pane *graphql1.TmuxPane }

func (f *staticPaneFinder) FindByPid(_ context.Context, _ string, _ int) (*graphql1.TmuxPane, bool) {
	if f.pane == nil {
		return nil, false
	}
	return f.pane, true
}

func (f *staticPaneFinder) FindBySession(_ context.Context, _, _ string) (*graphql1.TmuxPane, bool) {
	if f.pane == nil {
		return nil, false
	}
	return f.pane, true
}

// alwaysAlive satisfies claudeinstance.LivenessChecker and reports every pid
// as alive so state derivation never collapses to no_claude in tests.
type alwaysAlive struct{}

func (alwaysAlive) IsAlive(_ int) bool { return true }

// --------------------------------------------------------------------------
// Helper: build a claudeinstance.Provider pre-seeded with one instance
// whose Pane.ID is paneID. The heartbeat is written to a temp dir and
// loaded via provider.Start so the cache is hydrated before the test runs.
// --------------------------------------------------------------------------
func buildProviderWithPane(t *testing.T, paneID string) *claudeinstance.Provider {
	t.Helper()

	heartbeatDir := t.TempDir()
	now := time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC)
	freshTimestamp := now.Add(-2 * time.Second).Format(time.RFC3339)
	clock := func() time.Time { return now }

	// Write a single heartbeat file with a known pid so the instance id is
	// deterministic (ClaudeInstance:local:10042) and the liveness check passes.
	payload := map[string]any{
		"tmux_session":    "test-session",
		"session_id":      "uuid-test",
		"state":           "working",
		"timestamp":       freshTimestamp,
		"claudePid":       10042,
		"lastHeartbeatAt": freshTimestamp,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(heartbeatDir, "orchard-claude-test-session.json"), b, 0o600); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	// The static pane finder returns a TmuxPane whose ID is paneID, so the
	// composer attaches that pane to the instance it builds.
	pane := &graphql1.TmuxPane{ID: paneID}
	composer := claudeinstance.NewComposerWith(
		"local",
		&staticPaneFinder{pane: pane},
		nil, // no process finder needed for these tests
		nil, // no account finder needed
		alwaysAlive{},
		clock,
		claudeinstance.HeartbeatStaleAfter,
	)
	reader := claudeinstance.NewFileReader(heartbeatDir)
	provider := claudeinstance.NewWith("local", reader, composer, clock)

	if err := provider.Start(context.Background()); err != nil {
		t.Fatalf("provider.Start: %v", err)
	}
	return provider
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestTmuxPaneClaudeInstance_MatchingPane verifies that when the provider
// contains a ClaudeInstance whose Pane.ID equals obj.ID, the resolver returns
// that instance (non-nil).
func TestTmuxPaneClaudeInstance_MatchingPane(t *testing.T) {
	const paneID = "TmuxPane:local:%59"

	provider := buildProviderWithPane(t, paneID)
	r := &tmuxPaneResolver{&Resolver{ClaudeInstance: provider}}
	obj := &graphql1.TmuxPane{ID: paneID}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("ClaudeInstance() returned nil, want non-nil *graphql1.ClaudeInstance")
	}
	if got.Pane == nil || got.Pane.ID != paneID {
		t.Errorf("ClaudeInstance().Pane.ID = %v, want %q", got.Pane, paneID)
	}
}

// TestTmuxPaneClaudeInstance_NoMatchingPane verifies that when no instance in
// the provider has a pane matching obj.ID, the resolver returns (nil, nil).
func TestTmuxPaneClaudeInstance_NoMatchingPane(t *testing.T) {
	// Provider is seeded with a pane whose ID does NOT match the query object.
	provider := buildProviderWithPane(t, "TmuxPane:local:%59")
	r := &tmuxPaneResolver{&Resolver{ClaudeInstance: provider}}
	// Ask for a different pane — %99, which no instance is associated with.
	obj := &graphql1.TmuxPane{ID: "TmuxPane:local:%99"}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (no matching pane)", got)
	}
}

// TestTmuxPaneClaudeInstance_NilProvider verifies that when r.ClaudeInstance
// is nil (provider not wired), the resolver returns (nil, nil) without
// panicking.
func TestTmuxPaneClaudeInstance_NilProvider(t *testing.T) {
	r := &tmuxPaneResolver{&Resolver{ClaudeInstance: nil}}
	obj := &graphql1.TmuxPane{ID: "TmuxPane:local:%42"}

	got, err := r.ClaudeInstance(context.Background(), obj)
	if err != nil {
		t.Fatalf("ClaudeInstance() unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("ClaudeInstance() = %+v, want nil (nil provider)", got)
	}
}
