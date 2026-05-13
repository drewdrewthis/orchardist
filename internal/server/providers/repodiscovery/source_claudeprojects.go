package repodiscovery

import (
	"context"

	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/claudeprojects"
)

// ConversationLister is the narrow read-side contract the
// [ClaudeProjectsSource] depends on. The package-local [claudeprojects.Provider]
// already implements it.
type ConversationLister interface {
	List(ctx context.Context) ([]claudeprojects.Conversation, error)
}

// ClaudeProjectsSource discovers candidate repo CWDs by reading every
// known Claude Code conversation's `cwd` record. Conversations without
// a CWD (older transcripts that predate the `cwd` JSONL record) are
// skipped silently — they contribute nothing rather than failing the
// source.
type ClaudeProjectsSource struct {
	lister ConversationLister
}

// NewClaudeProjectsSource wraps a [ConversationLister].
func NewClaudeProjectsSource(lister ConversationLister) *ClaudeProjectsSource {
	return &ClaudeProjectsSource{lister: lister}
}

// Roots returns every distinct `Conversation.Cwd` known to the
// provider. Walk-to-repo-root + dedupe happens at the [Provider] layer.
// A nil lister or a List error degrades to "no contribution this tick";
// the [Provider]'s union still produces a result from the other sources.
func (s *ClaudeProjectsSource) Roots(ctx context.Context) ([]string, error) {
	if s == nil || s.lister == nil {
		return nil, nil
	}
	convs, err := s.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(convs))
	for _, c := range convs {
		if c.Cwd == nil || *c.Cwd == "" {
			continue
		}
		out = append(out, *c.Cwd)
	}
	return out, nil
}
