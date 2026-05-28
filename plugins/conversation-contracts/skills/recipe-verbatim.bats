#!/usr/bin/env bats
# Recipe-verbatim verification: extract the FIRST bash code block from each
# SKILL.md (the recipe the agent is told to run), substitute the harness-
# injected <this-skill-dir> placeholder, execute the recipe in a clean shell,
# and assert the output is what the recipe is supposed to produce.
#
# Why this layer exists: bats coverage on emit-sentinel.sh and fold-contracts.sh
# tests the SCRIPTS. It does NOT test the RECIPES that the SKILL.md files
# instruct the agent to construct. PR #673 surfaced this gap — the documented
# recipe used `$CLAUDE_PLUGIN_ROOT/scripts/...` for four releases, never
# exercised verbatim under a real shell, and silently failed in production
# because $CLAUDE_PLUGIN_ROOT is only exported to hook subprocesses (not
# skill subprocesses) per
# https://code.claude.com/docs/en/hooks#plugin-scripts.
#
# Test discipline: this file tests the RECIPE as documented, not the script.
# If a SKILL.md change breaks the recipe shape, these tests must catch it.

setup() {
  SKILLS_DIR="$BATS_TEST_DIRNAME"
  TMP_HOME="$(cd -P -- "$(mktemp -d)" && pwd -P)"
  CWD="$TMP_HOME/work"
  mkdir -p "$CWD"
}

teardown() {
  rm -rf "$TMP_HOME"
}

# _extract_recipe <skill-name> — print the first bash codeblock body from
# <skill-name>/SKILL.md to stdout. The "recipe" by convention is the first
# bash codeblock, attached to step 2 (open) or step 1 (list/close).
#
# Codeblocks in numbered list items are indented (typically 3 spaces).
# We strip a common leading indent from the body so the extracted recipe
# is executable as-is.
_extract_recipe() {
  local skill="$1"
  python3 - "$SKILLS_DIR/$skill/SKILL.md" <<'PY'
import re, sys, textwrap
md = open(sys.argv[1]).read()
m = re.search(r"^[ \t]*```bash\n(.*?)\n[ \t]*```", md, re.DOTALL | re.MULTILINE)
if not m:
    sys.exit("no bash codeblock in " + sys.argv[1])
sys.stdout.write(textwrap.dedent(m.group(1)))
PY
}

# _substitute_skill_dir <skill-name> <recipe-text> — replace the documented
# `<this-skill-dir>` placeholder with the skill's actual absolute base dir.
# This mirrors what the agent does at runtime when it reads the harness-
# injected "Base directory" line.
_substitute_skill_dir() {
  local skill="$1"; shift
  local recipe="$*"
  printf '%s' "${recipe//<this-skill-dir>/$SKILLS_DIR/$skill}"
}

# _run_recipe_clean <recipe> — execute the recipe under a clean env that
# mirrors what a skill subprocess gets: no $CLAUDE_PLUGIN_ROOT (the bug
# vector), $PWD set to a real cwd, $HOME pointed at a temp dir.
_run_recipe_clean() {
  local recipe="$1"
  run env -i \
        HOME="$TMP_HOME" \
        PATH="/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin" \
        SHELL="/bin/bash" \
      bash -c "cd '$CWD' && $recipe"
}

# ---- /open-contract recipe -------------------------------------------------

@test "/open-contract recipe verbatim emits a well-formed open sentinel" {
  recipe=$(_extract_recipe open-contract)
  # Substitute the user-provided statement placeholder before running.
  recipe="${recipe//<one-line statement>/recipe verbatim test — open}"
  resolved=$(_substitute_skill_dir open-contract "$recipe")
  _run_recipe_clean "$resolved"

  [ "$status" -eq 0 ]
  # The whole stdout must be a single JSON line; parse it.
  python3 -c "
import json, sys
obj = json.loads('''$output''')
assert obj['orchard_contract'] == 'open', obj
assert obj['statement'] == 'recipe verbatim test — open', obj
assert obj['id'].startswith('C-'), obj
" || { echo "stdout was: $output"; false; }
}

# ---- /close-contract recipe ------------------------------------------------

@test "/close-contract recipe verbatim emits a well-formed close sentinel" {
  recipe=$(_extract_recipe close-contract)
  recipe="${recipe//<id>/C-2026-05-28-deadbeef}"
  recipe="${recipe//<reason>/delivered: recipe verbatim test}"
  resolved=$(_substitute_skill_dir close-contract "$recipe")
  _run_recipe_clean "$resolved"

  [ "$status" -eq 0 ]
  python3 -c "
import json, sys
obj = json.loads('''$output''')
assert obj['orchard_contract'] == 'close', obj
assert obj['id'] == 'C-2026-05-28-deadbeef', obj
assert obj['reason'] == 'delivered: recipe verbatim test', obj
" || { echo "stdout was: $output"; false; }
}

# ---- /my-contracts recipe --------------------------------------------------

@test "/my-contracts recipe verbatim folds the current session jsonl" {
  # Seed a jsonl under the encoded $CWD path so the recipe's --auto branch
  # (CLAUDE_SESSION_ID unset under env -i) finds it.
  enc=$(printf '%s' "$CWD" | tr '/.' '--')
  mkdir -p "$TMP_HOME/.claude/projects/$enc"
  inner='{\"orchard_contract\":\"open\",\"id\":\"C-RECIPE-MY\",\"statement\":\"recipe-driven open\",\"ts\":\"t\"}'
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}\n' \
    "$inner" > "$TMP_HOME/.claude/projects/$enc/s.jsonl"

  recipe=$(_extract_recipe my-contracts)
  resolved=$(_substitute_skill_dir my-contracts "$recipe")
  _run_recipe_clean "$resolved"

  [ "$status" -eq 0 ]
  [[ "$output" == *"C-RECIPE-MY"* ]]
  [[ "$output" == *"recipe-driven open"* ]]
}

# ---- /close-conversation recipe (step 1: fold inventory) --------------------

@test "/close-conversation step-1 recipe verbatim folds the inventory" {
  enc=$(printf '%s' "$CWD" | tr '/.' '--')
  mkdir -p "$TMP_HOME/.claude/projects/$enc"
  inner='{\"orchard_contract\":\"open\",\"id\":\"C-RECIPE-CC\",\"statement\":\"recipe-driven conversation contract\",\"ts\":\"t\"}'
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}\n' \
    "$inner" > "$TMP_HOME/.claude/projects/$enc/s.jsonl"

  recipe=$(_extract_recipe close-conversation)
  resolved=$(_substitute_skill_dir close-conversation "$recipe")
  _run_recipe_clean "$resolved"

  [ "$status" -eq 0 ]
  [[ "$output" == *"C-RECIPE-CC"* ]]
}
