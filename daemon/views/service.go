// Package views owns the WorkView composite type and the Query.workView
// resolver. Per S14, WorkView DELEGATES to per-domain services — it does not
// re-implement any join logic. The round-trip economy is the value: clients
// pull repos + tmux + claude in one query instead of three.
//
// Cross-domain service interfaces are defined here (consumer module), per R4
// ISP. The concrete domain implementations are wired at daemon startup.
package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	graphql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// providerLabel is the stable Meta.Provider label for the WorkView envelope.
const providerLabel = "workView"

// GitService is the narrow interface this domain needs from the git domain.
// Defined here per R4 ISP — the consumer owns the interface.
type GitService interface {
	Repos(ctx context.Context) ([]*graphql.Repo, error)
}

// TmuxService is the narrow interface this domain needs from the tmux domain.
type TmuxService interface {
	TmuxSessions(ctx context.Context, filter *graphql.TmuxSessionFilter) ([]*graphql.TmuxSession, error)
}

// ClaudeInstanceService is the narrow interface this domain needs from the
// claude-instance domain.
type ClaudeInstanceService interface {
	ClaudeInstances(ctx context.Context) ([]*graphql.ClaudeInstance, error)
}

// NowFunc returns the current wall-clock time as an RFC3339 string pointer.
// Swappable in tests.
type NowFunc func() *string

// WorkViewService is the R2 public contract for the views domain.
// Callers import this interface; they never import the concrete Service type.
type WorkViewService interface {
	// GetWorkView returns the composite WorkView, delegating to per-domain
	// services per S14. Sub-errors are folded into Meta.FailureReason rather
	// than failing the entire query, preserving partial results (#469 F1).
	GetWorkView(ctx context.Context) (*graphql.WorkView, error)
}

// Service is the concrete WorkViewService. Consumers depend on the
// WorkViewService interface, not this type (R11: accept interfaces, return
// structs).
type Service struct {
	git    GitService
	tmux   TmuxService
	claude ClaudeInstanceService
	now    NowFunc
}

// NewService constructs a Service. Individual domain dependencies are wired
// via the Set* methods below; nil dependencies degrade gracefully (#469 F1).
func NewService() *Service {
	return &Service{now: defaultNow}
}

// SetGit wires the git service.
func (s *Service) SetGit(g GitService) *Service {
	s.git = g
	return s
}

// SetTmux wires the tmux service.
func (s *Service) SetTmux(t TmuxService) *Service {
	s.tmux = t
	return s
}

// SetClaudeInstance wires the claude-instance service.
func (s *Service) SetClaudeInstance(c ClaudeInstanceService) *Service {
	s.claude = c
	return s
}

// SetNow overrides the wall-clock function (for tests).
func (s *Service) SetNow(fn NowFunc) *Service {
	s.now = fn
	return s
}

// GetWorkView implements WorkViewService. Calls per-domain services in
// parallel-friendly sequence (all are independent reads), folds sub-errors
// into the Meta envelope, and returns empty slices on nil-provider
// configurations so callers never receive nil lists.
func (s *Service) GetWorkView(ctx context.Context) (*graphql.WorkView, error) {
	var (
		repos     []*graphql.Repo
		sessions  []*graphql.TmuxSession
		instances []*graphql.ClaudeInstance
		errs      []string
	)

	if s.git != nil {
		r, err := s.git.Repos(ctx)
		if err != nil {
			errs = append(errs, "repos: "+err.Error())
		} else {
			repos = r
		}
	} else {
		errs = append(errs, "repos: git service not wired")
	}

	if s.tmux != nil {
		ss, err := s.tmux.TmuxSessions(ctx, nil)
		if err != nil {
			errs = append(errs, "tmuxSessions: "+err.Error())
		} else {
			sessions = ss
		}
	} else {
		errs = append(errs, "tmuxSessions: tmux service not wired")
	}

	if s.claude != nil {
		ci, err := s.claude.ClaudeInstances(ctx)
		if err != nil {
			errs = append(errs, "claudeInstances: "+err.Error())
		} else {
			instances = ci
		}
	} else {
		errs = append(errs, "claudeInstances: claude-instance service not wired")
	}

	// Ensure non-nil slices (callers must never receive nil lists).
	if repos == nil {
		repos = []*graphql.Repo{}
	}
	if sessions == nil {
		sessions = []*graphql.TmuxSession{}
	}
	if instances == nil {
		instances = []*graphql.ClaudeInstance{}
	}

	meta := &graphql.Meta{Provider: providerLabel}
	if len(errs) > 0 {
		joined := strings.Join(errs, "; ")
		meta.FailureReason = &joined
		// LastSuccessfulFetchAt stays nil when any sub-error occurred.
	} else {
		meta.LastSuccessfulFetchAt = s.now()
	}

	return &graphql.WorkView{
		Repos:           repos,
		TmuxSessions:    sessions,
		ClaudeInstances: instances,
		Meta:            meta,
	}, nil
}

// defaultNow returns the current time as an RFC3339 string pointer.
func defaultNow() *string {
	t := time.Now().UTC().Format(time.RFC3339)
	return &t
}

// compile-time assertion: Service implements WorkViewService.
var _ WorkViewService = (*Service)(nil)

// FmtError is a thin sentinel so callers can distinguish views errors from
// sub-domain errors. Satisfies R8 (one error style per module).
func FmtError(msg string, args ...any) error {
	return fmt.Errorf("views: "+msg, args...)
}
