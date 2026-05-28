#!/usr/bin/env bats
# Tests for the conversation-contracts marketplace + plugin scaffold.
#
# Verifies the three JSON files the Claude Code plugin system requires are
# present, valid JSON, and carry the fields the plugins reference mandates
# (https://code.claude.com/docs/en/plugins-reference,
#  https://code.claude.com/docs/en/plugin-marketplaces):
#
#   .claude-plugin/marketplace.json
#     - name == "orchard"; owner is an object with a name
#     - exactly one plugin entry, conversation-contracts
#     - that entry's source is the canonical same-repo relative string
#       "./plugins/conversation-contracts" (NOT a {source:"github",url:"."} object)
#
#   plugins/conversation-contracts/.claude-plugin/plugin.json
#     - name (the only required field), description, current semver version,
#       author as an object with a name
#
#   plugins/conversation-contracts/hooks/hooks.json
#     - SessionStart and Stop hook keys present
#     - hook commands anchored on ${CLAUDE_PLUGIN_ROOT} (the documented contract)

setup() {
  REPO_ROOT="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
}

# _jq <file> <filter> — run jq over a repo file, fail the test on invalid JSON.
_jq() {
  jq -er "$2" "$REPO_ROOT/$1"
}

# ---- marketplace.json -------------------------------------------------------

@test "marketplace.json is valid JSON named orchard with an owner object" {
  [ "$(_jq .claude-plugin/marketplace.json '.name')" = "orchard" ]
  [ -n "$(_jq .claude-plugin/marketplace.json '.owner.name')" ]
}

@test "marketplace.json hosts exactly one plugin: conversation-contracts" {
  [ "$(_jq .claude-plugin/marketplace.json '.plugins | length')" -eq 1 ]
  [ "$(_jq .claude-plugin/marketplace.json '.plugins[0].name')" = "conversation-contracts" ]
}

@test "the plugin source is the canonical same-repo relative string" {
  # Reference: for a plugin in the same repo, source is a string "./plugins/..".
  # The object form {source:"github",url:".",path:..} is invalid and was the
  # "not the correct format to install" defect.
  [ "$(_jq .claude-plugin/marketplace.json '.plugins[0].source')" = "./plugins/conversation-contracts" ]
  [ "$(_jq .claude-plugin/marketplace.json '.plugins[0].source | type')" = "string" ]
}

# ---- plugin.json ------------------------------------------------------------

@test "plugin.json is valid JSON with name, description, version 0.10.3, author" {
  local f=plugins/conversation-contracts/.claude-plugin/plugin.json
  [ -n "$(_jq "$f" '.name')" ]
  [ -n "$(_jq "$f" '.description')" ]
  [ "$(_jq "$f" '.version')" = "0.10.3" ]
  [ -n "$(_jq "$f" '.author.name')" ]
}

# ---- hooks.json -------------------------------------------------------------

@test "hooks.json declares SessionStart and Stop hooks" {
  local f=plugins/conversation-contracts/hooks/hooks.json
  [ "$(_jq "$f" '.hooks | has("SessionStart")')" = "true" ]
  [ "$(_jq "$f" '.hooks | has("Stop")')" = "true" ]
}

@test "hook commands are anchored on CLAUDE_PLUGIN_ROOT" {
  # Reference: plugin hook commands reference scripts via "${CLAUDE_PLUGIN_ROOT}".
  local f=plugins/conversation-contracts/hooks/hooks.json
  [[ "$(_jq "$f" '.hooks.SessionStart[0].hooks[0].command')" == *'${CLAUDE_PLUGIN_ROOT}'* ]]
  [[ "$(_jq "$f" '.hooks.Stop[0].hooks[0].command')" == *'${CLAUDE_PLUGIN_ROOT}'* ]]
}
