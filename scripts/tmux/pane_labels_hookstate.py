import glob, json, os, stat, sys

from pane_labels_fmt import cell, path_under

# --- Claude hook state (ADR-007) -------------------------------------------
# The hook writes one sidecar per tmux session, named
# orchard-claude-<tmux_session>.json. Directory resolution mirrors
# claudeinstance.ResolveDir(): $ORCHARD_HEARTBEAT_DIR, then $TMPDIR, then /tmp.


def heartbeat_dir():
    arg = os.environ.get("ORCHARD_HEARTBEAT_DIR_ARG") or ""
    if arg:
        return arg
    for var in ("ORCHARD_HEARTBEAT_DIR", "TMPDIR"):
        v = os.environ.get(var)
        if v:
            return v
    return "/tmp"


def _warn(msg):
    print(f"orchard-tmux-labels: {msg}", file=sys.stderr)


def trusted_dir(dirpath):
    """True when a heartbeat dir is safe to read sidecars from.

    The default resolution ends at /tmp, which is world-writable, so any local
    unprivileged process can drop a sidecar naming a session it does not own.
    World-writable is only acceptable with the sticky bit — the property that
    stops one user deleting or replacing another's files in a shared /tmp.
    """
    try:
        st = os.stat(dirpath)
    except OSError:
        return False
    if st.st_uid not in (0, os.geteuid()):
        return False
    if (st.st_mode & stat.S_IWOTH) and not (st.st_mode & stat.S_ISVTX):
        return False
    return True


def trusted_file(path):
    """True when a sidecar's provenance holds up.

    Owner is the check that closes the attack: a sidecar planted by another
    local user in a shared /tmp is rejected however well-formed it looks. The
    rest is defence in depth — a symlink could redirect the read outside the
    dir, and a world-writable file can be rewritten after it was created.
    """
    try:
        st = os.lstat(path)
    except OSError:
        return False
    if not stat.S_ISREG(st.st_mode):
        return False
    if st.st_uid != os.geteuid():
        return False
    if st.st_mode & stat.S_IWOTH:
        return False
    return True


def load_hook_states(dirpath):
    """Map tmux session name -> parsed hook state dict.

    Unreadable, non-JSON, non-object and untrusted files are skipped so
    neither a partially written sidecar nor a planted one breaks labelling.
    `.inflight.json` companions are not state files and are ignored.
    """
    if not trusted_dir(dirpath):
        _warn(f"heartbeat dir is not trustworthy, ignoring hook state: {dirpath}")
        return {}
    states = {}
    for path in sorted(glob.glob(os.path.join(dirpath, "orchard-claude-*.json"))):
        name = os.path.basename(path)
        if name.endswith(".inflight.json"):
            continue
        if not trusted_file(path):
            _warn(f"ignoring hook state file with untrusted provenance: {path}")
            continue
        try:
            with open(path) as fh:
                st = json.load(fh)
        except (OSError, ValueError):
            continue
        if not isinstance(st, dict):
            continue
        session = st.get("tmux_session")
        if not session:
            session = name[len("orchard-claude-"):-len(".json")]
        states[session] = st
    return states


HOOK_STATES = load_hook_states(heartbeat_dir())

# Glyph + colour per hook `state` value written by orchard-state.sh:
# working (PreToolUse/PostToolUse), idle (Stop/SessionStart), input
# (AskUserQuestion, permission_prompt, elicitation_dialog, idle_prompt).
CLAUDE_STATE_STYLE = {
    "working": ("⏺", "green"),
    "idle":    ("⏸", "brightblack"),
    "input":   ("⌨", "yellow"),
}


def hook_state_for_pane(session, pane_path, is_claude_pane):
    """Pick the sidecar belonging to this pane, or None.

    A sidecar is per-SESSION but a label is per-PANE, so a session-name hit
    alone would stamp Claude state onto every shell and editor in the
    session. Narrow it to the pane whose cwd the hook recorded, or failing
    that to the pane actually running claude.
    """
    st = HOOK_STATES.get(session)
    if not st:
        return None
    if path_under(st.get("cwd") or "", pane_path):
        return st
    if is_claude_pane:
        return st
    return None


def claude_cells(st):
    """Render the hook state as label cells, omitting every absent field."""
    if not st:
        return []
    cells = []
    # Only the values orchard-state.sh actually writes are rendered. Anything
    # else is a sidecar this script does not understand, so it is dropped
    # rather than passed through as free text.
    state = (st.get("state") or "").strip() if isinstance(st.get("state"), str) else ""
    if state in CLAUDE_STATE_STYLE:
        glyph, color = CLAUDE_STATE_STYLE[state]
        cells.append(cell(f"fg={color}", f"{glyph} {state}"))
    elif state:
        _warn(f"ignoring unknown claude state: {state[:40]!r}")
    # `model` and `context_window_pct` are statusline telemetry, not part of
    # the hook payload (ClaudeSessionInfo::from_state_file in
    # crates/orchard/src/session.rs leaves both None). Rendered only when a
    # writer actually supplies them; never placeholdered.
    model = st.get("model")
    if isinstance(model, str) and model.strip():
        cells.append(cell("fg=brightblack", model.strip()[:40]))
    ctx = st.get("context_window_pct")
    if isinstance(ctx, (int, float)) and not isinstance(ctx, bool):
        cells.append(cell("fg=brightblack", f"{ctx:.0f}%"))
    return cells
