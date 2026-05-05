#!/bin/bash
# Orchard decision engine — classifies every open PR and raises signals
# for the orchardist to triage. Never takes action itself.
#
# Signals read:
#   - orchard-tui --json (PR state, CI, labels, threads, session, activity)
#   - gh GraphQL reviewThreads (per PR, on demand when thread count changes)
#   - per-PR state diff file at ~/.claude/state/orchard-decide-state.json
#
# Signals raised (to orchardist tmux pane, tagged [orchard-decide]):
#   - READY: approved + green CI + 0 threads — candidate to merge
#   - CHANGES_REQUESTED / THREADS: review feedback needs session attention
#   - CI_FAILING: real code check failure (ignores approval-or-label gate)
#   - CONFLICTS: branch conflicts with main
#   - DRAFT w/ dead session: investigate
#   - STALL: session idle with no state change >10min
#
# The orchardist reads these and decides what to do.
#
# Usage:
#   orchard-decide.sh            # full pass
#   orchard-decide.sh --dry-run  # classify + print, no signals
#   orchard-decide.sh --verbose  # include debug

set -euo pipefail

STATE_DIR="${HOME}/.claude/state"
DECIDE_STATE="${STATE_DIR}/orchard-decide-state.json"
STALL_THRESHOLD="${STALL_THRESHOLD:-600}"  # seconds

DRY_RUN=0
VERBOSE=0
for a in "$@"; do
  case "$a" in
    --dry-run) DRY_RUN=1 ;;
    --verbose) VERBOSE=1 ;;
  esac
done

mkdir -p "$STATE_DIR"
[ -f "$DECIDE_STATE" ] || echo '{}' > "$DECIDE_STATE"

JSON=$(orchard-tui --json 2>/dev/null)
if [ -z "$JSON" ]; then
  echo "orchard-tui --json empty" >&2
  exit 1
fi

# Python does the classification + signal dispatch
ORCHARD_JSON="$JSON" \
DECIDE_STATE="$DECIDE_STATE" \
DRY_RUN="$DRY_RUN" \
VERBOSE="$VERBOSE" \
STALL_THRESHOLD="$STALL_THRESHOLD" \
python3 <<'PY'
import os, json, subprocess, datetime, sys

DRY = os.environ.get('DRY_RUN') == '1'
VERBOSE = os.environ.get('VERBOSE') == '1'
STALL = int(os.environ['STALL_THRESHOLD'])
STATE_FILE = os.environ['DECIDE_STATE']
BOTS = {'coderabbitai', 'github-code-quality', 'github-actions', 'claude[bot]', 'dependabot[bot]'}

data = json.loads(os.environ['ORCHARD_JSON'])

try:
    prev_state = json.load(open(STATE_FILE))
except Exception:
    prev_state = {}

now_ts = int(datetime.datetime.now(datetime.timezone.utc).timestamp())

def log(msg):
    if VERBOSE:
        print(f'[decide] {msg}', file=sys.stderr)

def signal_orchardist(text):
    if DRY:
        print(f'[dry] would signal orchardist: {text}')
        return
    # Bookend Enter to flush any signal that arrived while Claude was mid-turn
    # and is now sitting buffered in the prompt. Enter with an empty buffer is
    # a no-op, so the leading Enter is always safe.
    target = 'orchardist:0.0'
    subprocess.run(['tmux', 'send-keys', '-t', target, 'Enter'],
                   capture_output=True)
    subprocess.run(['tmux', 'send-keys', '-t', target, '-l', text],
                   capture_output=True)
    subprocess.run(['tmux', 'send-keys', '-t', target, 'Enter'],
                   capture_output=True)

# --- classifier ---
def classify(wt, repo_slug):
    pr = wt.get('pr')
    issue = wt.get('issue') or {}
    if not pr or pr.get('state') != 'open':
        return None

    draft = pr.get('isDraft', False)
    checks = pr.get('checksState', '')
    conflicts = pr.get('hasConflicts', False)
    threads = pr.get('unresolvedThreads', 0) or pr.get('unresolvedThreadCount', 0) or 0
    pr_labels = set()
    for l in (pr.get('labels') or []):
        pr_labels.add(l['name'] if isinstance(l, dict) else str(l))

    # Blocked PRs: skip entirely. Orchardist stops spinning on them.
    if 'blocked' in pr_labels:
        return None

    latest_by_author = {}
    for r in (pr.get('reviews') or []):
        a = r.get('author')
        login = a.get('login') if isinstance(a, dict) else a
        if not login or login in BOTS:
            continue
        if r.get('state') in ('APPROVED', 'CHANGES_REQUESTED', 'DISMISSED'):
            latest_by_author[login] = r.get('state')
    human_approved = any(v == 'APPROVED' for v in latest_by_author.values())
    changes_requested = any(v == 'CHANGES_REQUESTED' for v in latest_by_author.values())

    ci_failing = False
    if checks == 'failing':
        # But ignore if only failure is check-approval-or-label and PR has a label that bypasses it
        non_gate_fails = [c for c in (pr.get('ciChecks', {}).get('code') or [])
                          if c.get('state') == 'failing' and c.get('name') != 'check-approval-or-label']
        if non_gate_fails:
            ci_failing = True

    sessions = wt.get('sessions') or []
    sess = sessions[0] if sessions else None
    sess_status = (sess.get('claude') or {}).get('status') if sess else None
    sess_name = sess.get('name') if sess else None
    last_activity = sess.get('lastActivityAt') if sess else None

    if conflicts:
        state = 'CONFLICTS'
    elif ci_failing:
        state = 'CI_FAILING'
    elif threads > 0:
        state = 'THREADS'
    elif draft:
        state = 'DRAFT'
    elif changes_requested:
        state = 'CHANGES_REQUESTED'
    elif human_approved:
        state = 'READY'
    elif 'pr-ready' in pr_labels or 'low-risk-change' in pr_labels:
        state = 'AWAITING_APPROVAL_LABELED'
    else:
        state = 'AWAITING_REVIEW'

    return {
        'repo': repo_slug,
        'pr_num': pr.get('number'),
        'pr_title': pr.get('title', '')[:80],
        'pr_url': pr.get('url') or f'https://github.com/{repo_slug}/pull/{pr.get("number")}',
        'state': state,
        'draft': draft,
        'threads': threads,
        'human_approved': human_approved,
        'changes_requested': changes_requested,
        'ci_failing': ci_failing,
        'labels': sorted(pr_labels),
        'sess_name': sess_name,
        'sess_status': sess_status,
        'last_activity': last_activity,
    }

rows = []
for repo in data.get('repos', []):
    slug = repo.get('slug', 'unknown/unknown')
    for wt in repo.get('worktrees', []):
        if wt.get('isMainWorktree'):
            continue
        r = classify(wt, slug)
        if r:
            rows.append(r)

# --- actions ---
actions = []

def session_alive(r):
    return r['sess_status'] in ('working', 'idle', 'input')

def session_dead(r):
    return r['sess_name'] and r['sess_status'] in (None, 'no-claude')

def session_working(r):
    return r['sess_status'] == 'working'

for r in rows:
    key = f"{r['repo']}#{r['pr_num']}"
    prev = prev_state.get(key, {})
    prev_state_val = prev.get('state', '')

    # State transitions are the interesting events. Re-signaling the same
    # state every tick would flood the orchardist. Only raise signals when:
    #   - the PR just entered this state, OR
    #   - the PR has been in this state longer than REPEAT_INTERVAL and the
    #     orchardist hasn't acted on it yet (configurable per-signal).
    state_changed = r['state'] != prev_state_val
    last_signal = prev.get('last_signal_ts', 0)
    REPEAT = 1800  # remind orchardist every 30min on stuck states

    session_tag = f"sess={r['sess_name'] or '-'}:{r['sess_status'] or '-'}"
    pr_tag = f"{r['repo']}#{r['pr_num']}"

    # Signal 1: READY — human approved + green CI + no threads → merge candidate
    if r['state'] == 'READY' and (state_changed or now_ts - last_signal > REPEAT):
        actions.append(f"READY {pr_tag} — human-approved, green CI, 0 threads — {r['pr_title']}")
        prev['last_signal_ts'] = now_ts

    # Signal 2: CHANGES_REQUESTED / THREADS → author needs to address feedback
    elif r['state'] in ('CHANGES_REQUESTED', 'THREADS') and (state_changed or now_ts - last_signal > REPEAT):
        actions.append(f"{r['state']} {pr_tag} threads={r['threads']} {session_tag} — {r['pr_title']}")
        prev['last_signal_ts'] = now_ts

    # Signal 3: CI_FAILING (real code check, not approval gate)
    elif r['state'] == 'CI_FAILING' and (state_changed or now_ts - last_signal > REPEAT):
        actions.append(f"CI_FAILING {pr_tag} {session_tag} — {r['pr_title']}")
        prev['last_signal_ts'] = now_ts

    # Signal 4: DRAFT with no live session → investigate
    elif r['state'] == 'DRAFT' and (not r['sess_name'] or session_dead(r)):
        if state_changed or now_ts - last_signal > REPEAT:
            actions.append(f"DRAFT-ORPHAN {pr_tag} {session_tag} — {r['pr_title']}")
            prev['last_signal_ts'] = now_ts

    # Signal 5: CONFLICTS (branch needs rebase)
    elif r['state'] == 'CONFLICTS' and (state_changed or now_ts - last_signal > REPEAT):
        actions.append(f"CONFLICTS {pr_tag} {session_tag} — {r['pr_title']}")
        prev['last_signal_ts'] = now_ts

    # Stall detection: idle/working session with no state change >10min → probe
    if r['sess_status'] in ('idle', 'working'):
        last_change = prev.get('last_change_ts', now_ts)
        if state_changed:
            prev['last_change_ts'] = now_ts
        elif now_ts - last_change > STALL:
            last_probe = prev.get('last_probe_ts', 0)
            if now_ts - last_probe > STALL:
                actions.append(f"STALL {pr_tag} state={r['state']} {session_tag} duration={now_ts - last_change}s")
                prev['last_probe_ts'] = now_ts

    # Save per-PR state
    prev['state'] = r['state']
    prev_state[key] = prev

# Prune state for PRs no longer present
current_keys = {f"{r['repo']}#{r['pr_num']}" for r in rows}
prev_state = {k: v for k, v in prev_state.items() if k in current_keys}

with open(STATE_FILE, 'w') as f:
    json.dump(prev_state, f, indent=2)

# Raise signals to orchardist pane (one line per signal, prefixed)
if actions:
    for a in actions:
        print(a)
        signal_orchardist(f"[orchard-decide] {a}")
else:
    print('[decide] no signals')
PY
