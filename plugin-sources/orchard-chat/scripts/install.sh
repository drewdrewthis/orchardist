#!/usr/bin/env bash
# Install hook for orchard-chat plugin.
# - bun install in server/ (so @modelcontextprotocol/sdk is available)
# - symlinks scripts/chat-post into ~/.local/bin if PATH-friendly
set -euo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"

cd "${PLUGIN_ROOT}/server"
if command -v bun >/dev/null 2>&1; then
  bun install --silent
else
  echo "[orchard-chat] bun not found; install bun first (https://bun.sh)" >&2
  exit 1
fi

# Make chat-post available on PATH for workers/sisters.
mkdir -p "${HOME}/.local/bin"
ln -sf "${PLUGIN_ROOT}/scripts/chat-post" "${HOME}/.local/bin/chat-post"

echo "[orchard-chat] installed; chat-post -> ${HOME}/.local/bin/chat-post"
echo "[orchard-chat] ensure ~/.local/bin is on \$PATH for worker shells"
