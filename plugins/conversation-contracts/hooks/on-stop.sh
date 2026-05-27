#!/usr/bin/env bash
# on-stop.sh — Stop hook for the conversation-contracts plugin.
#
# Folds the `orchard_contract` open/close sentinels out of the current session's
# jsonl (via the shared scripts/fold-contracts.sh) and HARD-BLOCKS Stop while any
# contract is open. The block `reason` is self-documenting: it names the verbs
# the agent needs (/my-contracts to list, /close-contract to deliver/abandon) so
# the block itself is the discovery surface for the contracts mechanism.
#
# Stateless: the jsonl is the only store. No daemon query, no sidecar, no
# resident process. The shared fold script gates its work behind a fixed-string
# grep, so the common "no open contracts" path is cheap.
#
# Block convention (Claude Code): emit {"decision":"block","reason":"..."} on
# stdout and exit 0. Emitting nothing (exit 0) allows Stop.

set -uo pipefail

# ---- read hook payload --------------------------------------------------------

input=$(cat)
hook_event=$(printf '%s' "$input" | jq -r '.hook_event_name // empty' 2>/dev/null)
[ "$hook_event" = "Stop" ] || exit 0

session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null)
[ -z "$session_id" ] && exit 0

# ---- resolve session jsonl path -----------------------------------------------
# Pattern: ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
# Encoding: '/' → '-', '.' → '-'. Honor CLAUDE_PROJECTS_DIR override.

_encode_cwd() {
  printf '%s' "$1" | tr '/' '-' | tr '.' '-'
}

cwd="${PWD:-}"
home="${HOME:-}"
projects_root="${CLAUDE_PROJECTS_DIR:-}"
if [ -z "$projects_root" ] && [ -n "$home" ]; then
  projects_root="$home/.claude/projects"
fi
if [ -z "$cwd" ] || [ -z "$projects_root" ]; then
  # Cannot resolve the jsonl; nothing to fold, allow Stop.
  exit 0
fi
encoded=$(_encode_cwd "$cwd")
session_jsonl="$projects_root/$encoded/$session_id.jsonl"

# ---- fold open contracts via the shared script --------------------------------

fold_script="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/scripts/fold-contracts.sh"

# Optional timing probe: set CONTRACTS_FOLD_TIMING=1 to emit fold latency on
# stderr. Premature to checkpoint; this gives a real number to decide later.
if [ "${CONTRACTS_FOLD_TIMING:-}" = "1" ]; then
  _t0=$(date +%s%N 2>/dev/null || echo 0)
fi

open_contracts=$(bash "$fold_script" "$session_jsonl" 2>/dev/null || true)

if [ "${CONTRACTS_FOLD_TIMING:-}" = "1" ]; then
  _t1=$(date +%s%N 2>/dev/null || echo 0)
  if [ "$_t0" != "0" ] && [ "$_t1" != "0" ]; then
    _lines=$(grep -c . "$session_jsonl" 2>/dev/null || echo 0)
    echo "conversation-contracts: folded ${_lines} jsonl lines in $(( (_t1 - _t0) / 1000000 ))ms" >&2
  fi
fi

# ---- emit hook response -------------------------------------------------------

if [ -z "$open_contracts" ]; then
  # No open contracts — allow Stop.
  exit 0
fi

count=$(printf '%s\n' "$open_contracts" | grep -c .)
reason="You own ${count} open contract(s). Close each before stopping: deliver with /close-contract <id> (cite evidence in the reason) or abandon with /close-contract <id> (reason 'abandoned: ...'). List them anytime with /my-contracts.

Open contracts:
${open_contracts}"

jq -n --arg reason "$reason" '{decision:"block", reason:$reason}'
exit 0
