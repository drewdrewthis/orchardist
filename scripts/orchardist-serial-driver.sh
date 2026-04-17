#!/usr/bin/env bash
# orchardist-serial-driver.sh — Polls a PR and exits when the target tmux session dies.
#
# Usage: REPO=owner/repo [POLL_INTERVAL=120] [MAX_ERR=5] [ORCHARDIST_PANE=target]
#        ./orchardist-serial-driver.sh <PR_NUMBER> <SESSION_NAME>

set -euo pipefail

# ---------------------------------------------------------------------------
# Args & config
# ---------------------------------------------------------------------------
if [[ $# -lt 2 ]]; then
  echo "usage: $0 <PR_NUMBER> <SESSION_NAME>" >&2
  exit 2
fi

FOCUS_PR="$1"
FOCUS_SESSION="$2"

REPO="${REPO:-}"
if [[ -z "${REPO}" ]]; then
  echo "error: REPO env var is required (e.g. REPO=owner/repo)" >&2
  exit 2
fi

POLL_INTERVAL="${POLL_INTERVAL:-120}"
MAX_ERR="${MAX_ERR:-5}"
ORCHARDIST_PANE="${ORCHARDIST_PANE:-}"
STATE_FILE="${TMPDIR:-/tmp}/orchardist-driver-state-${FOCUS_PR}.txt"

# ---------------------------------------------------------------------------
# Cleanup — remove state file on any exit (#243: no leftover files from zombies)
# ---------------------------------------------------------------------------
cleanup() {
  rm -f "${STATE_FILE}"
}
trap cleanup EXIT INT TERM

# ---------------------------------------------------------------------------
# Notify helper — send to orchardist pane if configured; never fatal if pane gone
# ---------------------------------------------------------------------------
notify() {
  local msg="$1"
  echo "[driver] ${msg}"
  if [[ -n "${ORCHARDIST_PANE}" ]]; then
    tmux send-keys -t "${ORCHARDIST_PANE}" Enter 2>/dev/null || true
    tmux send-keys -t "${ORCHARDIST_PANE}" -l "[driver] ${msg}" 2>/dev/null || true
    tmux send-keys -t "${ORCHARDIST_PANE}" Enter 2>/dev/null || true
  fi
}

# ---------------------------------------------------------------------------
# Fail fast if the target session is already gone before we advertise startup.
# ---------------------------------------------------------------------------
if ! tmux has-session -t "${FOCUS_SESSION}" 2>/dev/null; then
  echo "[driver] session ${FOCUS_SESSION} does not exist — nothing to drive" >&2
  exit 0
fi

: > "${STATE_FILE}"
notify "started — watching PR #${FOCUS_PR} in session ${FOCUS_SESSION}"

# ---------------------------------------------------------------------------
# Poll loop
# ---------------------------------------------------------------------------
err_count=0

while true; do
  # Liveness check first — prevents zombie drivers if the session died mid-poll (#243)
  if ! tmux has-session -t "${FOCUS_SESSION}" 2>/dev/null; then
    notify "session ${FOCUS_SESSION} gone — exiting"
    exit 0
  fi

  # Poll GitHub for PR state and CI rollup
  raw=$(gh pr view "${FOCUS_PR}" -R "${REPO}" \
        --json statusCheckRollup,state 2>/dev/null) || raw=""

  # Pass JSON via env var (not stdin) so the heredoc below is the only stdin.
  # python3 always prints + exits 0, so $() always succeeds with a non-empty result;
  # checks with neither conclusion nor status fall into PENDING.
  result=$(DRIVER_RAW="${raw}" python3 - <<'PYEOF'
import json, os, sys

raw = os.environ.get("DRIVER_RAW", "")
try:
    data = json.loads(raw)
except Exception:
    print("ERR")
    sys.exit(0)

state = data.get("state", "")
if state == "MERGED":
    print("MERGED")
    sys.exit(0)
if state == "CLOSED":
    print("CLOSED")
    sys.exit(0)

checks = data.get("statusCheckRollup") or []
if not checks:
    print("PENDING")
    sys.exit(0)

statuses = [c.get("conclusion") or c.get("status", "") for c in checks]
if any(s in ("FAILURE", "ERROR", "TIMED_OUT", "CANCELLED") for s in statuses):
    print("RED")
elif all(s in ("SUCCESS", "SKIPPED", "NEUTRAL") for s in statuses):
    print("GREEN")
else:
    print("PENDING")
PYEOF
)

  # Track consecutive ERR — network-blip zombie prevention (#243)
  if [[ "${result}" == "ERR" ]]; then
    err_count=$(( err_count + 1 ))
    if [[ "${err_count}" -ge "${MAX_ERR}" ]]; then
      notify "PR #${FOCUS_PR}: ${MAX_ERR} consecutive errors — giving up"
      exit 1
    fi
  else
    err_count=0
  fi

  # Notify on state change
  prev=$(cat "${STATE_FILE}" 2>/dev/null || echo "")
  if [[ "${result}" != "${prev}" ]]; then
    echo "${result}" > "${STATE_FILE}"
    notify "PR #${FOCUS_PR}: ${result}"
  fi

  # Exit on terminal states
  case "${result}" in
    GREEN|MERGED|CLOSED)
      notify "PR #${FOCUS_PR}: ${result} — done"
      exit 0
      ;;
  esac

  sleep "${POLL_INTERVAL}"
done
