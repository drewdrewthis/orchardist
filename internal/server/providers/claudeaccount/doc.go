// Package claudeaccount implements the ClaudeAccount provider — orchard's
// reflection of the local `claude` CLI's auth subject and the quota
// numbers `ccusage` reports for that subject.
//
// Per ADR-011 §5.1, ClaudeAccount carries:
//
//   - Identity: (host_id, email). The email is what `claude auth status`
//     reports for the active session.
//   - Quota: used / cap / resets-at. Read from `ccusage blocks --json`,
//     so `quotaEstimated` is always true in v1 (we do not call any
//     first-party Anthropic billing API).
//   - Edges: host (back-edge), instances (resolved by the
//     B-claudeinstance workstream; v1 returns []).
//
// The provider holds:
//
//   - one ShellAdapter (raw shellouts to `claude` and `ccusage`).
//   - an in-memory cache of (AccountID -> Account) with a 60s TTL.
//   - a poll-based watcher (60s); no fsnotify because there is no
//     observable file under the user's control — the source of truth
//     is the CLI exit code + stdout.
//   - a fan-out of invalidation events for subscribers.
//
// Per ADR-011 §6 ("per-field errors"), if either CLI is missing the
// adapter returns a typed `ErrToolNotInstalled`. The provider
// propagates the error verbatim; the resolver maps it to a per-field
// GraphQL error so the daemon does not collapse just because one field
// could not be resolved.
//
// PII: the adapter MUST NOT log raw stdout, because `claude auth
// status` includes the user's email. The provider's logger only sees
// structured fields the implementation has explicitly redacted.
package claudeaccount
