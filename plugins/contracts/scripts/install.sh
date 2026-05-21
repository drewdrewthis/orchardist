#!/bin/bash
# claude-contracts plugin — install script
#
# Wires the plugin into ~/.claude/ via symlinks following the orchard-codex
# convention. Idempotent: re-runnable to re-create or repair links.
#
# v0.6: renamed from `contracts` to `claude-contracts`. The script removes
# the old `contracts` symlink + settings entry if present.
#
# Also handles drew-sim symlinks (see schema doc §11.3 — boxd was missing them).
#
# Usage:
#   bash install.sh                    # default: ~/workspace/orchard-codex
#   CODEX_ROOT=/path/to/codex bash install.sh

set -euo pipefail

CODEX_ROOT="${CODEX_ROOT:-$HOME/workspace/orchard-codex}"
CLAUDE_HOME="${CLAUDE_HOME:-$HOME/.claude}"

if [[ ! -d "$CODEX_ROOT" ]]; then
  echo "❌ CODEX_ROOT not found: $CODEX_ROOT" >&2
  exit 1
fi

mkdir -p "$CLAUDE_HOME/plugins" "$CLAUDE_HOME/agents" "$CLAUDE_HOME/skills"

ensure_symlink() {
  local src="$1"
  local dst="$2"
  if [[ ! -e "$src" ]]; then
    echo "⚠ source missing: $src — skipping" >&2
    return 0
  fi
  if [[ -L "$dst" ]]; then
    local current
    current=$(readlink "$dst")
    if [[ "$current" == "$src" ]]; then
      echo "✓ already linked: $dst → $src"
      return 0
    fi
    echo "→ relinking: $dst (was → $current)"
    rm "$dst"
  elif [[ -e "$dst" ]]; then
    echo "❌ $dst exists and is not a symlink — refusing to overwrite" >&2
    return 1
  fi
  ln -s "$src" "$dst"
  echo "✓ linked: $dst → $src"
}

echo "─── claude-contracts plugin ───"

# Migrate from old name (idempotent — no-op if absent)
if [[ -L "$CLAUDE_HOME/plugins/contracts" ]]; then
  echo "→ removing legacy 'contracts' symlink (renamed to 'claude-contracts')"
  rm "$CLAUDE_HOME/plugins/contracts"
fi

ensure_symlink "$CODEX_ROOT/plugins/claude-contracts" "$CLAUDE_HOME/plugins/claude-contracts"

# v0.7: drew-sim and /ask-drew are NOT plugin dependencies. Question /
# answer flow is retired. Skipping the legacy symlink step.

echo
echo "─── enable plugin in settings.json ───"
SETTINGS="$CLAUDE_HOME/settings.json"
# enabledPlugins is an object keyed by "<name>" or "<name>@<marketplace>" → true.
# Local plugins loaded via symlink in ~/.claude/plugins/ use just the name.
if [[ -f "$SETTINGS" ]]; then
  # Remove legacy 'contracts' entry if present, then add 'claude-contracts: true'
  if jq -e '.enabledPlugins["claude-contracts"] == true' "$SETTINGS" >/dev/null 2>&1; then
    echo "✓ already enabled as 'claude-contracts' in $SETTINGS"
  else
    TMP="$SETTINGS.tmp.$$"
    jq '.enabledPlugins = ((.enabledPlugins // {}) | del(.contracts) + {"claude-contracts": true})' "$SETTINGS" > "$TMP"
    mv "$TMP" "$SETTINGS"
    echo "✓ added 'claude-contracts: true' to enabledPlugins (removed legacy 'contracts' if present)"
  fi
else
  echo '{ "enabledPlugins": { "claude-contracts": true } }' > "$SETTINGS"
  echo "✓ created $SETTINGS with claude-contracts enabled"
fi

echo
echo "─── install MCP server deps ───"
if command -v bun >/dev/null 2>&1; then
  (cd "$CODEX_ROOT/plugins/claude-contracts/server" && bun install --frozen-lockfile 2>&1 | tail -3)
else
  echo "⚠ bun not found — install bun before using the plugin (https://bun.sh)" >&2
fi

echo
echo "─── ensure contracts dir exists ───"
mkdir -p "$CODEX_ROOT/contracts"
echo "✓ $CODEX_ROOT/contracts/ ready"

echo
echo "Done. Run /reload-plugins in any active Claude session, or restart sessions, to pick up the plugin."
echo "Verify: ls -la $CLAUDE_HOME/plugins/claude-contracts $CLAUDE_HOME/agents/drew-sim.md $CLAUDE_HOME/skills/ask-drew"
