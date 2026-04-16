# Orchard Monitor Scripts

Background helpers used by the **orchardist** tmux session to monitor the orchard
and drive long-running work to green.

| Script | Purpose |
|--------|---------|
| `orchard-monitor.sh` | Polls `orchard --json`, detects state changes, signals the orchardist pane via `tmux send-keys`. Also throttles a watchlist check. |
| `orchard-decide.sh`  | State-diff decision tree: classifies per-PR transitions (auto-merge, nudge, alert) and signals the orchardist. |

## Running

```sh
nohup scripts/orchard-monitor.sh > /tmp/orchard-monitor.log 2>&1 &
echo $! > /tmp/orchard-monitor.pid
```

`orchard-decide.sh` is resolved relative to `orchard-monitor.sh`, so both files
must live in the same directory.

## Requirements

- `bash`, `python3` on PATH
- `orchard` CLI on PATH (produces `--json` output)
- a tmux session named `orchardist` with at least one pane

## Environment

| Var | Default | Purpose |
|-----|---------|---------|
| `ORCHARDIST_PANE` | `orchardist:0.0` | tmux target for signals. Use a session-name target — pane IDs (`%0`) are not stable across tmux restarts. |
| `POLL_INTERVAL` | `60` | Seconds between orchard polls. |
| `WATCH_INTERVAL` | `300` | Seconds between watchlist alert checks. |
| `REPORT_INTERVAL` | `300` | Seconds between `ORCHARD_REPORT_CMD` refreshes. |
| `ORCHARD_REPORT_CMD` | _(unset)_ | Optional path to an executable that publishes a dashboard (e.g. to Telegram, Slack). Invoked on every state change and every `REPORT_INTERVAL` seconds. If unset, no dashboard is published. |

## tmux send-keys

Every signal send is bookended with a leading `Enter`:

```sh
tmux send-keys -t "$ORCHARDIST_PANE" Enter       # drain any stuck prior signal
tmux send-keys -t "$ORCHARDIST_PANE" -l "$MSG"   # deliver text literally
tmux send-keys -t "$ORCHARDIST_PANE" Enter       # submit this signal
```

**Why the leading Enter:** Claude Code's input prompt buffers text + newline if a
turn is in progress when the signal arrives. The `\n` is not converted to a
submit when the turn finishes — the text sits in the prompt until a later Enter
drains it. The leading Enter flushes whatever was buffered before; `Enter` on an
empty buffer is a no-op in Claude Code, so this is always safe.

**The old fix wasn't enough.** A previous attempt replaced single-call
`send-keys "$MSG" Enter` with two-call `send-keys -l "$MSG"` + `send-keys Enter`,
reasoning that the single-call form sometimes failed to submit. A wire-level
PTY trace proved both forms produce byte-identical output; the buffering bug is
in Claude Code's input handling, not in tmux. The bookend is the actual fix.

## Runtime state

Both scripts persist state under `${HOME}/.claude/state/`. This default is a
historical artifact — see the follow-up issue tracking the canonical source
of truth for these scripts. Override via env vars documented at the top of
each script if you need a different location.
