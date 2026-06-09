// Package claudejsonls implements the claude-jsonls domain: raw
// Claude Code JSONL parsing. It owns the Conversation node and the
// Query.{conversations,conversation} + Subscription.conversationChanged
// resolvers.
//
// This is the lowest layer of the Claude stack. Higher-layer domains
// (claude-instance, contracts) consume this domain's Service interface;
// they do NOT import the Provider or adapter types.
//
// Heavy fields (full transcripts, message bodies) are deliberately
// absent per RULES.md S10 and ADR-016. Clients read those via
// GET /v1/conversations/<sessionUuid>/jsonl on the same listener.
package claudejsonls

import (
	"context"
	"time"

	gql "github.com/drewdrewthis/orchardist/internal/server/graphql"
)

// ConversationID uniquely identifies a Conversation across hosts.
// HostID is a free-form string (machine-id on Linux, IOPlatformUUID
// on macOS). Tests inject a stable fixture ("test-host").
type ConversationID struct {
	HostID      string
	SessionUUID string
}

// GraphQLID returns the stable orchard id: "Conversation:<sessionUuid>".
func (id ConversationID) GraphQLID() string {
	return "Conversation:" + id.SessionUUID
}

// Conversation is the in-memory representation of a Claude Code
// transcript. All fields derive from the JSONL on disk — no sidecar
// metadata. FirstSeenAt and LastSeenAt are pointers because an empty
// JSONL (file exists but contains no records) has neither timestamp.
type Conversation struct {
	ID           ConversationID
	Path         string
	Cwd          *string
	FirstSeenAt  *time.Time
	LastSeenAt   *time.Time
	MessageCount int64
	CustomTitle  *string
	AgentName    *string
}

// Service is the only API that resolvers and other domains may call.
// Resolvers import daemon/claude-jsonls and use this interface; they
// never import Provider or adapter types directly (RULES.md R2).
type Service interface {
	// Get returns one Conversation by ID. Returns an error wrapping
	// fs.ErrNotExist when not found.
	Get(ctx context.Context, key ConversationID) (Conversation, error)

	// GetMany is the DataLoader-friendly batch read. Unique keys are
	// deduplicated and a single FetchAll covers all cache misses.
	GetMany(ctx context.Context, keys []ConversationID) (map[ConversationID]Conversation, error)

	// Keys returns every cached ConversationID.
	Keys(ctx context.Context) ([]ConversationID, error)

	// List returns every cached Conversation, sorted descending by
	// LastSeenAt (most-recently active first).
	List(ctx context.Context) ([]Conversation, error)

	// IsOpen returns true when the conversation's last record was
	// written within the heartbeat threshold (default 60s).
	IsOpen(c Conversation) bool

	// ToGraphQL maps an in-memory Conversation onto the wire type.
	// recap is always nil in v1.
	ToGraphQL(c Conversation) *gql.Conversation

	// PathForSessionUUID returns the on-disk JSONL path for the given
	// sessionUUID. Returns ("", false) when not in cache.
	PathForSessionUUID(ctx context.Context, uuid string) (string, bool)

	// GetBySessionUUID looks up a cached Conversation by sessionUUID.
	// Returns (zero, false) on miss.
	GetBySessionUUID(ctx context.Context, uuid string) (Conversation, bool)

	// Subscribe returns a channel that emits invalidation events as the
	// underlying JSONL files change. Channel closes when ctx is done.
	Subscribe(ctx context.Context) <-chan InvalidationEvent

	// Refresh forces a full re-walk of the projects root. Used by tests
	// and the daemon-meta reload endpoint; production callers should let
	// the watcher do this work.
	Refresh(ctx context.Context) error
}

// InvalidationEvent is the signal emitted on a Subscribe channel when
// a Conversation's value may have changed.
type InvalidationEvent struct {
	Key    ConversationID
	Reason string
	At     time.Time
}

// ClaudeInstanceReader is the narrow interface this domain uses to
// resolve Conversation.liveInstances. Defined here per RULES.md R4
// (consumer defines the interface); implemented by daemon/claude-instance.
type ClaudeInstanceReader interface {
	LiveInstancesByConversationUUID(ctx context.Context, sessionUUID string) ([]*gql.ClaudeInstance, error)
}
