#!/usr/bin/env bats
# Content tests for the /close-conversation skill.
#
# SKILL.md is a human-readable markdown document; correctness is verified by
# asserting the required phrases and flow steps appear in it.
#
#   L2.9  — confirms-closure path: fold open contracts (inventory), then emit an
#           orchard_contract close sentinel with reason "delivered", identifying
#           the conversation contract by its fixed deliverable string.
#   L2.10 — names-open-items path: file child contracts via /open-contract; the
#           conversation contract stays open. Plus the auto-close paths
#           (/exit, /quit, /bye) and the direct /close-contract escape hatch.

setup() {
  SKILL="$BATS_TEST_DIRNAME/SKILL.md"
  DELIVERABLE="user agrees conversation has come to a close and there are no loose ends"
}

_has() { grep -qF -- "$1" "$SKILL"; }

# ---- L2.9 — close as delivered after an inventory fold ----------------------

@test "L2.9: describes emitting an orchard_contract close sentinel, reason delivered" {
  _has "orchard_contract"
  _has "close"
  _has "delivered"
}

@test "L2.9: describes folding the inventory before closing" {
  _has "inventory"
  grep -qiE 'fold|contracts' "$SKILL"
}

@test "L2.9: identifies the conversation contract by its fixed deliverable" {
  _has "$DELIVERABLE"
}

# ---- L2.10 — name items → child contracts, conversation stays open ----------

@test "L2.10: files child contracts via /open-contract" {
  _has "/open-contract"
  _has "child"
}

@test "L2.10: conversation contract stays open when items are named" {
  grep -qiE 'stays open|remain' "$SKILL"
}

@test "L2.10: documents the auto-close and direct-close paths" {
  _has "/exit"
  _has "/quit"
  _has "/bye"
  grep -qiE 'direct|escape' "$SKILL"
}
