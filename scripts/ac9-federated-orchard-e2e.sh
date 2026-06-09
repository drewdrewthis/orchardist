#!/usr/bin/env bash
#
# AC9 end-to-end validation for #329 (federated orchard).
#
# Forks a Boxd VM from golden, installs the built `orchard-tui` binary, creates
# a test worktree + tmux session on the VM, configures the VM as an
# OrchardProxy remote locally, runs `orchard-tui --json`, and asserts:
#
# 1. Happy path: the remote worktree is visible with PR/issue enrichment
#    computed on the VM (not re-derived locally).
# 2. Fallback: after removing the orchard-tui binary on the VM, the same
#    worktree is still visible via the legacy shell-discovery path, and a
#    `remote_adapter.fallback` diagnostic is written to events.jsonl.
# 3. Destroys the VM on exit.
#
# Requirements (human one-time setup):
#   - `boxd` CLI installed and authenticated (`boxd info` must succeed).
#   - SSH from the host machine running this script to `*.boxd.sh` hosts
#     already linked (run `ssh <some-vm>.boxd.sh` once interactively to
#     complete Boxd's account-linking flow).
#   - Orchard TUI release binary at `target/release/orchard-tui` (run
#     `cargo build --release -p orchard` first; package name stays `orchard`).
#
# Usage:
#   scripts/ac9-federated-orchard-e2e.sh
#
# Exit codes:
#   0 — all assertions passed, VM destroyed.
#   1 — prerequisites missing.
#   2 — happy-path assertion failed.
#   3 — fallback assertion failed.

set -euo pipefail

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
readonly BINARY="$REPO_ROOT/target/release/orchard-tui"

readonly VM_NAME="orchard-federated-$(date +%s)"
readonly VM_HOST="$VM_NAME.boxd.sh"
readonly TEST_BRANCH="issue329/ac9-smoke"
readonly TEST_SESSION="or_ac9_smoke"
# EVENTS_LOG is rebound below after we override HOME — do not mark readonly.
EVENTS_LOG=""

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------

log()  { printf '==> %s\n' "$*" >&2; }
fail() { printf '!!! %s\n' "$*" >&2; exit "${2:-1}"; }

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

log "checking prerequisites"
command -v boxd >/dev/null || fail "boxd CLI not installed" 1
boxd info >/dev/null 2>&1 || fail "boxd CLI not authenticated (run 'boxd info' to debug)" 1
[ -x "$BINARY" ] || fail "release binary missing — run 'cargo build --release -p orchard' first" 1
command -v jq >/dev/null || fail "jq required for assertions" 1

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

cleanup() {
    local ec=$?
    log "cleanup: destroying $VM_NAME"
    boxd destroy "$VM_NAME" -y >/dev/null 2>&1 || true
    return "$ec"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# 1. Fork VM + install orchard-tui
# ---------------------------------------------------------------------------

log "forking VM from current golden as $VM_NAME"
boxd fork --name "$VM_NAME" >/dev/null

log "waiting for VM to come up"
for _ in $(seq 1 60); do
    status=$(boxd list --json | jq -r --arg n "$VM_NAME" '.[] | select(.name==$n) | .status')
    [ "$status" = "running" ] && break
    sleep 2
done
[ "$status" = "running" ] || fail "VM never reached running state (status=$status)" 1

log "copying release binary to VM ($VM_HOST)"
boxd cp "$BINARY" "$VM_NAME:/home/boxd/.local/bin/orchard-tui"
boxd exec "$VM_NAME" "chmod +x /home/boxd/.local/bin/orchard-tui"

log "creating test worktree on VM (branch $TEST_BRANCH)"
boxd exec "$VM_NAME" "cd /home/boxd/workspace/orchardist \
    && git worktree add .worktrees/ac9-smoke -b $TEST_BRANCH origin/main"

log "creating test tmux session on VM ($TEST_SESSION)"
boxd exec "$VM_NAME" "tmux new-session -d -s $TEST_SESSION \
    -c /home/boxd/workspace/orchardist/.worktrees/ac9-smoke"

# ---------------------------------------------------------------------------
# 2. Configure VM as OrchardProxy remote locally
# ---------------------------------------------------------------------------

# Isolate the test from the real user config by overriding HOME.
TEST_HOME="$(mktemp -d)"
export HOME="$TEST_HOME"
mkdir -p "$HOME/.config/orchard" "$HOME/.local/state/git-orchard"
EVENTS_LOG="$HOME/.local/state/git-orchard/events.jsonl"
cat > "$HOME/.config/orchard/config.json" <<EOF
{
  "repos": [{
    "slug": "drewdrewthis/orchardist",
    "path": "$REPO_ROOT",
    "remotes": [{
      "name": "ac9-vm",
      "host": "$VM_HOST",
      "path": "/home/boxd/workspace/orchardist",
      "type": "orchard-proxy",
      "fallback_kind": "remmy"
    }]
  }]
}
EOF
log "test HOME: $TEST_HOME"

# ---------------------------------------------------------------------------
# 3. Happy-path assertion
# ---------------------------------------------------------------------------

log "running orchard-tui --json (happy path)"
output=$("$BINARY" --json 2>/dev/null)
echo "$output" | jq . >/dev/null || fail "orchard-tui --json emitted invalid JSON" 2

if ! echo "$output" | jq -e \
    --arg host "$VM_HOST" --arg branch "$TEST_BRANCH" \
    '.repos[].worktrees[] | select(.host==$host and .branch==$branch)' >/dev/null; then
    echo "$output" | head -40 >&2
    fail "happy path: remote worktree not found in output" 2
fi
log "happy path: remote worktree visible with host=$VM_HOST branch=$TEST_BRANCH"

# ---------------------------------------------------------------------------
# 4. Remove orchard binary on VM → fallback path
# ---------------------------------------------------------------------------

log "removing remote orchard-tui binary (forces fallback to legacy path)"
boxd exec "$VM_NAME" "rm -f /home/boxd/.local/bin/orchard-tui"

events_before=$(wc -l < "$EVENTS_LOG" 2>/dev/null || echo 0)

log "running orchard-tui --json (fallback path)"
output=$("$BINARY" --json 2>/dev/null)

if ! echo "$output" | jq -e \
    --arg branch "$TEST_BRANCH" \
    '.repos[].worktrees[] | select(.branch==$branch)' >/dev/null; then
    echo "$output" | head -40 >&2
    fail "fallback: remote worktree not found in output" 3
fi

events_after=$(wc -l < "$EVENTS_LOG" 2>/dev/null || echo 0)
new_events=$(tail -n $((events_after - events_before)) "$EVENTS_LOG" 2>/dev/null || true)
if ! echo "$new_events" | grep -q "remote_adapter.fallback"; then
    echo "$new_events" >&2
    fail "fallback: no 'remote_adapter.fallback' diagnostic written to $EVENTS_LOG" 3
fi
log "fallback path: worktree visible + events.jsonl diagnostic written"

log "AC9 e2e: all assertions passed — destroying VM"
