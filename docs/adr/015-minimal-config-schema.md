# ADR-015: Minimal Config Schema

**Status:** Accepted (2026-05-10)

**Context:** ADR-014 moved the config to `~/.orchard/config.json` (dotdir).
This ADR follows up by collapsing the file's *contents* to the minimum viable
shape: three top-level keys, no duplication, no orphan features.

## The problem this ADR fixes

Two separate schemas accreted in `~/.orchard/config.json` over time:

1. **`projects[]`** (Go side): `{ id, directory, name }`. Written by
   `orchard config add-repo`. Surfaced as the GraphQL `Project` type. Used
   by the daemon and the GUI.
2. **`repos[]`** (Rust side): `{ slug, path, remotes }`. Written by the
   Rust orchard-tui binary. Used by the TUI dashboard, federation, and
   per-repo remote SSH worktrees.

Same concept, two shapes, two writers, two sets of consumers, two parallel
type hierarchies in two languages. The GUI couldn't see `repos[]` and the
TUI couldn't see `projects[]`. Editing one didn't affect the other.

In addition, the file had accumulated dead fields with no live readers in
the current product:

- `chat_target` — the chat daemon owns its own files; this never re-pointed
  anything.
- `ci_gate_patterns` — moved into per-PR resolver logic; the global field
  is no longer consulted.
- `terminal_app` — the notification path changed; the field is read but the
  result is never load-bearing.
- `tmux_sessions` — the standalone-session feature was retired; the array
  is empty in every observed config.
- `watch.{local_poll_secs, full_poll_secs, threshold_cooldown_secs,
  notifications, keep_diagnostic_caches}` — the watch daemon's polling
  cadence is hardcoded; user-tunable polling was removed when the daemon
  switched to event-driven refresh.

Test isolation was also broken: `orchard config add-repo` integration tests
ran against the real `~/.orchard/config.json` because the test harness
didn't override `HOME`. The user's actual config picked up six fixture
entries (`TestAddRepo_*`, `alpha`, `beta`, `first`, `second`, `002`,
`repo-name`) before this ADR.

## The decision

**`~/.orchard/config.json` is the only config file. It has exactly three
top-level keys: `version`, `repos`, `peers`.**

```json
{
  "version": 1,
  "repos": [
    {
      "slug": "drewdrewthis/git-orchard-rs",
      "path": "/Users/USER/workspace/git-orchard-rs",
      "remotes": []
    }
  ],
  "peers": [
    { "name": "orchard", "address": "graphql.orchard.boxd.sh", "tls": true }
  ]
}
```

### `repos[]`

Each entry: `{ slug, path, remotes }`.

- `slug` is `owner/repo`. **Identity** of the repo.
- `path` is the absolute local path to the working tree. **Display name**
  is `path.basename` (or `slug.split('/').last` if path is unusable).
- `remotes` is the existing per-repo remote-host list (remmy / SSH
  worktrees). Unchanged from the old shape.

`projects[]` is gone. So is per-repo `id` and `name` — they were redundant
with `slug` and `path`.

### `peers[]`

Unchanged from the old shape. `{ name, address, tls }`. Used by the
peer-proxy provider to federate other orchard instances.

### Everything else dies

| Old field | Replaced by | What dies with it |
|---|---|---|
| `chat_target` | (nothing — chat daemon owns chat) | the unused field; chat already worked without it |
| `ci_gate_patterns` | per-resolver gate detection | the global override; the gate logic itself stays |
| `terminal_app` | OS default terminal | the click-handler-redirect; macOS will fall back to default |
| `tmux_sessions` | (nothing — standalone session feature retired) | the empty array; no readers were exercising it |
| `watch.*` | hardcoded daemon defaults | user-tunable polling; daemon is event-driven anyway |
| `keep_diagnostic_caches` | `--keep-diagnostic-caches` CLI flag only | the persistent setting; the flag remains for one-off debug runs |

For each: greps confirmed no production read path depends on the field.
Tests referencing the field move with the field (or die outright if the
test was only checking that the field round-tripped).

### Per-repo `id` and `name` go away

- `id` is replaced by `slug`. The `Project.id` GraphQL field becomes
  `Repo.id` (still a `Node` ID derived from `slug`).
- `name` is computed from `path.basename` at render time. The GUI computes
  it; the daemon does not store it.

### `~/.config/orchard/` (legacy XDG path) is dead

Per ADR-014, `~/.orchard/` has been canonical for some time. The legacy
XDG path was kept as a read-only migration hint emitter. This ADR removes
even that — the hint emission code is deleted; the test for it is deleted.

The `~/.config/orchard/config.json` file is **not** auto-migrated. Users
on the legacy path see no config and need to either (a) re-run `orchard
config init` and `orchard config add-repo` for each project, or (b)
manually `mv ~/.config/orchard/config.json ~/.orchard/config.json`.

This is acceptable because:

- Both paths have coexisted since the dotdir migration. Anyone still
  exclusively on the legacy path has been ignoring multi-month-old hints.
- The new shape is a strict subset of the old shape — `mv` works. There's
  no schema translation needed.
- A migration command (`orchard config migrate`) can be added later if the
  pain proves real. Skipping it now keeps the change minimal.

## Consequences

### Positive

- **One source of truth.** No more "did I edit projects or repos."
- **One reader, one writer per concept.** The Go and Rust sides operate on
  the same `repos[]` array.
- **Smaller blast radius for clobbers.** PR #479's "preserve unknown keys"
  fix becomes mostly moot: with three top-level keys, there's nothing
  unknown to clobber.
- **Test isolation is enforceable.** The `repos[]` shape only has three
  fields; a guard test that fails on `/var/folders/` substrings or
  `TestAddRepo_` prefixes can run in CI cheaply.
- **GUI and TUI converge on the same data.** The sidebar's "where can I
  open a worktree?" dialog and the TUI's repo list both read the same
  array.

### Negative

- **Breaking change for any external consumer of the GraphQL `Project`
  type.** The daemon currently emits `Project { id directory name }`;
  after this ADR it emits `Repo { id slug path remotes }`. The GUI is
  the only known consumer and migrates in the same PR.
- **`projects[]` is removed without auto-translation.** Users with a
  `projects[]`-only config see an empty repo list until they re-run
  `orchard config add-repo` for each. (See "Migration story" below.)
- **`watch.*` config tunables go away.** Users who customised polling
  intervals revert to defaults. None of the observed configs in the wild
  had non-default values, so this is theoretical.
- **`terminal_app` redirection is gone.** Notification clicks fall back
  to the OS-default terminal. Re-add as a CLI flag or per-app preference
  if user demand surfaces.

### Migration story

For each repo in the old `projects[]`, regenerate as a `repos[]` entry:

```bash
# Old shape:
#   "projects": [{ "id": "git-orchard-rs", "directory": "/Users/USER/workspace/git-orchard-rs", "name": "git-orchard-rs" }]
# New shape:
#   "repos": [{ "slug": "drewdrewthis/git-orchard-rs", "path": "/Users/USER/workspace/git-orchard-rs", "remotes": [] }]
```

The slug requires `gh repo view` (or manual entry); the path is the same;
`remotes[]` is empty unless the user had remmy/SSH remotes (those were
already under `repos[]`, not `projects[]`, so they survive untouched).

The PR landing this ADR includes a one-shot `orchard config migrate`
command that does this translation in-place. After one release window the
migrate command is removed.

### Test isolation guard

`internal/cli/config/` tests must override `HOME` to a temp dir before
calling `orchard config init` / `add-repo`. A CI check fails if any path
under `/var/folders/` or any name matching `TestAddRepo_` appears in the
real `~/.orchard/config.json` after a test run.

## References

- [#540](https://github.com/drewdrewthis/git-orchard-rs/issues/540) — the
  cleanup issue that prompted this ADR.
- [ADR-014](014-config-dotdir-location.md) — `~/.orchard/` becomes the
  canonical config directory.
- [#19](https://github.com/drewdrewthis/git-orchard-rs/issues/19) — the
  original `ci_gate_patterns` feature, whose config field is removed here.
- [#52](https://github.com/drewdrewthis/git-orchard-rs/issues/52) — the
  `terminal_app` feature, whose config field is removed here.
- [#413](https://github.com/drewdrewthis/git-orchard-rs/issues/413) —
  `peer_secret` removal; verified clean of zombies.
- [#479](https://github.com/drewdrewthis/git-orchard-rs/pull/479) — the
  "preserve unknown keys" clobber fix, mostly subsumed by reducing the
  schema to three keys.
