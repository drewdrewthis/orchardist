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

@test "v0.11: /close-contract documents the consent gate (AskUserQuestion)" {
  # Closing requires user consent. The skill must instruct the agent to gate
  # the close sentinel behind an AskUserQuestion when the user has not typed
  # a close path. The four user-typed close paths bypass the gate (typing
  # IS the consent).
  run grep -F 'AskUserQuestion' "$SKILLS_DIR/close-contract/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-contract/SKILL.md missing AskUserQuestion consent gate"; false; }
  run grep -F 'consent' "$SKILLS_DIR/close-contract/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-contract/SKILL.md does not mention consent"; false; }
}

@test "v0.11: /close-conversation documents the consent gate" {
  # The conversation contract has the four user-typed bypass paths, but when
  # the agent infers the conversation is winding down it must gate the close
  # behind AskUserQuestion.
  run grep -F 'AskUserQuestion' "$SKILLS_DIR/close-conversation/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-conversation/SKILL.md missing AskUserQuestion consent gate"; false; }
  run grep -F 'consent' "$SKILLS_DIR/close-conversation/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-conversation/SKILL.md does not mention consent"; false; }
}

@test "v0.11.1: both close-* SKILL.md embed /i-am-done's decision in AskUserQuestion text" {
  # Concern 10 from /review on v0.11.0: agent's self-graded /i-am-done decision
  # must be presented verbatim in the consent gate's question text, so the
  # user sees what the agent saw and cannot rubber-stamp a `partial` close.
  for s in close-contract close-conversation; do
    run grep -F '/i-am-done' "$SKILLS_DIR/$s/SKILL.md"
    [ "$status" -eq 0 ] || { echo "$s/SKILL.md must reference /i-am-done's decision in the question text"; false; }
  done
}

@test "v0.11.1: /close-contract bypass requires literal commands, not paraphrased intent" {
  # Concern 3 from /review on v0.11.0: leaving "clear directive" undefined
  # re-opens the bypass for paraphrased close intents and defeats the v0.11
  # thesis. The SKILL.md must name "literal" and "first non-whitespace token"
  # as the bypass condition.
  run grep -F 'literal' "$SKILLS_DIR/close-contract/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-contract/SKILL.md must require literal close commands for bypass"; false; }
  run grep -F 'first non-whitespace' "$SKILLS_DIR/close-contract/SKILL.md"
  [ "$status" -eq 0 ] || { echo "close-contract/SKILL.md must scope bypass to 'first non-whitespace token'"; false; }
}
