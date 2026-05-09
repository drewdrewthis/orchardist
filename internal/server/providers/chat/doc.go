// Package chat exposes Chat rooms + ChatMessage events to the GraphQL
// resolver layer.
//
// Owns:
//
//   - one [Adapter] (JSONL read).
//   - one [Watcher] (fsnotify on the chat directory).
//   - the in-memory fold result, keyed by RoomID.
//   - the per-room Subscribe fan-out for invalidation events.
//
// Per ADR-011 §2 the surface is read-only. Writes happen out-of-band:
// the `chat-core` Rust library + `orchard chat send` CLI append to
// `<dir>/<room>.jsonl`. The watcher turns those writes into
// invalidation events, and the next read picks up the fresh fold.
//
// The on-disk JSONL schema is the **cross-language contract** between
// the Rust writer side (chat-core) and this Go reader side. See
// research/038 for the rationale and chat-core/README.md for the
// authoritative event grammar.
//
// Three event kinds, discriminated by `type`:
//
//   - "message"          — a user-visible message line (id, ts, sender,
//     sender_machine, text, source).
//   - "member.joined"    — handle joined the room (ts, handle, machine,
//     tmux_session).
//   - "member.left"      — handle left the room (ts, handle).
//
// Membership at time T is the fold of joined/left events: last-event-
// wins per handle.
package chat
