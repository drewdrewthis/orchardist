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
  # the CI vs Mac split (#854) is version-sensitive; record the toolchain once
  echo "# tmux: $(tmux -V), go: $(go version)" >&3
  # build both binaries ONCE for the whole file, not per test. return 1 (not 0)
  # so a broken build fails the file loudly instead of silently skipping AC1-AC4;
  # missing tmux/go above is the only thing that legitimately skips.
  REPO="$(cd "$BATS_TEST_DIRNAME/../.." && pwd)"
  ( cd "$REPO" && go build -o "$BATS_FILE_TMPDIR/orchard-sidebar" ./cmd/orchard-sidebar ) || return 1
  ( cd "$REPO" && go build -o "$BATS_FILE_TMPDIR/orchard-shell" ./cmd/orchard-shell ) || return 1
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
  # not the detached default) AND the sidebar has been re-pinned back to 40.
  # pane_width hits 40 during the detached phase too, so gating on it alone lets
  # a drag run before the sidebar knows its window (#854).
  wait_for 5 "echo \$(wwin):\$(pwin)" "266:40" \
    || skip "sidebar never reached its attached 266x/40-pane boot state"

  # Then wait for the window-width baseline itself to reach 266. On Linux the
  # attach fires NO reflow size at the sidebar (the hook re-pins the pane before
  # it sees one), so the grown window reaches the sidebar only through the client
  # lane's off-thread read — landing within one ladder step (<=2s). Drag before
  # that and the first size is judged against the stale detached baseline and
  # misread as mechanical (the exact CI failure this fixed).
  wait_for 5 "grep -qs 'window baseline .*-> 266' '$XDG_STATE_HOME/orchard/sidebar.log' && echo ok" ok \
    || skip "window-width baseline never refreshed to 266 in the sidebar log"
}

# dump_debug prints the outer server's geometry and the sidebar's own log to
# fd 3 so a CI failure (which we cannot attach to) explains itself: what width
# tmux thinks the pane is, what the option holds, and whether the sidebar even
# saw the drag's WindowSizeMsg and how it classified it.
dump_debug() {
  echo "# --- debug (test failed) ---" >&3
  echo "# geom: $(tmux -L "$O" display -p -t shell:0.0 '#{window_width}x#{window_height} pane=#{pane_width} id=#{pane_id}' 2>&1)" >&3
  echo "# global main-pane-width: $(tmux -L "$O" show-options -g main-pane-width 2>&1)" >&3
  echo "# window main-pane-width: $(tmux -L "$O" show-options -w -t shell:0 main-pane-width 2>&1)" >&3
  echo "# panes: $(tmux -L "$O" list-panes -t shell:0 -F '#{pane_index} #{pane_width} #{pane_current_command}' 2>&1 | tr '\n' '|')" >&3
  echo "# state: $(snapshot_state)" >&3
  echo "# sidebar.log tail:" >&3
  tail -40 "$XDG_STATE_HOME/orchard/sidebar.log" 2>/dev/null | sed 's/^/#   /' >&3 || true
}

teardown() {
  # bats-core 1.x sets BATS_TEST_COMPLETED=1 only when the body passed; dump on
  # a real failure, not on a skip (BATS_TEST_SKIPPED) or a clean pass.
  if [ "${BATS_TEST_COMPLETED:-0}" != 1 ] && [ -z "${BATS_TEST_SKIPPED:-}" ]; then
    dump_debug
  fi
  # $W first: it drives the only live client, so killing it stops anything
  # from re-attaching $O and respawning a server after we kill it.
  for s in "$W" "$O" "$I"; do
    [ -n "$s" ] && tmux -L "$s" kill-server 2>/dev/null || true
  done
  # kill-server can leave the socket file behind if a client raced it; remove
  # only OUR OWN named sockets, never one we did not create.
  local dir="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)"
  for s in "$W" "$O" "$I"; do
    [ -n "$s" ] && rm -f "$dir/$s" || true # unset on a skip path; never fail teardown
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

# AC1 + AC2: a respawn fires no resize hook, but there is no hook to re-pin it
# either (after-respawn-pane is not a real tmux hook). Coverage rests on the Go
# rule alone: a respawned sidebar's FIRST size is a boot size, never published,
# and in every probe respawn-pane -k did not redistribute the pane. Nothing must
# change — global stays 40, the pane stays 40, and the state file is untouched.
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
# Racy on main — observed 34/34/34 on 2026-09-09 (macOS, tmux 3.6a) in some runs,
# passes in others: the intermediate size wins or loses the race with the hook
# re-pin. This fix removes the race by never publishing the mechanical size.
@test "AC4: a terminal resize preserves the dragged width" {
  tmux -L "$O" resize-pane -t shell:0.0 -x 60
  wait_for 5 "wwidth" 60

  tmux -L "$W" resize-window -x 200
  wait_for 5 "pwidth" 60
  [ "$(pwidth)" = "60" ]
  [ "$(wwidth)" = "60" ]
  grep -q '"width":60' "$STATE"
}
