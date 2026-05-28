#!/usr/bin/env bats
# Tests that every SKILL.md uses the simplified relative-path recipe
# (<this-skill-dir>/../../scripts/<script>) instead of the prior
# `${CLAUDE_PLUGIN_ROOT:-$(find ...)}` env-var-with-fallback dance.
#
# Why: $CLAUDE_PLUGIN_ROOT is not exported to skill subprocesses, so the
# original `bash "$CLAUDE_PLUGIN_ROOT/scripts/..."` recipe failed in real use.
# The fallback `${CLAUDE_PLUGIN_ROOT:-$(find ...)}` worked but was complex,
# fragile (ordering when multiple cached versions coexist), and unnecessary —
# the harness already gives the agent the absolute skill directory via the
# "Base directory" line injected at the top of every SKILL.md. The recipe just
# needs the agent to substitute that path inline.

setup() {
  SKILLS_DIR="$BATS_TEST_DIRNAME"
}

# All four skill SKILL.md files we ship.
SKILLS=(open-contract close-contract my-contracts close-conversation)

@test "no SKILL.md recipe USES \$CLAUDE_PLUGIN_ROOT to construct a path" {
  # Mentions in explanatory prose are OK ("Do not use \$CLAUDE_PLUGIN_ROOT,
  # it's unset in skill subprocesses"). What's NOT OK is a recipe that
  # actually constructs a path with the env var: `"$CLAUDE_PLUGIN_ROOT/...`.
  for s in "${SKILLS[@]}"; do
    run grep -F '"$CLAUDE_PLUGIN_ROOT/' "$SKILLS_DIR/$s/SKILL.md"
    [ "$status" -ne 0 ] || { echo "$s/SKILL.md still constructs a path with \$CLAUDE_PLUGIN_ROOT:"; echo "$output"; false; }
  done
}

@test "no SKILL.md uses the brittle \`find ~/.claude/plugins/cache\` fallback" {
  for s in "${SKILLS[@]}"; do
    run grep -F 'find ~/.claude/plugins/cache' "$SKILLS_DIR/$s/SKILL.md"
    [ "$status" -ne 0 ] || { echo "$s/SKILL.md still uses the find-fallback:"; echo "$output"; false; }
  done
}

@test "every recipe-bearing SKILL.md references <this-skill-dir>/../../scripts/" {
  # open-contract + close-contract emit sentinels; my-contracts + close-
  # conversation fold. Either way the recipe should reach the scripts/ dir
  # via the skill's own base directory.
  for s in "${SKILLS[@]}"; do
    run grep -F '<this-skill-dir>/../../scripts/' "$SKILLS_DIR/$s/SKILL.md"
    [ "$status" -eq 0 ] || { echo "$s/SKILL.md missing the simplified recipe shape"; false; }
  done
}

@test "every SKILL.md tells the agent to substitute the Base directory" {
  # The harness injects "Base directory for this skill: <abs path>" at the top
  # of every SKILL.md it loads. The recipe relies on the agent reading that
  # and substituting; the SKILL.md must say so explicitly.
  for s in "${SKILLS[@]}"; do
    run grep -F 'Base directory' "$SKILLS_DIR/$s/SKILL.md"
    [ "$status" -eq 0 ] || { echo "$s/SKILL.md does not point the agent at the 'Base directory' line"; false; }
  done
}
