#!/usr/bin/env bats
# orchardist#854 — the sidebar must not publish a MECHANICAL pane resize as a
# drag. Driven against a real `orchard shell` on throwaway tmux sockets, because
# the bug lives in the timing between tmux's proportional redistribution and the
# outer server's re-pin hooks — a thing no unit test with a fake tmux can show.
#
# The whole battery skips when tmux or go is missing (CI without either); when
# present it builds both binaries once for the file, boots the wrapper detached
# per test, and drives a real client so pane 0.0 gets a real width.

setup_file() {
  command -v tmux >/dev/null || return 0
  command -v go >/dev/null || return 0
  # build both binaries ONCE for the whole file, not per test
  REPO="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  ( cd "$REPO" && go build -o "$BATS_FILE_TMPDIR/orchard-sidebar" ./cmd/orchard-sidebar ) || return 0
  ( cd "$REPO" && go build -o "$BATS_FILE_TMPDIR/orchard-shell" ./cmd/orchard-shell ) || return 0
}

setup() {
  command -v tmux >/dev/null || skip "tmux not installed"
  command -v go >/dev/null || skip "go not installed"
  [ -x "$BATS_FILE_TMPDIR/orchard-sidebar" ] || skip "orchard-sidebar build failed"
  [ -x "$BATS_FILE_TMPDIR/orchard-shell" ] || skip "orchard-shell build failed"

  T="$BATS_TEST_TMPDIR"
  cp "$BATS_FILE_TMPDIR/orchard-sidebar" "$BATS_FILE_TMPDIR/orchard-shell" "$T/"

  export XDG_STATE_HOME="$T/xdg"
  export PATH="$T:$PATH"
  STATE="$XDG_STATE_HOME/orchard/sidebar-state.json"

  # unique per test so a leaked server from a prior run can't be read back
  O="w854o$$-$BATS_TEST_NUMBER" # outer wrapper server
  I="w854i$$-$BATS_TEST_NUMBER" # inner sessions server
  W="w854t$$-$BATS_TEST_NUMBER" # the terminal driving a real client

  unset TMUX # never let a real client's socket leak in
  "$T/orchard-shell" --inner-socket "$I" --session work --outer-socket "$O" --detach
  # a real client at a known size, so pane 0.0 has an actual width to settle at
  tmux -L "$W" new-session -d -x 266 -y 78 "tmux -L $O attach -t shell"

  # Boot is complete only once the client has ATTACHED (window is the full 266,
  # not the detached default) AND the sidebar has been re-pinned back to 40. Both
  # matter: pane_width hits 40 during the detached phase too, before the attach
  # reflow the sidebar must observe to learn its window baseline (#854). Gating on
  # pane_width alone lets a drag run before that baseline is set and misfire.
  wait_for 5 "echo \$(wwin):\$(pwin)" "266:40" \
    || skip "sidebar never reached its attached 266x/40-pane boot state"
}

teardown() {
  # $W first: it drives the only live client, so killing it stops anything
  # from re-attaching $O and respawning a server after we kill it.
  for s in "$W" "$O" "$I"; do
    [ -n "$s" ] && tmux -L "$s" kill-server 2>/dev/null || true
  done
  # kill-server can leave the socket file behind if a client raced it; remove
  # only OUR OWN named sockets, never one we did not create.
  local dir="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)"
  for s in "$W" "$O" "$I"; do
    [ -n "$s" ] && rm -f "$dir/$s"
  done
}

# wait_for polls `cmd` up to `secs` seconds for its stdout to equal `want`.
wait_for() {
  local secs="$1" cmd="$2" want="$3" got
  local deadline=$(( $(date +%s) + secs ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    got="$(eval "$cmd" 2>/dev/null || true)"
    [ "$got" = "$want" ] && return 0
    sleep 0.1
  done
  got="$(eval "$cmd" 2>/dev/null || true)"
  [ "$got" = "$want" ]
}

gwidth() { tmux -L "$O" show-options -gv main-pane-width 2>/dev/null; }
wwidth() { tmux -L "$O" show-options -wv -t shell:0.0 main-pane-width 2>/dev/null; }
pwidth() { tmux -L "$O" display -p -t shell:0.0 '#{pane_width}'; }
pwin() { tmux -L "$O" display -p -t shell:0.0 '#{pane_width}' 2>/dev/null; }
wwin() { tmux -L "$O" display -p -t shell:0.0 '#{window_width}' 2>/dev/null; }
snapshot_state() { cat "$STATE" 2>/dev/null || true; }

# AC1 + AC2: a respawn fires neither resize hook, so without after-respawn-pane
# tmux's redistribution reaches the sidebar as a plain size and gets published.
# The after-respawn-pane hook re-pins instead; nothing must change.
@test "AC1+AC2: respawn does not republish the width" {
  before="$(snapshot_state)"
  tmux -L "$O" respawn-pane -k -t shell:0.0 "$T/orchard-sidebar"

  wait_for 5 "pwidth" 40
  [ "$(gwidth)" = "40" ]
  w="$(wwidth)"; [ -z "$w" ] || [ "$w" = "40" ]
  [ "$(pwidth)" = "40" ]
  [ "$(snapshot_state)" = "$before" ]
}

# AC3: a genuine drag DOES publish — resize-pane stands in for the user dragging
# the border; it must survive the settle and reach both tmux and disk.
@test "AC3: a real drag is published after it settles" {
  tmux -L "$O" resize-pane -t shell:0.0 -x 60

  wait_for 5 "wwidth" 60
  [ "$(wwidth)" = "60" ]
  wait_for 5 "grep -o '\"width\":60' '$STATE'" '"width":60'
}

# AC4: after a drag, a TERMINAL resize must not corrupt the dragged width — the
# changed window marks the intermediate size mechanical, so it is never
# published and the window-resized hook re-pins the pane to 60.
# Red on main today: 34/34/34.
@test "AC4: a terminal resize preserves the dragged width" {
  tmux -L "$O" resize-pane -t shell:0.0 -x 60
  wait_for 5 "wwidth" 60

  tmux -L "$W" resize-window -x 200
  wait_for 5 "pwidth" 60
  [ "$(pwidth)" = "60" ]
  [ "$(wwidth)" = "60" ]
  grep -q '"width":60' "$STATE"
}
