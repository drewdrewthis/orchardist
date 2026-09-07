#!/usr/bin/env bash
# pane-labels.sh — derive a rich label per tmux PANE from orchard daemon
# state plus Claude hook state, and write it to @orchard_pane_label so
# choose-tree (expanded) renders it.
#
# Not invoked directly by users: `orchard.tmux` (the tmux-plugin entry
# script in this directory) binds it to `prefix + s` and passes the
# resolved daemon URL. See scripts/tmux/README.md.
#
# Usage:
#   pane-labels.sh [--daemon-url URL] [--heartbeat-dir DIR]
#                  [--panes-file FILE] [--print]
#
#   --daemon-url    The daemon's GraphQL endpoint. Either shape works — a
#                   base URL ("http://host:7777") has /graphql appended, a
#                   full endpoint is used as given. Defaults to
#                   $ORCHARD_DAEMON_URL, which accepts the same two shapes,
#                   then http://127.0.0.1:7777/graphql.
#   --heartbeat-dir Directory holding orchard-claude-<session>.json hook
#                   state files. Defaults to $ORCHARD_HEARTBEAT_DIR, then
#                   $TMPDIR, then /tmp — mirroring claudeinstance.ResolveDir()
#                   in internal/server/providers/claudeinstance/types.go.
#   --panes-file    Read the pane table from FILE instead of `tmux list-panes`.
#   --print         Emit "<pane_id>\t<label>" on stdout instead of setting
#                   the tmux option. Implies no tmux writes.
#
# Daemon contract: queries `repos { slug worktrees { ... } }` per the v0.8
# schema (ADR-015 rename project→repo). Falls back to empty results when
# the daemon is unreachable so the picker still works.
#
# Claude enrichment: per ADR-007, Claude state is read from hook state
# files, not from tmux. The hook (`orchard-state.sh`) writes exactly
# {state, session_id, tmux_session, cwd, event, timestamp}. Only fields
# actually present in a file are rendered — nothing is placeholdered.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DAEMON="${ORCHARD_DAEMON_URL:-http://127.0.0.1:7777}"
DAEMON_URL=""
HEARTBEAT_DIR=""
PANES_FILE=""
PRINT_MODE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --daemon-url)    DAEMON_URL="$2"; shift 2 ;;
    --heartbeat-dir) HEARTBEAT_DIR="$2"; shift 2 ;;
    --panes-file)    PANES_FILE="$2"; shift 2 ;;
    --print)         PRINT_MODE=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

# One concept, one meaning. `--daemon-url` and `@orchard_daemon_url` are
# documented as FULL endpoints while `$ORCHARD_DAEMON_URL` was historically a
# BASE url with /graphql appended, so mirroring the documented option value
# into the env var produced `.../graphql/graphql` and silently empty results.
# Both spellings now accept either shape.
normalize_daemon_url() {
  local url="$1"
  url="${url%/}"
  case "$url" in
    */graphql) printf '%s' "$url" ;;
    *)         printf '%s/graphql' "$url" ;;
  esac
}

[[ -z "$DAEMON_URL" ]] && DAEMON_URL="$DAEMON"
DAEMON_URL="$(normalize_daemon_url "$DAEMON_URL")"

run_once() {
  local qfile panes
  qfile=$(mktemp)
  panes=$(mktemp)
  # Expansion at trap-definition time is deliberate: the paths are fixed for
  # this call and must survive any later reassignment of the variables.
  # shellcheck disable=SC2064
  trap "rm -f '$qfile' '$panes'" RETURN

  # `claudeInstances` and `tmuxSessions` were selected here and never read.
  # Claude state deliberately comes from the hook sidecars instead (ADR-007):
  # they are local files, so state still renders when the daemon is down —
  # the degradation the README promises and the bats suite pins. Selecting a
  # second copy of the same data and dropping it is the "same data in two
  # shapes" smell of ADR-022, so the selection goes rather than the sidecars.
  #
  # The query is intentionally lean (path+branch only) so the picker boot
  # stays under ~1s even with 30+ worktrees across many repos. Enriching
  # each worktree with PR/issue/labels hits the gh provider per-worktree
  # and can take 30s+ under cold cache. Set ORCHARD_LABEL_ENRICH=1 to
  # opt-in to the heavy query (useful when running outside the prefix-s
  # hot path).
  local query
  if [ "${ORCHARD_LABEL_ENRICH:-0}" = "1" ]; then
    query='{"query":"{ repos { slug worktrees { branch path host pr { number draft mergeStateStatus statusCheckRollup labels { name } reviewDecision } issue { number title } } } }"}'
  else
    query='{"query":"{ repos { slug worktrees { branch path host } } }"}'
  fi
  if ! curl -sf --max-time 15 -X POST "${DAEMON_URL}" \
      -H 'Content-Type: application/json' \
      -d "$query" \
      > "$qfile" 2>/dev/null; then
    printf '{"data":{"repos":[]}}' > "$qfile"
  fi

  local TAB
  TAB=$(printf '\t')
  # Per-pane: target id, session_name, window_index, pane_index, current_path, current_command.
  if [[ -n "$PANES_FILE" ]]; then
    cat "$PANES_FILE" > "$panes"
  else
    tmux list-panes -aF "#{pane_id}${TAB}#{session_name}${TAB}#{window_index}${TAB}#{pane_index}${TAB}#{pane_current_path}${TAB}#{pane_current_command}" > "$panes" 2>/dev/null || return 1
  fi

  # -B: never write __pycache__ into the checkout; the labeler runs from the repo tree.
  ORCHARD_PRINT_MODE=$PRINT_MODE \
  ORCHARD_HEARTBEAT_DIR_ARG="$HEARTBEAT_DIR" \
  python3 -B "$SCRIPT_DIR/pane_labels.py" "$qfile" "$panes"
}

run_once
