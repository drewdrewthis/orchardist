// T1: Every typed field has a resolver test against a stubbed service.
// T3: No tautological assertions — all assertions can fail on the paths they exercise.

package views_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"

	"github.com/drewdrewthis/git-orchard-rs/daemon/views"
)

// --- stubs ---

type stubGit struct {
	repos []*graphql.Repo
	err   error
}

func (s *stubGit) Repos(_ context.Context) ([]*graphql.Repo, error) {
	return s.repos, s.err
}

type stubTmux struct {
	sessions []*graphql.TmuxSession
	err      error
}

func (s *stubTmux) TmuxSessions(_ context.Context, _ *graphql.TmuxSessionFilter) ([]*graphql.TmuxSession, error) {
	return s.sessions, s.err
}

type stubClaude struct {
	instances []*graphql.ClaudeInstance
	err       error
}

func (s *stubClaude) ClaudeInstances(_ context.Context) ([]*graphql.ClaudeInstance, error) {
	return s.instances, s.err
}

// fixedNow is a stable timestamp for assertions.
const fixedNow = "2026-01-01T00:00:00Z"

func fixedNowFn() *string {
	s := fixedNow
	return &s
}

// --- helpers ---

func newService() *views.Service {
	return views.NewService().SetNow(fixedNowFn)
}

// TestGetWorkView_AllServicesHealthy verifies that when all three sub-services
// return data, the WorkView carries those results and Meta is clean.
func TestGetWorkView_AllServicesHealthy(t *testing.T) {
	svc := newService().
		SetGit(&stubGit{repos: []*graphql.Repo{{ID: "r1", Slug: "owner/repo"}}}).
		SetTmux(&stubTmux{sessions: []*graphql.TmuxSession{{ID: "s1"}}}).
		SetClaudeInstance(&stubClaude{instances: []*graphql.ClaudeInstance{{ID: "ci1"}}})

	view, err := svc.GetWorkView(context.Background())
	if err != nil {
		t.Fatalf("GetWorkView: unexpected error: %v", err)
	}
	if view == nil {
		t.Fatal("GetWorkView returned nil")
	}

	// Repos field
	if len(view.Repos) != 1 || view.Repos[0].ID != "r1" {
		t.Errorf("Repos = %v; want [{ID:r1}]", view.Repos)
	}

	// TmuxSessions field
	if len(view.TmuxSessions) != 1 || view.TmuxSessions[0].ID != "s1" {
		t.Errorf("TmuxSessions = %v; want [{ID:s1}]", view.TmuxSessions)
	}

	// ClaudeInstances field
	if len(view.ClaudeInstances) != 1 || view.ClaudeInstances[0].ID != "ci1" {
		t.Errorf("ClaudeInstances = %v; want [{ID:ci1}]", view.ClaudeInstances)
	}

	// Meta — healthy path
	if view.Meta == nil {
		t.Fatal("Meta nil on healthy path")
	}
	if view.Meta.Provider != "workView" {
		t.Errorf("Meta.Provider = %q; want %q", view.Meta.Provider, "workView")
	}
	if view.Meta.FailureReason != nil {
		t.Errorf("Meta.FailureReason should be nil on healthy path; got %q", *view.Meta.FailureReason)
	}
	if view.Meta.LastSuccessfulFetchAt == nil {
		t.Error("Meta.LastSuccessfulFetchAt should be non-nil on healthy path")
	} else if *view.Meta.LastSuccessfulFetchAt != fixedNow {
		t.Errorf("Meta.LastSuccessfulFetchAt = %q; want %q", *view.Meta.LastSuccessfulFetchAt, fixedNow)
	}
}

// TestGetWorkView_NoProvidersReturnsEmptySlicesAndFailureReason verifies that
// a bare Service (no sub-services wired) returns empty slices (not nil) and
// a populated Meta.FailureReason — per #469 F1.
func TestGetWorkView_NoProvidersReturnsEmptySlicesAndFailureReason(t *testing.T) {
	svc := newService() // no sub-services wired

	view, err := svc.GetWorkView(context.Background())
	if err != nil {
		t.Fatalf("GetWorkView: unexpected error on bare service: %v", err)
	}
	if view == nil {
		t.Fatal("GetWorkView returned nil")
	}

	if view.Repos == nil {
		t.Error("Repos must be non-nil even when git is unwired")
	}
	if view.TmuxSessions == nil {
		t.Error("TmuxSessions must be non-nil even when tmux is unwired")
	}
	if view.ClaudeInstances == nil {
		t.Error("ClaudeInstances must be non-nil even when claude is unwired")
	}
	if view.Meta.FailureReason == nil {
		t.Error("FailureReason must be set when no services are wired")
	}
	if view.Meta.LastSuccessfulFetchAt != nil {
		t.Errorf("LastSuccessfulFetchAt must be nil when errors exist; got %v", *view.Meta.LastSuccessfulFetchAt)
	}
}

// TestGetWorkView_PartialFailure verifies that a single sub-service error
// folds into Meta.FailureReason without failing the entire query; the other
// two fields are populated from their healthy services.
func TestGetWorkView_PartialFailure(t *testing.T) {
	svc := newService().
		SetGit(&stubGit{err: errors.New("git provider down")}).
		SetTmux(&stubTmux{sessions: []*graphql.TmuxSession{{ID: "s1"}}}).
		SetClaudeInstance(&stubClaude{instances: []*graphql.ClaudeInstance{{ID: "ci1"}}})

	view, err := svc.GetWorkView(context.Background())
	if err != nil {
		t.Fatalf("GetWorkView: unexpected error: %v", err)
	}

	// Repos should be empty (service errored), not nil.
	if view.Repos == nil {
		t.Error("Repos must not be nil even on partial failure")
	}
	if len(view.Repos) != 0 {
		t.Errorf("Repos should be empty on git error; got %d", len(view.Repos))
	}

	// Other two fields should be populated.
	if len(view.TmuxSessions) != 1 {
		t.Errorf("TmuxSessions should have 1 session; got %d", len(view.TmuxSessions))
	}
	if len(view.ClaudeInstances) != 1 {
		t.Errorf("ClaudeInstances should have 1 instance; got %d", len(view.ClaudeInstances))
	}

	// FailureReason must mention repos.
	if view.Meta.FailureReason == nil {
		t.Fatal("FailureReason must be set on partial failure")
	}
	if !strings.Contains(*view.Meta.FailureReason, "repos") {
		t.Errorf("FailureReason %q should mention 'repos'", *view.Meta.FailureReason)
	}

	// LastSuccessfulFetchAt must be nil (there was a failure).
	if view.Meta.LastSuccessfulFetchAt != nil {
		t.Error("LastSuccessfulFetchAt must be nil when there is any failure")
	}
}

// TestGetWorkView_MetaProviderLabel verifies the stable provider label that
// clients switch on.
func TestGetWorkView_MetaProviderLabel(t *testing.T) {
	svc := newService()
	view, _ := svc.GetWorkView(context.Background())
	if view.Meta.Provider != "workView" {
		t.Errorf("Meta.Provider = %q; want %q", view.Meta.Provider, "workView")
	}
}

// TestWorkViewResolver_DelegatesToService verifies that WorkViewResolver.WorkView
// calls the service and returns its result without transformation.
func TestWorkViewResolver_DelegatesToService(t *testing.T) {
	svc := newService().
		SetGit(&stubGit{repos: []*graphql.Repo{{ID: "r2", Slug: "a/b"}}}).
		SetTmux(&stubTmux{}).
		SetClaudeInstance(&stubClaude{})

	r := views.NewWorkViewResolver(svc)
	view, err := r.WorkView(context.Background())
	if err != nil {
		t.Fatalf("WorkView resolver: %v", err)
	}
	if len(view.Repos) != 1 || view.Repos[0].ID != "r2" {
		t.Errorf("WorkView resolver did not delegate correctly; repos = %v", view.Repos)
	}
}
