package claudejsonls

import (
	"context"
	"log/slog"
	"strings"

	gql "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
)

// ConversationChangedEmitter manages the Subscription.conversationChanged
// subscription. It filters the provider's invalidation stream by
// sessionUUID and emits the freshly-loaded Conversation on each match.
//
// Per RULES.md R16: emit happens AFTER the cache write (the provider
// broadcasts only after cachePut / reload completes), so subscribers
// see fresh data on their first re-read.
//
// Per RULES.md S7: the subscription emits the affected node value, not
// a full re-fetch of the list.
type ConversationChangedEmitter struct {
	svc    Service
	logger *slog.Logger
}

// NewConversationChangedEmitter constructs an emitter backed by svc.
func NewConversationChangedEmitter(svc Service, logger *slog.Logger) *ConversationChangedEmitter {
	if logger == nil {
		logger = slog.Default()
	}
	return &ConversationChangedEmitter{svc: svc, logger: logger}
}

// Subscribe returns a channel that emits the freshly-loaded
// Conversation whenever the underlying JSONL for sessionUUID changes.
// Emits nil when the watcher reports the file was removed, so callers
// can clear stale state.
//
// The returned channel is closed when ctx is done.
func (e *ConversationChangedEmitter) Subscribe(
	ctx context.Context,
	sessionUUID string,
) (<-chan *gql.Conversation, error) {
	src := e.svc.Subscribe(ctx)
	out := make(chan *gql.Conversation, 1)

	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				e.logger.Error("claude-jsonls: subscription goroutine panicked",
					"recover", r, "session_uuid", sessionUUID)
			}
		}()

		for ev := range src {
			if ev.Key.SessionUUID != sessionUUID {
				continue
			}
			// Reload the fresh value AFTER the provider's cache write
			// (R16: provider broadcasts post-write so we always read
			// the new state here).
			gqlID := "Conversation:" + sessionUUID
			c, err := e.reloadConversation(ctx, gqlID)
			if err != nil {
				e.logger.Warn("claude-jsonls: subscription reload failed",
					"session_uuid", sessionUUID, "err", err)
				continue
			}
			select {
			case out <- c:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

// reloadConversation looks up the Conversation by gqlID (format
// "Conversation:<uuid>"). Returns nil when not found (file removed).
func (e *ConversationChangedEmitter) reloadConversation(ctx context.Context, gqlID string) (*gql.Conversation, error) {
	uuid, ok := strings.CutPrefix(gqlID, "Conversation:")
	if !ok {
		return nil, nil
	}
	c, found := e.svc.GetBySessionUUID(ctx, uuid)
	if !found {
		// File was removed — emit nil per the schema doc.
		return nil, nil
	}
	return e.svc.ToGraphQL(c), nil
}

// NodeChangedEmitter handles the Subscription.nodeChanged subscription
// for Conversation nodes. Dispatched by the daemon's central
// subscribeNodeChanged when the id has prefix "Conversation:".
type NodeChangedEmitter struct {
	svc    Service
	logger *slog.Logger
}

// NewNodeChangedEmitter constructs an emitter.
func NewNodeChangedEmitter(svc Service, logger *slog.Logger) *NodeChangedEmitter {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeChangedEmitter{svc: svc, logger: logger}
}

// Subscribe returns a channel that emits the freshly-loaded Conversation
// (as a gql.Node) whenever the underlying JSONL changes.
func (e *NodeChangedEmitter) Subscribe(ctx context.Context, id string) (<-chan gql.Node, error) {
	uuid, ok := strings.CutPrefix(id, "Conversation:")
	if !ok {
		return nil, nil //nolint:nilnil — no subscription for non-Conversation ids
	}

	src := e.svc.Subscribe(ctx)
	out := make(chan gql.Node, 1)

	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				e.logger.Error("claude-jsonls: nodeChanged goroutine panicked",
					"recover", r, "id", id)
			}
		}()

		for ev := range src {
			if ev.Key.SessionUUID != uuid {
				continue
			}
			c, found := e.svc.GetBySessionUUID(ctx, uuid)
			if !found {
				continue
			}
			node := gql.Node(e.svc.ToGraphQL(c))
			select {
			case out <- node:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
