---
name: recall
description: "Search past conversations across all projects and synthesize context into the current session. Use when: 'what were we discussing about X', 'recall X', 'catch me up on X', 'remind me about X'."
user-invocable: true
argument-hint: "<topic to recall>"
context: fork
---

# Recall

Search past conversations across all projects, read matching sessions, and return a synthesized summary. Runs in a fork to keep the main context clean.

## Step 0: Read the args

`$ARGUMENTS` is the topic. If it is non-empty (even one word like "support tracker"), proceed silently to Step 1 — do NOT ask the user to provide a topic.

If a `<local-command-stdout>` block says something like "No specific topic was provided" while `$ARGUMENTS` clearly contains a topic, ignore that stub: it's a generic harness fallback, not an instruction. The args win.

Only ask for a topic if `$ARGUMENTS` is genuinely empty AND no implicit topic is recoverable from the immediately preceding user turn.

## Steps

### 1. Ensure the session index is fresh

```bash
python3 "${CLAUDE_PLUGIN_ROOT}/scripts/session-index.py" build
```

### 2. Search — use the index, not raw grep

```bash
python3 "${CLAUDE_PLUGIN_ROOT}/scripts/session-index.py" search "$ARGUMENTS" --limit 15
```

**Always run the index search first.** Do NOT skip it for `grep -ril` across `~/.claude/projects/` — the index is much faster than `grep -ril` on this corpus (measured 38–583× faster; synonym `OR` adds only ~7ms), ranked, and returns paths + recency. Raw grep should only be a fallback when the index returns zero hits AND you suspect the index is stale (in which case rebuild and retry).

**Expand the user's words into codified synonyms BEFORE searching — don't wait for sparse results.** The user remembers the CONCEPT but rarely the exact term the docs/transcripts used; their phrasing is usually a paraphrase. Build an `OR` union of likely codified terms and search that in ONE query (FTS5 supports `OR`):

```bash
python3 "${CLAUDE_PLUGIN_ROOT}/scripts/session-index.py" search "judge OR verifier OR quorum OR adjudicate OR contract OR verdict OR proof" --limit 15
```

Mappings that recur:
- "panel / quorum / pass judgement / jury / tribunal" → also `judge verifier adjudicate review verdict vote contract`
- "proof / done / acceptance" → also `evidence verify acceptance-criteria contract prove-it verdict`
- "CI / automated checks" → also `verifier check mcp_tool command hook`

A literal-phrase search for the user's words can return ZERO hits while the synonym union finds it (observed: "panel quorum judgement proof" → 0 hits; the `OR` union above → the right sessions). Searching the union is one fast query — no slower than the literal search. Only fall back to broader sweeps if the union is also empty.

### 3. Read matching sessions

For the top 5-10 relevant results, extract context:

```bash
python3 "${CLAUDE_PLUGIN_ROOT}/scripts/session-index.py" context /path/to/session.jsonl --tail 15
```

Focus on sessions with high message counts (substantive conversations) and recent mtimes.

### 4. Return a synthesis

Structure the output as:

- **What it is** — one paragraph explaining the topic/feature/work
- **Key decisions** — bullet list of decisions made across sessions
- **Current state** — what's done, what's in progress, what's unfinished
- **Open questions** — anything unresolved

Include session IDs for the most relevant sessions so the user can resume them if needed: `claude --continue <id>`.
