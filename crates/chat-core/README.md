# chat-core

Cross-machine chat substrate for orchard. Append-only JSONL rooms with
tmux send-keys fanout and Level 2 receipts.

This crate is the source-of-truth for the **chat JSONL format**. The Go
daemon will read this format in a future issue; any reader (TUI pane,
GUI view, daemon watcher, third-party tail) MUST treat the JSONL schema
documented below as the cross-language contract.

## Storage layout

```
$ORCHARD_CHAT_DIR/<room>.jsonl   (default: ~/.orchard/chat/<room>.jsonl)
```

- **One file per room.** No metadata file, no subdirectories.
- **Direct sends** (`@handle` targets) are persisted under `@<handle>.jsonl`
  so their history is queryable. The leading `@` is part of the filename.
- **Rooms exist iff their JSONL exists.** Creating a room is just appending
  to a fresh file; deleting a room is removing the file.
- The directory is created lazily on the first append.
- Tests can root the chat dir in a tempdir via `ORCHARD_CHAT_DIR`.

## JSONL row schema

Each line is a single JSON object. The top-level `type` field discriminates
the row kind. Three types are defined; readers MUST silently skip rows with
an unknown `type` (forwards-compat).

### `type: "message"`

A user-visible chat message.

```json
{
  "type": "message",
  "id": "01J9X3FZJW6Y8K1QV2A0Q5PF7H",
  "ts": "2026-05-09T17:12:33.812Z",
  "sender": "@alice",
  "sender_machine": "drew-mac",
  "text": "PR went green",
  "source": "internal"
}
```

| Field | Type | Notes |
|---|---|---|
| `id` | string (ULID) | 26-char Crockford base32. Monotonic per-process. |
| `ts` | string (RFC3339) | UTC, milliseconds, `Z` suffix. |
| `sender` | string | Handle, including the leading `@`. |
| `sender_machine` | string | Hostname without `.local` suffix, lowercased. |
| `text` | string | Message body. No length cap; ≤4KB recommended for atomicity. |
| `source` | string | `"internal"` for chat sends; `"external"` reserved for future ingest paths. Defaults to `"internal"` when absent. |

### `type: "member.joined"`

A handle joined the room.

```json
{
  "type": "member.joined",
  "ts": "2026-05-09T17:11:00.000Z",
  "handle": "@alice",
  "machine": "drew-mac",
  "tmux_session": "card-alice"
}
```

| Field | Type | Notes |
|---|---|---|
| `ts` | string (RFC3339) | UTC, milliseconds. |
| `handle` | string | The joining handle (with leading `@`). |
| `machine` | string | Hostname of the joining machine. |
| `tmux_session` | string | tmux session name on `machine` (no leading `@`). |

### `type: "member.left"`

A handle left the room.

```json
{"type":"member.left","ts":"2026-05-09T18:00:00.000Z","handle":"@alice"}
```

## Membership derivation

Membership at time T = scan the file, fold `member.joined` / `member.left`
events chronologically, last-event-wins per handle. A handle is a current
member if its most recent event is `member.joined`. Re-joining after a
leave puts the handle back with the new event's metadata.

This is O(file size). Acceptable at v1 scale (rooms with hundreds to low
thousands of lines). If a file ever grows large enough to matter, a snapshot
event type can be appended without breaking forward-compat readers.

## Concurrency

- Writes use POSIX `O_APPEND`. POSIX guarantees that `write(2)` of ≤
  `PIPE_BUF` (4096 bytes on Linux, 512 on macOS) is atomic — interleaved
  writers will not tear each other's lines.
- **No flock anywhere.** Cross-machine concurrency is not in scope for v1;
  same-machine concurrency relies on `O_APPEND`.
- Readers tolerate partial last-line writes (a tail without a newline) by
  treating the unterminated suffix as not-yet-readable.

## Receipts

`tmux_fanout` returns one of four `FanoutOutcome` variants per recipient:

| Variant | Meaning |
|---|---|
| `delivered` | `send-keys` succeeded AND a verifier confirmed the prefixed message landed at `verified_at`. `verified_via` records which verifier won — `transcript` (strongest: recipient's Claude transcript JSONL ingested it) or `scrollback` (`tmux capture-pane -p` saw it). |
| `byte_only` | `send-keys` succeeded but every verifier timed out. Bytes are in the input buffer; we don't know if the recipient processed them. Common when the recipient is a non-Claude pane that's actively redrawing. |
| `failed` | `send-keys` itself errored — for example, no such tmux session. |
| `skipped` | Sender's own pane, empty handle, or other pre-flight skip. |

### Verify ladder

Within `delivered`, the substrate tries the strongest receipt first:

1. **Transcript verify (Level 3).** Resolve the recipient's working directory via `tmux display-message -p '#{pane_current_path}'`, slugify it (replace `/` and `.` with `-`), open the freshest `.jsonl` in `~/.claude/projects/<slug>/`, and look for a `type: "user"` row whose `message.content` contains our prefix. If found within ~2s, the recipient REPL ingested the message — strongest possible same-machine proof.
2. **Scrollback verify (Level 2).** If no Claude transcript is locatable, or transcript-verify times out, fall back to `tmux capture-pane -p` looking for the prefix in the recipient's pane scrollback (~500ms budget).
3. **ByteOnly (Level 1).** Both above failed; `send-keys` returned 0 but we have no proof the recipient processed it.

The transcript path solves a known failure mode against active Claude REPLs:
the pane is constantly redrawing, so `capture-pane` can miss the prefix
even though the bytes landed. The transcript file is append-only — once
the entry's there, the REPL has it.

### Determinism

The JSONL line is appended **before** fanout starts. A fanout failure does
not roll back the append — the message is in history. Callers MUST NOT
retry on partial failure; the message id is the same and recipients who
already received would double up.

## CLI

The `orchard-chat` binary is the user-facing surface. Routed by the
`orchard` dispatcher:

- `orchard send <target> <text>` — bare-verb shortcut for `orchard chat send`.
- `orchard chat send|join|leave|members|list|history|tail …` — full surface.

All subcommands accept `-j`/`--json` for machine-readable output. Sender
identity comes from `$TMUX` / current tmux session name, with `--as <handle>`
overriding. Outside tmux without `--as`, the CLI exits 3 with a clear message.

See the issue body of [#495](https://github.com/drewdrewthis/orchardist/issues/495)
for the full design rationale and AC list.

## Tests

```bash
cargo test -p chat-core            # unit tests (no tmux needed)
cargo test -p orchard-chat         # CLI tests, including two-session tmux end-to-end
```

The integration tests skip themselves with a stderr message if `tmux` is
not installed.
