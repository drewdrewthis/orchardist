#!/usr/bin/env bash
# Regression test for issue #243:
#   orchardist-serial-driver.sh must exit when its target tmux session dies.
#
# The test verifies that the driver process terminates within 5 seconds of the
# target session being killed, rather than running forever as a zombie.
#
# Run: ./scripts/tests/orchardist-serial-driver.test.sh
# Exit 0 = pass, non-zero = fail.

set -euo pipefail

# ---------------------------------------------------------------------------
# Paths and names
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DRIVER="${REPO_ROOT}/scripts/orchardist-serial-driver.sh"

SESSION_NAME="test_driver_$$"
FAKE_BIN_DIR="$(mktemp -d)"
DRIVER_PID=""
DRIVER_LOG="$(mktemp)"

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
cleanup() {
  # Kill the driver if still alive
  if [[ -n "${DRIVER_PID}" ]] && kill -0 "${DRIVER_PID}" 2>/dev/null; then
    kill "${DRIVER_PID}" 2>/dev/null || true
  fi
  # Kill the test tmux session if still alive
  tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
  # Remove temp dirs/files
  rm -rf "${FAKE_BIN_DIR}" "${DRIVER_LOG}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Pre-flight: driver script must exist
# ---------------------------------------------------------------------------
if [[ ! -f "${DRIVER}" ]]; then
  fail "driver script not found: ${DRIVER}  (expected scripts/orchardist-serial-driver.sh)"
fi
if [[ ! -x "${DRIVER}" ]]; then
  fail "driver script is not executable: ${DRIVER}"
fi

# ---------------------------------------------------------------------------
# Install a fake 'gh' on PATH that returns canned JSON so the driver never
# blocks on a real GitHub API call.
#
# The fake gh responds to any invocation — issue fetch, PR fetch, etc. — with
# a minimal valid payload that keeps the driver happy during its startup phase.
# ---------------------------------------------------------------------------
cat > "${FAKE_BIN_DIR}/gh" <<'EOF'
#!/usr/bin/env bash
# Stub gh CLI — returns minimal canned responses regardless of arguments.
# Used by orchardist-serial-driver.test.sh to avoid real API calls.
echo '{"number":999999,"title":"test PR","state":"OPEN","isDraft":false}'
EOF
chmod +x "${FAKE_BIN_DIR}/gh"

# Also stub 'orchard' in case the driver calls it
cat > "${FAKE_BIN_DIR}/orchard" <<'EOF'
#!/usr/bin/env bash
echo '{}'
EOF
chmod +x "${FAKE_BIN_DIR}/orchard"

export PATH="${FAKE_BIN_DIR}:${PATH}"

# ---------------------------------------------------------------------------
# 1. Create a disposable tmux session
# ---------------------------------------------------------------------------
tmux new-session -d -s "${SESSION_NAME}" -x 80 -y 24
echo "Created tmux session: ${SESSION_NAME}"

# ---------------------------------------------------------------------------
# 2. Launch the driver in the background with fast poll interval
#    Args: <pr-number> <session-name>  (matches expected driver interface)
# ---------------------------------------------------------------------------
POLL_INTERVAL=1 \
REPO="drewdrewthis/git-orchard-rs" \
  "${DRIVER}" 999999 "${SESSION_NAME}" >"${DRIVER_LOG}" 2>&1 &
DRIVER_PID=$!
echo "Started driver PID: ${DRIVER_PID}"

# Give the driver a moment to start up and enter its polling loop
sleep 1

# Confirm the driver is actually running before we kill the session
if ! kill -0 "${DRIVER_PID}" 2>/dev/null; then
  fail "driver exited too early (before session was killed) — check ${DRIVER_LOG}"
fi

# ---------------------------------------------------------------------------
# 3. Kill the target tmux session
# ---------------------------------------------------------------------------
tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
echo "Killed tmux session: ${SESSION_NAME}"

# ---------------------------------------------------------------------------
# 4. Wait up to 5 seconds for the driver to exit
# ---------------------------------------------------------------------------
TIMEOUT=5
ELAPSED=0
while kill -0 "${DRIVER_PID}" 2>/dev/null; do
  if [[ ${ELAPSED} -ge ${TIMEOUT} ]]; then
    echo "--- driver log ---"
    cat "${DRIVER_LOG}" >&2
    fail "driver (PID ${DRIVER_PID}) still alive ${TIMEOUT}s after session was killed — zombie bug reproduced"
  fi
  sleep 1
  ELAPSED=$(( ELAPSED + 1 ))
done

# ---------------------------------------------------------------------------
# 5. Assert: driver process is gone
# ---------------------------------------------------------------------------
if kill -0 "${DRIVER_PID}" 2>/dev/null; then
  fail "driver process is still alive after wait loop — this should not happen"
fi

pass "driver exited within ${ELAPSED}s of session death (issue #243 AC#1)"

# ---------------------------------------------------------------------------
# AC#2: driver exits when PR state is CLOSED
# ---------------------------------------------------------------------------
SESSION_NAME="test_driver_closed_$$"
DRIVER_PID=""
tmux new-session -d -s "${SESSION_NAME}" -x 80 -y 24

# Swap the fake gh to one that returns CLOSED
cat > "${FAKE_BIN_DIR}/gh" <<'EOF'
#!/usr/bin/env bash
echo '{"state":"CLOSED","statusCheckRollup":[]}'
EOF
chmod +x "${FAKE_BIN_DIR}/gh"

POLL_INTERVAL=1 \
REPO="drewdrewthis/git-orchard-rs" \
  "${DRIVER}" 999998 "${SESSION_NAME}" >"${DRIVER_LOG}" 2>&1 &
DRIVER_PID=$!

# Driver should hit CLOSED on its first poll (~1s) and exit
TIMEOUT=4
ELAPSED=0
while kill -0 "${DRIVER_PID}" 2>/dev/null; do
  if [[ ${ELAPSED} -ge ${TIMEOUT} ]]; then
    cat "${DRIVER_LOG}" >&2
    tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
    fail "driver did not exit on PR CLOSED within ${TIMEOUT}s"
  fi
  sleep 1
  ELAPSED=$(( ELAPSED + 1 ))
done
tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
pass "driver exited on PR CLOSED within ${ELAPSED}s (issue #243 AC#2)"

# ---------------------------------------------------------------------------
# AC#3: driver exits after MAX_ERR consecutive parse failures
# ---------------------------------------------------------------------------
SESSION_NAME="test_driver_err_$$"
DRIVER_PID=""
tmux new-session -d -s "${SESSION_NAME}" -x 80 -y 24

cat > "${FAKE_BIN_DIR}/gh" <<'EOF'
#!/usr/bin/env bash
echo 'not-json'
EOF
chmod +x "${FAKE_BIN_DIR}/gh"

POLL_INTERVAL=1 \
MAX_ERR=3 \
REPO="drewdrewthis/git-orchard-rs" \
  "${DRIVER}" 999997 "${SESSION_NAME}" >"${DRIVER_LOG}" 2>&1 &
DRIVER_PID=$!

# 3 ERRs at 1s each → exit by ~3s; allow 6s
TIMEOUT=6
ELAPSED=0
while kill -0 "${DRIVER_PID}" 2>/dev/null; do
  if [[ ${ELAPSED} -ge ${TIMEOUT} ]]; then
    cat "${DRIVER_LOG}" >&2
    tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
    fail "driver did not exit after MAX_ERR=3 consecutive errors within ${TIMEOUT}s"
  fi
  sleep 1
  ELAPSED=$(( ELAPSED + 1 ))
done
tmux kill-session -t "${SESSION_NAME}" 2>/dev/null || true
pass "driver exited after MAX_ERR consecutive errors within ${ELAPSED}s (issue #243 AC#3)"

echo "All issue #243 ACs verified."
