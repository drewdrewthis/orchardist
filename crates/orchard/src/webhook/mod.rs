//! Webhook server for receiving GitHub events.
//!
//! Receives signed GitHub webhook payloads, verifies HMAC-SHA256 signatures,
//! normalizes events into a common schema, and appends them to events.jsonl.
//!
//! See `specs/features/webhook-event-stream.feature` for the full contract.

pub mod handler;
pub mod normalize;
pub mod port;
pub mod server;
pub mod signature;
pub mod tailer;
