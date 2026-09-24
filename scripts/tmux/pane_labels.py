import json, subprocess, sys, os

from pane_labels_fmt import cell, path_under
from pane_labels_hookstate import hook_state_for_pane, claude_cells

PRINT_MODE = os.environ.get("ORCHARD_PRINT_MODE", "0") == "1"

# `curl -sf` only rejects non-2xx, so a proxy error page, a truncated body or
# a JSON document of the wrong shape all arrive here as a "successful"
# response. Fall back to the same empty result set the unreachable-daemon
# branch installs, rather than raising and labelling nothing at all.
try:
    with open(sys.argv[1]) as f:
        resp = json.load(f)
except (OSError, ValueError):
    print("orchard-tmux-labels: daemon response was not valid JSON; "
          "labelling from local state only", file=sys.stderr)
    resp = {}
if not isinstance(resp, dict):
    print("orchard-tmux-labels: daemon response was not a JSON object; "
          "labelling from local state only", file=sys.stderr)
    resp = {}

data = resp.get("data")
if not isinstance(data, dict):
    data = {}
# v0.8 schema renamed `projects` → `repos` (ADR-015). Each repo has `slug`
# (was `name`) and the same nested worktree shape.
repos = data.get("repos") or []

# (worktree_path, worktree_data, repo_slug) sorted longest-prefix first.
# Entries of the wrong shape are skipped, not fatal — see the JSON guard above.
all_worktrees = []
for r in repos if isinstance(repos, list) else []:
    if not isinstance(r, dict):
        continue
    for wt in (r.get("worktrees") or []):
        if not isinstance(wt, dict) or not isinstance(wt.get("path"), str):
            continue
        all_worktrees.append((wt["path"], wt, r.get("slug")))
all_worktrees.sort(key=lambda x: -len(x[0]))


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
        if path_under(wt_path, pane_path):
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
        #   CLAUDE  : per-state (green/brightblack/yellow) — hook state
        #   CMD     : green,bold         (running process — claude/zsh/vim/etc.)
        #   PATH    : brightblack        (only when no worktree)

        if wt:
            s_g, s_c = status_glyph(wt.get("pr"))
            if s_g:
                cells.append(cell(f"fg={s_c}", s_g))

            ids = []
            iss = wt.get("issue")
            pr = wt.get("pr")
            if iss: ids.append(f"#{iss['number']}")
            if pr:  ids.append(f"PR#{pr['number']}")
            if ids:
                cells.append(cell("fg=cyan,bold", " / ".join(ids)))

            title = (iss or {}).get("title")
            if title:
                cells.append(cell("fg=white", str(title)[:55]))

            b = wt.get("branch") or ""
            if b:
                cells.append(cell("fg=magenta", b))

            # labels is [{name, color, description}, ...] in v0.8 (was [String]
            # in pre-ADR-015 shape). Extract `.name` for the rendered chips.
            label_names = [l.get("name") for l in ((pr or {}).get("labels") or []) if l.get("name")]
            if label_names:
                cells.append(cell("fg=yellow", " ".join(f"[{l}]" for l in label_names[:3])))

            if repo:
                cells.append(cell("fg=blue,italics", repo))
        else:
            # No worktree match — show truncated path; cmd is rendered separately below.
            # HOME can be unset (cron, a stripped `env`, a systemd unit) and can be
            # empty; str.replace("") would splice a `~` between every character.
            home = os.environ.get("HOME") or ""
            short = pane_path.replace(home, "~") if home else pane_path
            if len(short) > 50:
                short = "…" + short[-49:]
            cells.append(cell("fg=brightblack", short))

        # Process indicator (always last) — what's actually running in the pane.
        # Note: tmux's pane_current_command often shows a version string (e.g. "2.1.132")
        # for claude because claude sets its window title; map that to "claude" explicitly.
        cmd_str = (cmd or "").strip()
        # Heuristic: if cmd looks like a version (digits.digits.digits), it's likely claude
        # which prints its semver as the process title.
        is_version_str = bool(cmd_str) and all(part.isdigit() for part in cmd_str.split(".") if part)
        if is_version_str and "." in cmd_str:
            cmd_str = "claude"
        is_claude_pane = cmd_str.startswith("claude")

        # Claude hook state (ADR-007) sits just before the process indicator.
        cells.extend(claude_cells(hook_state_for_pane(session, pane_path, is_claude_pane)))

        if cmd_str:
            interesting = {"claude","node","python","python3","go","cargo","ssh","mosh","vim","nvim","emacs"}
            if cmd_str in interesting or is_claude_pane:
                cells.append(cell("fg=green,bold", f"⏵ {cmd_str}"))
            elif cmd_str in {"zsh","bash","fish","sh","-zsh","-bash"}:
                cells.append(cell("fg=brightblack", f"⏵ {cmd_str}"))
            else:
                cells.append(cell("fg=cyan", f"⏵ {cmd_str}"))


        label = "  ".join(cells)
        if PRINT_MODE:
            print(f"{pane_id}\t{label}")
        else:
            # Set on the pane via target -t ${pane_id}
            subprocess.run(["tmux","set-option","-pt", pane_id, "@orchard_pane_label", label],
                           check=False)
        count += 1

print(f"orchard-tmux-labels: updated {count} panes", file=sys.stderr)

if not PRINT_MODE:
    # Also clear any stale session-level @orchard_label so the new pane labels are
    # what choose-tree renders (sessions fall back to default chrome).
    out = subprocess.run(["tmux","list-sessions","-F","#{session_name}"], capture_output=True, text=True)
    if out.returncode == 0:
        for n in out.stdout.splitlines():
            subprocess.run(["tmux","set-option","-t",n,"-u","@orchard_label"], check=False)
