#!/bin/bash
# Orchard session monitor — polls orchard --json, sends changes to orchardist tmux session
# Also checks a watchlist for issues that should be driven to green

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORCHARDIST_PANE="${ORCHARDIST_PANE:-orchardist:0.0}"
STATE_FILE="/tmp/orchard-monitor-state.txt"
WATCH_FILE="/tmp/orchard-monitor-watch.txt"
DURATION_FILE="/tmp/orchard-monitor-durations.json"
WATCH_INTERVAL="${WATCH_INTERVAL:-300}"
LAST_WATCH_FILE="/tmp/orchard-monitor-last-watch"
REPORT_INTERVAL="${REPORT_INTERVAL:-300}"
LAST_REPORT_FILE="/tmp/orchard-monitor-last-report"

# Initialize duration file if missing
[ -f "$DURATION_FILE" ] || echo '{}' > "$DURATION_FILE"
[ -f "$LAST_WATCH_FILE" ] || echo "0" > "$LAST_WATCH_FILE"

while true; do
  CURRENT=$(orchard --json 2>/dev/null)
  if [ -z "$CURRENT" ]; then
    sleep 60
    continue
  fi

  NOW=$(date +%s)

  # Extract stable state fields — exclude volatile ones (cost, context%, timestamps)
  CURRENT_SESSIONS=$(echo "$CURRENT" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    lines = []
    for repo in data.get('repos', []):
        for wt in repo.get('worktrees', []):
            if wt.get('isMainWorktree'):
                continue
            issue = wt.get('issue') or {}
            issue_num = issue.get('number', '')
            pr = wt.get('pr') or {}
            pr_state = pr.get('state', 'none')
            pr_checks = pr.get('checksState', '')
            pr_conflicts = pr.get('hasConflicts', False)
            pr_threads = pr.get('unresolvedThreads', 0)
            session_info = []
            for s in wt.get('sessions', []):
                claude = s.get('claude') or {}
                cs = claude.get('status', 'no-claude') if claude else 'no-claude'
                session_info.append(f'{s[\"name\"]}={cs}')
            sessions_str = ','.join(session_info) if session_info else 'no-session'
            lines.append(f'#{issue_num}|{sessions_str}|pr:{pr_state}|ci:{pr_checks}|conflicts:{pr_conflicts}|threads:{pr_threads}')
    print('\n'.join(sorted(lines)))
except:
    pass
" 2>/dev/null)

  # --- State change detection ---
  if [ -f "$STATE_FILE" ]; then
    PREV_SESSIONS=$(cat "$STATE_FILE")
    if [ "$CURRENT_SESSIONS" != "$PREV_SESSIONS" ]; then
      MSG=$(diff <(echo "$PREV_SESSIONS") <(echo "$CURRENT_SESSIONS") 2>/dev/null | python3 -c "
import sys
lines = sys.stdin.read().strip().split('\n')
removed = [l[2:] for l in lines if l.startswith('< ')]
added = [l[2:] for l in lines if l.startswith('> ')]
changes = []
rm_by_issue = {l.split('|')[0]: l for l in removed}
add_by_issue = {l.split('|')[0]: l for l in added}
for issue in set(list(rm_by_issue.keys()) + list(add_by_issue.keys())):
    old = rm_by_issue.get(issue, '')
    new = add_by_issue.get(issue, '')
    if old and new:
        old_parts = dict(p.split(':',1) if ':' in p else (p,'') for p in old.split('|')[1:])
        new_parts = dict(p.split(':',1) if ':' in p else (p,'') for p in new.split('|')[1:])
        diffs = []
        for k in set(list(old_parts.keys()) + list(new_parts.keys())):
            if old_parts.get(k) != new_parts.get(k):
                diffs.append(f'{k}:{old_parts.get(k,\"?\")}→{new_parts.get(k,\"?\")}')
        if diffs:
            changes.append(f'{issue} {\" \".join(diffs)}')
    elif new:
        changes.append(f'{issue} NEW')
    elif old:
        changes.append(f'{issue} REMOVED')
if changes:
    print('[orchard-monitor] ' + '; '.join(changes[:5]))
" 2>/dev/null)

      # Filter to only report watched issues
      if [ -f "$WATCH_FILE" ] && [ -n "$MSG" ]; then
        WATCHED_NUMS=$(grep -v '^#' "$WATCH_FILE" | tr '\n' '|' | sed 's/|$//')
        if [ -n "$WATCHED_NUMS" ]; then
          MSG=$(echo "$MSG" | python3 -c "
import sys, re
line = sys.stdin.read().strip()
watched = set(['#$w' for w in '''$WATCHED_NUMS'''.split('|')])
# Parse changes from the message
prefix = '[orchard-monitor] '
if line.startswith(prefix):
    body = line[len(prefix):]
    parts = [p.strip() for p in body.split(';')]
    filtered = [p for p in parts if any(p.startswith(w) for w in watched)]
    if filtered:
        print(prefix + '; '.join(filtered))
" 2>/dev/null)
        fi
      fi
      if [ -n "$MSG" ]; then
        # Bookend Enter to flush any signal that arrived while Claude was mid-turn
        # and is now sitting buffered in the prompt. Enter with an empty buffer is
        # a no-op, so the leading Enter is always safe.
        tmux send-keys -t "$ORCHARDIST_PANE" Enter 2>/dev/null
        tmux send-keys -t "$ORCHARDIST_PANE" -l "$MSG" 2>/dev/null
        tmux send-keys -t "$ORCHARDIST_PANE" Enter 2>/dev/null
      fi

      # Optional dashboard refresh. Point ORCHARD_REPORT_CMD at an executable
      # (e.g. a user-specific Telegram poster) to receive state-change nudges.
      if [ -n "${ORCHARD_REPORT_CMD:-}" ] && [ -x "$ORCHARD_REPORT_CMD" ]; then
        ( "$ORCHARD_REPORT_CMD" >> /tmp/orchard-report.log 2>&1 ) &
        echo "$NOW" > "$LAST_REPORT_FILE"
      fi

      # State changed → run decision tree (auto-merge, nudge sessions, etc.)
      ( "$SCRIPT_DIR/orchard-decide.sh" >> /tmp/orchard-decide.log 2>&1 ) &

      # Update duration tracking — reset timers for issues whose state changed
      python3 -c "
import json, sys

now = $NOW
prev_lines = '''$PREV_SESSIONS'''.strip().split('\n')
curr_lines = '''$CURRENT_SESSIONS'''.strip().split('\n')
prev_by_issue = {l.split('|')[0]: l for l in prev_lines if l.strip()}
curr_by_issue = {l.split('|')[0]: l for l in curr_lines if l.strip()}

try:
    with open('$DURATION_FILE') as f:
        durations = json.load(f)
except:
    durations = {}

for issue, state in curr_by_issue.items():
    if state != prev_by_issue.get(issue, ''):
        durations[issue] = now
    elif issue not in durations:
        durations[issue] = now

# Remove issues no longer present
durations = {k: v for k, v in durations.items() if k in curr_by_issue}

with open('$DURATION_FILE', 'w') as f:
    json.dump(durations, f)
" 2>/dev/null
    fi
  else
    # First run — initialize all durations to now
    python3 -c "
import json
now = $NOW
lines = '''$CURRENT_SESSIONS'''.strip().split('\n')
durations = {l.split('|')[0]: now for l in lines if l.strip()}
with open('$DURATION_FILE', 'w') as f:
    json.dump(durations, f)
" 2>/dev/null
  fi

  echo "$CURRENT_SESSIONS" > "$STATE_FILE"

  # --- Watchlist check (throttled to WATCH_INTERVAL) ---
  LAST_WATCH=$(cat "$LAST_WATCH_FILE" 2>/dev/null || echo 0)
  WATCH_ELAPSED=$((NOW - LAST_WATCH))
  if [ -f "$WATCH_FILE" ] && [ "$WATCH_ELAPSED" -ge "$WATCH_INTERVAL" ]; then
    WATCH_ALERT=$(echo "$CURRENT_SESSIONS" | python3 -c "
import sys, json, time

now = $NOW
timestamp = time.strftime('%H:%M', time.localtime(now))

# Read durations
try:
    with open('$DURATION_FILE') as f:
        durations = json.load(f)
except:
    durations = {}

def fmt_duration(seconds):
    if seconds < 60:
        return '<1m'
    elif seconds < 3600:
        return f'{seconds // 60}m'
    else:
        h = seconds // 3600
        m = (seconds % 3600) // 60
        return f'{h}h{m}m' if m else f'{h}h'

# Read current state from stdin
state_lines = sys.stdin.read().strip().split('\n')
state_by_issue = {}
for line in state_lines:
    parts = line.split('|')
    if parts:
        state_by_issue[parts[0]] = line

# Read watchlist
alerts = []
try:
    with open('$WATCH_FILE') as f:
        watched = [l.strip() for l in f if l.strip() and not l.startswith('#')]
except:
    watched = []

for issue_num in watched:
    key = f'#{issue_num}'
    state = state_by_issue.get(key, '')
    if not state:
        alerts.append(f'{key}: not found in orchard')
        continue

    parts = dict(p.split(':',1) if ':' in p else (p,'') for p in state.split('|')[1:])
    ci = parts.get('ci', '')
    threads = parts.get('threads', '0')
    sessions = state.split('|')[1] if '|' in state else ''

    # Calculate how long in current state
    since = durations.get(key, now)
    elapsed = now - since
    dur = fmt_duration(elapsed)

    problems = []
    if ci == 'failing':
        problems.append('CI failing')
    if threads != '0':
        problems.append(f'{threads} unresolved threads')
    if 'no-session' in sessions or 'no-claude' in sessions:
        problems.append('no active session')
    elif 'idle' in sessions or 'input' in sessions:
        problems.append('session idle/waiting')

    if not problems and ci == 'passing' and threads == '0':
        alerts.append(f'{key}: GREEN \u2713')
    elif problems:
        alerts.append(f'{key}: {\" | \".join(problems)} ({dur})')

if alerts:
    print(f'[watch {timestamp}] ' + '; '.join(alerts))
" 2>/dev/null)

    if [ -n "$WATCH_ALERT" ]; then
      # See note at top send site — leading Enter drains any stuck buffered signal.
      tmux send-keys -t "$ORCHARDIST_PANE" Enter 2>/dev/null
      tmux send-keys -t "$ORCHARDIST_PANE" -l "$WATCH_ALERT" 2>/dev/null
      tmux send-keys -t "$ORCHARDIST_PANE" Enter 2>/dev/null
      echo "$NOW" > "$LAST_WATCH_FILE"
    fi
  fi

  # --- Periodic report refresh (keeps timestamp fresh even without state change) ---
  LAST_REPORT=$(cat "$LAST_REPORT_FILE" 2>/dev/null || echo 0)
  REPORT_ELAPSED=$((NOW - LAST_REPORT))
  if [ "$REPORT_ELAPSED" -ge "$REPORT_INTERVAL" ] \
      && [ -n "${ORCHARD_REPORT_CMD:-}" ] \
      && [ -x "$ORCHARD_REPORT_CMD" ]; then
    ( "$ORCHARD_REPORT_CMD" >> /tmp/orchard-report.log 2>&1 ) &
    echo "$NOW" > "$LAST_REPORT_FILE"
  fi

  sleep ${POLL_INTERVAL:-60}
done
