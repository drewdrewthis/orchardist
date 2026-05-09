package chat

import (
	"time"
)

// RoomID is the cache key for the chat provider — the room's basename
// without the `.jsonl` suffix. Keep `@`-prefixed handles intact so
// direct rooms (e.g. `@bob.jsonl` → RoomID "@bob") round-trip cleanly.
type RoomID string

// Event is one JSONL line. Discriminated by Type. The shape mirrors
// the chat-core Rust enum exactly — adding fields here REQUIRES adding
// fields to chat-core/src/types.rs and vice-versa, since the on-disk
// JSONL is the cross-language contract.
//
// Field order mirrors the JSONL so go-toolchain reflection and diffs
// read like the file does.
type Event struct {
	// Type discriminates the event. Allowed values:
	//   "message" | "member.joined" | "member.left"
	// Unknown values are skipped by Fold so the daemon stays
	// forward-compatible with chat-core extensions.
	Type string `json:"type"`

	// Common: timestamp on every event. RFC 3339.
	Timestamp time.Time `json:"ts"`

	// Message fields.
	ID            string `json:"id,omitempty"`
	Sender        string `json:"sender,omitempty"`
	SenderMachine string `json:"sender_machine,omitempty"`
	Text          string `json:"text,omitempty"`
	Source        string `json:"source,omitempty"` // "internal" | "external" (per chat-core)

	// Member-event fields.
	Handle      string `json:"handle,omitempty"`
	Machine     string `json:"machine,omitempty"`
	TmuxSession string `json:"tmux_session,omitempty"`
}

// Message is one folded message — what the resolver maps onto
// graphql.ChatMessage.
type Message struct {
	ID            string
	Room          RoomID
	Timestamp     time.Time
	Sender        string
	SenderMachine string
	Text          string
	Source        string
}

// Member is a current room member, derived from joined/left events.
// last-event-wins per handle: a handle present here had `member.joined`
// as its most recent membership event.
type Member struct {
	Handle      string
	Machine     string
	TmuxSession string
	JoinedAt    time.Time
}

// Room is the folded state of one chat room.
type Room struct {
	ID          RoomID
	Messages    []Message // chronological, oldest first
	Members     []Member
	LastEventAt time.Time
}
