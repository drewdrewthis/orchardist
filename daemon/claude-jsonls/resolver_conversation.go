package claudejsonls

import (
	"context"
	"fmt"
	"strings"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ConversationResolver implements all field resolvers for the
// Conversation GraphQL type. Per RULES.md R6 this is the only file that
// owns the Conversation type; per R3 all reads go through loaders.
//
// The zero value is not usable — construct via NewConversationResolver.
type ConversationResolver struct {
	svc      Service
	loaders  *Loaders
	instance ClaudeInstanceReader // may be nil when claude-instance domain is not wired
}

// NewConversationResolver constructs a resolver. instance may be nil
// when the claude-instance domain is not wired (e.g. in tests that only
// exercise the Conversation fields).
func NewConversationResolver(svc Service, loaders *Loaders, instance ClaudeInstanceReader) *ConversationResolver {
	return &ConversationResolver{svc: svc, loaders: loaders, instance: instance}
}

// --- Query resolvers ---

// Conversations resolves Query.conversations. Returns all cached
// Conversations sorted descending by lastSeenAt.
func (r *ConversationResolver) Conversations(ctx context.Context) ([]*gql.Conversation, error) {
	if r.svc == nil {
		return nil, fmt.Errorf("claude-jsonls service not initialised")
	}
	rows, err := r.loaders.LoadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("conversations: %w", err)
	}
	out := make([]*gql.Conversation, 0, len(rows))
	for _, c := range rows {
		out = append(out, r.svc.ToGraphQL(c))
	}
	return out, nil
}

// Conversation resolves Query.conversation(id). Returns nil when the id
// is unknown or does not have the "Conversation:" prefix.
func (r *ConversationResolver) Conversation(ctx context.Context, id string) (*gql.Conversation, error) {
	if r.svc == nil {
		return nil, fmt.Errorf("claude-jsonls service not initialised")
	}
	uuid, ok := strings.CutPrefix(id, "Conversation:")
	if !ok {
		return nil, nil //nolint:nilnil — unknown prefix; caller may pass a different node type
	}

	keys, err := r.svc.Keys(ctx)
	if err != nil {
		return nil, fmt.Errorf("conversation keys: %w", err)
	}
	for _, k := range keys {
		if k.SessionUUID != uuid {
			continue
		}
		c, err := r.loaders.LoadByID(ctx, k)
		if err != nil {
			return nil, fmt.Errorf("conversation load: %w", err)
		}
		return r.svc.ToGraphQL(c), nil
	}
	return nil, nil
}

// --- Field resolvers for Conversation type ---

// LiveInstances resolves Conversation.liveInstances — the cross-domain
// back-edge declared in this domain's schema partial (S15b). Resolved
// by calling the ClaudeInstanceReader interface (R4/R5: consumer defines
// the interface; never imports the claude-instance provider directly).
//
// Returns an empty slice when the claude-instance domain is not wired
// (e.g. tests, early startup) — not an error, because most historical
// conversations have no live instances.
func (r *ConversationResolver) LiveInstances(ctx context.Context, obj *gql.Conversation) ([]*gql.ClaudeInstance, error) {
	if r.instance == nil {
		return []*gql.ClaudeInstance{}, nil
	}
	instances, err := r.instance.LiveInstancesByConversationUUID(ctx, obj.SessionUUID)
	if err != nil {
		return nil, fmt.Errorf("liveInstances for %s: %w", obj.SessionUUID, err)
	}
	if instances == nil {
		return []*gql.ClaudeInstance{}, nil
	}
	return instances, nil
}
