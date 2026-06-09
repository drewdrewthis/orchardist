#!/usr/bin/env bash
# pane-labels.sh — derive a rich label per tmux PANE from orchard daemon
# state and write it to @orchard_pane_label so choose-tree (expanded)
# renders it.
#
# Wired into `prefix + s` via ~/.tmux.conf:
#   bind-key s run-shell '~/.local/bin/orchard-tmux-labels' \; choose-tree ...
#
# Install: copy to ~/.local/bin/orchard-tmux-labels
# Source of truth: scripts/tmux/pane-labels.sh in orchardist (L1).
#
# Daemon contract: queries `repos { slug worktrees { ... } }` per the v0.8
# schema (ADR-015 rename project→repo). Falls back to empty results when
# the daemon is unreachable so the picker still works.
set -euo pipefail

DAEMON="${ORCHARD_DAEMON_URL:-http://127.0.0.1:7777}"

run_once() {
  local qfile panes
  qfile=$(mktemp)
  panes=$(mktemp)
  trap "rm -f '$qfile' '$panes'" RETURN

  local daemon_ok=1
  # The query is intentionally lean (path+branch only) so the picker boot
  # stays under ~1s even with 30+ worktrees across many repos. Enriching
  # each worktree with PR/issue/labels hits the gh provider per-worktree
  # and can take 30s+ under cold cache. Set ORCHARD_LABEL_ENRICH=1 to
  # opt-in to the heavy query (useful when running outside the prefix-s
  # hot path).
  local query
  if [ "${ORCHARD_LABEL_ENRICH:-0}" = "1" ]; then
    query='{"query":"{ tmuxSessions { name lastActivityAt } claudeInstances { state pane { window { session { name } } } } repos { slug worktrees { branch path host pr { number draft mergeStateStatus statusCheckRollup labels { name } reviewDecision } issue { number title } } } }"}'
  else
    query='{"query":"{ tmuxSessions { name lastActivityAt } claudeInstances { state pane { window { session { name } } } } repos { slug worktrees { branch path host } } }"}'
  fi
  if ! curl -sf --max-time 15 -X POST "${DAEMON}/graphql" \
      -H 'Content-Type: application/json' \
      -d "$query" \
      > "$qfile" 2>/dev/null; then
    daemon_ok=0
    printf '{"data":{"tmuxSessions":[],"claudeInstances":[],"repos":[]}}' > "$qfile"
  fi

  local TAB
  TAB=$(printf '\t')
  # Per-pane: target id, session_name, window_index, pane_index, current_path, current_command.
  tmux list-panes -aF "#{pane_id}${TAB}#{session_name}${TAB}#{window_index}${TAB}#{pane_index}${TAB}#{pane_current_path}${TAB}#{pane_current_command}" > "$panes" 2>/dev/null || return 1

  ORCHARD_DAEMON_OK=$daemon_ok python3 - "$qfile" "$panes" <<'PY'
import json, subprocess, sys, datetime, os

DAEMON_OK = os.environ.get("ORCHARD_DAEMON_OK", "1") == "1"

with open(sys.argv[1]) as f:
    resp = json.load(f)
data = resp.get("data") or {}
# v0.8 schema renamed `projects` → `repos` (ADR-015). Each repo has `slug`
# (was `name`) and the same nested worktree shape.
repos = data.get("repos") or []

# (worktree_path, worktree_data, repo_slug) sorted longest-prefix first
all_worktrees = []
for r in repos:
    for wt in (r.get("worktrees") or []):
        all_worktrees.append((wt["path"], wt, r["slug"]))
all_worktrees.sort(key=lambda x: -len(x[0]))

now = datetime.datetime.now(datetime.timezone.utc)

def status_glyph(pr):
    if not pr: return ("", "default")
    if pr.get("statusCheckRollup") == "FAILURE":            return ("🚫", "red")
    if pr.get("reviewDecision") == "CHANGES_REQUESTED":     return ("🔴", "red")
    if pr.get("mergeStateStatus") in ("DIRTY","BLOCKED"):   return ("⚠", "yellow")
    if pr.get("draft"):                                     return ("📝", "default")
    if pr.get("statusCheckRollup") == "PENDING":            return ("⬆", "blue")
    if (pr.get("reviewDecision") == "APPROVED" and
        pr.get("statusCheckRollup") == "SUCCESS" and
        pr.get("mergeStateStatus") == "CLEAN"):             return ("🟢", "green")
    return ("⬆", "blue")

def head_branch(path):
    try:
        r = subprocess.run(["git","-C",path,"branch","--show-current"],
                           capture_output=True, text=True, timeout=2)
        if r.returncode == 0:
            return (r.stdout or "").strip() or None
    except Exception:
        pass
    return None

def pick_worktree(pane_path):
    """Longest-prefix match, then disambiguate by HEAD branch when path matches multiple."""
    matches = []
    seen = set()
    for wt_path, wt, repo in all_worktrees:
        if wt_path in seen: continue
        if pane_path == wt_path or pane_path.startswith(wt_path + "/"):
            matches.append((wt_path, wt, repo))
            seen.add(wt_path)
    if not matches:
        return (None, None)
    if len(matches) == 1:
        _, wt, repo = matches[0]
        return (wt, repo)
    head = head_branch(pane_path)
    if head:
        for _, wt, repo in matches:
            if wt.get("branch") == head:
                return (wt, repo)
    _, wt, repo = matches[0]
    return (wt, repo)

count = 0
with open(sys.argv[2]) as f:
    for line in f:
        line = line.rstrip()
        if not line: continue
        cols = line.split("\t")
        if len(cols) < 6: continue
        pane_id, session, window_idx, pane_idx, pane_path, cmd = cols
        wt, repo = pick_worktree(pane_path)

        cells = []

        # Color palette (deterministic per category):
        #   STATUS  : per-state (red/yellow/green/blue/default)
        #   ID      : cyan,bold        (#NNN / PR#NNN)
        #   TITLE   : white             (issue title)
        #   BRANCH  : magenta            (git branch)
        #   LABELS  : yellow             (gh labels)
        #   REPO    : blue,italics       (orchard repo slug)
        #   CMD     : green,bold         (running process — claude/zsh/vim/etc.)
        #   PATH    : brightblack        (only when no worktree)
        #   DOWN    : red                (daemon down badge)

        if wt:
            s_g, s_c = status_glyph(wt.get("pr"))
            if s_g:
                cells.append(f"#[fg={s_c}]{s_g}#[default]")

            ids = []
            iss = wt.get("issue")
            pr = wt.get("pr")
            if iss: ids.append(f"#{iss['number']}")
            if pr:  ids.append(f"PR#{pr['number']}")
            if ids:
                cells.append(f"#[fg=cyan,bold]{' / '.join(ids)}#[default]")

            title = (iss or {}).get("title")
            if title:
                cells.append(f"#[fg=white]{title[:55]}#[default]")

            b = wt.get("branch") or ""
            if b:
                cells.append(f"#[fg=magenta]{b}#[default]")

            # labels is [{name, color, description}, ...] in v0.8 (was [String]
            # in pre-ADR-015 shape). Extract `.name` for the rendered chips.
            label_names = [l.get("name") for l in ((pr or {}).get("labels") or []) if l.get("name")]
            if label_names:
                cells.append("#[fg=yellow]" + " ".join(f"[{l}]" for l in label_names[:3]) + "#[default]")

            if repo:
                cells.append(f"#[fg=blue,italics]{repo}#[default]")
        else:
            # No worktree match — show truncated path; cmd is rendered separately below.
            short = pane_path.replace(os.environ["HOME"], "~")
            if len(short) > 50:
                short = "…" + short[-49:]
            cells.append(f"#[fg=brightblack]{short}#[default]")

        # Process indicator (always last) — what's actually running in the pane.
        # Note: tmux's pane_current_command often shows a version string (e.g. "2.1.132")
        # for claude because claude sets its window title; map that to "claude" explicitly.
        cmd_str = (cmd or "").strip()
        # Heuristic: if cmd looks like a version (digits.digits.digits), it's likely claude
        # which prints its semver as the process title.
        is_version_str = bool(cmd_str) and all(part.isdigit() for part in cmd_str.split(".") if part)
        if is_version_str and "." in cmd_str:
            cmd_str = "claude"
        if cmd_str:
            interesting = {"claude","node","python","python3","go","cargo","ssh","mosh","vim","nvim","emacs"}
            if cmd_str in interesting or cmd_str.startswith("claude"):
                cells.append(f"#[fg=green,bold]⏵ {cmd_str}#[default]")
            elif cmd_str in {"zsh","bash","fish","sh","-zsh","-bash"}:
                cells.append(f"#[fg=brightblack]⏵ {cmd_str}#[default]")
            else:
                cells.append(f"#[fg=cyan]⏵ {cmd_str}#[default]")

        # Daemon-down badge only on panes that ARE attached to an orchard worktree —
        # otherwise it pollutes the labels on shells/editors that have no orchard data
        # to be "down" about. Status-line indicator (see ~/.tmux.conf) carries the
        # global signal.
        if not DAEMON_OK and wt:
            cells.append("#[fg=red]⚠ stale#[default]")

        label = "  ".join(cells)
        # Set on the pane via target -t ${pane_id}
        subprocess.run(["tmux","set-option","-pt", pane_id, "@orchard_pane_label", label],
                       check=False)
        count += 1

print(f"orchard-tmux-labels: updated {count} panes", file=sys.stderr)

# Also clear any stale session-level @orchard_label so the new pane labels are
# what choose-tree renders (sessions fall back to default chrome).
out = subprocess.run(["tmux","list-sessions","-F","#{session_name}"], capture_output=True, text=True)
if out.returncode == 0:
    for n in out.stdout.splitlines():
        subprocess.run(["tmux","set-option","-t",n,"-u","@orchard_label"], check=False)
PY
}

run_once
